// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import "testing"

// TestModuleCallCountIndexAdmitsInjectiveButRefusesCollision is GitHub issue
// #658's shape proof: a module call's own COUNT expands statically and one
// of its own arguments reads count.index, but the two forms this can take
// get different verdicts, and a check that only looks for the token
// "count.index" cannot tell them apart.
//
//   - count-index-module-count-direct reads count.index directly in a
//     template ("n-${count.index}"), the shape [analyzeCountIndexSafety]
//     proves injective. RuleChildModule must admit it.
//   - count-index-module-count-collision indexes a sibling collection at
//     count.index (var.suffixes[count.index]), a shape that cannot be
//     proven injective because what sits at that position is controlled by
//     the collection, not the index. RuleChildModule must still refuse it.
//
// Both fixtures contain the literal token "count.index" in a module call's
// own arguments, so this is the case a keyword-only check cannot
// distinguish; only [unsafeCountIndexHits]'s call into
// [analyzeCountIndexSafety] can.
func TestModuleCallCountIndexAdmitsInjectiveButRefusesCollision(t *testing.T) {
	childModuleRefusals := func(t *testing.T, dir string) []Issue {
		t.Helper()
		cfg := loadConfigDir(t, dir)
		var got []Issue
		for _, iss := range CheckContext(t.Context(), cfg) {
			if iss.Rule == RuleChildModule {
				got = append(got, iss)
			}
		}
		return got
	}

	t.Run("direct read is admitted", func(t *testing.T) {
		got := childModuleRefusals(t, "testdata/count-index-module-count-direct")
		if len(got) != 0 {
			t.Fatalf("want no %s issue for a direct, injective count.index read, got %d: %s",
				RuleChildModule, len(got), summarizeIssues(got))
		}
	})

	t.Run("collection index is refused", func(t *testing.T) {
		got := childModuleRefusals(t, "testdata/count-index-module-count-collision")
		if len(got) != 1 {
			t.Fatalf("want exactly one %s issue for a sibling-collection count.index index, got %d: %s",
				RuleChildModule, len(got), summarizeIssues(got))
		}
		if got[0].Rule != RuleChildModule {
			t.Errorf("want Rule %s, got %s", RuleChildModule, got[0].Rule)
		}
	})
}
