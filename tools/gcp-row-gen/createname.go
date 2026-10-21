// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// A generated Create whose id embeds a name the API returns can still send
// the name's last segment itself, as the create request's resource id
// field (#2145 item 3): Google's create methods take the new resource's id
// as `<resource>Id` beside the parent, and use it as the last segment of
// the resource's name. The provider sends it in one of two places:
//
//	url, err := tpgresource.ReplaceVars(d, config, transport_tpg.BaseUrl(Product, config)+"projects/{{project}}/locations/{{location}}/generators?generatorId={{generator_id}}")
//
// or in the body its encoder wraps the resource in:
//
//	templateIdProp, ok := d.GetOk("template_id")
//	if ok && templateIdProp != nil {
//		newObj["templateId"] = templateIdProp
//	}
//
// Either way the name is the create URL's collection path, a "/", and the
// argument's value, and the argument fixes it whenever it is set.
var (
	createFuncRe = regexp.MustCompile(`(?s)\nfunc (resource\w+)Create\(d \*schema\.ResourceData, meta interface\{\}\) error \{\n(.*?)\n\}\n`)
	createURLRe  = regexp.MustCompile(`\burl, err := tpgresource\.ReplaceVars\(d, config, transport_tpg\.BaseUrl\(Product, config\)\+"([^"]+)"\)`)
	queryIDRe    = regexp.MustCompile(`^(\w+Id)=\{\{([a-z0-9_]+)\}\}$`)

	// apiVersionRe is a version segment the create URL carries ahead of
	// the resource path because the product's base URL stops at the host
	// (monitoring's "v3/"). The resource name starts after it.
	apiVersionRe = regexp.MustCompile(`^v\d+(?:p\d+)?(?:alpha\d*|beta\d*)?/`)
)

// readCreateName records [Source.CreateCollection], [Source.CreateNameParam],
// [Source.CreateNameArg] and [Source.NameFlatten] for a generated resource
// file. Nothing is recorded unless the file has exactly one Create, its
// first create URL is a ReplaceVars template, and one id field it sends
// names the collection's own resource, from an argument the id format does
// not already name.
func readCreateName(s *Source, text string) {
	fns := createFuncRe.FindAllStringSubmatch(text, -1)
	if len(fns) != 1 {
		return
	}
	prefix, body := fns[0][1], fns[0][2]
	u := createURLRe.FindStringSubmatch(body)
	if u == nil {
		return
	}
	path, query, _ := strings.Cut(u[1], "?")
	path = apiVersionRe.ReplaceAllString(path, "")
	resource := collectionResource(path)
	if resource == "" {
		return
	}
	param, arg := "", ""
	for _, kv := range strings.Split(query, "&") {
		if m := queryIDRe.FindStringSubmatch(kv); m != nil && namesResource(m[1], resource) {
			if param != "" {
				return
			}
			param, arg = m[1], m[2]
		}
	}
	if param == "" {
		param, arg = encoderNameField(text, prefix, body, resource)
	}
	if param == "" || containsStr(placeholders(s.IDFormat), arg) {
		// No id field, or one the id format already names: the id is
		// configuration's either way, and there is nothing to record.
		return
	}
	s.CreateCollection, s.CreateNameParam, s.CreateNameArg = path, param, arg
	for _, name := range placeholders(s.IDFormat) {
		if f := nameFlatten(text, prefix, name); f != "" {
			if s.NameFlatten == nil {
				s.NameFlatten = map[string]string{}
			}
			s.NameFlatten[name] = f
		}
	}
}

// encoderNameField is the id field a Create's encoder copies an argument
// into, when the Create calls exactly that encoder and it sets exactly one
// field naming resource.
func encoderNameField(text, prefix, createBody, resource string) (param, arg string) {
	if !strings.Contains(createBody, "obj, err = "+prefix+"Encoder(d, meta, obj)") {
		return "", ""
	}
	loc := regexp.MustCompile(`\nfunc ` + regexp.QuoteMeta(prefix) + `Encoder\(`).FindStringIndex(text)
	if loc == nil {
		return "", ""
	}
	enc := funcBody(text, loc[0]+1)
	re := regexp.MustCompile(`(\w+) *, ok := d\.GetOk\("([a-z0-9_]+)"\)\s*if ok && (\w+) != nil \{\s*newObj\["(\w+Id)"\] = (\w+)\s*\}`)
	for _, m := range re.FindAllStringSubmatch(enc, -1) {
		if m[1] != m[3] || m[1] != m[5] || !namesResource(m[4], resource) {
			continue
		}
		if param != "" {
			return "", ""
		}
		param, arg = m[4], m[2]
	}
	return param, arg
}

// nameFlatten is how the file's flatten function for placeholder name
// keeps the API's value: "whole" for `return v`, "last-segment" for
// tpgresource.GetResourceNameFromSelfLink. Anything else is "".
func nameFlatten(text, prefix, name string) string {
	fn := "flatten" + strings.TrimPrefix(prefix, "resource") + camel(name)
	loc := regexp.MustCompile(`\nfunc ` + regexp.QuoteMeta(fn) + `\(v interface\{\}, d \*schema\.ResourceData, config \*transport_tpg\.Config\) interface\{\} \{\n`).FindStringIndex(text)
	if loc == nil {
		return ""
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(funcBody(text, loc[1])), "}"))
	switch body {
	case "return v":
		return NameWhole
	case "if v == nil {\n\t\treturn v\n\t}\n\treturn tpgresource.GetResourceNameFromSelfLink(v.(string))":
		return NameLastSegment
	}
	return ""
}

// The two ways a flatten function keeps a name ([Source.NameFlatten]).
const (
	NameWhole       = "whole"
	NameLastSegment = "last-segment"
)

// collectionResource is the singular of a create path's last literal
// segment, "serviceLevelObjectives" giving "serviceLevelObjective". A path
// that ends in a placeholder (firestore's documents/{{collection}}) is
// named by the literal segment before it. "" when there is none.
func collectionResource(path string) string {
	segs := strings.Split(path, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		seg := segs[i]
		if seg == "" || strings.Contains(seg, "{{") {
			continue
		}
		switch {
		case strings.HasSuffix(seg, "ies"):
			return strings.TrimSuffix(seg, "ies") + "y"
		case strings.HasSuffix(seg, "s"):
			return strings.TrimSuffix(seg, "s")
		}
		return ""
	}
	return ""
}

// namesResource reports whether the id field param ("templateId",
// "appVersionId") names resource ("inspectTemplate", "version"): with the
// "Id" dropped, one of the two is the other or ends in it at a camel-case
// word boundary. A parent's id ("parentId" on firewallPolicies) does not.
func namesResource(param, resource string) bool {
	stem, ok := strings.CutSuffix(param, "Id")
	if !ok || stem == "" {
		return false
	}
	return camelSuffix(stem, resource) || camelSuffix(resource, stem)
}

// camelSuffix reports whether short is long, or the trailing camel-case
// words of long, ignoring the case of short's first letter.
func camelSuffix(long, short string) bool {
	if strings.EqualFold(long[:1], short[:1]) && long[1:] == short[1:] {
		return true
	}
	upper := strings.ToUpper(short[:1]) + short[1:]
	return strings.HasSuffix(long, upper) && len(long) > len(upper)
}

func camel(name string) string {
	var b strings.Builder
	for _, part := range strings.Split(name, "_") {
		if part == "" {
			continue
		}
		r := []rune(part)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	return b.String()
}

// createNamed is the row of a type whose id embeds one placeholder the API
// fills in (computed), named only because the Create can send that name's
// last segment itself ([readCreateName]). ok is false, with why, when the
// source does not settle the name from configuration:
//
//   - the id is the computed placeholder whole ("{{name}}") and its flatten
//     keeps the API's value as is: the name is the collection path, "/",
//     and the argument. The API writes the path back, so the project in it
//     is the one the API spells, and the row is kept only where Google's
//     Asset names page spells that service's project as an ID
//     ([apiSpellsProjectID]);
//   - the id is the collection path, "/", and the computed placeholder,
//     whose flatten keeps the last path segment: the argument is that
//     segment, and the path is configuration's own.
//
// The argument's component is server-assigned when absent: an instance
// that leaves it unset sends no id, and the API names the object.
func createNamed(d Decision, src Source, tfs []TypeFacts, minted []string, names AssetNames, unrowed unrowedFunc) (Decision, string) {
	if src.CreateNameArg == "" {
		return d, ""
	}
	why := func(format string, args ...any) (Decision, string) {
		lead := fmt.Sprintf("The create sends {{%s}} as %s, which names the object, but ", src.CreateNameArg, src.CreateNameParam)
		return d, lead + strings.TrimSuffix(fmt.Sprintf(format, args...), ".")
	}
	if len(minted) != 1 {
		return why("the id embeds %s, more than one value the API assigns", joinQuoted(minted))
	}
	p := minted[0]
	switch f := attrFlags(tfs, src.CreateNameArg); f {
	case "R", "O", "OC":
	default:
		return why("{{%s}} is no argument of the schema (%q)", src.CreateNameArg, f)
	}
	arg := "{{" + src.CreateNameArg + "}}"
	var name string
	switch {
	case src.IDFormat == "{{"+p+"}}" && src.NameFlatten[p] == NameWhole:
		name = src.CreateCollection + "/" + arg
		if ok, reason := apiSpellsProjectID(src, name, names); !ok {
			return why("%s", reason)
		}
		if !importsWhole(src, p, name) {
			return why("the importer is not read as taking the whole name as {{%s}}", p)
		}
	case src.IDFormat == src.CreateCollection+"/{{"+p+"}}" && src.NameFlatten[p] == NameLastSegment:
		name = arg
		if src.ImportIDFormat != src.IDFormat {
			return why("the importer's id %q is not the id format", src.ImportIDFormat)
		}
	default:
		return why("the id format %s is neither {{%s}} kept whole nor the create path %s with {{%s}}'s last segment", src.IDFormat, p, src.CreateCollection, p)
	}
	s := src
	s.CreateNameArg = ""
	s.ImportIDFormat = src.IDFormat
	s.Derived = map[string]string{p: name}
	for k, v := range src.Derived {
		if k != p {
			s.Derived[k] = v
		}
	}
	nd := decideResource(d, s, tfs, names, unrowed)
	if nd.Verdict != VerdictRowed {
		return why("%s", nd.Reason)
	}
	for i := range nd.Row.Components {
		c := &nd.Row.Components[i]
		if len(c.Attrs) == 1 && c.Attrs[0] == src.CreateNameArg && attrFlags(tfs, src.CreateNameArg) != "R" {
			c.ServerAssignedIfAbsent = true
		}
	}
	nd.CreateNamed = src.CreateNameArg
	return nd, ""
}

// apiSpellsProjectID reports whether a name the API writes back, tmpl,
// carries a project as the ID configuration holds. A name with no project
// in it, and no placeholder standing for a whole parent name that could
// hold one, is settled already. Otherwise Google's Asset names page is
// asked, under the type's api_service: a documented name lining up with
// tmpl settles it, and failing that every project the page documents under
// that service must be spelled {PROJECT_ID}. A {PROJECT_NUMBER} anywhere
// there is a project number, which is not the project ID the provider's
// project argument holds.
func apiSpellsProjectID(src Source, tmpl string, names AssetNames) (bool, string) {
	if !mayCarryProject(tmpl) {
		return true, ""
	}
	host := src.APIService
	pages := pagePaths(names, host)
	for _, path := range pages {
		if ok, number := alignPage(path, tmpl); ok {
			if number {
				return false, fmt.Sprintf("the API writes the name back, and Google documents it as //%s/%s; a project number is not the project ID the provider's project argument holds", host, path)
			}
			return true, ""
		}
	}
	ids := 0
	for _, path := range pages {
		for _, seg := range strings.Split(path, "/") {
			if v := docsVarSegment.FindStringSubmatch(seg); v != nil && v[1] == docsProjectNumber {
				return false, fmt.Sprintf("the API writes the name back, and Google documents %s's names with a project number (//%s/%s), which is not the project ID the provider's project argument holds", host, host, path)
			}
		}
		if strings.Contains(path, "projects/{PROJECT_ID}") {
			ids++
		}
	}
	if ids == 0 {
		return false, fmt.Sprintf("the API writes the name back, and Google documents no name under %s that shows whether it spells the project as an ID or a number", host)
	}
	return true, ""
}

// mayCarryProject reports whether a name template has a project in it, or
// a placeholder that is no single segment after a literal one and so may
// stand for a whole parent name ({{parent}}/muteConfigs/...).
func mayCarryProject(tmpl string) bool {
	if strings.Contains(tmpl, "projects/{{") {
		return true
	}
	segs := strings.Split(tmpl, "/")
	for i, seg := range segs {
		if strings.HasPrefix(seg, "{{") && (i == 0 || strings.Contains(segs[i-1], "{{")) {
			return true
		}
	}
	return false
}

// importsWhole reports whether the importer gives an instance whose import
// ID is the whole name, tmpl, that name as its {{p}} and its id: it takes
// the whole import ID as p ([Source.ImportWhole], [Source.ImportIDSet]),
// rebuilds p from a template that is tmpl ([Source.ImportNameSet]), or is
// the generated importer over the id format itself.
func importsWhole(src Source, p, tmpl string) bool {
	switch {
	case src.ImportWhole == p, src.ImportIDSet == p:
		return true
	case src.ImportNameSet != nil:
		return src.ImportNameSet.Attr == p && src.ImportNameSet.Template == tmpl
	}
	return src.ImportIDFormat == src.IDFormat
}
