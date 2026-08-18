// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPropertyPathValue pins the Cloud Control Properties walk against
// every shape [scanTypeContentMatch] hands it: a top-level property, a
// property nested one level inside a wrapped config object (the CloudFront
// cache/origin-request policy shape), and the refusals - a missing
// segment, a non-object in the way, and a non-string leaf - none of which
// are ever guessed at.
func TestPropertyPathValue(t *testing.T) {
	tests := []struct {
		name   string
		props  map[string]any
		path   []string
		want   string
		wantOK bool
	}{
		{
			name:   "top-level property",
			props:  map[string]any{"Name": "my-policy", "Id": "abc-123"},
			path:   []string{"Name"},
			want:   "my-policy",
			wantOK: true,
		},
		{
			name: "wrapped config object (CachePolicyConfig.Name)",
			props: map[string]any{
				"Id": "abc-123",
				"CachePolicyConfig": map[string]any{
					"Name":    "my-policy",
					"Comment": "a comment",
				},
			},
			path:   []string{"CachePolicyConfig", "Name"},
			want:   "my-policy",
			wantOK: true,
		},
		{
			name:   "missing top-level segment",
			props:  map[string]any{"Id": "abc-123"},
			path:   []string{"Name"},
			wantOK: false,
		},
		{
			name: "missing nested segment",
			props: map[string]any{
				"CachePolicyConfig": map[string]any{"Comment": "no Name here"},
			},
			path:   []string{"CachePolicyConfig", "Name"},
			wantOK: false,
		},
		{
			name: "a non-object in the way of a longer path",
			props: map[string]any{
				"CachePolicyConfig": "not an object",
			},
			path:   []string{"CachePolicyConfig", "Name"},
			wantOK: false,
		},
		{
			name:   "a non-string leaf",
			props:  map[string]any{"Name": float64(42)},
			path:   []string{"Name"},
			wantOK: false,
		},
		{
			name:   "nil properties",
			props:  nil,
			path:   []string{"Name"},
			wantOK: false,
		},
		{
			name:   "empty path",
			props:  map[string]any{"Name": "x"},
			path:   nil,
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := propertyPathValue(tc.props, tc.path)
			if ok != tc.wantOK {
				t.Fatalf("propertyPathValue ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("propertyPathValue = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStaticArgumentValue exercises every shape [scanTypeContentMatch]'s
// static read has to tell apart, against a real parsed configuration
// (testdata/contentmatch-static): a literal value, a value read through a
// local (statically evaluable), an argument the block never sets, an empty
// string, and a value that depends on another resource's own attribute
// (not knowable until the run is under way, so it disqualifies rather than
// evaluates to anything).
func TestStaticArgumentValue(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "contentmatch-static"))

	tests := []struct {
		addr    string
		want    string
		wantWhy string // substring expected in the failure reason, or "" for success
	}{
		{addr: "aws_cloudfront_cache_policy.literal", want: "literal-name"},
		{addr: "aws_cloudfront_cache_policy.from_local", want: "from-a-local"},
		{addr: "aws_cloudfront_cache_policy.no_name", wantWhy: "sets no name argument"},
		{addr: "aws_cloudfront_cache_policy.empty", wantWhy: "is empty"},
		{addr: "aws_cloudfront_cache_policy.dynamic", wantWhy: "not known until the run is under way"},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			rc, ok := cfg.Module.ManagedResources[tc.addr]
			if !ok {
				t.Fatalf("fixture does not declare %s", tc.addr)
			}
			got, why := staticArgumentValue(t.Context(), cfg.Module, rc, "name")
			if tc.wantWhy != "" {
				if why == "" {
					t.Fatalf("staticArgumentValue succeeded with %q, want a refusal containing %q", got, tc.wantWhy)
				}
				if !strings.Contains(why, tc.wantWhy) {
					t.Errorf("staticArgumentValue reason = %q, want it to contain %q", why, tc.wantWhy)
				}
				return
			}
			if why != "" {
				t.Fatalf("staticArgumentValue refused: %s", why)
			}
			if got != tc.want {
				t.Errorf("staticArgumentValue = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDecideContentMatch is issue #272's own verification bar, pinned
// directly against the pure decision function: zero matches leaves the
// instance unbound (both fields nil, the same shape bind() already reads
// as "propose a create"); exactly one match binds it; two or more is a
// refusal that names every candidate and never guesses which one is right.
func TestDecideContentMatch(t *testing.T) {
	addr := mustAddr(t, "aws_cloudfront_cache_policy.example")

	t.Run("zero matches - unbound, unchanged behavior", func(t *testing.T) {
		got := decideContentMatch("aws_cloudfront_cache_policy", addr, "aws_cloudfront_cache_policy.example",
			"name", "CachePolicyConfig.Name", "my-policy", nil)
		if got.Claimant != nil || got.Problem != nil {
			t.Errorf("decideContentMatch with zero matches = %+v, want both fields nil", got)
		}
	})

	t.Run("one match - binds", func(t *testing.T) {
		got := decideContentMatch("aws_cloudfront_cache_policy", addr, "aws_cloudfront_cache_policy.example",
			"name", "CachePolicyConfig.Name", "my-policy",
			[]cloudControlCandidate{{identifier: "658327ea-f89d-4fab-a63d-7e88639e58f6", value: "my-policy"}})
		if got.Problem != nil {
			t.Fatalf("decideContentMatch with one match produced a Problem: %+v", got.Problem)
		}
		if got.Claimant == nil {
			t.Fatal("decideContentMatch with one match produced no Claimant")
		}
		if got.Claimant.importID != "658327ea-f89d-4fab-a63d-7e88639e58f6" {
			t.Errorf("Claimant.importID = %q, want the matched candidate's identifier", got.Claimant.importID)
		}
		if got.Claimant.identityAttr != "id" {
			t.Errorf("Claimant.identityAttr = %q, want %q", got.Claimant.identityAttr, "id")
		}
	})

	t.Run("one match, uncomposable multi-part identifier - refuses, does not fall back to a guess", func(t *testing.T) {
		// A "|"-joined identifier resolveCloudControlImportID cannot
		// compose (no identity table entry for a made-up type) must
		// refuse, not silently hand out the raw joined string.
		got := decideContentMatch("aws_made_up_type", addr, "aws_made_up_type.example",
			"name", "Config.Name", "my-policy",
			[]cloudControlCandidate{{identifier: "part-one|part-two", value: "my-policy"}})
		if got.Claimant != nil {
			t.Fatalf("decideContentMatch bound an uncomposable multi-part identifier: %+v", got.Claimant)
		}
		if got.Problem == nil {
			t.Fatal("decideContentMatch with an uncomposable identifier produced no Problem")
		}
		if got.Problem.Kind != ProblemUncomposableIdentifier {
			t.Errorf("Problem.Kind = %q, want %q", got.Problem.Kind, ProblemUncomposableIdentifier)
		}
	})

	t.Run("two or more matches - refuses, never guesses", func(t *testing.T) {
		matches := []cloudControlCandidate{
			{identifier: "id-one", value: "my-policy"},
			{identifier: "id-two", value: "my-policy"},
		}
		got := decideContentMatch("aws_cloudfront_cache_policy", addr, "aws_cloudfront_cache_policy.example",
			"name", "CachePolicyConfig.Name", "my-policy", matches)
		if got.Claimant != nil {
			t.Fatalf("decideContentMatch with two matches produced a Claimant instead of refusing: %+v", got.Claimant)
		}
		if got.Problem == nil {
			t.Fatal("decideContentMatch with two matches produced no Problem")
		}
		if got.Problem.Kind != ProblemAmbiguousContentMatch {
			t.Errorf("Problem.Kind = %q, want %q", got.Problem.Kind, ProblemAmbiguousContentMatch)
		}
		if len(got.Problem.LiveIDs) != 2 {
			t.Errorf("Problem.LiveIDs = %v, want both candidates named", got.Problem.LiveIDs)
		}
		for _, id := range []string{"id-one", "id-two"} {
			found := false
			for _, got := range got.Problem.LiveIDs {
				if got == id {
					found = true
				}
			}
			if !found {
				t.Errorf("Problem.LiveIDs = %v, missing %q", got.Problem.LiveIDs, id)
			}
		}
	})

	t.Run("three matches is still a refusal, not a special case", func(t *testing.T) {
		matches := []cloudControlCandidate{
			{identifier: "id-one", value: "dup"},
			{identifier: "id-two", value: "dup"},
			{identifier: "id-three", value: "dup"},
		}
		got := decideContentMatch("aws_cloudfront_cache_policy", addr, "aws_cloudfront_cache_policy.example",
			"name", "CachePolicyConfig.Name", "dup", matches)
		if got.Claimant != nil || got.Problem == nil || got.Problem.Kind != ProblemAmbiguousContentMatch {
			t.Errorf("decideContentMatch with three matches = %+v, want an AMBIGUOUS_CONTENT_MATCH refusal", got)
		}
		if len(got.Problem.LiveIDs) != 3 {
			t.Errorf("Problem.LiveIDs = %v, want all three candidates named", got.Problem.LiveIDs)
		}
	})
}
