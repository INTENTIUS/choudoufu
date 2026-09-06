// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// This file writes live/rowgen-mismatches.json, -mismatches' whole output:
// per admitted type that a fresh proposal exists for, did row-gen's own
// classifier reproduce the ratified row, and does the row carry a ruling in
// tools/row-gen/annotations.json.
//
// It is issue #695's replacement for live/rowgen-convergence.json, and the
// narrowing is the point. That artifact was 516KB and carried nine summary
// counts, a 191-entry per-service breakdown, seven fields per row and two
// whole side buckets. Measured against its readers, exactly one per-type
// fact and four counts were load-bearing: internal/live/harness's
// rowgen-unannotated-mismatches entry recomputes the unruled count from the
// rows and cross-checks it against the summary, its sibling
// rowgen-annotation-rulings refuses a ruling naming a type nothing
// compares, and [validateAnnotations] refuses a stale one. Everything else
// had no reader, and the headline it existed for - adopted-unchanged, the
// share of rows the classifier already agrees with - is on record as the
// metric that does not predict onboarding success.
//
// So this artifact deliberately carries no ratio and no percentage. Its
// numbers are debt counts that travel toward zero, not a score.
const mismatchesJSONRel = "live/rowgen-mismatches.json"

// mismatchLedgerNote is written into the artifact itself, because a reader
// who opens the file is exactly the reader who will otherwise quote a
// number out of it as coverage - three sessions in a row did that with its
// predecessor, in a repository full of machinery for moving the number.
const mismatchLedgerNote = "Per admitted type with a fresh proposal: did tools/row-gen's own classifier " +
	"reproduce the hand-ratified row in tools/row-gen/ratified.json, and does a mismatched row carry a " +
	"ruling in tools/row-gen/annotations.json. NOT A COVERAGE METRIC. The ratified row is what ships - " +
	"emit.go copies every field of it verbatim - so a mismatch is generator-autonomy debt, not a failure " +
	"any user experiences. The gate users actually hit is admission, and above that the config-language " +
	"subset; issue #102 and live/corpus-refusals.json measure that. Regenerate with " +
	"`go run ./tools/row-gen -mismatches`."

// mismatchRow is one compared type's verdict: the three fields every reader
// of this artifact actually reads.
type mismatchRow struct {
	TFType    string `json:"tf_type"`
	Matched   bool   `json:"matched"`
	Annotated bool   `json:"annotated"`
}

// mismatchSummary is the counts, all four of which have a named reader:
// AdmittedTotal and Compared are the two denominators internal/live/harness
// pins, and the other two are what its rowgen-unannotated-mismatches entry
// cross-checks its own recomputation against.
type mismatchSummary struct {
	AdmittedTotal         int `json:"admitted_total"`
	Compared              int `json:"compared"`
	GenuineMismatches     int `json:"genuine_mismatches"`
	UnannotatedMismatches int `json:"unannotated_mismatches"`
}

// mismatchLedger is live/rowgen-mismatches.json's whole shape.
type mismatchLedger struct {
	GeneratedBy string          `json:"generated_by"`
	Note        string          `json:"note"`
	Summary     mismatchSummary `json:"summary"`

	// Rows is every COMPARED type, matched ones included: the harness's
	// rowgen-annotation-rulings entry needs the whole compared roster to
	// tell a ruling nothing can retire from one that names a real row, and
	// [validateAnnotations] needs it to tell "matched now, delete the
	// annotation" from "never compared, leave it alone".
	Rows []mismatchRow `json:"types"`
}

// buildMismatchLedger narrows a [comparison] to the artifact.
func buildMismatchLedger(c comparison) mismatchLedger {
	l := mismatchLedger{
		GeneratedBy: "tools/row-gen -mismatches (go run ./tools/row-gen -mismatches)",
		Note:        mismatchLedgerNote,
		Summary: mismatchSummary{
			AdmittedTotal:         c.AdmittedTotal,
			Compared:              c.Compared,
			GenuineMismatches:     c.GenuineMismatches,
			UnannotatedMismatches: c.UnannotatedMismatches,
		},
		Rows: make([]mismatchRow, 0, len(c.Rows)),
	}
	for _, row := range c.Rows {
		l.Rows = append(l.Rows, mismatchRow{TFType: row.TFType, Matched: row.Matched, Annotated: row.Annotated})
	}
	return l
}

// runMismatches is -mismatches' entry point: loads the same mapped set
// run() classifies plus tools/row-gen/annotations.json's own rulings,
// compares (comparison.go's buildComparison), and writes the ledger.
func runMismatches(out, errOut *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	proposals, err := loadProposals(root)
	if err != nil {
		return err
	}
	annotations, err := loadAnnotations(filepath.Join(root, annotationsJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", annotationsJSONRel, err)
	}
	emitted, err := loadEmittedTable(root, proposals)
	if err != nil {
		return err
	}

	ledger := buildMismatchLedger(buildComparison(emitted, proposals, annotations))

	if problems := validateAnnotations(ledger.Rows, annotations); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(errOut, "row-gen: annotations.json: %s\n", p)
		}
		return fmt.Errorf("%d stale or invalid annotation(s) in %s", len(problems), annotationsJSONRel)
	}

	if err := writeJSONArtifact(filepath.Join(root, mismatchesJSONRel), ledger); err != nil {
		return fmt.Errorf("writing %s: %w", mismatchesJSONRel, err)
	}

	fmt.Fprintf(out, "wrote %s\n", mismatchesJSONRel)
	fmt.Fprintf(errOut, "row-gen -mismatches: %d/%d admitted types compared, %d genuine mismatches (%d unannotated)\n",
		ledger.Summary.Compared, ledger.Summary.AdmittedTotal,
		ledger.Summary.GenuineMismatches, ledger.Summary.UnannotatedMismatches)
	return nil
}

// writeJSONArtifact marshals v the way every committed artifact in this
// repository is written - two-space indent, one trailing newline - so a
// regeneration that changes nothing produces no diff.
func writeJSONArtifact(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644) //nolint:gosec // a fixed path inside the checkout
}
