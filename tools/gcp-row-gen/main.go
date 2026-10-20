// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command gcp-row-gen writes the identity rows for hashicorp/google and
// hashicorp/google-beta resource types (#1119), pinned to the release in
// [ProviderVersion]. It is tools/row-gen's counterpart for the second
// family: AWS's rows come from CloudFormation evidence, these come from the
// provider's own source.
//
// Three modes, and only the first two need anything outside the tree:
//
//	go run ./tools/gcp-row-gen -extract -google DIR -google-beta DIR -schema FILE
//
// reads the generated provider source at the pinned tag (a checkout of
// hashicorp/terraform-provider-google and -google-beta, treated as text and
// never built or run) and the `providers schema -json` dump of the same
// release, and writes the two evidence artifacts: live/gcp-identity-evidence.json
// (each type's id format, import formats and IAM grammar, as the source
// states them) and live/gcp-schema-facts.json (each type's attributes, nested
// blocks and identity schema, as the schema states them).
//
//	go run ./tools/gcp-row-gen -asset-names FILE
//
// reads a saved copy of Google's Cloud Asset Inventory asset names page
// (AssetNamesURL, fetched with curl and treated as text) and writes
// live/gcp-asset-names.json: each asset type's documented full resource
// name formats, with the page's URL and "Last updated" date. It is the
// Asset name of a type whose provider source states none (assetnames.go).
//
//	go run ./tools/gcp-row-gen
//
// reads only those three committed artifacts and writes
// internal/live/identity/table_generated_google.go, live/gcp-survey.json
// and internal/live/identity/importids_generated_google.go (the import-ID
// forms of Asset hits and of composite records, importids.go).
// The test TestGeneratedFilesAreCurrent runs the same derivation and fails
// when either committed output has drifted from it.
//
// # Why the generated provider source and not Magic Modules
//
// Both were measured at the commit that generated 8.6.0 (magic-modules
// b8f14a6c, the upstream of the last code commit before the v8.6.0 tag). Magic
// Modules describes the 836 mmv1 types, and states id_format for 396 of them
// and import_format for 621; the rest take a default the generator computes
// from base_url and self_link, which a reader would have to reimplement. The
// provider source at the tag carries the result of that computation for those
// types, plus the 105 handwritten ones and the 429 IAM types the IAM
// framework builds, in the code the provider actually runs: the id format is
// the template its Create and its importer both pass to ReplaceVars, and the
// import formats are the regexes its importer parses. It is also the pin
// itself, with no second commit to keep in step.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ProviderVersion is the hashicorp/google and hashicorp/google-beta release
// every artifact this tool writes is pinned to.
const ProviderVersion = "8.6.0"

// Repository-relative paths of everything this tool reads and writes.
const (
	EvidenceJSONRel = "live/gcp-identity-evidence.json"
	FactsJSONRel    = "live/gcp-schema-facts.json"
	SurveyJSONRel   = "live/gcp-survey.json"
	TableGoRel      = "internal/live/identity/table_generated_google.go"
)

// The two provider names one row serves (#1119 item 6).
const (
	ProviderGoogle     = "google"
	ProviderGoogleBeta = "google-beta"
)

func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/gcp-row-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	extract := flag.Bool("extract", false, "read the pinned provider source and schema dump and write the two evidence artifacts, instead of deriving rows from them")
	googleDir := flag.String("google", "", "-extract: a checkout of hashicorp/terraform-provider-google at the pinned tag")
	betaDir := flag.String("google-beta", "", "-extract: a checkout of hashicorp/terraform-provider-google-beta at the pinned tag")
	schemaFile := flag.String("schema", "", "-extract: `providers schema -json` output covering both providers at the pinned release")
	assetNamesFile := flag.String("asset-names", "", "a saved copy of "+AssetNamesURL+": read its asset type table into "+AssetNamesJSONRel+", instead of deriving rows")
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		fail(err)
	}
	if *assetNamesFile != "" {
		if err := runAssetNames(root, *assetNamesFile); err != nil {
			fail(err)
		}
		return
	}
	if *extract {
		if *googleDir == "" || *betaDir == "" || *schemaFile == "" {
			fail(fmt.Errorf("-extract needs -google, -google-beta and -schema"))
		}
		if err := runExtract(root, *googleDir, *betaDir, *schemaFile); err != nil {
			fail(err)
		}
		return
	}
	if err := runGenerate(root); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "gcp-row-gen: %v\n", err)
	os.Exit(1)
}

// runGenerate derives the rows from the committed evidence and writes the
// generated table and the survey.
func runGenerate(root string) error {
	ev, facts, err := loadInputs(root)
	if err != nil {
		return err
	}
	names, err := loadAssetNames(root)
	if err != nil {
		return err
	}
	out, err := Generate(ev, facts, names)
	if err != nil {
		return err
	}
	for rel, data := range out.Files() {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), data, 0o644); err != nil { //nolint:gosec // committed artifacts
			return fmt.Errorf("writing %s: %w", rel, err)
		}
	}
	c := out.Survey.Counts
	fmt.Printf("%s: %d types; %d rowed (%d server-assigned), %d markerless, %d identity-schema routed, %d refused\n",
		SurveyJSONRel, c.Types, c.Rowed, c.ServerAssigned, c.Markerless, c.SchemaRouted, c.Refused)
	ic := out.ImportCounts
	fmt.Printf("%s: %d labelled types (%d hashicorp/google): %d as-is (%d), %d rewritten (%d), %d unknown (%d); %d composite types (%d labelled): %d compose (%d labelled), %d refused\n",
		ImportIDsGoRel, ic.Labelled, ic.GoogleLabelled, ic.AsIs, ic.GoogleAsIs, ic.Rewritten, ic.GoogleRewritten, ic.Unknown, ic.GoogleUnknown,
		ic.Composite, ic.CompositeLabelled, ic.Composed, ic.CompositeLabelledComposed, ic.CompositeRefused)
	return nil
}
