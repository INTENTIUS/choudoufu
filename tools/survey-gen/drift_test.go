// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// This file is issue #169's gate. Until it existed the two survey artifacts
// had none, alone among this repository's generated artifacts -
// live/registry.json has TestRegistryCounts_MatchIssue42ReferenceValues,
// live/rowgen-convergence.json has TestConvergenceArtifactMatchesCommitted,
// the generated tables have TestEmitFilesMatchCommitted, and
// live/LIMITATIONS.md's spans have their own render check.
//
// live/pins_drift_test.go looks like it covers this and does not: it reads
// provider_version out of the artifact's OWN header, so it catches a pin
// bump nobody regenerated for and cannot catch an artifact that is stale at
// the same pin. That is exactly what had happened. Regenerating with no code
// change at all moved four of six path counts, account-derived 2 -> 19 among
// them, and nothing failed.
//
// These artifacts need a provider to regenerate, so the gate compares the
// committed file against a committed expectation rather than rebuilding it.
// That is the same shape registry-gen's reference counts use.

// surveyExpectation is what one artifact's headline numbers must be.
//
// A number here moves for one of two reasons, and they want different
// responses:
//
//   - The pinned provider release changed what it publishes. Check
//     internal/live/pins.AWSProviderVersion first; if it moved, this is an
//     ordinary regeneration and the new values go in with the artifact.
//   - The classifier changed. Then the question is whether the new
//     distribution is the intended consequence, and the commit should say
//     which types moved and why - #167 moved parent-derived 65 -> 47 and
//     said so.
//
// Either way it is an edit, never a silent adjustment.
type surveyExpectation struct {
	Rel    string
	Types  int
	Counts struct {
		Taggable       int
		ListResource   int
		IdentitySchema int
	}
	Paths map[string]int
}

var surveyExpectations = []surveyExpectation{
	{
		Rel:   "live/survey.json",
		Types: 68,
		Counts: struct {
			Taggable       int
			ListResource   int
			IdentitySchema int
		}{Taggable: 47, ListResource: 58, IdentitySchema: 61},
		Paths: map[string]int{
			"marker":               37,
			"client-named":         14,
			"list + content match": 6,
			"moves to Ops":         5,
			"parent-derived":       4,
			"account-derived":      2,
		},
	},
	{
		Rel:   "live/survey-full.json",
		Types: 1699,
		Counts: struct {
			Taggable       int
			ListResource   int
			IdentitySchema int
		}{Taggable: 847, ListResource: 195, IdentitySchema: 479},
		Paths: map[string]int{
			"marker":               790,
			"moves to Ops":         702,
			"client-named":         117,
			"parent-derived":       47,
			"list + content match": 24,
			"account-derived":      19,
		},
	},
}

// TestSurveyArtifactsMatchTheirExpectations is the gate.
func TestSurveyArtifactsMatchTheirExpectations(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range surveyExpectations {
		t.Run(want.Rel, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, want.Rel)) //nolint:gosec // a fixed path in the checkout
			if err != nil {
				t.Fatalf("reading %s: %v", want.Rel, err)
			}
			var got Survey
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("decoding %s: %v", want.Rel, err)
			}

			if len(got.Types) != want.Types {
				t.Errorf("%s carries %d rows, want %d", want.Rel, len(got.Types), want.Types)
			}
			if got.Counts.Taggable != want.Counts.Taggable ||
				got.Counts.ListResource != want.Counts.ListResource ||
				got.Counts.IdentitySchema != want.Counts.IdentitySchema {
				t.Errorf("%s raw signals are (taggable %d, list %d, identity %d), want (%d, %d, %d)",
					want.Rel, got.Counts.Taggable, got.Counts.ListResource, got.Counts.IdentitySchema,
					want.Counts.Taggable, want.Counts.ListResource, want.Counts.IdentitySchema)
			}

			paths := map[string]int{}
			for _, row := range got.Types {
				paths[row.Path]++
			}
			for path, n := range want.Paths {
				if paths[path] != n {
					t.Errorf("%s has %d rows on path %q, want %d - regenerate (`just survey`) and, if the move is intended, edit surveyExpectations saying which of the two causes it was",
						want.Rel, paths[path], path, n)
				}
			}
			for path, n := range paths {
				if _, expected := want.Paths[path]; !expected {
					t.Errorf("%s has %d rows on unexpected path %q; add it to surveyExpectations", want.Rel, n, path)
				}
			}
		})
	}
}
