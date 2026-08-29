// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Render mode (issue #419, mirroring tools/survey-gen's own -render and
// tools/tagverbs-gen's -render): `go run ./tools/readiness-gen -render`
// rewrites the readiness-tiers span of live/COVERAGE.md and of the docs
// site's compatibility page in place, between
// `<!-- readiness-gen:begin readiness-tiers -->` /
// `<!-- readiness-gen:end readiness-tiers -->` marker pairs, from the
// already-committed live/readiness.json - not a fresh Build(). That is a
// deliberate choice, the same one survey-gen's own -render makes against
// live/survey.json: reading the committed artifact rather than
// recomputing it is what makes a hand-edited or freshly regenerated
// live/readiness.json that never got rendered show up as a doc-render
// diff (TestReadinessRenderedSpansAreCurrent in render_test.go) instead of
// silently passing because the render step re-derived the same numbers
// itself. No provider, no network, no other generator's process.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/intentius/choudoufu/internal/live/mdspan"
)

// markers is this generator's marker vocabulary - a distinct tool name
// ("readiness-gen") from survey-gen's own spans in live/COVERAGE.md, so the
// two generators' regions never collide even though they render into the
// same file.
var markers = mdspan.For("readiness-gen")

// The two docs this mode renders into, and the one span name both share.
const (
	// CoverageMDRel is the coverage ledger this generator adds a new
	// section to, alongside survey-gen's existing spans there.
	CoverageMDRel = "live/COVERAGE.md"

	// CompatibilityMDRel is the docs site's compatibility reference page -
	// the closest existing site page to where live/COVERAGE.md's content
	// already surfaces (site/content/docs/use/reference.md merely links to
	// it; this page already discusses admitted types and links to
	// live/LIMITATIONS.md for per-type detail).
	CompatibilityMDRel = "site/content/docs/use/compatibility.md"

	// spanReadinessTable is the tier-by-status cross tab, rendered
	// identically into both docs above.
	spanReadinessTable = "readiness-tiers"
)

// tierOrder and statusOrder fix the rendered table's row and column order:
// the tiers in rfc/20260828-readiness-tiers.md's own precedence order (A,
// B, C, D), the statuses in the order issue #418 names them. Both are
// checked against the artifact's actual vocabulary by
// TestReadinessCrossTabCoversEveryTierAndStatus in render_test.go, so a
// tier or status name added to build.go's consts without a matching entry
// here fails a test instead of silently losing a row or column.
var (
	tierOrder = []string{
		TierMarkerCarried,
		TierDeclarationCarried,
		TierRecordCarried,
		TierExcludedByDesign,
	}
	statusOrder = []string{
		StatusInContract,
		StatusPendingRatification,
		StatusNeedsSeparator,
		StatusNeedsEvidence,
		StatusPendingMechanism,
		StatusExcluded,
	}
)

// crossKey is one tier/status pair, the join key readinessCrossTab tallies
// over.
type crossKey struct {
	Tier   string
	Status string
}

// readinessCrossTab tallies every row of the artifact into its (tier,
// status) cell. Counts.Tiers and Counts.Statuses (build.go) each carry only
// one axis's marginal; a reader of the rendered table needs the joint
// count, which is exactly what the RFC's tier definitions and issue #418's
// statuses compose into and neither field alone states.
func readinessCrossTab(a Artifact) map[crossKey]int {
	cross := make(map[crossKey]int, len(tierOrder)*len(statusOrder))
	for _, row := range a.Types {
		cross[crossKey{Tier: row.Tier, Status: row.Status}]++
	}
	return cross
}

// loadArtifact reads and decodes the already-committed live/readiness.json.
// Kept separate from build_test.go's own readCommitted (a test-only helper
// in the same package) because this one is called from production code
// (runRender), not only from tests.
func loadArtifact(root string) (Artifact, error) {
	var a Artifact
	if err := decodeJSON(root, OutputJSONRel, &a); err != nil {
		return Artifact{}, err
	}
	return a, nil
}

// renderReadinessTable builds the span's body: every tier crossed with
// every status, plus a total row and column so a reader can see both
// marginals without cross-referencing live/readiness.json's own Counts
// block. Built from readinessCrossTab alone, with no file I/O, so
// render_test.go's drift guard renders the same bytes runRender would
// write without touching the filesystem.
func renderReadinessTable(a Artifact) string {
	cross := readinessCrossTab(a)

	var b strings.Builder
	b.WriteString("| Tier")
	for _, s := range statusOrder {
		fmt.Fprintf(&b, " | %s", s)
	}
	b.WriteString(" | Total |\n|---")
	for range statusOrder {
		b.WriteString("|---")
	}
	b.WriteString("|---|\n")

	colTotal := make(map[string]int, len(statusOrder))
	grand := 0
	for _, tier := range tierOrder {
		fmt.Fprintf(&b, "| %s", tier)
		rowTotal := 0
		for _, s := range statusOrder {
			n := cross[crossKey{Tier: tier, Status: s}]
			rowTotal += n
			colTotal[s] += n
			fmt.Fprintf(&b, " | %d", n)
		}
		grand += rowTotal
		fmt.Fprintf(&b, " | %d |\n", rowTotal)
	}

	b.WriteString("| **Total**")
	for _, s := range statusOrder {
		fmt.Fprintf(&b, " | %d", colTotal[s])
	}
	fmt.Fprintf(&b, " | %d |\n", grand)
	return b.String()
}

// runRender is the -render entry point: read the committed
// live/readiness.json, replace the readiness-tiers span in both docs,
// write back whichever changed.
func runRender() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	artifact, err := loadArtifact(root)
	if err != nil {
		return fmt.Errorf("%w (run `go run ./tools/readiness-gen` and commit the result first)", err)
	}
	table := renderReadinessTable(artifact)

	for _, rel := range []string{CoverageMDRel, CompatibilityMDRel} {
		if err := renderReadinessSpan(root, rel, table); err != nil {
			return err
		}
	}
	return nil
}

// renderReadinessSpan rewrites one doc's readiness-tiers span in place.
func renderReadinessSpan(root, rel, table string) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}

	out, err := markers.Replace(rel, string(doc), spanReadinessTable, table)
	if err != nil {
		return err
	}
	if out == string(doc) {
		fmt.Fprintf(os.Stderr, "readiness-gen: %s's %q span is already current\n", rel, spanReadinessTable)
		return nil
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil { //nolint:gosec // a committed doc, not a secret
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	fmt.Fprintf(os.Stderr, "readiness-gen: rewrote %s's %q span\n", rel, spanReadinessTable)
	return nil
}
