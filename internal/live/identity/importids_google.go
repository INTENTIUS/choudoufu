// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"regexp"
	"strings"
	"sync"
)

// GoogleImportFormat is one import ID form a hashicorp/google importer
// accepts, as tools/gcp-row-gen read it from the provider source at the
// pinned tag.
type GoogleImportFormat struct {
	// Template is the form, in ReplaceVars syntax:
	// "{{key_ring}}/cryptoKeys/{{name}}".
	Template string

	// Regex is the importer's own ParseImportId regex for Template, when the
	// importer is the generated one. A composed ID must match it. Empty
	// when the importer is custom code and Template comes from the type's
	// documented Import formats instead.
	Regex string
}

// GoogleAssetImport says how a Cloud Asset Inventory hit of one labelled
// google type becomes an import ID ([GoogleAssetImports]).
type GoogleAssetImport struct {
	// Host is the service host the Asset name carries:
	// "compute.googleapis.com".
	Host string

	// Asset is the Asset name's path after the host, in ReplaceVars
	// syntax: "projects/{{project}}/zones/{{zone}}/disks/{{name}}".
	Asset string

	// Import is the import ID the parts parsed out of Asset are put into.
	// Equal to Asset when the importer accepts the Asset path as it is.
	Import GoogleImportFormat

	// Documented is set when the provider source states no Asset name for
	// the type, and Asset is the one Google documents for its asset type
	// (https://docs.cloud.google.com/asset-inventory/docs/asset-names,
	// last updated 2026-10-19, read into live/gcp-asset-names.json). The
	// page's every variable is one path segment, so each placeholder of
	// such an Asset matches exactly one: a bucket's is its bare name.
	Documented bool
}

// AsIs reports whether the importer takes the Asset path unchanged.
func (a GoogleAssetImport) AsIs() bool { return a.Import.Template == a.Asset }

var googlePlaceholderRe = regexp.MustCompile(`\{\{%?([a-zA-Z0-9_]+)\}\}`)

// googleTemplatePlaceholders lists a template's placeholder names in order.
func googleTemplatePlaceholders(tmpl string) []string {
	var out []string
	for _, m := range googlePlaceholderRe.FindAllStringSubmatch(tmpl, -1) {
		out = append(out, m[1])
	}
	return out
}

// fillGoogleTemplate puts values into tmpl. It refuses when a placeholder
// has no value, or is path-escaped ({{%name}}), which this does not
// reproduce.
func fillGoogleTemplate(tmpl string, values map[string]string) (string, bool) {
	ok := true
	out := googlePlaceholderRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		if strings.HasPrefix(m, "{{%") {
			ok = false
			return m
		}
		name := googlePlaceholderRe.FindStringSubmatch(m)[1]
		v := values[name]
		if v == "" {
			ok = false
		}
		return v
	})
	return out, ok
}

var (
	googleTemplateRegexMu sync.Mutex
	googleTemplateRegex   = map[string]*regexp.Regexp{}
)

// parseGoogleTemplate reads values out of path by tmpl. A placeholder
// matches one path segment, except one that opens the template, which
// names a parent and matches several ("{{key_ring}}/cryptoKeys/{{name}}"),
// unless segments says every placeholder is one segment. A path-escaped
// placeholder is refused.
func parseGoogleTemplate(tmpl, path string, segments bool) (map[string]string, bool) {
	if strings.Contains(tmpl, "{{%") {
		return nil, false
	}
	key := tmpl
	if segments {
		key = "segments:" + tmpl
	}
	googleTemplateRegexMu.Lock()
	re, ok := googleTemplateRegex[key]
	if !ok {
		var b strings.Builder
		b.WriteString("^")
		rest := tmpl
		first := true
		for {
			loc := googlePlaceholderRe.FindStringSubmatchIndex(rest)
			if loc == nil {
				b.WriteString(regexp.QuoteMeta(rest))
				break
			}
			b.WriteString(regexp.QuoteMeta(rest[:loc[0]]))
			name := rest[loc[2]:loc[3]]
			if first && loc[0] == 0 && !segments {
				b.WriteString("(?P<" + name + ">.+)")
			} else {
				b.WriteString("(?P<" + name + ">[^/]+)")
			}
			first = false
			rest = rest[loc[1]:]
		}
		b.WriteString("$")
		re, _ = regexp.Compile(b.String())
		googleTemplateRegex[key] = re
	}
	googleTemplateRegexMu.Unlock()
	if re == nil {
		return nil, false
	}
	m := re.FindStringSubmatch(path)
	if m == nil {
		return nil, false
	}
	out := map[string]string{}
	for i, name := range re.SubexpNames() {
		if name == "" {
			continue
		}
		if prev, dup := out[name]; dup && prev != m[i] {
			return nil, false
		}
		out[name] = m[i]
	}
	return out, true
}

// acceptedBy reports whether id matches f's importer regex, or f has none.
func (f GoogleImportFormat) acceptedBy(id string) bool {
	if f.Regex == "" {
		return true
	}
	re, err := regexp.Compile(f.Regex)
	return err == nil && re.MatchString(id)
}

// GoogleAssetImportID turns a Cloud Asset Inventory full resource name
// ("//compute.googleapis.com/projects/p/zones/z/disks/d") of a labelled
// google type into the import ID that type's importer accepts, by the
// generated [GoogleAssetImports] row: the path as it is, or its parts put
// into an accepted form. ok is false when the type has no row (neither the
// source at the pin nor Google's documentation says), or the name does not
// have the row's host and shape; the caller refuses such a hit by name
// rather than import a guess.
//
// The name is read in the form Google documents for each asset type at
// https://docs.cloud.google.com/asset-inventory/docs/asset-names (last
// updated 2026-10-19): a Cloud Storage bucket is //storage.googleapis.com/BUCKET,
// a Compute Engine instance
// //compute.googleapis.com/projects/PROJECT_ID/zones/ZONE/instances/INSTANCE.
// No other spelling is accepted: projects/_/buckets/BUCKET is the Cloud
// Storage JSON API's and IAM's name for a bucket, not its Asset name.
func GoogleAssetImportID(typeName, assetName string) (string, bool) {
	row, ok := GoogleAssetImports[typeName]
	if !ok {
		return "", false
	}
	rest, ok := strings.CutPrefix(assetName, "//")
	if !ok {
		return "", false
	}
	host, path, ok := strings.Cut(rest, "/")
	if !ok || host != row.Host {
		return "", false
	}
	values, ok := parseGoogleTemplate(row.Asset, path, row.Documented)
	if !ok {
		return "", false
	}
	id, ok := fillGoogleTemplate(row.Import.Template, values)
	if !ok || !row.Import.acceptedBy(id) {
		return "", false
	}
	return id, true
}

// GoogleCompositeImportID composes the import ID of a google type with no
// identity-table row from a record's identity Components, by the first
// [GoogleCompositeImports] form whose every part the record carries, the
// order the importer tries them in. ok is false when the type has no form
// (it is refused by name instead) or the record carries no form's parts.
func GoogleCompositeImportID(typeName string, components map[string]string) (string, bool) {
	for _, f := range GoogleCompositeImports[typeName] {
		id, ok := fillGoogleTemplate(f.Template, components)
		if ok && f.acceptedBy(id) {
			return id, true
		}
	}
	return "", false
}
