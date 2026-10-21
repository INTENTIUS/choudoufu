// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// This file reads an IAM type's updater: the struct its constructor builds,
// and the parent id its GetResourceId returns from that struct (#2145). The
// IAM framework's create sets the id to GetResourceId() plus the binding, so
// that template, spelled in argument names, is the parent the row must
// build. The IdParseFunc says only what an import accepts.
//
// The constructor is the one function in the file named New...Updater (a
// hand-written updater) or ...IamUpdaterProducer (a generated one). Every
// IAM file at v8.6.0 has exactly one.

var (
	constructorRe = regexp.MustCompile(`func (?:New\w*Updater|\w+IamUpdaterProducer)\(d tpgresource\.TerraformResourceData`)

	// getOkValueRe is a generated producer's copy of one argument into its
	// values map, which GetImportIdQualifiers' captured groups then
	// overwrite by key: `if v, ok := d.GetOk("service_id"); ok {
	// values["serviceId"] = v.(string)`. The key is the regex group name,
	// camelCase where the API's field is.
	getOkValueRe = regexp.MustCompile(`if v, ok := d\.GetOk\("([a-z0-9_]+)"\); ok \{\s*values\["(\w+)"\] = v\.\(string\)`)
	// valuesCloudRe is the same copy of a provider default: `values["project"] = project`.
	valuesCloudRe = regexp.MustCompile(`values\["(\w+)"\] = (project|region|zone|location)\n`)
	// cloudVarRe declares one of those defaults: `project, err := tpgresource.GetProject(d, config)`.
	cloudVarRe = regexp.MustCompile(`\b(project|region|zone|location), (?:err|_) :?= tpgresource\.Get(Project|Region|Zone|Location)\(d, config\)`)

	resourceIDRe   = regexp.MustCompile(`(?s)func \(u \*(\w+)\) GetResourceId\(\) string \{\s*return (.*?)\n\}`)
	structFieldRe  = regexp.MustCompile(`(?m)^\s*(\w+):\s*(.+?),\s*$`)
	valuesFieldRe  = regexp.MustCompile(`^values\["(\w+)"\]$`)
	getFieldRe     = regexp.MustCompile(`^d\.Get\("([a-z0-9_]+)"\)\.\(string\)$`)
	sprintfCallRe  = regexp.MustCompile(`(?s)^fmt\.Sprintf\(("(?:[^"\\]|\\.)*"|\w+), (.*)\)$`)
	structMethodRe = regexp.MustCompile(`(?s)^(\w+)\{(.*)\}\.(\w+)\(\)$`)
	bareFieldRe    = regexp.MustCompile(`^u\.(\w+)$`)
	// helperGetRe is a struct field set from one argument passed through
	// a function of the package: `folderId:
	// CanonicalFolderId(d.Get("folder").(string))`.
	helperGetRe = regexp.MustCompile(`^(\w+)\(d\.Get\("([a-z0-9_]+)"\)\.\(string\)\)$`)

	// The IdParseFunc shapes a hand-written updater uses instead of
	// GetImportIdQualifiers: ParseImportId over regexes, or a parse helper
	// whose result's fields are d.Set onto arguments and whose method
	// becomes the id.
	parseImportCallRe = regexp.MustCompile(`tpgresource\.ParseImportId\(\[\]string\{("(?:[^"\\]|\\.)*")`)
	helperCallRe      = regexp.MustCompile(`\b(\w+), err := (?:tpgresource\.)?(\w+)\(((?:"[^"]*", )?)d\.Id\(\)`)
	setFromFieldRe    = regexp.MustCompile(`d\.Set\("([a-z0-9_]+)", (\w+)\.(\w+)\)`)
	setIDMethodRe     = regexp.MustCompile(`d\.SetId\((\w+)\.(\w+)\(\)\)`)
)

// funcBody is the text of the function starting at loc, up to its closing
// brace at column zero.
func funcBody(text string, start int) string {
	body := text[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end+2]
	}
	return body
}

// iamKeyArgs maps each key of a generated producer's values map to the
// argument or provider default it is read from.
func iamKeyArgs(body string) map[string]string {
	out := map[string]string{}
	for _, m := range getOkValueRe.FindAllStringSubmatch(body, -1) {
		out[m[2]] = m[1]
	}
	cloud := map[string]bool{}
	for _, m := range cloudVarRe.FindAllStringSubmatch(body, -1) {
		if strings.ToLower(m[2]) == m[1] {
			cloud[m[1]] = true
		}
	}
	for _, m := range valuesCloudRe.FindAllStringSubmatch(body, -1) {
		if cloud[m[2]] {
			out[m[1]] = m[2]
		}
	}
	return out
}

// renameGroups respells each `(?P<key>` of a regex as the argument the key
// is read from. A group's name is only a label: renaming it changes
// nothing a match captures, and lets a captured value be matched to the
// row's component by the argument that component reads. A group whose key
// the updater's struct never reads (read non-nil and without it) loses its
// name, since the provider discards what it captures: IAP's
// `(?P<version>[^/]+)` beside a struct that reads values["versionId"].
func renameGroups(re string, keys map[string]string, read map[string]bool) string {
	return groupNameRe.ReplaceAllStringFunc(re, func(g string) string {
		k := groupNameRe.FindStringSubmatch(g)[1]
		if read != nil && !read[k] {
			return "("
		}
		if arg, ok := keys[k]; ok {
			return "(?P<" + arg + ">"
		}
		return g
	})
}

var groupNameRe = regexp.MustCompile(`\(\?P<(\w+)>`)

// iamReadKeys is every values-map key the updater struct built in body
// reads, or nil when body builds none from a values map.
func iamReadKeys(body string) map[string]bool {
	var out map[string]bool
	for _, m := range regexp.MustCompile(`(?m)^\s*\w+:\s*values\["(\w+)"\],\s*$`).FindAllStringSubmatch(body, -1) {
		if out == nil {
			out = map[string]bool{}
		}
		out[m[1]] = true
	}
	return out
}

// updaterResourceID is the parent id GetResourceId returns, as a template
// over the arguments the constructor reads, or "" and the first piece
// that is no argument read as written: a parse helper's result, a computed
// name.
//
// A piece passed through a canonicalising helper of the package is read
// when the helper is one of two shapes, and anything else stays unread:
// one that returns its argument unchanged (cloudbilling's
// canonicalBillingAccountId), and one that adds a fixed prefix the value
// lacks (resourcemanager's CanonicalFolderId, "folders/"), which prefixes
// records by argument.
func updaterResourceID(text, pkg string) (tmpl, unread string, prefixes map[string]string) {
	rid := resourceIDRe.FindAllStringSubmatch(text, -1)
	if len(rid) != 1 {
		return "", "", nil
	}
	typ, ret := rid[0][1], strings.TrimSpace(rid[0][2])
	ctors := constructorRe.FindAllStringIndex(text, -1)
	if len(ctors) != 1 {
		return "", "", nil
	}
	body := funcBody(text, ctors[0][0])
	lit := strings.Index(body, "&"+typ+"{")
	if lit < 0 {
		return "", "", nil
	}
	litBody := body[lit:]
	if end := strings.Index(litBody, "\n\t}"); end >= 0 {
		litBody = litBody[:end]
	}
	keys := iamKeyArgs(body)
	cloud := map[string]bool{}
	for _, m := range cloudVarRe.FindAllStringSubmatch(body, -1) {
		if strings.ToLower(m[2]) == m[1] {
			cloud[m[1]] = true
		}
	}
	fields := map[string]string{}
	exprs := map[string]string{}
	for _, m := range structFieldRe.FindAllStringSubmatch(litBody, -1) {
		name, expr := m[1], m[2]
		exprs[name] = expr
		switch {
		case valuesFieldRe.MatchString(expr):
			if arg, ok := keys[valuesFieldRe.FindStringSubmatch(expr)[1]]; ok {
				fields[name] = arg
			}
		case getFieldRe.MatchString(expr):
			fields[name] = getFieldRe.FindStringSubmatch(expr)[1]
		case cloud[expr]:
			fields[name] = expr
		case helperGetRe.MatchString(expr):
			m := helperGetRe.FindStringSubmatch(expr)
			prefix, ok := canonicalHelper(pkg, m[1])
			if !ok {
				break
			}
			fields[name] = m[2]
			if prefix != "" {
				if prefixes == nil {
					prefixes = map[string]string{}
				}
				prefixes[m[2]] = prefix
			}
		}
	}
	field := func(f string) (string, bool) {
		if arg, ok := fields[f]; ok {
			return "{{" + arg + "}}", true
		}
		if unread == "" {
			e := exprs[f]
			if e == "" {
				e = "nothing"
			}
			unread = "u." + f + " is set from " + e
		}
		return "", false
	}

	switch {
	case bareFieldRe.MatchString(ret):
		v, ok := field(bareFieldRe.FindStringSubmatch(ret)[1])
		if !ok {
			return "", unread, nil
		}
		return v, "", prefixes
	case sprintfCallRe.MatchString(ret):
		m := sprintfCallRe.FindStringSubmatch(ret)
		out, ok := fillSprintf(m[1], m[2], "", func(a string) (string, bool) {
			f, ok := strings.CutPrefix(a, "u.")
			if !ok {
				return "", false
			}
			return field(f)
		}, nil)
		if !ok {
			return "", unread, nil
		}
		return out, "", prefixes
	case structMethodRe.MatchString(ret):
		m := structMethodRe.FindStringSubmatch(ret)
		sfields := map[string]string{}
		for _, f := range structFieldRe.FindAllStringSubmatch(m[2], -1) {
			uf, ok := strings.CutPrefix(f[2], "u.")
			if !ok {
				return "", "", nil
			}
			v, ok := field(uf)
			if !ok {
				return "", unread, nil
			}
			sfields[f[1]] = v
		}
		out, ok := methodTemplate(pkg, m[1], m[3], func(f string) (string, bool) {
			v, ok := sfields[f]
			return v, ok
		})
		if !ok {
			return "", "", nil
		}
		return out, "", prefixes
	}
	return "", "", nil
}

// fillSprintf renders a fmt.Sprintf call whose verbs are all %s, each
// argument through arg. format is a Go string literal, or a constant name
// looked up in consts.
func fillSprintf(format, args, recv string, arg func(string) (string, bool), consts map[string]string) (string, bool) {
	f, err := strconv.Unquote(format)
	if err != nil {
		v, ok := consts[format]
		if !ok {
			return "", false
		}
		f = v
	}
	parts := strings.Split(args, ",")
	if strings.Count(f, "%") != len(parts) || strings.Count(f, "%s") != len(parts) {
		return "", false
	}
	for _, a := range parts {
		a = strings.TrimSpace(a)
		if recv != "" {
			var ok bool
			if a, ok = strings.CutPrefix(a, recv+"."); !ok {
				return "", false
			}
		}
		v, ok := arg(a)
		if !ok {
			return "", false
		}
		f = strings.Replace(f, "%s", v, 1)
	}
	return f, true
}

// methodTemplate renders the method typ.method, whose body ends in a
// `return fmt.Sprintf(...)` over the receiver's fields, through field.
// Constants named as the format are looked up in the same text.
func methodTemplate(text, typ, method string, field func(string) (string, bool)) (string, bool) {
	re := regexp.MustCompile(`func \((\w+) \*?` + regexp.QuoteMeta(typ) + `\) ` + regexp.QuoteMeta(method) + `\(\) string \{`)
	loc := re.FindAllStringSubmatchIndex(text, -1)
	if len(loc) != 1 {
		return "", false
	}
	recv := text[loc[0][2]:loc[0][3]]
	body := funcBody(text, loc[0][0])
	ret := regexp.MustCompile(`(?m)^\treturn (fmt\.Sprintf\(.*\))$`).FindAllStringSubmatch(body, -1)
	if len(ret) == 0 {
		return "", false
	}
	m := sprintfCallRe.FindStringSubmatch(ret[len(ret)-1][1])
	if m == nil {
		return "", false
	}
	return fillSprintf(m[1], m[2], recv, field, goConsts(text))
}

// goConsts maps each `Name = "value"` constant of the text.
func goConsts(text string) map[string]string {
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\t(\w+)\s+= ("(?:[^"\\]|\\.)*")$`).FindAllStringSubmatch(text, -1) {
		if v, err := strconv.Unquote(m[2]); err == nil {
			out[m[1]] = v
		}
	}
	return out
}

// handParseTemplate is the parent an import gives back through a
// hand-written IdParseFunc: the first ParseImportId regex as a template, or
// the template of the method its d.SetId calls on a parse helper's result,
// over the arguments the function d.Sets from that result's fields.
// helpers is the text the helper and its result type are looked up in.
func handParseTemplate(body, helpers string) string {
	if m := parseImportCallRe.FindStringSubmatch(body); m != nil {
		re, err := strconv.Unquote(m[1])
		if err != nil {
			return ""
		}
		tmpl, _ := regexToTemplate(re)
		return tmpl
	}
	call := helperCallRe.FindAllStringSubmatch(body, -1)
	set := setIDMethodRe.FindAllStringSubmatch(body, -1)
	if len(call) != 1 || len(set) != 1 || set[0][1] != call[0][1] {
		return ""
	}
	v, helper, lead := call[0][1], call[0][2], strings.TrimSuffix(call[0][3], ", ")
	ret := regexp.MustCompile(`func `+regexp.QuoteMeta(helper)+`\([^)]*\) \(\*?(\w+), error\)`).FindAllStringSubmatch(helpers, -1)
	if len(ret) != 1 {
		return ""
	}
	args := map[string]string{}
	for _, m := range setFromFieldRe.FindAllStringSubmatch(body, -1) {
		if m[2] == v {
			args[m[3]] = "{{" + m[1] + "}}"
		}
	}
	tmpl, ok := methodTemplate(helpers, ret[0][1], set[0][2], func(f string) (string, bool) {
		if a, ok := args[f]; ok {
			return a, true
		}
		// The resource type a tpgresource field-value parser is
		// handed as its first argument, "datasets".
		if strings.EqualFold(f, "resourceType") && lead != "" {
			if s, err := strconv.Unquote(lead); err == nil {
				return s, true
			}
		}
		return "", false
	})
	if !ok {
		return ""
	}
	return tmpl
}

var (
	parseQualifiersRe = regexp.MustCompile(`GetImportIdQualifiers\(\[\]string\{((?:"(?:[^"\\]|\\.)*"(?:, )?)*)\}, d, config, d\.Id\(\)\)`)
	valuesAssignRe    = regexp.MustCompile(`\bvalues\["(\w+)"\] = `)
)

// parseUnsetKeys are the values-map keys a generated IdParseFunc's updater
// struct reads that nothing in the function sets: no named group of any of
// its GetImportIdQualifiers regexes captures them (the provider defaults
// GetImportIdQualifiers adds are named groups of the first regex too), and
// no `values["key"] = ` assigns them. Such a key is "" after every import,
// so the parent the import rebuilds is not the one the create used (#2145
// item 7). Nil when the function is not that shape.
func parseUnsetKeys(body string) []string {
	m := parseQualifiersRe.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	read := iamReadKeys(body)
	if read == nil {
		return nil
	}
	set := map[string]bool{}
	for _, re := range unquoteAll(m[1]) {
		for _, g := range groupNameRe.FindAllStringSubmatch(re, -1) {
			set[g[1]] = true
		}
	}
	for _, a := range valuesAssignRe.FindAllStringSubmatch(body, -1) {
		set[a[1]] = true
	}
	var out []string
	for k := range read {
		if !set[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// canonicalHelper reads the package function name of one string argument,
// and says whether it is the identity (prefix "") or adds prefix to a
// value that lacks it:
//
//	func CanonicalFolderId(folder string) string {
//		if strings.HasPrefix(folder, "folders/") {
//			return folder
//		}
//
//		return "folders/" + folder
//	}
//
// ok is false for any other body, or when the package does not define
// name exactly once.
func canonicalHelper(pkg, name string) (prefix string, ok bool) {
	re := regexp.MustCompile(`(?s)\nfunc ` + regexp.QuoteMeta(name) + `\((\w+) string\) string \{\n(.*?)\n\}\n`)
	ms := re.FindAllStringSubmatch(pkg, -1)
	if len(ms) != 1 {
		return "", false
	}
	param, body := regexp.QuoteMeta(ms[0][1]), ms[0][2]
	if regexp.MustCompile(`^\s*return ` + param + `\s*$`).MatchString(body) {
		return "", true
	}
	m := regexp.MustCompile(`^\s*if strings\.HasPrefix\(` + param + `, "([^"\\]+)"\) \{\s*return ` + param + `\s*\}\s*return "([^"\\]+)" \+ ` + param + `\s*$`).FindStringSubmatch(body)
	if m == nil || m[1] != m[2] {
		return "", false
	}
	return m[1], true
}
