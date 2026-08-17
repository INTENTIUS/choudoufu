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
		// "X ID" and "X ARN" are deliberately NOT trimmed. They name a property
		// OF X - usually one the server assigns - where "X name" names X
		// itself. aws_athena_named_query's "the query ID" trimmed to `query`,
		// which is the resource's SQL text, and overwrote a correct
		// server-assigned reading.
		{"analysisid", "analysis", false, "an ID is a property of the thing, not the thing"},
		{"gatewayarn", "gateway", false, "an ARN is a property of the thing, not the thing"},

		// Plural in the prose, singular in the schema, and the reverse.
		// "group names" against `groups`.
		{"groupnames", "groups", true, "head noun dropped, then plural tolerated"},
		{"groups", "group", true, "plural candidate, singular argument"},
		{"group", "groups", true, "singular candidate, plural argument"},

		// Negatives. Each of these would be a wrong attribution.
		{"name", "user", false, "a bare head noun must not match an unrelated argument"},
		{"id", "application_id", false, "a bare head noun must not match by suffix"},
		{"name", "gateway", false, "dropping the whole candidate would match everything"},
		{"policyname", "user", false, "unrelated stem"},
		// The shape the original rule got wrong, and the one the old negative
		// cases all missed: they were unrelated stems, while the real failure
		// is a RELATED stem that is a sibling argument. "the identity and
		// policy name" on aws_ses_identity_policy names `name`; trimming to
		// `policy` picks the IAM policy JSON document instead, and the row was
		// correct before the trim existed.
		{"policyname", "policy", true, "plainNameMatches alone cannot see the sibling; " +
			"plainNameMatchesAmong is what withdraws this - see TestPlainNameMatchesAmong"},
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
// TestPlainNameMatchesAmong is the guard the audit's finding turned into a
// test: a trimmed match is withdrawn when the head noun is itself an argument
// of the same resource, because then the phrase names the head and the stem
// only qualifies it.
func TestPlainNameMatchesAmong(t *testing.T) {
	for _, tc := range []struct {
		cand, arg string
		others    []string
		want      bool
		why       string
	}{
		{"policyname", "policy", []string{"identity", "policy", "name"}, false,
			"aws_ses_identity_policy declares `name`, so \"policy name\" is that one"},
		{"policyname", "name", []string{"identity", "policy", "name"}, false,
			"the stem does not match `name` either; the phrase resolves by exact match, not here"},
		{"username", "user", []string{"user", "groups"}, true,
			"aws_iam_user_group_membership declares no `name`, so the head is decoration"},
		{"bucketname", "bucket", []string{"bucket", "policy"}, true,
			"aws_s3_bucket_policy declares no `name`"},
		{"groupnames", "groups", []string{"user", "groups"}, true,
			"trim then plural, with no `name` argument to withdraw it"},
		{"user", "user", []string{"user", "name"}, true,
			"an EXACT match is never withdrawn, whatever else the resource declares"},
		{"groups", "group", []string{"group", "name"}, true,
			"nor is a plural one"},
	} {
		if got := plainNameMatchesAmong(tc.cand, tc.arg, tc.others); got != tc.want {
			t.Errorf("plainNameMatchesAmong(%q, %q, %v) = %v, want %v - %s",
				tc.cand, tc.arg, tc.others, got, tc.want, tc.why)
		}
	}
}

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
