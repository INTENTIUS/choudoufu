// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadinessRenderedSpansAreCurrent is issue #419's staleness guard: it
// holds live/COVERAGE.md's and the docs site's compatibility page's
// readiness-tiers spans byte-for-byte to what renderReadinessTable produces
// from the committed live/readiness.json. Because the comparison is against
// the artifact's already-committed bytes (readCommitted, build_test.go) and
// not a fresh Build(), a live/readiness.json that changes - hand-edited or
// regenerated - without a matching `go run ./tools/readiness-gen -render`
// run fails this test, the same fail-when-stale pattern
// TestContractMDXRenderedSpans (tools/survey-gen) and TestSpansAreCurrent
// (tools/toggles-gen) already apply to their own docs.
func TestReadinessRenderedSpansAreCurrent(t *testing.T) {
	root := testRepoRoot(t)
	artifact := readCommitted(t, root)
	want := renderReadinessTable(artifact)

	for _, rel := range []string{CoverageMDRel, CompatibilityMDRel} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		md := string(doc)

		got, err := markers.Content(rel, md, spanReadinessTable)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if got != want {
			t.Errorf("%s's %q span is stale; run `go run ./tools/readiness-gen -render` and commit the result.\n--- committed ---\n%s\n--- rendered ---\n%s",
				rel, spanReadinessTable, got, want)
		}

		// The whole-file check catches what the per-span one cannot: the
		// marker pair itself going missing or duplicated.
		out, err := markers.Replace(rel, md, spanReadinessTable, want)
		if err != nil {
			t.Errorf("rendering %s: %v", rel, err)
			continue
		}
		if out != md {
			t.Errorf("%s differs from its rendered form; run `go run ./tools/readiness-gen -render` and commit the result", rel)
		}
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
