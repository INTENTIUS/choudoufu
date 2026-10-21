// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// AssetNamesJSONRel is Google's table of Cloud Asset Inventory asset
// types and their full resource name formats, read off the documentation
// page (`-asset-names`), so the derivation stays offline.
const AssetNamesJSONRel = "live/gcp-asset-names.json"

// AssetNamesURL is the page the table is read from. cloud.google.com
// redirects to it.
const AssetNamesURL = "https://docs.cloud.google.com/asset-inventory/docs/asset-names"

// AssetNames is live/gcp-asset-names.json.
type AssetNames struct {
	GeneratedBy string `json:"generated_by"`
	Source      string `json:"source"`
	// LastUpdated is the page's own "Last updated" date.
	LastUpdated string `json:"last_updated"`
	// Notation says how a format spells the page's variables.
	Notation string `json:"notation"`
	// Types maps an asset type, "storage.googleapis.com/Bucket", to the
	// name formats the page lists for it, in its order:
	// "//storage.googleapis.com/{BUCKET}".
	Types map[string][]string `json:"asset_types"`
}

const assetNamesNotation = "{NAME} is a variable the page sets in <var>; every other character is literal"

var (
	assetRowRe     = regexp.MustCompile(`(?s)<tr>\s*<td>\s*<code id="([^"]+)"[^>]*>.*?</td>\s*<td>(.*?)</td>\s*</tr>`)
	assetFormatRe  = regexp.MustCompile(`(?s)<code translate="no" dir="ltr">(.*?)</code>`)
	assetVarRe     = regexp.MustCompile(`<var[^>]*>([^<]*)</var>`)
	anyTagRe       = regexp.MustCompile(`<[^>]+>`)
	lastUpdatedRe  = regexp.MustCompile(`Last updated (\d{4}-\d{2}-\d{2}) UTC`)
	canonicalRe    = regexp.MustCompile(`<link rel="canonical" href="([^"]+)"`)
	docsVarSegment = regexp.MustCompile(`^\{([A-Z0-9_]+)\}$`)
)

// extractAssetNames reads the asset type table out of the page's HTML.
// The page is treated as text and nothing in it is run.
func extractAssetNames(page []byte) (AssetNames, error) {
	s := string(page)
	out := AssetNames{
		GeneratedBy: "go run ./tools/gcp-row-gen -asset-names FILE",
		Notation:    assetNamesNotation,
		Types:       map[string][]string{},
	}
	m := canonicalRe.FindStringSubmatch(s)
	if m == nil || m[1] != AssetNamesURL {
		return AssetNames{}, fmt.Errorf("the page's canonical URL is not %s; this is not the asset names page", AssetNamesURL)
	}
	out.Source = m[1]
	m = lastUpdatedRe.FindStringSubmatch(s)
	if m == nil {
		return AssetNames{}, fmt.Errorf("the page states no \"Last updated\" date")
	}
	out.LastUpdated = m[1]
	for _, row := range assetRowRe.FindAllStringSubmatch(s, -1) {
		assetType := html.UnescapeString(row[1])
		for _, f := range assetFormatRe.FindAllStringSubmatch(row[2], -1) {
			name := assetVarRe.ReplaceAllString(f[1], "{$1}")
			name = html.UnescapeString(anyTagRe.ReplaceAllString(name, ""))
			name = strings.TrimSpace(name)
			if !strings.HasPrefix(name, "//") {
				return AssetNames{}, fmt.Errorf("%s: name format %q is not //host/path", assetType, name)
			}
			out.Types[assetType] = append(out.Types[assetType], name)
		}
	}
	if len(out.Types) < 500 {
		return AssetNames{}, fmt.Errorf("read %d asset types from the page; its table has changed shape", len(out.Types))
	}
	return out, nil
}

// runAssetNames writes live/gcp-asset-names.json from a saved copy of the
// page.
func runAssetNames(root, file string) error {
	page, err := os.ReadFile(file) //nolint:gosec // an operator-named input
	if err != nil {
		return err
	}
	names, err := extractAssetNames(page)
	if err != nil {
		return err
	}
	data, err := marshalAssetNames(names)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(AssetNamesJSONRel)), data, 0o644); err != nil { //nolint:gosec // committed artifact
		return err
	}
	fmt.Printf("%s: %d asset types from %s, last updated %s\n", AssetNamesJSONRel, len(names.Types), names.Source, names.LastUpdated)
	return nil
}

// marshalAssetNames renders one asset type per line, so a re-read of the
// page reads as a diff of the types that moved.
func marshalAssetNames(n AssetNames) ([]byte, error) {
	var b bytes.Buffer
	enc := func(v any) string {
		var e bytes.Buffer
		j := json.NewEncoder(&e)
		j.SetEscapeHTML(false)
		_ = j.Encode(v)
		return strings.TrimSuffix(e.String(), "\n")
	}
	fmt.Fprintf(&b, "{\n\"generated_by\": %s,\n\"source\": %s,\n\"last_updated\": %s,\n\"notation\": %s,\n\"asset_types\": {\n",
		enc(n.GeneratedBy), enc(n.Source), enc(n.LastUpdated), enc(n.Notation))
	types := sortedKeys(n.Types)
	for i, t := range types {
		fmt.Fprintf(&b, "%s: %s", enc(t), enc(n.Types[t]))
		if i < len(types)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n}\n")
	return b.Bytes(), nil
}

func loadAssetNames(root string) (AssetNames, error) {
	var n AssetNames
	if err := readJSON(root, AssetNamesJSONRel, &n); err != nil {
		return AssetNames{}, err
	}
	if len(n.Types) == 0 || n.Source == "" || n.LastUpdated == "" {
		return AssetNames{}, fmt.Errorf("%s decoded empty; the shape this tool reads has changed", AssetNamesJSONRel)
	}
	return n, nil
}

// documentedAsset is the asset type a labelled google type's objects are
// indexed under, for a type whose provider source states no Asset name.
type documentedAsset struct {
	// AssetType is the page's asset type. Its service host must be the
	// api_service_name the type's *_meta.yaml records, which the
	// derivation checks; the kind after the host is read by hand.
	AssetType string

	// Basis says how the mapping was made.
	Basis string

	// Parts names the page's variables by the placeholders of an import
	// form, by hand, for a type whose import forms do not line up with
	// the page's format segment by segment. Empty when they do, and the
	// derivation names them by that alignment.
	Parts map[string]string
}

// basisKind is the usual basis: the host is mechanical, the kind is the
// page's own name for the object the resource manages.
const basisKind = "host from the type's api_service_name; kind is the page's asset type for the object this resource creates, read by hand"

// documentedAssets maps every labelled google type whose provider source
// states no Asset name to its asset type on Google's asset names page
// ([AssetNamesURL]). Every mapping here is a hand reading, and its Basis
// says what it rests on. The derivation refuses a mapping whose host is not
// the type's api_service_name, whose asset type the page does not list, or
// whose type does not need one.
var documentedAssets = map[string]documentedAsset{
	"google_assured_workloads_workload":    {AssetType: "assuredworkloads.googleapis.com/Workload", Basis: basisKind},
	"google_bigquery_table":                {AssetType: "bigquery.googleapis.com/Table", Basis: basisKind},
	"google_clouddeploy_delivery_pipeline": {AssetType: "clouddeploy.googleapis.com/DeliveryPipeline", Basis: basisKind},
	"google_clouddeploy_target":            {AssetType: "clouddeploy.googleapis.com/Target", Basis: basisKind},
	"google_cloudfunctions_function": {
		AssetType: "cloudfunctions.googleapis.com/CloudFunction",
		Basis:     basisKind + "; the page's LOCATION is the resource's region argument, the location the Cloud Functions v1 API puts in the name, also by hand",
		Parts:     map[string]string{"PROJECT_ID": "project", "LOCATION": "region", "CLOUD_FUNCTION": "name"},
	},
	"google_composer_environment":                {AssetType: "composer.googleapis.com/Environment", Basis: basisKind},
	"google_compute_instance":                    {AssetType: "compute.googleapis.com/Instance", Basis: basisKind},
	"google_compute_instance_from_machine_image": {AssetType: "compute.googleapis.com/Instance", Basis: basisKind + "; the resource creates an ordinary instance"},
	"google_compute_instance_from_template":      {AssetType: "compute.googleapis.com/Instance", Basis: basisKind + "; the resource creates an ordinary instance"},
	"google_compute_security_policy":             {AssetType: "compute.googleapis.com/SecurityPolicy", Basis: basisKind},
	"google_dataflow_flex_template_job":          {AssetType: "dataflow.googleapis.com/Job", Basis: basisKind + "; a flex template launches an ordinary Dataflow job"},
	"google_dataflow_job":                        {AssetType: "dataflow.googleapis.com/Job", Basis: basisKind},
	"google_dataplex_asset":                      {AssetType: "dataplex.googleapis.com/Asset", Basis: basisKind},
	"google_dataplex_lake":                       {AssetType: "dataplex.googleapis.com/Lake", Basis: basisKind},
	"google_dataplex_zone":                       {AssetType: "dataplex.googleapis.com/Zone", Basis: basisKind},
	"google_dataproc_cluster":                    {AssetType: "dataproc.googleapis.com/Cluster", Basis: basisKind},
	"google_dataproc_job":                        {AssetType: "dataproc.googleapis.com/Job", Basis: basisKind},
	"google_project":                             {AssetType: "cloudresourcemanager.googleapis.com/Project", Basis: basisKind},
	"google_recaptcha_enterprise_key":            {AssetType: "recaptchaenterprise.googleapis.com/Key", Basis: basisKind},
	"google_storage_bucket":                      {AssetType: "storage.googleapis.com/Bucket", Basis: basisKind},
}

// docsProjectNumber is the page's variable for a project number. The
// provider's project argument holds a project ID, so a format naming the
// number never lines up with it.
const docsProjectNumber = "PROJECT_NUMBER"

// assetCandidates are the import forms an Asset name's parts may be put
// into: the importer's regexes, or, for a custom importer, every form its
// Import section lists. Unlike [importCandidates], a documented form's
// placeholders need not be attributes, because they are filled from the
// Asset name and not from the configuration: google_storage_bucket's
// "{{bucket}}" is the name its importer sets.
func assetCandidates(src Source) []identity.GoogleImportFormat {
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
		if len(placeholders(tmpl)) > 0 {
			out = append(out, identity.GoogleImportFormat{Template: tmpl})
		}
	}
	return out
}

// alignDocumented lines a documented format's path up with an import
// form segment by segment: the same number of segments, equal literals,
// and each of the page's variables against a placeholder that is the
// whole segment. When they line up, the form is the Asset path in the
// form's own placeholder names.
func alignDocumented(path, tmpl string) bool {
	ds, ts := strings.Split(path, "/"), strings.Split(tmpl, "/")
	if len(ds) != len(ts) {
		return false
	}
	for i := range ds {
		dv := docsVarSegment.FindStringSubmatch(ds[i])
		tp := placeholderRe.FindStringSubmatch(ts[i])
		switch {
		case dv == nil && tp == nil:
			if ds[i] != ts[i] {
				return false
			}
		case dv != nil && tp != nil && tp[0] == ts[i] && tp[1] == "":
			if dv[1] == docsProjectNumber {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// nameDocumented renames a documented path's variables by parts, or
// reports false when one is not named.
func nameDocumented(path string, parts map[string]string) (string, bool) {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if dv := docsVarSegment.FindStringSubmatch(s); dv != nil {
			p, ok := parts[dv[1]]
			if !ok {
				return "", false
			}
			segs[i] = "{{" + p + "}}"
		}
	}
	return strings.Join(segs, "/"), true
}

// decideDocumented is [decideAsset] for a type whose provider source
// states no Asset name: the name Google documents for its asset type
// ([documentedAssets]), whose every variable is one path segment, in the
// first import form that lines up with one of the page's formats, as it
// is; else, when the mapping names the page's variables, in a form with
// exactly those parts, rewritten; else unknown.
func decideDocumented(d AssetDecision, src Source, names AssetNames) AssetDecision {
	m, ok := documentedAssets[d.Type]
	if !ok {
		d.Reason = "the provider source states no Asset name format for this type, and no asset type on Google's asset names page is mapped to it"
		return d
	}
	host, _, _ := strings.Cut(m.AssetType, "/")
	cands := assetCandidates(src)
	if len(cands) == 0 {
		d.Reason = fmt.Sprintf("the provider source states no import format for this type (its documented asset type is %s)", m.AssetType)
		return d
	}
	var paths []string
	for _, f := range names.Types[m.AssetType] {
		rest, _ := strings.CutPrefix(f, "//")
		h, path, ok := strings.Cut(rest, "/")
		if ok && h == host && path != "" {
			paths = append(paths, path)
		}
	}
	documented := func(asset string, imp identity.GoogleImportFormat, v AssetVerdict) AssetDecision {
		d.Verdict = v
		d.Row = identity.GoogleAssetImport{Host: host, Asset: asset, Import: imp, Documented: true}
		return d
	}
	for _, c := range cands {
		for _, p := range paths {
			if alignDocumented(p, c.Template) {
				return documented(c.Template, c, AssetAsIs)
			}
		}
	}
	if len(m.Parts) > 0 {
		for _, p := range paths {
			asset, ok := nameDocumented(p, m.Parts)
			if !ok {
				continue
			}
			for _, c := range cands {
				if c.Template == asset {
					return documented(asset, c, AssetAsIs)
				}
			}
			for _, c := range cands {
				if samePlaceholderSet(c.Template, asset) {
					return documented(asset, c, AssetRewritten)
				}
			}
		}
	}
	d.Reason = fmt.Sprintf("no import format carries exactly the parts of the documented Asset name %s", strings.Join(names.Types[m.AssetType], " or "))
	if r, ok := passthroughReason(src, names.Types[m.AssetType]); ok {
		d.Reason = r
		return d
	}
	for _, p := range paths {
		if strings.Contains(p, "{"+docsProjectNumber+"}") {
			d.Reason += "; a project number is not the project ID the provider's project argument holds"
			break
		}
	}
	return d
}

// checkDocumentedAssets refuses a [documentedAssets] entry that does not
// hold: one whose type is not a labelled type the source states no Asset
// name for, whose asset type the page does not list, or whose host is not
// the type's api_service_name.
func checkDocumentedAssets(needed map[string]Source, names AssetNames) error {
	var errs []string
	for _, t := range sortedKeys(documentedAssets) {
		m := documentedAssets[t]
		src, ok := needed[t]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s is mapped to %s but its provider source states its Asset name, or it is not a labelled type", t, m.AssetType))
			continue
		}
		if _, ok := names.Types[m.AssetType]; !ok {
			errs = append(errs, fmt.Sprintf("%s is mapped to %s, which %s does not list", t, m.AssetType, AssetNamesJSONRel))
		}
		if host, _, _ := strings.Cut(m.AssetType, "/"); host != src.APIService {
			errs = append(errs, fmt.Sprintf("%s is mapped to %s, whose host is not its api_service_name %q", t, m.AssetType, src.APIService))
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("documentedAssets: %s", strings.Join(errs, "; "))
	}
	return nil
}
