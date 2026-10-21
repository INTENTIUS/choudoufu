// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"reflect"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// ImportIDsGoRel is the generated import-ID tables (INTENTIUS/choudoufu#2139
// items 3 and 7).
const ImportIDsGoRel = "internal/live/identity/importids_generated_google.go"

// AssetVerdict is how a labelled type's Cloud Asset Inventory name becomes
// an import ID.
type AssetVerdict string

const (
	// AssetAsIs: the importer accepts the Asset path unchanged.
	AssetAsIs AssetVerdict = "as-is"
	// AssetRewritten: the Asset path's parts go into another form the
	// importer accepts.
	AssetRewritten AssetVerdict = "rewritten"
	// AssetUnknown: the source at the pin does not say, and a hit is
	// refused by name.
	AssetUnknown AssetVerdict = "unknown"
)

// AssetDecision is one labelled type's verdict.
type AssetDecision struct {
	Type    string
	Verdict AssetVerdict
	Row     identity.GoogleAssetImport
	Reason  string
}

// CompositeDecision is one unrowed composite-identity type's import forms,
// or why it has none.
type CompositeDecision struct {
	Type    string
	Labels  bool
	Formats []identity.GoogleImportFormat
	Reason  string
}

// ImportIDCounts are the totals the generated file states.
type ImportIDCounts struct {
	Labelled, AsIs, Rewritten, Unknown           int
	GoogleLabelled, GoogleAsIs, GoogleRewritten  int
	GoogleUnknown                                int
	Documented, GoogleDocumented                 int
	Composite, Composed, CompositeRefused        int
	CompositeLabelled, CompositeLabelledComposed int
}

// sameSource returns the one source every provider serving t states, or
// false when they differ in any way the import tables read.
func sameSource(sources map[string]Source, facts map[string]TypeFacts) (Source, bool) {
	if len(sources) == 0 || len(sources) != len(facts) {
		return Source{}, false
	}
	var first Source
	for i, p := range sortedProviders(sources) {
		s := providerNeutral(sources[p])
		if i == 0 {
			first = s
			continue
		}
		if !reflect.DeepEqual(first, s) {
			return Source{}, false
		}
	}
	return first, true
}

// importCandidates are the forms t's importer accepts, in the order it
// tries them: its ParseImportId regexes, each with the template it parses,
// or, for an importer that is custom code, the forms its documentation
// lists whose every placeholder is an attribute of the type.
func importCandidates(src Source, tfs []TypeFacts) []identity.GoogleImportFormat {
	var out []identity.GoogleImportFormat
	if len(src.ImportFormats) > 0 {
		for _, re := range src.ImportFormats {
			if tmpl, ok := regexToTemplate(re); ok {
				out = append(out, identity.GoogleImportFormat{Template: tmpl, Regex: re})
			}
		}
		return out
	}
	for _, tmpl := range src.DocImportFormats {
		ok := len(placeholders(tmpl)) > 0
		for _, name := range placeholders(tmpl) {
			for _, tf := range tfs {
				if _, has := tf.Attrs[name]; !has {
					ok = false
				}
			}
		}
		if ok {
			out = append(out, identity.GoogleImportFormat{Template: tmpl})
		}
	}
	return out
}

func samePlaceholderSet(a, b string) bool {
	pa, pb := placeholders(a), placeholders(b)
	if len(pa) == 0 || len(pa) != len(pb) {
		return false
	}
	set := map[string]bool{}
	for _, n := range pa {
		set[n] = true
	}
	for _, n := range pb {
		if !set[n] {
			return false
		}
	}
	return true
}

// decideAsset reads one labelled type's Asset name and import forms.
//
// The Asset name is what the type's *_meta.yaml records under
// cai_asset_name_formats, else, for a Magic Modules type, the default
// Magic Modules computes when it records none (api/resource.go's
// GetCaiAssetNameTemplate at the commit that generated the pin):
// "//<api_service_name>/<id format>". A handwritten type with no recorded
// format has no Asset name in the source, and takes the one Google
// documents for its asset type instead ([decideDocumented]).
func decideAsset(t string, sources map[string]Source, facts map[string]TypeFacts, names AssetNames) AssetDecision {
	d := AssetDecision{Type: t, Verdict: AssetUnknown}
	src, ok := sameSource(sources, facts)
	if !ok {
		d.Reason = "google and google-beta state different sources for this type, or one states none"
		return d
	}
	var asset string
	switch {
	case len(src.CAINameFormats) == 1:
		asset = src.CAINameFormats[0]
	case len(src.CAINameFormats) > 1:
		d.Reason = "the meta file records several Asset name formats, so a hit's form is not one"
		return d
	case src.Generation == "mmv1" && src.APIService != "" && src.IDFormat != "":
		asset = "//" + src.APIService + "/" + src.IDFormat
	default:
		return decideDocumented(d, src, names)
	}
	rest, ok := strings.CutPrefix(asset, "//")
	host, path, ok2 := strings.Cut(rest, "/")
	if !ok || !ok2 || host == "" || path == "" {
		d.Reason = fmt.Sprintf("the Asset name format %q is not //host/path", asset)
		return d
	}
	if strings.Contains(path, "{{%") {
		d.Reason = "the Asset name format has a path-escaped part"
		return d
	}
	if len(placeholders(path)) == 0 {
		d.Reason = "the Asset name format names no part of the object"
		return d
	}
	d.Row = identity.GoogleAssetImport{Host: host, Asset: path}

	var tfs []TypeFacts
	for _, p := range sortedKeys(facts) {
		tfs = append(tfs, facts[p])
	}
	cands := importCandidates(src, tfs)
	for _, c := range cands {
		if c.Template == path {
			d.Verdict, d.Row.Import = AssetAsIs, c
			return d
		}
	}
	for _, c := range cands {
		if samePlaceholderSet(c.Template, path) {
			d.Verdict, d.Row.Import = AssetRewritten, c
			return d
		}
	}
	if fd, ok := decideFullName(d, src, host, path, names); ok {
		if fd.Verdict == AssetUnknown {
			fd.Row = identity.GoogleAssetImport{}
		}
		return fd
	}
	d.Row = identity.GoogleAssetImport{}
	if len(cands) == 0 {
		d.Reason = "the provider source states no import format for this type"
	} else {
		d.Reason = fmt.Sprintf("no import format carries exactly the parts of the Asset name %q", path)
	}
	return d
}

// decideComposite reads the import forms of an unrowed type whose identity
// schema requires two or more attributes, whose record carries only those
// components: each form whose parts are all identity attributes and include
// every required one.
func decideComposite(t string, labels bool, sources map[string]Source, facts map[string]TypeFacts) (CompositeDecision, bool) {
	var identityAttrs map[string]string
	for _, p := range sortedKeys(facts) {
		id := facts[p].Identity
		if identityAttrs == nil {
			identityAttrs = id
		} else if !reflect.DeepEqual(identityAttrs, id) {
			return CompositeDecision{Type: t, Labels: labels, Reason: "google and google-beta serve different identity schemas"}, requiredCount(identityAttrs) >= 2
		}
	}
	if requiredCount(identityAttrs) < 2 {
		return CompositeDecision{}, false
	}
	d := CompositeDecision{Type: t, Labels: labels}
	src, ok := sameSource(sources, facts)
	if !ok {
		d.Reason = "google and google-beta state different sources for this type, or one states none"
		return d, true
	}
	var tfs []TypeFacts
	for _, p := range sortedKeys(facts) {
		tfs = append(tfs, facts[p])
	}
	for _, c := range importCandidates(src, tfs) {
		ok := true
		have := map[string]bool{}
		for _, name := range placeholders(c.Template) {
			have[name] = true
			if _, isID := identityAttrs[name]; !isID {
				ok = false
			}
		}
		for name, flag := range identityAttrs {
			if flag == "R" && !have[name] {
				ok = false
			}
		}
		if ok && !strings.Contains(c.Template, "{{%") {
			d.Formats = append(d.Formats, c)
		}
	}
	if len(d.Formats) == 0 {
		switch {
		case src.Kind != "resource":
			d.Reason = "an IAM type's import ID is space-separated words, not a form its identity parts fill"
		case len(src.ImportFormats) == 0 && len(src.DocImportFormats) == 0:
			d.Reason = "the provider source states no import format for this type"
		default:
			d.Reason = "no import format is made of the identity schema's attributes"
		}
	}
	return d, true
}

func requiredCount(id map[string]string) int {
	n := 0
	for _, f := range id {
		if f == "R" {
			n++
		}
	}
	return n
}

// deriveImportIDs runs both decisions over every type.
func deriveImportIDs(ev Evidence, names AssetNames, byType map[string]map[string]TypeFacts, decisions []Decision) ([]AssetDecision, []CompositeDecision, ImportIDCounts, error) {
	var assets []AssetDecision
	var composites []CompositeDecision
	var c ImportIDCounts
	undocumented := map[string]Source{}
	for _, d := range decisions {
		inGoogle := containsStr(d.Providers, ProviderGoogle)
		if d.Labels {
			if src, ok := sameSource(ev.Types[d.Type], byType[d.Type]); ok && len(src.CAINameFormats) == 0 && (src.Generation != "mmv1" || src.APIService == "" || src.IDFormat == "") {
				undocumented[d.Type] = src
			}
			a := decideAsset(d.Type, ev.Types[d.Type], byType[d.Type], names)
			assets = append(assets, a)
			c.Labelled++
			if inGoogle {
				c.GoogleLabelled++
			}
			if a.Row.Documented {
				c.Documented++
				if inGoogle {
					c.GoogleDocumented++
				}
			}
			switch a.Verdict {
			case AssetAsIs:
				c.AsIs++
				if inGoogle {
					c.GoogleAsIs++
				}
			case AssetRewritten:
				c.Rewritten++
				if inGoogle {
					c.GoogleRewritten++
				}
			default:
				c.Unknown++
				if inGoogle {
					c.GoogleUnknown++
				}
			}
		}
		if d.Verdict != VerdictUnrowed {
			continue
		}
		cd, ok := decideComposite(d.Type, d.Labels, ev.Types[d.Type], byType[d.Type])
		if !ok {
			continue
		}
		composites = append(composites, cd)
		c.Composite++
		if d.Labels {
			c.CompositeLabelled++
		}
		if len(cd.Formats) > 0 {
			c.Composed++
			if d.Labels {
				c.CompositeLabelledComposed++
			}
		} else {
			c.CompositeRefused++
		}
	}
	if err := checkDocumentedAssets(undocumented, names); err != nil {
		return nil, nil, ImportIDCounts{}, err
	}
	return assets, composites, c, nil
}

func renderImportIDs(assets []AssetDecision, composites []CompositeDecision, c ImportIDCounts, names AssetNames) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Code generated by tools/gcp-row-gen. DO NOT EDIT.

package identity

`)
	fmt.Fprintf(&b, `// GoogleAssetImports says, for each labelled hashicorp/google and
// google-beta %s type whose import form the provider source at the pin
// states, how a Cloud Asset Inventory hit's name becomes an import ID
// (INTENTIUS/choudoufu#2139 item 7). Of %d labelled types (%d served by
// hashicorp/google), %d take the Asset path as it is (%d), %d take it
// rewritten into another accepted form (%d), and %d are unknown (%d) and
// have no row: a hit of one is refused by name, never imported with a
// guessed ID.
//
// The provider source states no Asset name for a handwritten type, and
// %d rows (%d) take the name Google documents for the type's asset type
// instead, marked Documented. The page is
// %s
// (last updated %s), read into %s.
var GoogleAssetImports = map[string]GoogleAssetImport{
`, ProviderVersion, c.Labelled, c.GoogleLabelled, c.AsIs, c.GoogleAsIs, c.Rewritten, c.GoogleRewritten, c.Unknown, c.GoogleUnknown,
		c.Documented, c.GoogleDocumented, names.Source, names.LastUpdated, AssetNamesJSONRel)
	for _, a := range assets {
		if a.Verdict == AssetUnknown {
			continue
		}
		documented := ""
		if a.Row.Documented {
			documented = ", Documented: true"
		}
		fmt.Fprintf(&b, "\t%q: {Host: %q, Asset: %q, Import: GoogleImportFormat%s%s},\n", a.Type, a.Row.Host, a.Row.Asset, renderFormat(a.Row.Import), documented)
	}
	b.WriteString("}\n\n")

	b.WriteString("// GoogleAssetUnknown is every labelled type with no [GoogleAssetImports]\n// row, and why neither the source at the pin nor Google's asset names page\n// gives one.\n")
	b.WriteString("var GoogleAssetUnknown = map[string]string{\n")
	for _, a := range assets {
		if a.Verdict == AssetUnknown {
			fmt.Fprintf(&b, "\t%q: %q,\n", a.Type, a.Reason)
		}
	}
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, `// GoogleCompositeImports are the import forms of each google type with no
// [GoogleTable] row whose identity schema requires two or more attributes
// (INTENTIUS/choudoufu#2139 item 3). Its record holds those attributes as
// Components and no import ID, and the record leg composes one from the
// first form whose every part the record carries. Of %d such types (%d
// labelled), %d compose (%d labelled) and %d are refused by name
// ([GoogleCompositeRefused]).
var GoogleCompositeImports = map[string][]GoogleImportFormat{
`, c.Composite, c.CompositeLabelled, c.Composed, c.CompositeLabelledComposed, c.CompositeRefused)
	for _, cd := range composites {
		if len(cd.Formats) == 0 {
			continue
		}
		var fs []string
		for _, f := range cd.Formats {
			fs = append(fs, renderFormat(f))
		}
		fmt.Fprintf(&b, "\t%q: {%s},\n", cd.Type, strings.Join(fs, ", "))
	}
	b.WriteString("}\n\n")

	b.WriteString("// GoogleCompositeRefused is every such type with no form, and why.\n")
	b.WriteString("var GoogleCompositeRefused = map[string]string{\n")
	for _, cd := range composites {
		if len(cd.Formats) == 0 {
			fmt.Fprintf(&b, "\t%q: %q,\n", cd.Type, cd.Reason)
		}
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}

func renderFormat(f identity.GoogleImportFormat) string {
	if f.Regex == "" {
		return fmt.Sprintf("{Template: %q}", f.Template)
	}
	return fmt.Sprintf("{Template: %q, Regex: %q}", f.Template, f.Regex)
}
