// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import "testing"

// testParents is the taggable-admitted subset these tests exercise ParentOf
// and SingleParentComponent against: enough of the real DefaultTable's
// taggable types to cover every rule the doc comment names, not the whole
// admission table (that cross-check belongs to
// tools/survey-gen/parent_render.go and its own drift test, which reads the
// real taggability signal from live/survey-full.json instead of asserting
// it by hand here).
var testParents = map[string]bool{
	"aws_s3_bucket":        true,
	"aws_iam_role":         true,
	"aws_lb_target_group":  true,
	"aws_route_table":      true,
	"aws_route53_zone":     true,
	"aws_sns_topic":        true,
	"aws_sqs_queue":        true,
	"aws_subnet":           true,
	"aws_internet_gateway": true,
	// Deliberately NOT included, so the type-name-prefix rule's
	// disambiguation is exercised rather than assumed: aws_iam_role_policy
	// is itself untaggable and must never win as a parent even though its
	// name is the longer prefix match for aws_iam_role_policy_attachment.
}

func TestParentOfNamedSingletonChildren(t *testing.T) {
	cases := []struct {
		typeName string
		wantAttr string
		wantOf   string
	}{
		{"aws_s3_bucket_policy", "bucket", "aws_s3_bucket"},
		{"aws_s3_bucket_versioning", "bucket", "aws_s3_bucket"},
		{"aws_s3_bucket_public_access_block", "bucket", "aws_s3_bucket"},
		{"aws_s3_bucket_server_side_encryption_configuration", "bucket", "aws_s3_bucket"},
		{"aws_s3_bucket_lifecycle_configuration", "bucket", "aws_s3_bucket"},
		{"aws_sns_topic_policy", "arn", "aws_sns_topic"},
		{"aws_sqs_queue_policy", "queue_url", "aws_sqs_queue"},
	}
	for _, c := range cases {
		t.Run(c.typeName, func(t *testing.T) {
			links := ParentOf(c.typeName, testParents)
			if len(links) != 1 {
				t.Fatalf("ParentOf(%s) = %v, want exactly one link", c.typeName, links)
			}
			if links[0].Attr != c.wantAttr || links[0].Parent != c.wantOf {
				t.Errorf("ParentOf(%s) = %+v, want {%s %s}", c.typeName, links[0], c.wantAttr, c.wantOf)
			}

			link, ok := SingleParentComponent(c.typeName, testParents)
			if !ok {
				t.Fatalf("SingleParentComponent(%s) = false, want true (named-singleton-child shape)", c.typeName)
			}
			if link != links[0] {
				t.Errorf("SingleParentComponent(%s) = %+v, want %+v", c.typeName, link, links[0])
			}
		})
	}
}

// TestParentOfDisambiguatesByEligibility is aws_iam_role_policy_attachment's
// own case: its name is a longer prefix match for the untaggable
// aws_iam_role_policy than for aws_iam_role, and only the eligible-parents
// set - which excludes aws_iam_role_policy, itself untaggable - keeps the
// longest-prefix rule from picking the wrong one.
func TestParentOfDisambiguatesByEligibility(t *testing.T) {
	links := ParentOf("aws_iam_role_policy_attachment", testParents)
	if len(links) != 1 || links[0].Parent != "aws_iam_role" {
		t.Fatalf("ParentOf(aws_iam_role_policy_attachment) = %v, want exactly one link to aws_iam_role", links)
	}
	if links[0].Attr != "role" {
		t.Errorf("matched attr = %q, want %q", links[0].Attr, "role")
	}

	// Two attribute-supplying components (role, policy_arn): the parent
	// link exists, but the shape is not the single-component one a parent
	// read alone fully settles.
	if _, ok := SingleParentComponent("aws_iam_role_policy_attachment", testParents); ok {
		t.Error("aws_iam_role_policy_attachment reported as a single-parent-component type; it has a second free-standing argument (policy_arn)")
	}
}

// TestParentOfConventionFallback covers the two types the type-name-prefix
// rule cannot reach at all (aws_route's name does not extend
// aws_route_table's, aws_route53_record's does not extend
// aws_route53_zone's), so ParentOf has to fall back to the *_id naming
// convention on an individual argument.
func TestParentOfConventionFallback(t *testing.T) {
	if links := ParentOf("aws_route", testParents); len(links) != 1 || links[0] != (ParentLink{Attr: "route_table_id", Parent: "aws_route_table"}) {
		t.Errorf("ParentOf(aws_route) = %v, want [{route_table_id aws_route_table}]", links)
	}
	if links := ParentOf("aws_route53_record", testParents); len(links) != 1 || links[0] != (ParentLink{Attr: "zone_id", Parent: "aws_route53_zone"}) {
		t.Errorf("ParentOf(aws_route53_record) = %v, want [{zone_id aws_route53_zone}]", links)
	}
	// Neither is a single-parent-component type: aws_route's destination is
	// free-standing, and aws_route_table_association needs a subnet or
	// gateway on top of the route table.
	if _, ok := SingleParentComponent("aws_route", testParents); ok {
		t.Error("aws_route reported as single-parent-component; its destination argument is free-standing")
	}

	// aws_route_table_association's own name extends aws_route_table's, so
	// rule 1 (the more specific of the two) settles it before the
	// convention fallback ever runs, the same as the named-singleton
	// children above - one link, to the route table, even though a second
	// component (subnet or gateway) also feeds its import string.
	links := ParentOf("aws_route_table_association", testParents)
	if len(links) != 1 || links[0] != (ParentLink{Attr: "route_table_id", Parent: "aws_route_table"}) {
		t.Errorf("ParentOf(aws_route_table_association) = %v, want [{route_table_id aws_route_table}]", links)
	}
	if _, ok := SingleParentComponent("aws_route_table_association", testParents); ok {
		t.Error("aws_route_table_association reported as single-parent-component; it needs a subnet/gateway component beyond the route table")
	}
}

// TestParentOfNoParent is the residue: an untaggable admitted type whose
// identity does not depend on any other admitted type's at all.
func TestParentOfNoParent(t *testing.T) {
	for _, typeName := range []string{"aws_kms_alias", "aws_cloudwatch_dashboard"} {
		if links := ParentOf(typeName, testParents); len(links) != 0 {
			t.Errorf("ParentOf(%s) = %v, want none", typeName, links)
		}
		if _, ok := SingleParentComponent(typeName, testParents); ok {
			t.Errorf("SingleParentComponent(%s) = true, want false", typeName)
		}
	}
}

// TestParentOfServerAssignedHasNoComponents pins the other reason a type can
// carry no parent link: a ServerAssigned entry has no Components field at
// all, whatever set of eligible parents is offered.
func TestParentOfServerAssignedHasNoComponents(t *testing.T) {
	for _, typeName := range []string{"aws_ecr_registry_policy", "aws_lambda_layer_version"} {
		if links := ParentOf(typeName, testParents); len(links) != 0 {
			t.Errorf("ParentOf(%s) = %v, want none (ServerAssigned)", typeName, links)
		}
	}
}

// TestParentOfUnknownType is the zero-value floor: a type absent from
// DefaultTable entirely carries no parent link and does not panic.
func TestParentOfUnknownType(t *testing.T) {
	if links := ParentOf("aws_not_a_real_type", testParents); links != nil {
		t.Errorf("ParentOf(unknown type) = %v, want nil", links)
	}
	if _, ok := SingleParentComponent("aws_not_a_real_type", testParents); ok {
		t.Error("SingleParentComponent(unknown type) = true, want false")
	}
}
