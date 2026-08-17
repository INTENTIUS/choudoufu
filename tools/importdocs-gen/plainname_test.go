// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestPlainNameMatches pins both widenings and, more importantly, the shapes
// they must NOT match.
//
// The bar here is asymmetric on purpose. A missed match leaves a grammar row
// with no arguments, which is the state every one of these rows was already
// in. A wrong match attributes an import-ID segment to an argument that does
// not supply it, and that renders a wrong identity - which outranks a missing
// one every time. So the negative cases are the point of this test.
func TestPlainNameMatches(t *testing.T) {
	for _, tc := range []struct {
		cand, arg string
		want      bool
		why       string
	}{
		// Exact, the case that always worked.
		{"user", "user", true, "identical"},
		{"applicationid", "application_id", true, "normalization already ignored the underscore"},

		// The generic head noun the prose adds and the schema leaves off.
		// aws_iam_user_group_membership's "the user name" against `user`.
		{"username", "user", true, "trailing head noun 'name' dropped"},
		{"analysisid", "analysis", true, "trailing head noun 'id' dropped"},
		{"gatewayarn", "gateway", true, "trailing head noun 'arn' dropped"},

		// Plural in the prose, singular in the schema, and the reverse.
		// "group names" against `groups`.
		{"groupnames", "groups", true, "head noun dropped, then plural tolerated"},
		{"groups", "group", true, "plural candidate, singular argument"},
		{"group", "groups", true, "singular candidate, plural argument"},

		// Negatives. Each of these would be a wrong attribution.
		{"name", "user", false, "a bare head noun must not match an unrelated argument"},
		{"id", "application_id", false, "a bare head noun must not match by suffix"},
		{"arn", "gateway", false, "dropping the whole candidate would match everything"},
		{"policyname", "user", false, "unrelated stem"},
		{"keyname", "key_id", false, "different arguments that share a head noun are still different"},
		{"", "user", false, "empty candidate"},
		{"user", "", false, "empty argument"},
	} {
		if got := plainNameMatches(tc.cand, tc.arg); got != tc.want {
			t.Errorf("plainNameMatches(%q, %q) = %v, want %v - %s", tc.cand, tc.arg, got, tc.want, tc.why)
		}
	}
}

// TestTrimGenericHeadNeverEmptiesTheCandidate pins the length guard.
//
// Honest about what it is worth: this guard is defence in depth, not the
// load-bearing one. Relaxing `len(cand) > len(head)` to `>=` keeps every case
// above passing, because a trimmed-to-empty candidate is then simply skipped
// and an empty string can never equal a real argument name - so "the name"
// could not match everything even without it. Verified by mutating the
// comparison and watching nothing go red.
//
// It stays because it says the intent at the point of the trim rather than
// leaving it to be re-derived from plainNameMatches' skip, and it is pinned
// so that a later change which DOES make the empty case reachable - a matcher
// that treats "" as a wildcard, a caller that stops skipping - fails here
// instead of silently attributing every segment.
func TestTrimGenericHeadNeverEmptiesTheCandidate(t *testing.T) {
	for _, head := range genericHeads {
		if got := trimGenericHead(head); got != "" {
			t.Errorf("trimGenericHead(%q) = %q, want \"\" - a bare head noun has no stem", head, got)
		}
	}
	if got := trimGenericHead("user"); got != "" {
		t.Errorf("trimGenericHead(%q) = %q, want \"\" - nothing to trim is not a match", "user", got)
	}
}
