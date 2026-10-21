// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// IdentityImport is how a type's importer reads an identity object: an
// import block's identity with no import ID, which the plugin SDK hands
// the importer as d.Identity() with d.Id() empty (INTENTIUS/choudoufu#2145
// item 4). It is read off the code the importer runs, never inferred from
// the identity schema alone: a type can serve an identity schema and still
// have an importer that ignores it.
type IdentityImport struct {
	// Via names the code that reads the identity:
	//
	//   - "parse_import_id": the importer calls tpgresource.ParseImportId,
	//     whose d.Id() == "" branch (identityImport in tpgresource/import.go)
	//     copies the identity value of each named group of the FIRST regex
	//     into the resource, and setDefaultValues fills project, region and
	//     zone from the provider when they are still unset.
	//   - "importer": the importer has its own `if d.Id() == ""` branch
	//     that reads identity.GetOk.
	//   - "iam_parent": an IAM type registered with
	//     tpgiamresource.IamWithParentResourceIdentity, whose parent parser
	//     and the framework's setIam<Kind>IdFromParentResourceIdentity read
	//     the identity into the import ID the string form would have been.
	//
	// Empty when the importer reads no identity; Unread then says why.
	Via string `json:"via,omitempty"`

	// Required are the identity attributes the importer cannot do
	// without: it errors, or it builds an ID with an empty segment.
	Required []string `json:"required,omitempty"`

	// Defaulted are the attributes it fills from the provider
	// configuration when the identity leaves them unset.
	Defaulted []string `json:"defaulted,omitempty"`

	// Optional are the attributes it reads when present and otherwise
	// does without.
	Optional []string `json:"optional,omitempty"`

	// Unread is set when the importer depends on something an identity
	// import does not supply: an argument it reads that the identity did
	// not set, `d.Id()` read before the identity branch, or no importer at
	// all. Quoted from the code.
	Unread string `json:"unread,omitempty"`
}

var (
	resourceIdentityRe = regexp.MustCompile(`Identity:\s*&schema\.ResourceIdentity\{`)
	importerStateRe    = regexp.MustCompile(`Importer:\s*&schema\.ResourceImporter\{\s*State(?:Context)?:\s*([\w.]+),`)
	identityBranchRe   = regexp.MustCompile(`if d\.Id\(\) == "" \{`)
	identityGetOkRe    = regexp.MustCompile(`identity\.GetOk\("([a-z0-9_]+)"\)`)
	parseImportListRe  = regexp.MustCompile(`(?s)tpgresource\.ParseImportId\(\[\]string\{(.*?)\}, d, config\)`)
	dGetRe             = regexp.MustCompile(`\bd\.Get(?:Ok)?\("([a-z0-9_]+)"\)`)
	iamParentParserRe  = regexp.MustCompile(`IamWithParentResourceIdentity\((\w+)\)`)
	iamParamRe         = regexp.MustCompile(`\{Key: "(\w+)", IdentityKey: "([a-z0-9_]+)"\}`)
	iamFrameworkRe     = regexp.MustCompile(`func setIam(\w+)IdFromParentResourceIdentity\(`)
	camelBoundaryRe    = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// importDefaulted are the groups setDefaultValues fills from the provider.
var importDefaulted = map[string]bool{"project": true, "region": true, "zone": true}

// iamDefaulted are the parser params ParseIamResourceIdentity fills from
// the provider (DefaultConfigValueFuncs in iam_resource_identity.go).
var iamDefaulted = map[string]bool{"project": true, "region": true, "zone": true, "location": true}

// findFunc returns the body of func name in text, else in pkg.
func findFunc(name, text string, pkg func() string) string {
	re := regexp.MustCompile(`func ` + regexp.QuoteMeta(name) + `\(`)
	if loc := re.FindStringIndex(text); loc != nil {
		return funcBody(text, loc[0])
	}
	if pkg == nil {
		return ""
	}
	p := pkg()
	if loc := re.FindStringIndex(p); loc != nil {
		return funcBody(p, loc[0])
	}
	return ""
}

// getOkKeys sorts the identity.GetOk reads of body into required and
// optional: a read followed by `if !ok {` errors without the attribute.
func getOkKeys(body string) (required, optional []string) {
	lines := strings.Split(body, "\n")
	seen := map[string]bool{}
	for i, line := range lines {
		for _, m := range identityGetOkRe.FindAllStringSubmatch(line, -1) {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			next := ""
			for j := i + 1; j < len(lines); j++ {
				if t := strings.TrimSpace(lines[j]); t != "" {
					next = t
					break
				}
			}
			if strings.HasPrefix(next, "if !ok") {
				required = append(required, m[1])
			} else {
				optional = append(optional, m[1])
			}
		}
	}
	return required, optional
}

// resourceIdentityImport reads how a resource file's importer takes an
// identity. Nil when the file declares no identity schema.
func resourceIdentityImport(text string, pkg func() string) *IdentityImport {
	if !resourceIdentityRe.MatchString(text) {
		return nil
	}
	m := importerStateRe.FindStringSubmatch(text)
	if m == nil {
		return &IdentityImport{Unread: "the resource registers no importer"}
	}
	if strings.Contains(m[1], ".") {
		return &IdentityImport{Unread: "the importer is " + m[1] + ", which takes the import ID as it is"}
	}
	body := findFunc(m[1], text, pkg)
	if body == "" {
		return &IdentityImport{Unread: "the importer " + m[1] + " is not defined in the type's package"}
	}
	if identityBranchRe.MatchString(body) && strings.Contains(body, "d.Identity()") {
		req, opt := getOkKeys(body)
		return &IdentityImport{Via: "importer", Required: req, Optional: opt}
	}
	loc := parseImportListRe.FindStringSubmatchIndex(body)
	if loc == nil {
		return &IdentityImport{Unread: "the importer " + m[1] + " reads no identity and calls no tpgresource.ParseImportId"}
	}
	if strings.Contains(body[:loc[0]], "d.Id()") {
		return &IdentityImport{Unread: "the importer reads d.Id() before tpgresource.ParseImportId"}
	}
	list := unquoteAll(body[loc[2]:loc[3]])
	if len(list) == 0 {
		return &IdentityImport{Unread: "the importer's tpgresource.ParseImportId names no regex"}
	}
	re, err := regexp.Compile(list[0])
	if err != nil {
		return &IdentityImport{Unread: "the importer's first regex does not compile"}
	}
	if re.MatchString("") {
		// ParseImportId tries the ID branch first, and an empty ID that
		// matches it takes that branch and sets every group empty.
		return &IdentityImport{Unread: "the importer's first regex matches an empty ID, so ParseImportId never reaches its identity branch"}
	}
	ii := &IdentityImport{Via: "parse_import_id"}
	groups := map[string]bool{}
	for _, g := range re.SubexpNames() {
		if g == "" || groups[g] {
			continue
		}
		groups[g] = true
		if importDefaulted[g] {
			ii.Defaulted = append(ii.Defaulted, g)
		} else {
			ii.Required = append(ii.Required, g)
		}
	}
	for _, line := range strings.Split(body[loc[1]:], "\n") {
		if strings.Contains(line, "fmt.Errorf(") {
			// A value quoted in an error message names nothing.
			continue
		}
		for _, r := range dGetRe.FindAllStringSubmatch(line, -1) {
			if !groups[r[1]] {
				return &IdentityImport{Unread: "after tpgresource.ParseImportId the importer reads " + r[0] + ", which its first regex does not set"}
			}
		}
	}
	if im := importRe.FindStringSubmatch(body); im != nil {
		for _, p := range placeholders(im[2]) {
			if !groups[p] {
				return &IdentityImport{Unread: "the importer's id format " + im[2] + " names {{" + p + "}}, which its first regex does not set"}
			}
		}
	}
	return ii
}

// iamKindIdentity reads, per IAM kind, the identity attributes the
// framework's setIam<Kind>IdFromParentResourceIdentity reads beside the
// parent (role, member, condition_title). A kind with no such function
// takes no identity.
func iamKindIdentity(dir string) map[string]IdentityImport {
	out := map[string]IdentityImport{}
	fwDir := filepath.Join(sourceRoot(dir), "tpgiamresource")
	entries, err := os.ReadDir(fwDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(fwDir, n))
		if err != nil {
			continue
		}
		text := string(data)
		for _, loc := range iamFrameworkRe.FindAllStringSubmatchIndex(text, -1) {
			kind := "iam_" + strings.ToLower(camelBoundaryRe.ReplaceAllString(text[loc[2]:loc[3]], "${1}_${2}"))
			req, opt := getOkKeys(funcBody(text, loc[0]))
			out[kind] = IdentityImport{Via: "iam_parent", Required: req, Optional: opt}
		}
	}
	return out
}

// iamIdentityImport reads how an IAM type's importer takes an identity:
// its parent parser's params and its kind's framework reads. Nil when
// the type is registered with no parent identity parser.
func iamIdentityImport(text, kind string, pkg func() string, kinds map[string]IdentityImport) *IdentityImport {
	m := iamParentParserRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	fw, ok := kinds[kind]
	if !ok {
		return &IdentityImport{Unread: "the IAM framework's " + kind + " importer reads no identity"}
	}
	body := findFunc(m[1], text, pkg)
	if body == "" {
		return &IdentityImport{Unread: "the parent parser " + m[1] + " is not defined in the type's package"}
	}
	ii := &IdentityImport{Via: "iam_parent"}
	if strings.Contains(body, "ParseIamResourceIdentity(") {
		for _, p := range iamParamRe.FindAllStringSubmatch(body, -1) {
			if iamDefaulted[p[1]] {
				ii.Defaulted = append(ii.Defaulted, p[2])
			} else {
				ii.Required = append(ii.Required, p[2])
			}
		}
	} else {
		req, opt := getOkKeys(body)
		ii.Required, ii.Optional = req, opt
	}
	if len(ii.Required)+len(ii.Defaulted)+len(ii.Optional) == 0 {
		return &IdentityImport{Unread: "the parent parser " + m[1] + " reads no identity attribute"}
	}
	ii.Required = append(ii.Required, fw.Required...)
	ii.Optional = append(ii.Optional, fw.Optional...)
	return ii
}

// sortedCopy returns ss sorted, nil when empty.
func sortedCopy(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

// IdentityImportsGoRel is the generated identity-import table
// (INTENTIUS/choudoufu#2145 item 4).
const IdentityImportsGoRel = "internal/live/identity/identityimports_generated_google.go"

// IdentityImportDecision is one composite-identity type's verdict: the
// attributes a record must carry for its importer to take the record as an
// identity, or why it cannot.
type IdentityImportDecision struct {
	Type      string
	Via       string
	Required  []string
	Defaulted []string
	Reason    string
}

// decideIdentityImport reads whether the importer of a type whose identity
// schema requires two or more attributes takes an identity object. Such a
// type's record holds those attributes as Components and no import ID
// (projection.LocatedRecordFrom's composite case). ok is false for a type
// outside that population.
func decideIdentityImport(t string, sources map[string]Source, facts map[string]TypeFacts) (IdentityImportDecision, bool) {
	var identityAttrs map[string]string
	for _, p := range sortedKeys(facts) {
		id := facts[p].Identity
		if identityAttrs == nil {
			identityAttrs = id
		} else if !reflect.DeepEqual(identityAttrs, id) {
			return IdentityImportDecision{Type: t, Reason: "google and google-beta serve different identity schemas"}, requiredCount(identityAttrs) >= 2
		}
	}
	if requiredCount(identityAttrs) < 2 {
		return IdentityImportDecision{}, false
	}
	d := IdentityImportDecision{Type: t}
	var ii *IdentityImport
	for i, p := range sortedKeys(facts) {
		s, ok := sources[p]
		if !ok {
			d.Reason = p + "'s source registers no resource by this name"
			return d, true
		}
		if i == 0 {
			ii = s.IdentityImport
		} else if !reflect.DeepEqual(ii, s.IdentityImport) {
			d.Reason = "google and google-beta read an identity differently"
			return d, true
		}
	}
	switch {
	case ii == nil:
		d.Reason = "the source declares no identity schema for the type"
		return d, true
	case ii.Via == "":
		d.Reason = ii.Unread
		return d, true
	}
	required := map[string]bool{}
	for _, name := range ii.Required {
		if _, isID := identityAttrs[name]; !isID {
			d.Reason = fmt.Sprintf("the importer needs %s, which the identity schema does not carry", name)
			return d, true
		}
		required[name] = true
	}
	for _, name := range ii.Optional {
		if _, isID := identityAttrs[name]; !isID {
			d.Reason = fmt.Sprintf("the importer reads %s from the identity, which the identity schema does not carry", name)
			return d, true
		}
	}
	for name, flag := range identityAttrs {
		if flag == "R" {
			required[name] = true
		}
	}
	d.Via = ii.Via
	d.Required = sortedKeys(required)
	d.Defaulted = sortedCopy(ii.Defaulted)
	return d, true
}

// deriveIdentityImports runs decideIdentityImport over every type.
func deriveIdentityImports(ev Evidence, byType map[string]map[string]TypeFacts) []IdentityImportDecision {
	var out []IdentityImportDecision
	for _, t := range sortedKeys(byType) {
		if d, ok := decideIdentityImport(t, ev.Types[t], byType[t]); ok {
			out = append(out, d)
		}
	}
	return out
}

func renderIdentityImports(decisions []IdentityImportDecision) ([]byte, error) {
	accepted, byVia := 0, map[string]int{}
	for _, d := range decisions {
		if d.Via != "" {
			accepted++
			byVia[d.Via]++
		}
	}
	var b bytes.Buffer
	b.WriteString(`// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Code generated by tools/gcp-row-gen. DO NOT EDIT.

package identity

`)
	fmt.Fprintf(&b, `// GoogleIdentityImports says, for each hashicorp/google and google-beta
// %s type whose identity schema requires two or more attributes and whose
// importer takes an identity object, which attributes a record must carry
// for the importer to find the object by them (INTENTIUS/choudoufu#2145
// item 4). The record of such a type holds its identity as Components and
// no import ID. When no import ID composes from them, the record leg
// imports by the identity instead, exactly where the importer at the pin
// reads one: tpgresource.ParseImportId's identity branch (%d), the
// importer's own identity branch (%d), or the IAM framework's parent
// identity parser (%d). Of %d such types, %d take an identity and %d do
// not ([GoogleIdentityImportRefused]).
var GoogleIdentityImports = map[string]GoogleIdentityImport{
`, ProviderVersion, byVia["parse_import_id"], byVia["importer"], byVia["iam_parent"], len(decisions), accepted, len(decisions)-accepted)
	for _, d := range decisions {
		if d.Via == "" {
			continue
		}
		defaulted := ""
		if len(d.Defaulted) > 0 {
			defaulted = fmt.Sprintf(", Defaulted: %#v", d.Defaulted)
		}
		fmt.Fprintf(&b, "\t%q: {Via: %q, Required: %#v%s},\n", d.Type, d.Via, d.Required, defaulted)
	}
	b.WriteString("}\n\n")
	b.WriteString("// GoogleIdentityImportRefused is every such type whose importer takes no\n// identity, and why, quoted from the source at the pin.\n")
	b.WriteString("var GoogleIdentityImportRefused = map[string]string{\n")
	for _, d := range decisions {
		if d.Via == "" {
			fmt.Fprintf(&b, "\t%q: %q,\n", d.Type, d.Reason)
		}
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}
