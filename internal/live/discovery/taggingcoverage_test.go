// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"sort"
	"strings"
	"testing"
)

// This file is issue #1144's shape guard, and it is the thing the issue
// asks for in as many words: "a guard that the unserved set's shape can
// express 'roles unserved, policies served in us-east-1', shown red against
// today's prefix-only structure."
//
// The red is not hypothetical and it is not an unwritten claim. The
// pre-#1144 structure was one map[string]bool keyed by name prefix, read
// through a predicate that took no region. Every case in
// [TestTaggingAPICoverageExpressesTheMeasuredIAMSplit]'s table that names a
// region and expects a DIFFERENT answer from another region for the same
// type is a case that structure could not produce a distinct answer for,
// whatever value the one map held: it had no region parameter to branch on.
// The table therefore fails against it by construction, not by assertion -
// and the pre-#1144 shape is additionally RUN, against the real emulator,
// by TestPerRegionTaggingRoutingAgainstFloci's break/prefix-only arm.

// TestTaggingAPICoverageExpressesTheMeasuredIAMSplit is the measured truth,
// per type and per region, stated once.
//
// Every row is issue #1134's real-AWS measurement at scale 50, not an
// inference: RGTA returned 0 for iam:role in every region while
// iam:ListRoleTags showed 550 of 550 tagged, and 500 each for iam:policy
// and iam:instance-profile in us-east-1 against 0 in us-east-2, reproduced
// on two estates and stable over 35 minutes.
func TestTaggingAPICoverageExpressesTheMeasuredIAMSplit(t *testing.T) {
	cases := []struct {
		region   string
		typeName string
		unserved bool
		why      string
	}{
		// The role. Unserved everywhere, and that is what #692's probe
		// actually established before it was generalised to the service.
		{"us-east-1", "aws_iam_role", true, "GetResources returns 0 for iam:role in every region, us-east-1 included (#1134)"},
		{"us-east-2", "aws_iam_role", true, "same, outside the indexing region"},
		{"", "aws_iam_role", true, "same, with no region known"},

		// The two the prefix was wrong about. Served, and only from one
		// region - the half a prefix has no room for at all.
		{"us-east-1", "aws_iam_policy", false, "500 returned in us-east-1 at scale 50 (#1134); IAM is global and the index holds it there"},
		{"us-east-2", "aws_iam_policy", true, "0 returned in us-east-2 at the same scale, in the same pass"},
		{"us-west-2", "aws_iam_policy", true, "not an indexing region for a global service"},
		{"", "aws_iam_policy", true, "an unknown region cannot be shown to be us-east-1, and a sweep that guessed would be guessing about a destroy"},

		{"us-east-1", "aws_iam_instance_profile", false, "500 returned in us-east-1 at scale 50 (#1134)"},
		{"us-east-2", "aws_iam_instance_profile", true, "0 returned in us-east-2"},
		{"us-west-2", "aws_iam_instance_profile", true, "0 returned outside us-east-1; re-probed against the pinned emulator too"},
		{"", "aws_iam_instance_profile", true, "unknown region, conservative"},

		// An aws_iam_ type nobody has measured inherits the service
		// default, which is #692's finding kept where it belongs: as a
		// default, not as the whole truth.
		{"us-east-1", "aws_iam_saml_provider", true, "no per-type measurement, so the aws_iam_ service default stands"},

		// Controls. A type outside every entry is served everywhere,
		// which is the ordinary case and the reason the tables are small.
		{"us-east-1", "aws_s3_bucket", false, "no coverage entry at all"},
		{"eu-west-1", "aws_s3_bucket", false, "no coverage entry at all, in any region"},
		{"", "aws_s3_bucket", false, "no coverage entry at all, with no region known - an absent entry is not a restriction"},
		{"us-west-2", "aws_sqs_queue", false, "no coverage entry at all"},
	}

	for _, tc := range cases {
		got := taggingAPIUnservedTypeInRegion(tc.region, tc.typeName)
		if got != tc.unserved {
			t.Errorf("taggingAPIUnservedTypeInRegion(%q, %q) = %v, want %v.\n%s",
				tc.region, tc.typeName, got, tc.unserved, tc.why)
		}
	}

	// The shape claim, stated as a claim rather than left implicit in the
	// rows above: the same type answers differently in two regions, and
	// two types in the same service answer differently in one region.
	// Neither is expressible by a set of service prefixes, whatever its
	// contents, because such a set has nothing to key either distinction
	// on.
	if taggingAPIUnservedTypeInRegion("us-east-1", "aws_iam_policy") == taggingAPIUnservedTypeInRegion("us-east-2", "aws_iam_policy") {
		t.Error("aws_iam_policy answers the same in us-east-1 and us-east-2, so the region half of #1144 is not represented at all")
	}
	if taggingAPIUnservedTypeInRegion("us-east-1", "aws_iam_policy") == taggingAPIUnservedTypeInRegion("us-east-1", "aws_iam_role") {
		t.Error("aws_iam_policy and aws_iam_role answer the same in us-east-1, so the per-type half of #1144 is not represented either")
	}
}

// TestTaggingAPICoverageOverridesTheServicePrefix pins the precedence, which
// is the only thing that makes a default useful: the per-type row wins.
//
// Without it the three IAM rows would be decoration and the prefix would
// still be deciding, which is how a representation change silently fails to
// change anything.
func TestTaggingAPICoverageOverridesTheServicePrefix(t *testing.T) {
	svc, ok := taggingAPIServiceCoverage["aws_iam_"]
	if !ok {
		t.Fatal("the aws_iam_ service default is gone; this test's whole subject is that a per-type row beats it")
	}
	if svc.Indexed {
		t.Fatalf("the aws_iam_ service default reads Indexed=true, so aws_iam_policy's row no longer contradicts it and the precedence below proves nothing")
	}

	cov, ok := taggingAPICoverageFor("aws_iam_policy")
	if !ok {
		t.Fatal("aws_iam_policy has no coverage at all, not even the service default")
	}
	if !cov.Indexed {
		t.Errorf("aws_iam_policy resolved to Indexed=false, so the aws_iam_ prefix won over the type's own measured row. Evidence on the resolved row: %s", cov.Evidence)
	}
	if len(cov.Regions) != 1 || cov.Regions[0] != "us-east-1" {
		t.Errorf("aws_iam_policy resolved to regions %v, want [us-east-1]", cov.Regions)
	}

	// Every row carries its measurement. A row with no evidence is a row
	// the next reader cannot re-check, and re-checking is the only thing
	// that has ever corrected one of these.
	for name, tables := range map[string]map[string]taggingAPICoverage{
		"taggingAPIServiceCoverage": taggingAPIServiceCoverage,
		"taggingAPITypeCoverage":    taggingAPITypeCoverage,
	} {
		keys := make([]string, 0, len(tables))
		for k := range tables {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if strings.TrimSpace(tables[k].Evidence) == "" {
				t.Errorf("%s[%q] carries no Evidence. #692's prefix was wrong for two of the three types it covered and it took a year and a live account to find out; a row nobody can re-check is how that happens again", name, k)
			}
		}
	}
}

// TestTaggingAPIRestrictedTypeIsRegionBlind pins the OTHER predicate, and
// the reason there are two.
//
// [sweepTypes] asks "could some pass fail to see this type's live objects
// through the one estate-wide GetResources call", which is a property of the
// type. Answering it per region would drop a declared aws_iam_policy out of
// the sweep universe on a us-east-1 run - and [sweepViaTagging] files
// candidates only for types in the universe it was handed, so the type would
// lose its tagging-leg coverage on the very run that can serve it.
func TestTaggingAPIRestrictedTypeIsRegionBlind(t *testing.T) {
	for _, typeName := range []string{"aws_iam_role", "aws_iam_policy", "aws_iam_instance_profile", "aws_iam_saml_provider"} {
		if !taggingAPIRestrictedType(typeName) {
			t.Errorf("taggingAPIRestrictedType(%q) = false, but the type's index coverage is restricted in some way, which is exactly what sweepTypes has to know about", typeName)
		}
	}
	for _, typeName := range []string{"aws_s3_bucket", "aws_sqs_queue", "aws_dynamodb_table"} {
		if taggingAPIRestrictedType(typeName) {
			t.Errorf("taggingAPIRestrictedType(%q) = true for a type with no coverage entry, so sweepTypes would add ordinary types back into the sweep universe and pay for listing them twice", typeName)
		}
	}
}

// TestTaggingAPIIndexAndTagDroppingListAreSeparateFacts is the guard on the
// predicate SPLIT, and it is the one a future reader is most likely to undo.
//
// [sweepMarkerReadGap]'s doc comment set this trap deliberately, before the
// case existed: "Two facts riding one list is a shape that has misled this
// repository before ... Find one in a service GetResources DOES index and
// this gate is the wrong gate for it - the predicate has to split, and the
// tag-dropping half is the one this function wants."
//
// aws_iam_policy in us-east-1 is that case, arrived. GetResources DOES index
// it; iam:ListPolicies drops its tags all the same. The two predicates must
// therefore DISAGREE about it, and if they ever agree again someone has
// re-merged them and the marker-read gap is reading the wrong fact.
func TestTaggingAPIIndexAndTagDroppingListAreSeparateFacts(t *testing.T) {
	const typeName = "aws_iam_policy"

	if !taggingAPIListDropsTags(typeName) {
		t.Errorf("taggingAPIListDropsTags(%q) = false, but iam:ListPolicies returning objects with no tags is measured (#266, #1046, re-confirmed on a live account on #1134). sweepMarkerReadGap reads this to decide whether an untagged listing is evidence of a blind route", typeName)
	}
	if taggingAPIUnservedTypeInRegion("us-east-1", typeName) {
		t.Errorf("taggingAPIUnservedTypeInRegion(us-east-1, %q) = true, but #1134 measured 500 of them returned there. If this flips back, either the measurement was withdrawn or the two predicates have been re-merged", typeName)
	}

	// The role is the control: for it the two facts agree, and they agreed
	// for every aws_iam_ type before #1144, which is how one list came to
	// carry both.
	if !taggingAPIListDropsTags("aws_iam_role") || !taggingAPIUnservedTypeInRegion("us-east-1", "aws_iam_role") {
		t.Error("aws_iam_role no longer has both properties, so the control this split is measured against is gone")
	}
}
