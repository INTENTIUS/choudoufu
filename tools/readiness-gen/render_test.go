// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wantedSpan is one (doc, span) pair TestReadinessRenderedSpansAreCurrent
// holds to a rendered body.
type wantedSpan struct {
	Rel  string
	Span string
	Want string
}

// wantedSpans lists every span this generator's -render mode writes,
// computed once from the given (already-committed) artifact so
// TestReadinessRenderedSpansAreCurrent and any other test that needs the
// same list cannot drift from runRender's own list in render.go.
func wantedSpans(a Artifact) []wantedSpan {
	table := renderReadinessTable(a)
	types := renderReadinessTypesTable(a)
	return []wantedSpan{
		{CoverageMDRel, spanReadinessTable, table},
		{CompatibilityMDRel, spanReadinessTable, table},
		{ResourceTiersMDRel, spanReadinessTable, table},
		{ResourceTiersMDRel, spanReadinessTypesTable, types},
	}
}

// TestReadinessRenderedSpansAreCurrent is issue #419's staleness guard,
// extended by issue #420 to the new resource-tiers page and its
// readiness-types span: it holds every (doc, span) pair wantedSpans lists
// byte-for-byte to what render.go's own render functions produce from the
// committed live/readiness.json. Because the comparison is against the
// artifact's already-committed bytes (readCommitted, build_test.go) and not
// a fresh Build(), a live/readiness.json that changes - hand-edited or
// regenerated - without a matching `go run ./tools/readiness-gen -render`
// run fails this test, the same fail-when-stale pattern
// TestContractMDXRenderedSpans (tools/survey-gen) and TestSpansAreCurrent
// (tools/toggles-gen) already apply to their own docs.
func TestReadinessRenderedSpansAreCurrent(t *testing.T) {
	root := testRepoRoot(t)
	artifact := readCommitted(t, root)

	for _, w := range wantedSpans(artifact) {
		// Read fresh per case rather than once per Rel: ResourceTiersMDRel
		// carries two spans, and each case's whole-file Replace check below
		// needs the doc as committed, not as a previous case in this loop
		// may have left it in memory (nothing is written back here, but
		// reading fresh keeps the two checks independent by construction).
		path := filepath.Join(root, filepath.FromSlash(w.Rel))
		doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
		if err != nil {
			t.Fatalf("reading %s: %v", w.Rel, err)
		}
		md := string(doc)

		got, err := markers.Content(w.Rel, md, w.Span)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if got != w.Want {
			t.Errorf("%s's %q span is stale; run `go run ./tools/readiness-gen -render` and commit the result.\n--- committed ---\n%s\n--- rendered ---\n%s",
				w.Rel, w.Span, got, w.Want)
		}

		// The whole-file check catches what the per-span one cannot: the
		// marker pair itself going missing or duplicated. Replacing only
		// the one named span leaves any other span in the same doc
		// untouched, so this is safe to run per (doc, span) pair even for
		// ResourceTiersMDRel's two spans.
		out, err := markers.Replace(w.Rel, md, w.Span, w.Want)
		if err != nil {
			t.Errorf("rendering %s: %v", w.Rel, err)
			continue
		}
		if out != md {
			t.Errorf("%s differs from its rendered form; run `go run ./tools/readiness-gen -render` and commit the result", w.Rel)
		}
	}
}

// TestReadinessTypesTableRendersEveryRow is the readiness-types span's own
// completeness and safety check, independent of whatever is currently
// committed to ResourceTiersMDRel (that byte-for-byte comparison is
// TestReadinessRenderedSpansAreCurrent above): every row of the artifact
// produces exactly one data row in the rendered table, and no reason string
// contains a literal "|" or a newline, either of which would silently
// corrupt the markdown table it sits in.
func TestReadinessTypesTableRendersEveryRow(t *testing.T) {
	root := testRepoRoot(t)
	artifact := readCommitted(t, root)
	table := renderReadinessTypesTable(artifact)

	dataRows := 0
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, "| `") {
			dataRows++
		}
	}
	if dataRows != len(artifact.Types) {
		t.Errorf("renderReadinessTypesTable produced %d data rows, live/readiness.json has %d types", dataRows, len(artifact.Types))
	}

	for _, r := range artifact.Types {
		reason := reasonFor(r)
		if strings.Contains(reason, "|") {
			t.Errorf("%s: reasonFor contains a literal \"|\", which would split its markdown table row: %q", r.Type, reason)
		}
		if strings.Contains(reason, "\n") {
			t.Errorf("%s: reasonFor contains a newline, which would break its markdown table row: %q", r.Type, reason)
		}
		if r.Status == StatusInContract && reason != "" {
			t.Errorf("%s: in-contract row has a non-empty reason %q; the ruling's four tiers name no defect for an in-contract type", r.Type, reason)
		}
		if r.Status != StatusInContract && reason == "" {
			t.Errorf("%s: status %q is not in-contract but reasonFor returned an empty string", r.Type, r.Status)
		}
	}
}

// TestExcludedRowsLinkLimitationsMD is issue #420's own accept criterion:
// every tier D / excluded row's reason links live/LIMITATIONS.md at a real
// anchor. Checked against the file itself, not asserted, because
// rulings/20260828-readiness-tiers.md's own tier D section found that neither
// sanctioned exclusion has a heading of its own there yet - see reasonFor's
// comment for why "unadmitted-type" is the accurate anchor today.
func TestExcludedRowsLinkLimitationsMD(t *testing.T) {
	root := testRepoRoot(t)
	artifact := readCommitted(t, root)

	doc, err := os.ReadFile(filepath.Join(root, filepath.FromSlash("live/LIMITATIONS.md"))) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading live/LIMITATIONS.md: %v", err)
	}
	if !strings.Contains(string(doc), "\n### unadmitted-type\n") {
		t.Fatalf("live/LIMITATIONS.md no longer has an \"### unadmitted-type\" heading; reasonFor's excluded-row link needs a real anchor, and this generator's build does not check markdown anchors for it")
	}

	excludedCount := 0
	for _, r := range artifact.Types {
		if r.Status != StatusExcluded {
			continue
		}
		excludedCount++
		reason := reasonFor(r)
		if !strings.Contains(reason, "LIMITATIONS.md#unadmitted-type") {
			t.Errorf("%s: excluded row's reason does not link live/LIMITATIONS.md#unadmitted-type: %q", r.Type, reason)
		}
	}
	if excludedCount == 0 {
		t.Fatal("live/readiness.json has no excluded rows; this test is checking nothing - tier D's population moved or the artifact is stale")
	}
}

// TestReadinessCrossTabCoversEveryTierAndStatus proves renderReadinessTable
// is not a static table that happens to match live/readiness.json today: it
// checks the artifact's own tier and status marginals (Counts.Tiers,
// Counts.Statuses) against readinessCrossTab's per-cell tally, and checks
// that every (tier, status) pair the artifact actually contains is one this
// file's tierOrder/statusOrder can render - a tier or status name added to
// build.go without a matching row or column here would otherwise be
// silently dropped from the rendered table instead of failing a test. Made
// to fail on purpose while writing this test, by removing
// TierExcludedByDesign from tierOrder: it failed with a "tier ... not in
// tierOrder" error, as expected.
func TestReadinessCrossTabCoversEveryTierAndStatus(t *testing.T) {
	root := testRepoRoot(t)
	artifact := readCommitted(t, root)
	cross := readinessCrossTab(artifact)

	knownTier := make(map[string]bool, len(tierOrder))
	for _, t := range tierOrder {
		knownTier[t] = true
	}
	knownStatus := make(map[string]bool, len(statusOrder))
	for _, s := range statusOrder {
		knownStatus[s] = true
	}

	tierTotal := map[string]int{}
	statusTotal := map[string]int{}
	grand := 0
	for k, n := range cross {
		if !knownTier[k.Tier] {
			t.Errorf("live/readiness.json has tier %q, which is not in tierOrder; renderReadinessTable would silently drop its rows", k.Tier)
		}
		if !knownStatus[k.Status] {
			t.Errorf("live/readiness.json has status %q, which is not in statusOrder; renderReadinessTable would silently drop its column", k.Status)
		}
		tierTotal[k.Tier] += n
		statusTotal[k.Status] += n
		grand += n
	}

	if grand != artifact.Counts.Types {
		t.Errorf("readinessCrossTab sums to %d, want Counts.Types %d", grand, artifact.Counts.Types)
	}
	for tier, want := range artifact.Counts.Tiers {
		if got := tierTotal[tier]; got != want {
			t.Errorf("readinessCrossTab's %q row sums to %d, Counts.Tiers says %d", tier, got, want)
		}
	}
	for status, want := range artifact.Counts.Statuses {
		if got := statusTotal[status]; got != want {
			t.Errorf("readinessCrossTab's %q column sums to %d, Counts.Statuses says %d", status, got, want)
		}
	}
}
