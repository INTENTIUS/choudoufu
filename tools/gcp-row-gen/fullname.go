// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// Importer shapes the generated importRe does not read (#2145 item 5). Each
// is read inside the importer function only, so a match never spans into
// another function of the file.
var (
	importerFuncRe = regexp.MustCompile(`(?s)\nfunc resource\w+Import\(d \*schema\.ResourceData, meta interface\{\}\) \(\[\]\*schema\.ResourceData, error\) \{\n(.*?)\n\}\n`)

	// importWholeRe is a sole regex that takes the whole import ID into one
	// attribute: ParseImportId([]string{"(?P<name>.+)"}, d, config).
	importWholeRe = regexp.MustCompile(`tpgresource\.ParseImportId\(\[\]string\{"\(\?P<([a-z0-9_]+)>\.\+\)"\}, d, config\)`)

	// importNameSetRe is ParseImportId, then d.Set(attr, "template"), then
	// the id made from that attribute's value.
	importNameSetRe = regexp.MustCompile(`(?s)tpgresource\.ParseImportId\(\[\]string\{(.*?)\}, d, config\).*?d\.Set\("([a-z0-9_]+)", "([^"]+)"\).*?\bid, err := tpgresource\.ReplaceVars\(d, config, d\.Get\("([a-z0-9_]+)"\)\.\(string\)\)`)

	importPassthroughRe = regexp.MustCompile(`Importer: &schema\.ResourceImporter\{\s*State: schema\.ImportStatePassthrough,`)
)

// readImporterShapes records the importer shapes [Source.ImportWhole],
// [Source.ImportNameSet] and [Source.ImportPassthrough] name.
func readImporterShapes(s *Source, text string) {
	s.ImportPassthrough = importPassthroughRe.MatchString(text)
	m := importerFuncRe.FindStringSubmatch(text)
	if m == nil {
		return
	}
	body := m[1]
	if w := importWholeRe.FindStringSubmatch(body); w != nil {
		s.ImportWhole = w[1]
	}
	if n := importNameSetRe.FindStringSubmatch(body); n != nil && n[2] == n[4] {
		ns := &ImportNameSet{Attr: n[2], Template: n[3]}
		for _, q := range quotedRe.FindAllStringSubmatch(n[1], -1) {
			ns.Regexes = append(ns.Regexes, q[1])
		}
		s.ImportNameSet = ns
	}
}

// pagePaths are the paths after the host of every Asset name format the
// page lists under host, in asset type order.
func pagePaths(names AssetNames, host string) []string {
	var out []string
	for _, t := range sortedKeys(names.Types) {
		for _, f := range names.Types[t] {
			rest, _ := strings.CutPrefix(f, "//")
			h, path, ok := strings.Cut(rest, "/")
			if ok && h == host && path != "" {
				out = append(out, path)
			}
		}
	}
	return out
}

// alignPage is [alignDocumented] that reports a project number apart: ok
// when the page's path and tmpl line up segment by segment, number when the
// page puts {PROJECT_NUMBER} against one of tmpl's placeholders.
func alignPage(path, tmpl string) (ok, number bool) {
	ds, ts := strings.Split(path, "/"), strings.Split(tmpl, "/")
	if len(ds) != len(ts) {
		return false, false
	}
	for i := range ds {
		dv := docsVarSegment.FindStringSubmatch(ds[i])
		tp := placeholderRe.FindStringSubmatch(ts[i])
		switch {
		case dv == nil && tp == nil:
			if ds[i] != ts[i] {
				return false, false
			}
		case dv != nil && tp != nil && tp[0] == ts[i] && tp[1] == "":
			if dv[1] == docsProjectNumber {
				number = true
			}
		default:
			return false, false
		}
	}
	return true, number
}

// decideFullName decides a type whose Asset name is one attribute whole,
// "//apihub.googleapis.com/{{name}}", when no import form is that
// attribute. The importer's source says what shape the attribute has:
//
//   - it sets the attribute to a template after parsing the import ID,
//     and takes the attribute's value as the id ([Source.ImportNameSet]):
//     the shape is that template, and the import form is the regex that
//     parses it;
//   - it takes the whole import ID as the attribute ([Source.ImportWhole]):
//     the shapes are the forms its Import section lists.
//
// A shape is used only when an Asset name Google documents under the
// type's host lines up with it, and never when that name carries a project
// number where the shape has the project. ok is false when the importer
// has neither shape, and d is then unchanged.
func decideFullName(d AssetDecision, src Source, host, path string, names AssetNames) (AssetDecision, bool) {
	ph := placeholders(path)
	if len(ph) != 1 || path != "{{"+ph[0]+"}}" {
		return d, false
	}
	attr := ph[0]
	var shapes []identity.GoogleImportFormat
	var origin string
	switch {
	case src.ImportNameSet != nil && src.ImportNameSet.Attr == attr:
		ns := src.ImportNameSet
		origin = fmt.Sprintf("the importer sets %s to %q", attr, ns.Template)
		for _, re := range ns.Regexes {
			if tmpl, ok := regexToTemplate(re); ok && tmpl == ns.Template {
				shapes = append(shapes, identity.GoogleImportFormat{Template: tmpl, Regex: re})
				break
			}
		}
		if len(shapes) == 0 {
			d.Reason = origin + ", which none of its ParseImportId regexes parses"
			return d, true
		}
	case src.ImportWhole == attr:
		origin = fmt.Sprintf("the importer takes the whole import ID as %s", attr)
		for _, tmpl := range src.DocImportFormats {
			if len(placeholders(tmpl)) > 0 && !strings.Contains(tmpl, "{{%") {
				shapes = append(shapes, identity.GoogleImportFormat{Template: tmpl})
			}
		}
	default:
		return d, false
	}
	pages := pagePaths(names, host)
	numbered := ""
	for _, s := range shapes {
		for _, p := range pages {
			ok, number := alignPage(p, s.Template)
			switch {
			case ok && !number:
				d.Verdict = AssetAsIs
				d.Row = identity.GoogleAssetImport{Host: host, Asset: s.Template, Import: s}
				d.Reason = ""
				return d, true
			case ok && numbered == "":
				numbered = p
			}
		}
	}
	if numbered != "" {
		d.Reason = fmt.Sprintf("%s, and Google documents its Asset name as //%s/%s; a project number is not the project ID the provider's project argument holds", origin, host, numbered)
		return d, true
	}
	d.Reason = fmt.Sprintf("%s, and no Asset name Google documents under %s has that shape", origin, host)
	return d, true
}

// passthroughReason says why a type whose importer is
// schema.ImportStatePassthrough and whose Create names the object by no
// ReplaceVars template has no import form for its Asset name: the import ID
// is the id alone, which the page does not say is any one segment of the
// name, and the importer drops the name's other parts for the provider's
// defaults. ok is false for any other importer.
func passthroughReason(src Source, assetNames []string) (string, bool) {
	if !src.ImportPassthrough || src.IDFormat != "" || len(assetNames) == 0 {
		return "", false
	}
	last := ""
	if segs := strings.Split(assetNames[0], "/"); len(segs) > 0 {
		last = segs[len(segs)-1]
	}
	return fmt.Sprintf("the importer is schema.ImportStatePassthrough and Create sets the id from no ReplaceVars template, so the import ID is the id alone. Google documents the Asset name as %s, and only a real Asset response shows whether %s is the value Create sets. The importer would also drop the name's other parts for the provider's defaults", strings.Join(assetNames, " or "), last), true
}
