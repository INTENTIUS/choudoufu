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
// computed once from the given (already-committed) artifact and the same
// git-derived stamp runRender uses, so TestReadinessRenderedSpansAreCurrent
// and any other test that needs the same list cannot drift from runRender's
// own list in render.go.
func wantedSpans(t *testing.T, root string, a Artifact) []wantedSpan {
	t.Helper()
	stamp, err := readinessStamp(root)
	if err != nil {
		t.Fatalf("readinessStamp: %v", err)
	}
	table := renderReadinessTable(a, stamp)
	return []wantedSpan{
		{CoverageMDRel, spanReadinessTable, table},
	}
}

// TestReadinessSiteDataIsCurrent is the same staleness guard for the docs
// site's copy (#1055): site/data/readiness.json must equal what
// renderSiteData produces from the committed live/readiness.json and the
// same git-derived stamp runRender uses.
func TestReadinessSiteDataIsCurrent(t *testing.T) {
	root := testRepoRoot(t)
	artifact := readCommitted(t, root)
	stamp, err := readinessStamp(root)
	if err != nil {
		t.Fatalf("readinessStamp: %v", err)
	}
	want, err := renderSiteData(artifact, stamp)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(SiteDataRel))) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v (run `go run ./tools/readiness-gen -render` and commit the result)", SiteDataRel, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is stale; run `go run ./tools/readiness-gen -render` and commit the result", SiteDataRel)
	}
}

// TestReadinessRenderedSpansAreCurrent is issue #419's staleness guard: it
// holds every (doc, span) pair wantedSpans lists
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

	for _, w := range wantedSpans(t, root, artifact) {
		// Read fresh per case so each case's whole-file Replace check
		// below sees the doc as committed.
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
		// marker pair itself going missing or duplicated.
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

// TestReadinessSiteDataCarriesEveryRow is the per-type rows' own
// completeness and safety check, independent of whatever is currently
// committed to SiteDataRel (that byte-for-byte comparison is
// TestReadinessSiteDataIsCurrent above): every row of the artifact produces
// exactly one type row, every reason is one line, and a reason is present
// exactly when the row is short of in-contract.
func TestReadinessSiteDataCarriesEveryRow(t *testing.T) {
	root := testRepoRoot(t)
	artifact := readCommitted(t, root)
	data := buildSiteData(artifact, "stamp")

	if len(data.Types) != len(artifact.Types) {
		t.Errorf("buildSiteData produced %d type rows, live/readiness.json has %d types", len(data.Types), len(artifact.Types))
	}
	if data.Total != artifact.Counts.Types {
		t.Errorf("buildSiteData's cross tab sums to %d, want Counts.Types %d", data.Total, artifact.Counts.Types)
	}

	for _, r := range artifact.Types {
		reason := reasonFor(r)
		if strings.Contains(reason, "\n") {
			t.Errorf("%s: reasonFor contains a newline; the site renders it as one table cell: %q", r.Type, reason)
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
// the tier definitions (#417)'s own tier D section found that neither
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
