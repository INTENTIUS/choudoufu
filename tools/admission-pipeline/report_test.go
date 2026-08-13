// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
	"time"
)

func TestDiffCountsLine(t *testing.T) {
	cases := []struct {
		name          string
		before, after map[string]any
		want          string
	}{
		{
			name:   "new artifact (no before)",
			before: nil,
			after:  map[string]any{"types": float64(10)},
			want:   "types=10 (new)",
		},
		{
			name:   "removed artifact (no after)",
			before: map[string]any{"types": float64(10)},
			after:  nil,
			want:   "(removed)",
		},
		{
			name:   "unchanged",
			before: map[string]any{"types": float64(10)},
			after:  map[string]any{"types": float64(10)},
			want:   "types=10",
		},
		{
			name:   "increased",
			before: map[string]any{"types": float64(10)},
			after:  map[string]any{"types": float64(13)},
			want:   "types=13 (+3)",
		},
		{
			name:   "decreased",
			before: map[string]any{"types": float64(13)},
			after:  map[string]any{"types": float64(10)},
			want:   "types=10 (-3)",
		},
		{
			name:   "new key not present before",
			before: map[string]any{"types": float64(10)},
			after:  map[string]any{"types": float64(10), "mapped": float64(4)},
			want:   "mapped=4 (new), types=10",
		},
		{
			name:   "nested object skipped",
			before: map[string]any{"types": float64(10)},
			after:  map[string]any{"types": float64(10), "by_via": map[string]any{"name": float64(3)}},
			want:   "types=10",
		},
		{
			name:   "no scalar counts at all",
			before: map[string]any{},
			after:  map[string]any{"by_via": map[string]any{"name": float64(3)}},
			want:   "(no scalar counts)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := diffCountsLine(c.before, c.after)
			if got != c.want {
				t.Errorf("diffCountsLine(%v, %v) = %q, want %q", c.before, c.after, got, c.want)
			}
		})
	}
}

func TestFindRowGenSummary(t *testing.T) {
	stderr := "row-gen: reading live/registry.json\n" +
		"row-gen: 919 mapped types (312 server-assigned, 401 client-named, 88 needs-hand-separator, 118 evidence-only)\n" +
		"row-gen: done\n"
	got := findRowGenSummary(stderr)
	want := "row-gen: 919 mapped types (312 server-assigned, 401 client-named, 88 needs-hand-separator, 118 evidence-only)"
	if got != want {
		t.Errorf("findRowGenSummary = %q, want %q", got, want)
	}
}

func TestFindRowGenSummary_NoMatch(t *testing.T) {
	if got := findRowGenSummary("row-gen: reading live/registry.json\n"); got != "" {
		t.Errorf("findRowGenSummary with no summary line = %q, want \"\"", got)
	}
}

func TestRenderReport_DetectSkipped(t *testing.T) {
	in := ReportInput{GeneratedAt: time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)}
	out := renderReport(in)
	if !strings.Contains(out, "DETECT was skipped") {
		t.Errorf("renderReport with nil Drift: want a DETECT-skipped note, got:\n%s", out)
	}
	if !strings.Contains(out, "REGENERATE was skipped") {
		t.Errorf("renderReport with Regenerated=false: want a REGENERATE-skipped note, got:\n%s", out)
	}
}

func TestRenderReport_FullFixture(t *testing.T) {
	in := ReportInput{
		GeneratedAt: time.Date(2026, 8, 12, 1, 2, 0, 0, time.UTC),
		Drift: &DriftReport{
			Provider: PinState{Name: "provider hashicorp/aws", Pinned: "6.58.0", Current: "6.59.0", Drifted: true},
			Registry: PinState{Name: "CFN registry schema (#42)", Pinned: "sha256:aaa (1653 types)", Current: "sha256:aaa (1653 types)", Drifted: false},
		},
		Regenerated: true,
		Artifacts: []ArtifactDelta{
			{
				Path:         "live/mapping.json",
				LinesAdded:   12,
				LinesRemoved: 4,
				Before:       map[string]any{"types": float64(1690), "mapped": float64(900)},
				After:        map[string]any{"types": float64(1691), "mapped": float64(919)},
			},
		},
		RowGenSummary: "row-gen: 919 mapped types (312 server-assigned, 401 client-named, 88 needs-hand-separator, 118 evidence-only)",
		ProposalPath:  "tmp/admission-pipeline/row-gen-proposals.txt",
		Mapping:       MappingSummary{Former2Contradictions: 3, None: 754, Unclassifiable: 700},
	}

	out := renderReport(in)

	wantContains := []string{
		"provider hashicorp/aws",
		"6.58.0",
		"6.59.0",
		"DRIFT",
		"live/mapping.json",
		"+12/-4",
		"mapped=919 (+19)",
		"types=1691 (+1)",
		"919 mapped types",
		"tmp/admission-pipeline/row-gen-proposals.txt",
		"former2 contradictions: 3",
		"754 total, 700 unclassifiable",
	}
	for _, want := range wantContains {
		if !strings.Contains(out, want) {
			t.Errorf("renderReport output missing %q; full output:\n%s", want, out)
		}
	}

	// No attribution trailer anywhere in a generated PR-body-shaped document
	// (this is what -pr passes as the PR body - the repo directive applies
	// here too, and there's no reason for REPORT to ever emit one).
	if strings.Contains(out, "Co-Authored-By") {
		t.Error("renderReport output carries a Co-Authored-By trailer; REPORT must never emit one")
	}
}

func TestArtifactCounts_MissingFile(t *testing.T) {
	root := t.TempDir()
	if got := artifactCounts(root, "", "live/does-not-exist.json"); got != nil {
		t.Errorf("artifactCounts for a missing file = %v, want nil", got)
	}
}

// TestReadMappingSummaryAgainstCommittedMapping locks readMappingSummary's
// JSON tag to live/mapping.json's real header shape: issue #53 renamed the
// header's own via:"none" count from "none" to "unclassified"
// (tools/mapping-gen/mapping.go's MappingCounts), and this struct here
// decodes that same file independently (mapping-gen is package main and not
// importable - see mappingUnexplainedNote's comment) with no compiler tie
// between the two field tags, so a future rename here can silently decode
// to zero with no test catching it. This test would have caught issue #53's
// own rename doing exactly that.
func TestReadMappingSummaryAgainstCommittedMapping(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	summary, err := readMappingSummary(root)
	if err != nil {
		t.Fatalf("readMappingSummary: %v", err)
	}
	if summary.None == 0 {
		t.Error("readMappingSummary reports None = 0 against the committed live/mapping.json; the header's unclassified count did not decode (a JSON tag mismatch)")
	}
	if summary.None != summary.Unclassifiable {
		// True as of issue #53's mechanical pass: every remaining via:"none"
		// row carries the same generic unexplained note, so the two counts
		// coincide. A future overlay.json nones entry with its own curated
		// (non-generic) reason would make them diverge again - if that
		// happens, update this assertion rather than deleting it, so the
		// two fields stay independently exercised.
		t.Errorf("None = %d, Unclassifiable = %d; expected them equal today (see comment)", summary.None, summary.Unclassifiable)
	}
}
