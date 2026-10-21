// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// The provider source is read as text. Nothing in it is compiled, imported
// or executed: each fact below is one regular expression over one file,
// and a file the expression does not match contributes nothing rather than
// a guess.
var (
	registryRe = regexp.MustCompile(`registry\.Schema\{\s*Name:\s*"(google_[a-z0-9_]+)",\s*ProductName:\s*"[^"]*",\s*Type:\s*registry\.(SchemaTypeResource|SchemaTypeIAMResource),`)

	idFormatRe = regexp.MustCompile(`\bid, err := tpgresource\.ReplaceVars(ForId)?\(d, config, "([^"]+)"\)`)

	// importRe is the generated importer's shape: ParseImportId over a
	// list of regexes, then the id the imported instance is given.
	importRe = regexp.MustCompile(`(?s)tpgresource\.ParseImportId\(\[\]string\{(.*?)\}, d, config\).*?\bid, err := tpgresource\.ReplaceVars(?:ForId)?\(d, config, "([^"]+)"\)`)

	quotedRe = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

	metaFieldRe = regexp.MustCompile(`(?m)^(resource|generation_type|api_service_name|cai_asset_name_format):\s*'?([^'\n]*)'?\s*$`)

	// metaCAIListRe is the plural `cai_asset_name_formats:` key and the
	// list items under it.
	metaCAIListRe  = regexp.MustCompile(`(?m)^cai_asset_name_formats:\s*\n((?:[ \t]*-[^\n]*\n?)+)`)
	metaListItemRe = regexp.MustCompile(`(?m)^[ \t]*-[ \t]*'?([^'\n]*?)'?[ \t]*$`)

	// docImportRe is one bulleted format in a resource page's Import
	// section: "* `{{key_ring}}/cryptoKeys/{{name}}`".
	docImportRe = regexp.MustCompile("(?m)^\\* `([^`]+)`[ \t]*$")

	parseFuncRe  = regexp.MustCompile(`func (\w+IdParseFunc)\(d \*schema\.ResourceData`)
	qualifiersRe = regexp.MustCompile(`GetImportIdQualifiers\(\[\]string\{"([^"]+)"`)

	// producerFuncRe and qualifiedArgRe find the argument an IAM updater
	// producer hands to GetImportIdQualifiers: `GetImportIdQualifiers(
	// []string{...}, d, config, d.Get("secret_id").(string))`. That
	// argument is parsed as a long form or a short one and rebuilt into
	// the canonical parent name.
	producerFuncRe = regexp.MustCompile(`func \w+IamUpdaterProducer\(d tpgresource\.TerraformResourceData`)
	qualifiedArgRe = regexp.MustCompile(`GetImportIdQualifiers\(\[\]string\{((?:"(?:[^"\\]|\\.)*"(?:, )?)*)\}, d, config, d\.Get\("([a-z0-9_]+)"\)\.\(string\)\)`)
	goStringRe     = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

	// A hand-written IAM updater that passes its parent argument through a
	// computed-name helper of the provider's own package:
	//
	//	project, err := tpgresource.GetProject(d, config)
	//	subscription := GetComputedSubscriptionName(project, d.Get("subscription").(string))
	//
	// where the helper keeps a value matching its pattern and templates
	// anything else:
	//
	//	match, _ := regexp.MatchString("projects\\/.*\\/subscriptions\\/.*", subscription)
	//	if match {
	//		return subscription
	//	}
	//	return fmt.Sprintf("projects/%s/subscriptions/%s", project, subscription)
	updaterFuncRe   = regexp.MustCompile(`func New\w+IamUpdater\(d tpgresource\.TerraformResourceData`)
	updaterProjRe   = regexp.MustCompile(`\bproject, err := tpgresource\.GetProject\(d, config\)`)
	computedCallRe  = regexp.MustCompile(`\b(\w+) := (\w+)\(project, d\.Get\("([a-z0-9_]+)"\)\.\(string\)\)`)
	computedFieldRe = regexp.MustCompile(`func \(u \*\w+\) GetResourceId\(\) string \{\s*return u\.(\w+)\s*\}`)
	wholeIDSetRe    = regexp.MustCompile(`d\.Set\("([a-z0-9_]+)", d\.Id\(\)\)`)
	namedGroupRe    = regexp.MustCompile(`\(\?P<([a-z0-9_]+)>(?:\[\^/\]\+|\.\+)\)`)
	iamDefaultRe    = regexp.MustCompile(`tpgresource\.Get(Project|Region|Zone|Location)\(d, config\)`)
	iamExpectedRe   = regexp.MustCompile(`expected '([^']+)'`)

	setIDVarRe = regexp.MustCompile(`d\.SetId\((\w+)\)`)

	sprintfRe = regexp.MustCompile(`(\w+) := fmt\.Sprintf\("([^"]+)", ([\w, ]+)\)`)
	diffGetRe = regexp.MustCompile(`(\w+) := diff\.Get\("([a-z0-9_]+)"\)\.\(string\)`)
)

// iamKinds maps an IAM type's suffix to its kind and to the framework file
// whose own error message states the kind's import grammar. A policy's
// importer hands the whole id to the parent's IdParseFunc
// (iamPolicyImport in resource_iam_policy.go) and states no grammar of its
// own, so its grammar is the parent id alone.
var iamKinds = []struct {
	suffix, kind, file string
}{
	{"_iam_member", "iam_member", "resource_iam_member.go"},
	{"_iam_binding", "iam_binding", "resource_iam_binding.go"},
	{"_iam_audit_config", "iam_audit_config", "resource_iam_audit_config.go"},
	{"_iam_policy", "iam_policy", ""},
}

const iamPolicyGrammar = "resource_name"

func runExtract(root, googleDir, betaDir, schemaFile string) error {
	ev := Evidence{
		GeneratedBy: "go run ./tools/gcp-row-gen -extract",
		Pins:        map[string]SourcePin{},
		Types:       map[string]map[string]Source{},
	}
	for _, p := range []struct{ name, dir, repo string }{
		{ProviderGoogle, googleDir, "hashicorp/terraform-provider-google"},
		{ProviderGoogleBeta, betaDir, "hashicorp/terraform-provider-google-beta"},
	} {
		pin, err := readPin(p.dir, p.repo)
		if err != nil {
			return err
		}
		ev.Pins[p.name] = pin
		sources, err := extractSources(p.dir)
		if err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		for t, s := range sources {
			if ev.Types[t] == nil {
				ev.Types[t] = map[string]Source{}
			}
			ev.Types[t][p.name] = s
		}
	}

	facts, err := extractFacts(schemaFile)
	if err != nil {
		return err
	}
	// A type the source registers but the schema does not serve, or the
	// reverse, is a mismatch between two artifacts of the same release, and
	// is refused rather than half-recorded.
	for p, pf := range facts.Providers {
		for t := range pf.Types {
			if _, ok := ev.Types[t][p]; !ok {
				fmt.Fprintf(os.Stderr, "gcp-row-gen: %s serves %s but its source registers no resource by that name; recorded with no source evidence\n", p, t)
			}
		}
	}
	for t, byProv := range ev.Types {
		for p := range byProv {
			if _, ok := facts.Providers[p].Types[t]; !ok {
				return fmt.Errorf("%s's source registers %s, which its schema does not serve", p, t)
			}
		}
	}

	evData, err := marshalEvidence(ev)
	if err != nil {
		return err
	}
	factsData, err := marshalFacts(facts)
	if err != nil {
		return err
	}
	for rel, data := range map[string][]byte{EvidenceJSONRel: evData, FactsJSONRel: factsData} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), data, 0o644); err != nil { //nolint:gosec // committed artifacts
			return fmt.Errorf("writing %s: %w", rel, err)
		}
		fmt.Printf("%s: %d bytes\n", rel, len(data))
	}
	return nil
}

// readPin reads the checkout's HEAD and refuses it unless the v<ProviderVersion>
// tag names that same commit. The version is not in the source tree (the
// release build sets it), so the tag is the only statement of it.
func readPin(dir, repo string) (SourcePin, error) {
	head, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil {
		return SourcePin{}, fmt.Errorf("reading %s's HEAD: %w", repo, err)
	}
	commit := strings.TrimSpace(string(head))
	if strings.HasPrefix(commit, "ref:") {
		return SourcePin{}, fmt.Errorf("%s is on a branch (%s); check out the v%s tag", repo, commit, ProviderVersion)
	}
	tag := "v" + ProviderVersion
	tagged := ""
	if data, err := os.ReadFile(filepath.Join(dir, ".git", "refs", "tags", tag)); err == nil {
		tagged = strings.TrimSpace(string(data))
	} else if data, err := os.ReadFile(filepath.Join(dir, ".git", "packed-refs")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[1] == "refs/tags/"+tag {
				tagged = f[0]
			}
		}
	}
	if tagged != commit {
		return SourcePin{}, fmt.Errorf("%s's HEAD %s is not the %s tag (%q)", repo, commit, tag, tagged)
	}
	return SourcePin{Repository: repo, Tag: tag, Commit: commit}, nil
}

// sourceRoot is the provider package directory: google/ in
// terraform-provider-google, google-beta/ in -google-beta.
func sourceRoot(dir string) string {
	if fi, err := os.Stat(filepath.Join(dir, "google-beta")); err == nil && fi.IsDir() {
		return filepath.Join(dir, "google-beta")
	}
	return filepath.Join(dir, "google")
}

// extractSources reads every resource type one provider checkout registers.
func extractSources(dir string) (map[string]Source, error) {
	grammar, err := iamGrammars(dir)
	if err != nil {
		return nil, err
	}
	meta, err := metaFiles(dir)
	if err != nil {
		return nil, err
	}
	iamIdentity := iamKindIdentity(dir)
	fields, err := configFields(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]Source{}
	tpgresourceText := ""
	tpgresourceOnce := false
	helpers := func() string {
		if !tpgresourceOnce {
			tpgresourceText, tpgresourceOnce = packageText(filepath.Join(sourceRoot(dir), "tpgresource")), true
		}
		return tpgresourceText
	}
	loader := newPkgLoader(dir)
	services := filepath.Join(sourceRoot(dir), "services")
	err = filepath.WalkDir(services, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		for _, m := range registryRe.FindAllStringSubmatch(text, -1) {
			typeName, regType := m[1], m[2]
			if _, dup := out[typeName]; dup {
				return fmt.Errorf("%s is registered twice (again in %s)", typeName, rel)
			}
			var s Source
			pkg := func() string { return packageText(filepath.Dir(path)) }
			if regType == "SchemaTypeIAMResource" {
				s = iamSource(typeName, text, grammar, pkg, helpers)
				s.IdentityImport = iamIdentityImport(text, s.Kind, pkg, iamIdentity)
			} else {
				s = resourceSource(text)
				readConfigPlaceholders(&s, fields)
				s.IdentityImport = resourceIdentityImport(text, pkg)
				m := meta[typeName]
				s.Generation = m.generation
				s.APIService = m.apiService
				s.CAINameFormats = m.caiNames
				s.DocImportFormats = docImportFormats(dir, typeName)
				if s.IDFormat == "" {
					h, err := readHandwritten(loader, filepath.Dir(path), typeName)
					if err != nil {
						return fmt.Errorf("%s: %w", typeName, err)
					}
					s.CreateID, s.CreateIDAt, s.CreateUnread, s.CreateMinted = h.CreateID, h.CreateIDAt, h.CreateUnread, h.CreateMinted
					s.CreateRaw, s.CreateShort, s.CreateTrim, s.CreateLocated = h.CreateRaw, h.CreateShort, h.CreateTrim, h.CreateLocated
					s.Importer, s.ImporterAt, s.ImporterHash = h.Importer, h.ImporterAt, h.ImporterHash
					s.ImporterFormats, s.ImporterID, s.ImporterUnread = h.ImporterFormats, h.ImporterID, h.ImporterUnread
				}
			}
			s.File = rel
			out[typeName] = s
		}
		return nil
	})
	return out, err
}

// resourceSource reads one resource file.
func resourceSource(text string) Source {
	s := Source{Kind: "resource"}
	if m := idFormatRe.FindStringSubmatch(text); m != nil {
		s.IDFormat = m[2]
		s.IDShortened = m[1] != ""
	}
	if m := importRe.FindStringSubmatch(text); m != nil {
		for _, q := range quotedRe.FindAllStringSubmatch(m[1], -1) {
			s.ImportFormats = append(s.ImportFormats, q[1])
		}
		s.ImportIDFormat = m[2]
	}
	readImporterShapes(&s, text)
	readCreateName(&s, text)
	readCreateWrites(&s, text)
	readLocationSet(&s, text)
	s.IDFromResponse = idFromResponse(text)
	for _, name := range placeholders(s.IDFormat) {
		if v, ok := schemaDefault(text, name); ok {
			if s.Defaults == nil {
				s.Defaults = map[string]string{}
			}
			s.Defaults[name] = v
		} else if schemaHasDefault(text, name) {
			s.OtherDefaults = append(s.OtherDefaults, name)
		}
		if group := schemaAtLeastOneOf(text, name); group != nil {
			if s.AtLeastOneOf == nil {
				s.AtLeastOneOf = map[string][]string{}
			}
			s.AtLeastOneOf[name] = group
		}
		if tmpl, ok := customizeDiffDerivation(text, name); ok {
			if s.Derived == nil {
				s.Derived = map[string]string{}
			}
			s.Derived[name] = tmpl
		}
	}
	return s
}

// idFromResponse is the response field a d.SetId call's argument was read
// out of, or "": `val, ok := gr["gcpResourcename"].(string)` (or `val :=
// ...`) and then `d.SetId(val)`. A variable named id is skipped: a
// flatten function's own `id, ok := item["id"]` shadows nothing the
// resource's id is set from.
func idFromResponse(text string) string {
	for _, m := range setIDVarRe.FindAllStringSubmatch(text, -1) {
		v := m[1]
		if v == "id" {
			continue
		}
		bound := regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `(?:, ok)? :?= \w+\["([^"]+)"\]\.\(string\)`).FindStringSubmatch(text)
		if bound != nil {
			return bound[1]
		}
	}
	return ""
}

// schemaField is the text of the schema map's field for a top-level
// argument: a `"name": {` field opening, up to the closing brace at the
// same indentation, with the indentation it opened at. The identity
// schema's own field of the same name (RequiredForImport or
// OptionalForImport) is skipped.
func schemaField(text, name string) (field, indent string, ok bool) {
	open := regexp.MustCompile(`\n(\t+)"` + regexp.QuoteMeta(name) + `": \{\n`)
	for _, loc := range open.FindAllStringSubmatchIndex(text, -1) {
		indent := text[loc[2]:loc[3]]
		rest := text[loc[1]:]
		end := strings.Index(rest, "\n"+indent+"},")
		if end < 0 {
			continue
		}
		field := rest[:end]
		if strings.Contains(field, "ForImport:") {
			continue
		}
		return field, indent, true
	}
	return "", "", false
}

// schemaDefault reads the string Default the file's schema map declares for
// a top-level argument.
func schemaDefault(text, name string) (string, bool) {
	field, indent, ok := schemaField(text, name)
	if !ok {
		return "", false
	}
	m := regexp.MustCompile(`\n` + indent + `\tDefault:\s*"([^"]*)",`).FindStringSubmatch("\n" + field)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// schemaHasDefault is true when the field declares any Default at all, a
// string or otherwise (`Default: 443`, `Default: false`): what
// schemaDefault cannot carry as a value still rules out an empty segment.
func schemaHasDefault(text, name string) bool {
	field, indent, ok := schemaField(text, name)
	if !ok {
		return false
	}
	return regexp.MustCompile(`\n` + indent + `\tDefault:`).MatchString("\n" + field)
}

// schemaAtLeastOneOf is the field's AtLeastOneOf list, or nil.
func schemaAtLeastOneOf(text, name string) []string {
	field, indent, ok := schemaField(text, name)
	if !ok {
		return nil
	}
	m := regexp.MustCompile(`\n` + indent + `\tAtLeastOneOf:\s*\[\]string\{([^}]*)\}`).FindStringSubmatch("\n" + field)
	if m == nil {
		return nil
	}
	return unquoteAll(m[1])
}

// customizeDiffDerivation recognises a computed attribute the provider's own
// CustomizeDiff sets from configuration arguments alone:
//
//	aid := diff.Get("account_id").(string)
//	proj := diff.Get("project").(string)
//	email := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", aid, proj)
//	diff.SetNew("email", email)
//
// and returns the template it computes, "{{account_id}}@{{project}}.iam.gserviceaccount.com".
// Any other shape - a verb other than %s, an argument read some other way -
// is not recognised.
func customizeDiffDerivation(text, name string) (string, bool) {
	set := regexp.MustCompile(`diff\.SetNew\("` + regexp.QuoteMeta(name) + `", (\w+)\)`).FindStringSubmatch(text)
	if set == nil {
		return "", false
	}
	gets := map[string]string{}
	for _, g := range diffGetRe.FindAllStringSubmatch(text, -1) {
		gets[g[1]] = g[2]
	}
	for _, m := range sprintfRe.FindAllStringSubmatch(text, -1) {
		if m[1] != set[1] {
			continue
		}
		format := m[2]
		args := strings.Split(m[3], ",")
		if strings.Count(format, "%") != len(args) || strings.Count(format, "%s") != len(args) {
			return "", false
		}
		out := format
		for _, a := range args {
			attr, ok := gets[strings.TrimSpace(a)]
			if !ok {
				return "", false
			}
			out = strings.Replace(out, "%s", "{{"+attr+"}}", 1)
		}
		return out, true
	}
	return "", false
}

// iamSource reads one IAM type: its kind, its grammar, and its parent id.
//
// pkgText returns the text of every non-test file of the type's package,
// where a hand-written updater's helper lives, and tpgresource the text of
// the provider's tpgresource package; nil reads none.
func iamSource(typeName, text string, grammar map[string]string, pkgText, tpgresource func() string) Source {
	pkg, shared := "", ""
	if pkgText != nil {
		pkg = pkgText()
	}
	if tpgresource != nil {
		shared = tpgresource()
	}
	keys := map[string]string{}
	var read map[string]bool
	if ctors := constructorRe.FindAllStringIndex(text, -1); len(ctors) == 1 {
		body := funcBody(text, ctors[0][0])
		keys, read = iamKeyArgs(body), iamReadKeys(body)
	}
	s := Source{}
	for _, k := range iamKinds {
		if strings.HasSuffix(typeName, k.suffix) {
			s.Kind = k.kind
			s.IAMGrammar = grammar[k.kind]
			break
		}
	}
	seen := map[string]bool{}
	for _, m := range iamDefaultRe.FindAllStringSubmatch(text, -1) {
		name := strings.ToLower(m[1])
		if !seen[name] {
			seen[name] = true
			s.IAMDefaults = append(s.IAMDefaults, name)
		}
	}
	sort.Strings(s.IAMDefaults)
	if prods := producerFuncRe.FindAllStringIndex(text, -1); len(prods) == 1 {
		body := text[prods[0][0]:]
		if end := strings.Index(body, "\n}\n"); end >= 0 {
			body = body[:end]
		}
		if m := qualifiedArgRe.FindAllStringSubmatch(body, -1); len(m) == 1 {
			s.IAMQualified = m[0][2]
			for _, q := range unquoteAll(m[0][1]) {
				s.IAMQualifiers = append(s.IAMQualifiers, renameGroups(q, keys, read))
			}
		}
	}
	if s.IAMQualified == "" && pkgText != nil {
		s.IAMQualified, s.IAMComputedPattern, s.IAMComputedFormat = computedName(text, pkgText)
	}
	s.IAMResourceID, s.IAMResourceIDUnread, s.IAMResourceIDPrefixes = updaterResourceID(text, pkg)
	funcs := parseFuncRe.FindAllStringSubmatchIndex(text, -1)
	parseText := text
	if len(funcs) == 0 {
		// A file that registers the type with a parse function another
		// file of its package defines, by the one name it passes.
		if name := parseFuncRefs(text); name != "" {
			parseText = pkg
			funcs = regexp.MustCompile(`func `+regexp.QuoteMeta(name)+`\(d \*schema\.ResourceData`).FindAllStringSubmatchIndex(pkg, -1)
		}
	}
	if len(funcs) != 1 {
		// No parse function, or several in one file: which one this type
		// uses is not a regex question.
		return s
	}
	body := parseText[funcs[0][0]:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	switch {
	case qualifiersRe.MatchString(body):
		// Every group keeps its name here, read by the struct or not:
		// this is what an import string must spell, which decideIAM
		// checks against IAMResourceID.
		if tmpl, ok := regexToTemplate(renameGroups(qualifiersRe.FindStringSubmatch(body)[1], keys, nil)); ok {
			s.IAMParent = tmpl
		}
		s.IAMParseUnset = parseUnsetKeys(body)
	case wholeIDSetRe.MatchString(body):
		s.IAMParent = "{{" + wholeIDSetRe.FindStringSubmatch(body)[1] + "}}"
	default:
		s.IAMParent = handParseTemplate(body, pkg+"\n"+shared)
	}
	return s
}

// parseFuncRefs is the one IdParseFunc a file names without defining it,
// or "".
func parseFuncRefs(text string) string {
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`\b(\w+IdParseFunc)\b`).FindAllStringSubmatch(text, -1) {
		names[m[1]] = true
	}
	if len(names) != 1 {
		return ""
	}
	for n := range names {
		return n
	}
	return ""
}

// regexToTemplate turns an importer regex made only of literal segments and
// `(?P<name>[^/]+)` groups into the id template it parses. Anything else in
// the regex is refused.
func regexToTemplate(re string) (string, bool) {
	re = strings.TrimSuffix(strings.TrimPrefix(re, "^"), "$")
	out := namedGroupRe.ReplaceAllString(re, "{{$1}}")
	if strings.ContainsAny(namedGroupRe.ReplaceAllString(re, ""), `()[]?*+|^$\`) {
		return "", false
	}
	return out, true
}

// iamGrammars reads the IAM framework's grammar per kind from the error
// message each importer raises on a malformed id.
func iamGrammars(dir string) (map[string]string, error) {
	out := map[string]string{"iam_policy": iamPolicyGrammar}
	for _, k := range iamKinds {
		if k.file == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sourceRoot(dir), "tpgiamresource", k.file))
		if err != nil {
			return nil, err
		}
		m := iamExpectedRe.FindStringSubmatch(string(data))
		if m == nil {
			return nil, fmt.Errorf("%s states no import grammar", k.file)
		}
		out[k.kind] = m[1]
	}
	return out, nil
}

// metaFacts is what one *_meta.yaml records for a type: its
// generation_type, the API service it calls, and the Cloud Asset
// Inventory name formats Magic Modules writes there when they differ from
// "//<api_service_name>/<id format>".
type metaFacts struct {
	generation string
	apiService string
	caiNames   []string
}

// metaFiles maps each type to what its *_meta.yaml records.
func metaFiles(dir string) (map[string]metaFacts, error) {
	out := map[string]metaFacts{}
	err := filepath.WalkDir(sourceRoot(dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, "_meta.yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fields := map[string]string{}
		for _, m := range metaFieldRe.FindAllStringSubmatch(string(data), -1) {
			fields[m[1]] = strings.TrimSpace(m[2])
		}
		r := fields["resource"]
		if r == "" {
			return nil
		}
		mf := metaFacts{generation: fields["generation_type"], apiService: fields["api_service_name"]}
		if f := fields["cai_asset_name_format"]; f != "" {
			mf.caiNames = append(mf.caiNames, f)
		}
		if m := metaCAIListRe.FindStringSubmatch(string(data)); m != nil {
			for _, item := range metaListItemRe.FindAllStringSubmatch(m[1], -1) {
				if v := strings.TrimSpace(item[1]); v != "" {
					mf.caiNames = append(mf.caiNames, v)
				}
			}
		}
		out[r] = mf
		return nil
	})
	return out, err
}

// docImportFormats reads the bulleted formats in the "## Import" section of
// the type's page under website/docs/r, the formats the provider states
// its importer accepts. A type with no page, or a page whose Import
// section lists none, has none.
func docImportFormats(dir, typeName string) []string {
	page := filepath.Join(dir, "website", "docs", "r", strings.TrimPrefix(typeName, "google_")+".html.markdown")
	data, err := os.ReadFile(page)
	if err != nil {
		return nil
	}
	text := string(data)
	start := strings.Index(text, "\n## Import\n")
	if start < 0 {
		return nil
	}
	section := text[start+len("\n## Import\n"):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	// Only the list ahead of the first example: the examples repeat the
	// formats inside code blocks.
	if fence := strings.Index(section, "```"); fence >= 0 {
		section = section[:fence]
	}
	var out []string
	for _, m := range docImportRe.FindAllStringSubmatch(section, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// placeholderRe is one ReplaceVars variable. A leading % (path-escaped) is
// kept apart from the name.
var placeholderRe = regexp.MustCompile(`\{\{(%?)([a-zA-Z0-9_]+)\}\}`)

func placeholders(tmpl string) []string {
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(tmpl, -1) {
		out = append(out, m[2])
	}
	return out
}

// schemaDump is the subset of `providers schema -json` read here.
type schemaDump struct {
	ProviderSchemas map[string]struct {
		ResourceSchemas map[string]struct {
			Block jsonBlock `json:"block"`
		} `json:"resource_schemas"`
		ResourceIdentitySchemas map[string]struct {
			Attributes map[string]struct {
				RequiredForImport bool `json:"required_for_import"`
				OptionalForImport bool `json:"optional_for_import"`
			} `json:"attributes"`
		} `json:"resource_identity_schemas"`
	} `json:"provider_schemas"`
}

type jsonAttr struct {
	Required    bool   `json:"required"`
	Optional    bool   `json:"optional"`
	Computed    bool   `json:"computed"`
	Description string `json:"description"`
	NestedType  *struct {
		Attributes map[string]jsonAttr `json:"attributes"`
	} `json:"nested_type"`
}

type jsonBlock struct {
	Attributes map[string]jsonAttr `json:"attributes"`
	BlockTypes map[string]struct {
		Block    jsonBlock `json:"block"`
		MaxItems int       `json:"max_items"`
	} `json:"block_types"`
}

func flags(a jsonAttr) string {
	switch {
	case a.Required:
		return "R"
	case a.Optional && a.Computed:
		return "OC"
	case a.Optional:
		return "O"
	default:
		return "C"
	}
}

func extractFacts(schemaFile string) (Facts, error) {
	data, err := os.ReadFile(schemaFile)
	if err != nil {
		return Facts{}, err
	}
	var dump schemaDump
	if err := json.Unmarshal(data, &dump); err != nil {
		return Facts{}, fmt.Errorf("decoding %s: %w", schemaFile, err)
	}
	out := Facts{GeneratedBy: "go run ./tools/gcp-row-gen -extract", Providers: map[string]ProviderFacts{}}
	for _, p := range []string{ProviderGoogle, ProviderGoogleBeta} {
		addr := "registry.opentofu.org/hashicorp/" + p
		ps, ok := dump.ProviderSchemas[addr]
		if !ok {
			return Facts{}, fmt.Errorf("%s has no schema for %s", schemaFile, addr)
		}
		pf := ProviderFacts{Source: "hashicorp/" + p, Version: ProviderVersion, Types: map[string]TypeFacts{}}
		for t, rs := range ps.ResourceSchemas {
			tf := TypeFacts{Attrs: map[string]string{}}
			for name, a := range rs.Block.Attributes {
				tf.Attrs[name] = flags(a)
				if a.NestedType != nil {
					if tf.Nested == nil {
						tf.Nested = map[string]map[string]string{}
					}
					leaves := map[string]string{}
					for ln, la := range a.NestedType.Attributes {
						leaves[ln] = flags(la)
					}
					tf.Nested[name] = leaves
				}
			}
			if l, ok := rs.Block.Attributes[markers.GoogleLabelsAttr]; ok && markers.GoogleLabelsPropagate(l.Description) {
				tf.LabelsPropagate = true
			}
			for name, bt := range rs.Block.BlockTypes {
				if tf.Blocks == nil {
					tf.Blocks = map[string]BlockFacts{}
				}
				leaves := map[string]string{}
				for ln, la := range bt.Block.Attributes {
					leaves[ln] = flags(la)
				}
				tf.Blocks[name] = BlockFacts{Attrs: leaves, MaxItems: bt.MaxItems}
			}
			if ids, ok := ps.ResourceIdentitySchemas[t]; ok {
				tf.Identity = map[string]string{}
				for name, a := range ids.Attributes {
					if a.RequiredForImport {
						tf.Identity[name] = "R"
					} else {
						tf.Identity[name] = "O"
					}
				}
			}
			pf.Types[t] = tf
		}
		out.Providers[p] = pf
	}
	return out, nil
}

// sortedProviders lists the providers a type's evidence names, in a fixed
// order.
func sortedProviders(byProv map[string]Source) []string {
	out := make([]string, 0, len(byProv))
	for p := range byProv {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// unquoteAll reads the Go string literals of a []string{...} body, or nil
// when any of them is not one.
func unquoteAll(list string) []string {
	var out []string
	for _, q := range goStringRe.FindAllString(list, -1) {
		v, err := strconv.Unquote(q)
		if err != nil {
			return nil
		}
		out = append(out, v)
	}
	return out
}

// computedName reads a hand-written IAM updater whose parent argument goes
// through a computed-name helper (see computedCallRe): the argument, the
// helper's pattern, and its template with the argument and the provider's
// project as placeholders. Any piece not in exactly that shape reads as
// nothing.
func computedName(text string, pkgText func() string) (arg, pattern, format string) {
	loc := updaterFuncRe.FindAllStringIndex(text, -1)
	if len(loc) != 1 {
		return "", "", ""
	}
	body := text[loc[0][0]:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	call := computedCallRe.FindAllStringSubmatch(body, -1)
	if len(call) != 1 || !updaterProjRe.MatchString(body) {
		return "", "", ""
	}
	variable, helper, argName := call[0][1], call[0][2], call[0][3]
	if f := computedFieldRe.FindAllStringSubmatch(text, -1); len(f) != 1 || f[0][1] != variable ||
		!strings.Contains(body, variable+": "+variable+",") {
		return "", "", ""
	}
	helperRe := regexp.MustCompile(`func ` + regexp.QuoteMeta(helper) +
		`\(project, (\w+) string\) string \{\s*match, _ := regexp\.MatchString\(("(?:[^"\\]|\\.)*"), (\w+)\)\s*if match \{\s*return (\w+)\s*\}\s*return fmt\.Sprintf\("([^"%]*)%s([^"%]*)%s", project, (\w+)\)\s*\}`)
	h := helperRe.FindAllStringSubmatch(pkgText(), -1)
	if len(h) != 1 {
		return "", "", ""
	}
	param := h[0][1]
	if h[0][3] != param || h[0][4] != param || h[0][7] != param {
		return "", "", ""
	}
	p, err := strconv.Unquote(h[0][2])
	if err != nil {
		return "", "", ""
	}
	if _, err := regexp.Compile(p); err != nil {
		return "", "", ""
	}
	return argName, p, h[0][5] + "{{project}}" + h[0][6] + "{{" + argName + "}}"
}

// packageText is every non-test .go file of one package directory, joined.
func packageText(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return ""
		}
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String()
}
