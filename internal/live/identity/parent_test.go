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
	"aws_iam_policy":       true,
	"aws_lb_target_group":  true,
	"aws_route_table":      true,
	"aws_route53_zone":     true,
	"aws_sns_topic":        true,
	"aws_sqs_queue":        true,
	"aws_subnet":           true,
	"aws_internet_gateway": true,
	"aws_security_group":   true,
	"aws_eks_node_group":   true,
	// Deliberately NOT included, so the type-name-prefix rule's
	// disambiguation is exercised rather than assumed: aws_iam_role_policy
	// is itself untaggable and must never win as a parent even though its
	// name is the longer prefix match for aws_iam_role_policy_attachment.
	//
	// Also deliberately NOT included, for
	// TestParentOfRefusesUnrelatedSuffixMatch below: aws_iam_group and
	// aws_iam_group_policy are both untaggable (IAM has no TagGroup API),
	// which is exactly what makes aws_iam_group_policy_attachment's own
	// "group" argument a trap for the *_group suffix convention -
	// aws_security_group and aws_eks_node_group are both eligible here,
	// both end in "_group", and neither has anything to do with an IAM
	// group.
}

// testServiceOf is the ServiceOf these fixtures run under: nothing is
// mapped, so sameServiceAffinity falls back to the Terraform-prefix test.
// The CFN-service half is exercised against the real roster in
// tools/survey-gen, which is where the mapping lives.
var testServiceOf = ServiceOf(func(string) (string, bool) { return "", false })

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
			links := ParentOf(c.typeName, testParents, testServiceOf)
			if len(links) != 1 {
				t.Fatalf("ParentOf(%s) = %v, want exactly one link", c.typeName, links)
			}
			if links[0].Attr != c.wantAttr || links[0].Parent != c.wantOf {
				t.Errorf("ParentOf(%s) = %+v, want {%s %s}", c.typeName, links[0], c.wantAttr, c.wantOf)
			}

			link, ok := SingleParentComponent(c.typeName, testParents, testServiceOf)
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
	links := ParentOf("aws_iam_role_policy_attachment", testParents, testServiceOf)
	if len(links) != 1 || links[0].Parent != "aws_iam_role" {
		t.Fatalf("ParentOf(aws_iam_role_policy_attachment) = %v, want exactly one link to aws_iam_role", links)
	}
	if links[0].Attr != "role" {
		t.Errorf("matched attr = %q, want %q", links[0].Attr, "role")
	}

	// Two attribute-supplying components (role, policy_arn): the parent
	// link exists, but the shape is not the single-component one a parent
	// read alone fully settles.
	if _, ok := SingleParentComponent("aws_iam_role_policy_attachment", testParents, testServiceOf); ok {
		t.Error("aws_iam_role_policy_attachment reported as a single-parent-component type; it has a second free-standing argument (policy_arn)")
	}
}

// TestParentOfRefusesUnrelatedSuffixMatch is
// aws_iam_group_policy_attachment's own case, the rule-2 (argument
// convention) sibling of TestParentOfDisambiguatesByEligibility's rule-1
// case above: its "group" component names the untaggable aws_iam_group
// (IAM has no TagGroup API - the same reason aws_iam_role_policy_attachment
// above needs the eligibility fallback at all), but unlike
// aws_iam_role_policy_attachment's prefix chain, aws_security_group and
// aws_eks_node_group are not ancestors of aws_iam_group at any remove -
// they only happen to end in "_group" too. ParentOf must report nothing
// for the "group" argument rather than pick either stranger, and it must
// do so the same way on every run: before the fix this file's own history
// names, parentByConvention searched only the eligible subset, so whichever
// of aws_security_group/aws_eks_node_group Go's randomized map iteration
// happened to visit first silently won.
func TestParentOfRefusesUnrelatedSuffixMatch(t *testing.T) {
	links := ParentOf("aws_iam_group_policy_attachment", testParents, testServiceOf)
	if len(links) != 1 || links[0] != (ParentLink{Attr: "policy_arn", Parent: "aws_iam_policy"}) {
		t.Fatalf("ParentOf(aws_iam_group_policy_attachment) = %v, want exactly one link, {policy_arn aws_iam_policy} - "+
			"never aws_security_group or aws_eks_node_group by way of the unrelated \"group\" argument", links)
	}

	// Not a single-parent-component type either: "group" is a real,
	// free-standing second component even though it resolves to no parent
	// link, the same shape aws_route's own destination argument has.
	if _, ok := SingleParentComponent("aws_iam_group_policy_attachment", testParents, testServiceOf); ok {
		t.Error("aws_iam_group_policy_attachment reported as single-parent-component; it has a second free-standing argument (group)")
	}
}

// TestParentOfConventionFallback covers the two types the type-name-prefix
// rule cannot reach at all (aws_route's name does not extend
// aws_route_table's, aws_route53_record's does not extend
// aws_route53_zone's), so ParentOf has to fall back to the *_id naming
// convention on an individual argument.
func TestParentOfConventionFallback(t *testing.T) {
	if links := ParentOf("aws_route", testParents, testServiceOf); len(links) != 1 || links[0] != (ParentLink{Attr: "route_table_id", Parent: "aws_route_table"}) {
		t.Errorf("ParentOf(aws_route) = %v, want [{route_table_id aws_route_table}]", links)
	}
	if links := ParentOf("aws_route53_record", testParents, testServiceOf); len(links) != 1 || links[0] != (ParentLink{Attr: "zone_id", Parent: "aws_route53_zone"}) {
		t.Errorf("ParentOf(aws_route53_record) = %v, want [{zone_id aws_route53_zone}]", links)
	}
	// Neither is a single-parent-component type: aws_route's destination is
	// free-standing, and aws_route_table_association needs a subnet or
	// gateway on top of the route table.
	if _, ok := SingleParentComponent("aws_route", testParents, testServiceOf); ok {
		t.Error("aws_route reported as single-parent-component; its destination argument is free-standing")
	}

	// aws_route_table_association's own name extends aws_route_table's, so
	// rule 1 (the more specific of the two) settles it before the
	// convention fallback ever runs, the same as the named-singleton
	// children above - one link, to the route table, even though a second
	// component (subnet or gateway) also feeds its import string.
	links := ParentOf("aws_route_table_association", testParents, testServiceOf)
	if len(links) != 1 || links[0] != (ParentLink{Attr: "route_table_id", Parent: "aws_route_table"}) {
		t.Errorf("ParentOf(aws_route_table_association) = %v, want [{route_table_id aws_route_table}]", links)
	}
	if _, ok := SingleParentComponent("aws_route_table_association", testParents, testServiceOf); ok {
		t.Error("aws_route_table_association reported as single-parent-component; it needs a subnet/gateway component beyond the route table")
	}
}

// TestParentOfNoParent is the residue: an untaggable admitted type whose
// identity does not depend on any other admitted type's at all.
func TestParentOfNoParent(t *testing.T) {
	for _, typeName := range []string{"aws_kms_alias", "aws_cloudwatch_dashboard"} {
		if links := ParentOf(typeName, testParents, testServiceOf); len(links) != 0 {
			t.Errorf("ParentOf(%s) = %v, want none", typeName, links)
		}
		if _, ok := SingleParentComponent(typeName, testParents, testServiceOf); ok {
			t.Errorf("SingleParentComponent(%s) = true, want false", typeName)
		}
	}
}

// TestParentOfServerAssignedHasNoComponents pins the other reason a type can
// carry no parent link: a ServerAssigned entry has no Components field at
// all, whatever set of eligible parents is offered.
func TestParentOfServerAssignedHasNoComponents(t *testing.T) {
	for _, typeName := range []string{"aws_ecr_registry_policy", "aws_lambda_layer_version"} {
		if links := ParentOf(typeName, testParents, testServiceOf); len(links) != 0 {
			t.Errorf("ParentOf(%s) = %v, want none (ServerAssigned)", typeName, links)
		}
	}
}

// TestParentOfUnknownType is the zero-value floor: a type absent from
// DefaultTable entirely carries no parent link and does not panic.
func TestParentOfUnknownType(t *testing.T) {
	if links := ParentOf("aws_not_a_real_type", testParents, testServiceOf); links != nil {
		t.Errorf("ParentOf(unknown type) = %v, want nil", links)
	}
	if _, ok := SingleParentComponent("aws_not_a_real_type", testParents, testServiceOf); ok {
		t.Error("SingleParentComponent(unknown type) = true, want false")
	}
}
