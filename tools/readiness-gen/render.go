// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Render mode (issue #419, mirroring tools/survey-gen's own -render and
// tools/tagverbs-gen's -render): `go run ./tools/readiness-gen -render`
// rewrites the readiness-tiers span of live/COVERAGE.md, of the docs site's
// compatibility page, and of the docs site's per-type resource-tiers page in
// place, between `<!-- readiness-gen:begin readiness-tiers -->` /
// `<!-- readiness-gen:end readiness-tiers -->` marker pairs, from the
// already-committed live/readiness.json - not a fresh Build(). That is a
// deliberate choice, the same one survey-gen's own -render makes against
// live/survey.json: reading the committed artifact rather than
// recomputing it is what makes a hand-edited or freshly regenerated
// live/readiness.json that never got rendered show up as a doc-render
// diff (TestReadinessRenderedSpansAreCurrent in render_test.go) instead of
// silently passing because the render step re-derived the same numbers
// itself. No provider, no network, no other generator's process.
//
// Issue #420 adds a second span, readiness-types, written only into the
// resource-tiers page: the full per-type table (every row of
// live/readiness.json, not just the tier-by-status tally readiness-tiers
// already renders), so a customer can paste their own resource type and get
// a tier, a status, and - for anything short of in-contract - a one-line
// reason, without reading this generator's internals or row-gen's ledger
// prose. reasonFor synthesizes that reason from Facts rather than quoting
// tools/row-gen/rejected.json's free text directly: those entries run to
// paragraphs (see build.go's own package doc comment on classifyRejectedReason),
// which is neither "one-line" nor safe to drop into a markdown table cell
// unescaped.
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

// The docs this mode renders into, and the span names it writes.
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

	// ResourceTiersMDRel is the docs site's customer-facing per-type lookup
	// page, issue #420. It carries both spans below: the same
	// readiness-tiers cross tab CoverageMDRel and CompatibilityMDRel carry,
	// so its rendered counts are never hand-typed either, plus its own
	// readiness-types span.
	ResourceTiersMDRel = "site/content/docs/use/resource-tiers.md"

	// spanReadinessTable is the tier-by-status cross tab, rendered
	// identically into all three docs above.
	spanReadinessTable = "readiness-tiers"

	// spanReadinessTypesTable is the full per-type lookup table (every row
	// of live/readiness.json), rendered only into ResourceTiersMDRel - the
	// other two docs already had their own scope before issue #420 and
	// gain nothing from repeating 1699 rows.
	spanReadinessTypesTable = "readiness-types"
)

// limitationsMDURL is live/LIMITATIONS.md's GitHub blob URL, the same
// linking convention site/content/docs/use/reference.md already uses for
// every repo-root doc it indexes. Built as a const, not discovered, because
// there is nothing to discover: the file's path in the repo is fixed.
const limitationsMDURL = "https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md"

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

// reasonFor is the readiness-types span's fourth column: a one-line,
// customer-facing reason a row is not in-contract, synthesized from Facts.
// Empty for an in-contract row - nothing to explain.
//
// This deliberately does not quote Facts.RejectedReason. Those entries run
// to multi-hundred-word paragraphs (see build.go's package doc comment on
// classifyRejectedReason, and its own worked aws_quicksight_folder example),
// which is neither one line nor safe to drop unescaped into a markdown
// table cell - a stray "|" in the ledger prose would split the row. Six
// status tokens is a small, fixed vocabulary; this switches on it once
// rather than making every reader of the table re-derive the same six
// sentences from Facts themselves.
//
// Every branch that names a mechanism links live/LIMITATIONS.md at a real,
// checked anchor (see this package's own read of that file while this
// function was written: "unadmitted-type" and "markerless-type" are both
// "### " headings there today) rather than a guessed one - issue #420's own
// accept criterion for the tier D / excluded case, extended here to every
// status that has an accurate anchor to point at.
func reasonFor(r Row) string {
	switch r.Status {
	case StatusInContract:
		return ""
	case StatusExcluded:
		// Tier D's population (harness.SanctionedCredentialExclusions) has
		// no dedicated live/LIMITATIONS.md heading yet -
		// rfc/20260828-readiness-tiers.md's tier D section confirms neither
		// name appears there, and names giving them one as a follow-up
		// outside its own scope. What is accurate today, and what
		// RuleUnadmittedType actually reports for these two types
		// (see that RFC section's "What live-import does"), is
		// unadmitted-type.
		return fmt.Sprintf(
			"excluded by design: generates credential material this fork can never read back and verify again (maintainer ruling, 2026-08-15, issue #175). See [LIMITATIONS.md](%s#unadmitted-type).",
			limitationsMDURL,
		)
	case StatusPendingMechanism:
		// Every pending-mechanism row is markerless (build.go's classify:
		// this status is only reached inside the MarkerlessTypes branch),
		// so markerless-type is always the accurate anchor; the detail
		// clause says which of the two located-route conditions this
		// generator's static approximation could check (see build.go's
		// package doc comment, "What is approximated").
		detail := "the located-record mechanism does not reach it yet"
		switch {
		case r.Facts.NotImportable:
			detail = "the provider offers no import support for this type at all"
		case r.Facts.IDNotProvenWhole:
			detail = "its identity is composite and the record can only carry a flat id today (issue #429)"
		}
		return fmt.Sprintf("record-carried, markerless, but %s. See [LIMITATIONS.md](%s#markerless-type).", detail, limitationsMDURL)
	case StatusNeedsSeparator:
		return fmt.Sprintf("a composite identity with no worked import example to read its separator from. See [LIMITATIONS.md](%s#unadmitted-type).", limitationsMDURL)
	case StatusNeedsEvidence:
		return fmt.Sprintf("the provider documents no import example for this type yet. See [LIMITATIONS.md](%s#unadmitted-type).", limitationsMDURL)
	case StatusPendingRatification:
		return fmt.Sprintf("no ratification batch has reached this type's admission table row yet. See [LIMITATIONS.md](%s#unadmitted-type).", limitationsMDURL)
	default:
		// build.go's Row.Status is one of the six consts above by
		// construction (TestPartitionGuard, tools/readiness-gen/build_test.go);
		// reaching this branch means a new status token was added there
		// without a matching case here. Say so rather than rendering a
		// blank cell that looks intentional.
		return fmt.Sprintf("unrecognized status %q - reasonFor needs a case for it", r.Status)
	}
}

// renderReadinessTypesTable builds the readiness-types span's body: every
// row of live/readiness.json, one table row each, wrapped in a scrolling
// container.
//
// Precedent check for the wrapper, per issue #420's Accept criteria: this
// site's hugo-book theme already gives every markdown table `display:
// block; overflow: auto` (site/themes/hugo-book/assets/styles/markdown.css),
// which is exactly the "own overflow-x: auto scrolling container" behavior
// the issue asks for, applied automatically to every table on the site
// today, including the existing readiness-tiers cross tab above. The div
// wrapper below is redundant with that theme rule but kept anyway so the
// requirement holds by construction from this page's own markup, not only
// from a theme default a future theme swap could silently drop; it costs
// nothing extra since goldmark (site/hugo.toml sets `unsafe = true`) passes
// the surrounding raw HTML straight through and still parses the enclosed
// markdown table as a table, given the blank line on each side that keeps
// each raw-HTML line and the table in separate blocks.
func renderReadinessTypesTable(a Artifact) string {
	var b strings.Builder
	b.WriteString(`<div class="readiness-table-wrap" style="overflow-x: auto;">`)
	b.WriteString("\n\n")
	b.WriteString("| Type | Tier | Status | Reason |\n|---|---|---|---|\n")
	for _, r := range a.Types {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", r.Type, r.Tier, r.Status, reasonFor(r))
	}
	b.WriteString("\n</div>\n")
	return b.String()
}

// runRender is the -render entry point: read the committed
// live/readiness.json, replace every doc's span, write back whichever
// changed.
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
	types := renderReadinessTypesTable(artifact)

	for _, rel := range []string{CoverageMDRel, CompatibilityMDRel, ResourceTiersMDRel} {
		if err := renderSpan(root, rel, spanReadinessTable, table); err != nil {
			return err
		}
	}
	if err := renderSpan(root, ResourceTiersMDRel, spanReadinessTypesTable, types); err != nil {
		return err
	}
	return nil
}

// renderSpan rewrites one named span of one doc in place. Parameterized by
// span name (rather than the single spanReadinessTable this function used
// to hardcode) because ResourceTiersMDRel carries two different spans and
// this same logic - read, replace bounds, write only if changed - applies
// to both.
func renderSpan(root, rel, span, body string) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}

	out, err := markers.Replace(rel, string(doc), span, body)
	if err != nil {
		return err
	}
	if out == string(doc) {
		fmt.Fprintf(os.Stderr, "readiness-gen: %s's %q span is already current\n", rel, span)
		return nil
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil { //nolint:gosec // a committed doc, not a secret
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	fmt.Fprintf(os.Stderr, "readiness-gen: rewrote %s's %q span\n", rel, span)
	return nil
}
