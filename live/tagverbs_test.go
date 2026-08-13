// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import "testing"

// TestTagVerbForType_KnownComposable pins the shape internal/live/foreign's
// adoptionHint depends on for the EC2 family: KnownComposable, the exact
// Resources/Tags/Key/Value fields the pinned
// TestClassifyAdoptionHintIsPasteable output is built from.
func TestTagVerbForType_KnownComposable(t *testing.T) {
	tv, ok := TagVerbForType("aws_security_group")
	if !ok {
		t.Fatal("aws_security_group has no CFN mapping at all")
	}
	if !tv.Known || tv.Service != "EC2" {
		t.Fatalf("tv = %+v, want Known EC2", tv)
	}
	if tv.Operation != "CreateTags" || !tv.Composable {
		t.Errorf("tv = %+v, want CreateTags/Composable", tv)
	}
	if tv.ResourceArg != "Resources" || tv.TagsArg != "Tags" {
		t.Errorf("tv = %+v, want Resources/Tags", tv)
	}
	if tv.TagsShape != "list" || tv.TagKeyField != "Key" || tv.TagValueField != "Value" {
		t.Errorf("tv = %+v, want list/Key/Value", tv)
	}
}

// TestTagVerbForType_AmbiguousService: aws_iam_role maps to IAM, whose
// artifact row is ambiguous (eight per-entity Tag<X> operations) - Known
// true, Operation empty, Ambiguous true.
func TestTagVerbForType_AmbiguousService(t *testing.T) {
	tv, ok := TagVerbForType("aws_iam_role")
	if !ok {
		t.Fatal("aws_iam_role has no CFN mapping at all")
	}
	if !tv.Known || !tv.Ambiguous || tv.Operation != "" || tv.Composable {
		t.Errorf("tv = %+v, want Known/Ambiguous with no Operation and not Composable", tv)
	}
}

// TestTagVerbForType_KnownButNotComposable: aws_route53_zone maps to
// Route53, whose ChangeTagsForResource is known but takes two identifying
// arguments (ResourceType, ResourceId), which this package refuses to guess
// values for.
func TestTagVerbForType_KnownButNotComposable(t *testing.T) {
	tv, ok := TagVerbForType("aws_route53_zone")
	if !ok {
		t.Fatal("aws_route53_zone has no CFN mapping at all")
	}
	if !tv.Known || tv.Operation != "ChangeTagsForResource" || tv.Composable {
		t.Errorf("tv = %+v, want Known ChangeTagsForResource, not Composable", tv)
	}
	if tv.Reason == "" {
		t.Error("Reason is empty for a non-composable row")
	}
}

// TestTagVerbForType_NoMapping: a type live/mapping.json has no row for (or
// whose row carries no cfn_type) reports ok=false rather than a zero-value
// TagVerb that could be misread as "known, nothing found".
func TestTagVerbForType_NoMapping(t *testing.T) {
	if _, ok := TagVerbForType("aws_no_such_type"); ok {
		t.Error("TagVerbForType resolved a type with no mapping.json row at all")
	}
	// aws_iam_role_policy_attachment is via:fold-shaped in spirit (a
	// composite identity with no CFN type of its own); if mapping.json
	// ever carries it with a null cfn_type, this must also report false.
	if tv, ok := TagVerbForType("aws_route_table_association"); ok && tv.Service == "" {
		t.Errorf("TagVerbForType reported ok=true with an empty Service: %+v", tv)
	}
}

// TestXformName pins the CLI-flag-name transform against real AWS CLI
// spellings this package's callers depend on: acronym runs (ARN) collapse
// to one word rather than splitting per letter.
func TestXformName(t *testing.T) {
	cases := map[string]string{
		"Resources":      "resources",
		"KeyId":          "key-id",
		"ResourceArns":   "resource-arns",
		"ResourceArn":    "resource-arn",
		"ResourceARN":    "resource-arn",
		"QueueUrl":       "queue-url",
		"resourceArn":    "resource-arn",
		"CertificateArn": "certificate-arn",
	}
	for in, want := range cases {
		if got := XformName(in); got != want {
			t.Errorf("XformName(%q) = %q, want %q", in, got, want)
		}
	}
}
