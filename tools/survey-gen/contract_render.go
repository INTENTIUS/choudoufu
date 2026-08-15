// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The Contract render (issue #54): live/COVERAGE.md's "The admitted set"
// section both counts and enumerates the admitted types. Its count and
// roster used to be hand-updated every wiring batch (37 -> 42 in the Lambda
// pilot); this file renders both from identity.AdmittedTypes, the same
// compiled admission table TestContractMDXRenderedSpans holds them to, no
// provider and no network - the same render/drift pattern SURVEY.md's and
// LIMITATIONS.md's spans already use.
//
// The spans lived in website/docs/language/live-markers.mdx until issue #79
// moved the docs site to hand-written pages under site/content/ and #112
// deleted that file. An enumeration of 800-plus entries is reference material, so it
// moved to the coverage ledger rather than onto a user-facing page.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// contractMDXRel is the coverage ledger whose admitted-set section this
// file renders spans into.
const contractMDXRel = "live/COVERAGE.md"

// The two rendered spans in "The Contract" section, on the same marker
// convention every other survey-gen span uses (see render.go's
// spanMarkers). Both markers sit mid-line, attached to surrounding prose
// rather than starting their own line, so the HTML comments stay inline
// content instead of interrupting the bullet's paragraph.
const (
	// spanContractCount is the resource-type count in "AWS only, N resource
	// types, count-expanded modules refused."
	spanContractCount = "contract-count"

	// spanContractTypes is the backtick-quoted, comma-separated enumeration
	// naming every admitted type.
	spanContractTypes = "contract-types"

	// spanCoverageLayers is the layers-at-a-glance table (issue #139). Every
	// cell comes from a committed artifact - live/rowgen-buckets.json,
	// live/mapping.json, live/cohort-acceptance.json - never from another
	// tool's classifier, so this renderer stays a reader of artifacts.
	spanCoverageLayers = "coverage-layers"
)

// renderContractMDX rewrites COVERAGE.md's two admitted-set spans in
// place, from the compiled admission table.
func renderContractMDX(root string) error {
	mdPath := filepath.Join(root, contractMDXRel)
	md, err := os.ReadFile(mdPath) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return err
	}

	out, err := renderContractSpans(root, string(md))
	if err != nil {
		return err
	}
	if out == string(md) {
		fmt.Fprintf(os.Stderr, "survey-gen: %s's Contract spans are already current\n", contractMDXRel)
		return nil
	}
	if err := os.WriteFile(mdPath, []byte(out), 0o644); err != nil { //nolint:gosec // a committed doc, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr, "survey-gen: rewrote %s's Contract spans\n", contractMDXRel)
	return nil
}

// renderContractSpans returns the doc with its spans replaced by their
// rendered bodies. The rest of the file passes through byte-for-byte: this
// render mode is scoped to the admitted set's count and roster (issue #54)
// and the layers table (issue #139).
func renderContractSpans(root, md string) (string, error) {
	md, err := replaceSpan(contractMDXRel, md, spanContractCount, renderContractCount())
	if err != nil {
		return "", err
	}
	md, err = replaceSpan(contractMDXRel, md, spanContractTypes, renderContractTypes())
	if err != nil {
		return "", err
	}
	layers, err := renderCoverageLayers(root)
	if err != nil {
		return "", err
	}
	return replaceSpan(contractMDXRel, md, spanCoverageLayers, layers)
}

// renderCoverageLayers builds the layers-at-a-glance table from the three
// committed artifacts that already know its numbers (issue #139). The row
// prose is this generator's; the counts never are.
func renderCoverageLayers(root string) (string, error) {
	var buckets struct {
		Mapped             int `json:"mapped"`
		ServerAssigned     int `json:"server_assigned"`
		ClientNamed        int `json:"client_named"`
		Composite          int `json:"composite"`
		NeedsHandSeparator int `json:"needs_hand_separator"`
		FoldChild          int `json:"fold_child"`
		EvidenceOnly       int `json:"evidence_only"`
	}
	if err := readJSON(root, "live/rowgen-buckets.json", &buckets); err != nil {
		return "", err
	}
	var mapping struct {
		Counts struct {
			Types             int `json:"types"`
			Mapped            int `json:"mapped"`
			Fold              int `json:"fold"`
			TFOnly            int `json:"tf_only"`
			CFNUnmodeled      int `json:"cfn_unmodeled"`
			DeprecatedService int `json:"deprecated_service"`
			Unclassified      int `json:"unclassified"`
		} `json:"counts"`
	}
	if err := readJSON(root, "live/mapping.json", &mapping); err != nil {
		return "", err
	}
	var acceptance struct {
		Totals struct {
			Cohorts int `json:"cohorts"`
			Pass    int `json:"pass"`
			Fail    int `json:"fail"`
		} `json:"totals"`
	}
	if err := readJSON(root, "live/cohort-acceptance.json", &acceptance); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("| Layer | Count | What stands between it and support |\n")
	b.WriteString("| ----- | ----- | ---------------------------------- |\n")
	fmt.Fprintf(&b, "| Round-trip proven against the emulator | %d of %d cohorts | Nothing. Applied, state deleted, replanned empty (`live/cohort-acceptance.json`). |\n",
		acceptance.Totals.Pass, acceptance.Totals.Cohorts)
	fmt.Fprintf(&b, "| Admitted (the shipped table) | %d types | Nothing at lint. Runtime support varies by type; see the layers below. |\n",
		len(identity.AdmittedTypes()))
	fmt.Fprintf(&b, "| Pastable proposals (server-assigned %d, client-named %d, composite %d) | %d types | A ratification batch: paste, fixture, test. |\n",
		buckets.ServerAssigned, buckets.ClientNamed, buckets.Composite,
		buckets.ServerAssigned+buckets.ClientNamed+buckets.Composite)
	fmt.Fprintf(&b, "| Needs a hand separator | %d types | One one-character import-separator decision each. |\n",
		buckets.NeedsHandSeparator)
	fmt.Fprintf(&b, "| Evidence-only | %d types | An identity-argument name no current evidence source states. |\n",
		buckets.EvidenceOnly)
	fmt.Fprintf(&b, "| Fold-children | %d types | Nothing of their own; identity is the parent's. |\n",
		buckets.FoldChild)
	fmt.Fprintf(&b, "| Mapped in total | %d of %d provider types | The layers above partition this set. |\n",
		buckets.Mapped, mapping.Counts.Types)
	fmt.Fprintf(&b, "| Excluded, each with a generated reason | %d cfn-unmodeled, %d tf-only, %d deprecated-service, %d unclassified | See `live/LIMITATIONS.md`'s exclusion cohorts. |",
		mapping.Counts.CFNUnmodeled, mapping.Counts.TFOnly, mapping.Counts.DeprecatedService, mapping.Counts.Unclassified)
	return b.String(), nil
}

func readJSON(root, rel string, v any) error {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) //nolint:gosec // fixed paths in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("parsing %s: %w", rel, err)
	}
	return nil
}

// renderContractCount is the admission table's size, the same figure
// SURVEY.md's wired-count span renders (render.go's renderWiredCount).
func renderContractCount() string {
	return fmt.Sprintf("%d", len(identity.AdmittedTypes()))
}

// renderContractTypes enumerates every admitted type, backtick-quoted,
// Oxford-comma joined, and word-wrapped to the bullet's own 2-space
// continuation indent - the convention the hand-written list already used.
func renderContractTypes() string {
	return wrapIndented(joinWithAnd(backtickTypes(identity.AdmittedTypes()), true), 78, "  ")
}
