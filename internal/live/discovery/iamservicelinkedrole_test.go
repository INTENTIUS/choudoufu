// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"
)

// TestIAMServiceLinkedRoleSibling is GitHub issue #302's own coverage for
// [iamServiceLinkedRoleSibling], mirroring TestDefaultAdopterSiblings'
// table-driven shape for the sibling-recognition function it is precedent
// for. Only the exact (aws_iam_role, aws_iam_service_linked_role) pair, in
// either order, may match - a genuinely unrelated type pairing with either
// of these two names must still read as false, or the caller's malformed-
// marker refusal would be silently swallowed for a real bug.
func TestIAMServiceLinkedRoleSibling(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"role and service-linked role", "aws_iam_role", "aws_iam_service_linked_role", true},
		{"reversed argument order", "aws_iam_service_linked_role", "aws_iam_role", true},

		{"identical type names", "aws_iam_role", "aws_iam_role", false},
		{"role paired with an unrelated type", "aws_iam_role", "aws_s3_bucket", false},
		{"service-linked role paired with an unrelated type", "aws_iam_service_linked_role", "aws_s3_bucket", false},
		{"neither name is in the pair at all", "aws_vpc", "aws_subnet", false},
		// The default-adopter family (#305) shares the same "AWS's own list
		// call surfaces the special case too" shape, but is a DIFFERENT
		// pair with a different naming convention - this function must not
		// widen to match it.
		{"a default-adopter pair, not this one", "aws_route_table", "aws_default_route_table", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := iamServiceLinkedRoleSibling(c.a, c.b); got != c.want {
				t.Errorf("iamServiceLinkedRoleSibling(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// TestDiscoverIAMServiceLinkedRoleSiblingBindsWithARNIdentity is issue #302's
// direct repro and fix confirmation. IAM has no separate
// ListServiceLinkedRoles operation, so iam:ListRoles - aws_iam_role's own
// native list call, run here because the fixture also declares an ordinary
// aws_iam_role - returns the service-linked role right alongside the
// ordinary one, carrying a marker for aws_iam_service_linked_role.app rather
// than aws_iam_role.
//
// Before the fix, [scanType]'s marker-type-equality check required an exact
// string match (or the unrelated defaultAdopterSiblings pattern) and
// reported this as a malformed ownership marker - even though the estate's
// own aws_iam_service_linked_role.app block was declared and waiting.
//
// This also confirms the correctness half #305's own pairs never had to
// worry about: aws_iam_role and aws_iam_service_linked_role do NOT share an
// import scheme (bare role name vs ARN), so the fix must recompute the
// import identity from the listed object's own arn attribute rather than
// reusing aws_iam_role's role-name-based one - a naive bindType-only flip
// would bind the address to an unusable (wrong-scheme) import ID instead of
// refusing outright, which is a worse defect than the refusal it replaces.
func TestDiscoverIAMServiceLinkedRoleSiblingBindsWithARNIdentity(t *testing.T) {
	const arn = "arn:aws:iam::000000000000:role/aws-service-role/elasticbeanstalk.amazonaws.com/AWSServiceRoleForElasticBeanstalk"

	cloud := newFakeCloud()
	cloud.listable("aws_iam_role")
	cloud.withAttr("aws_iam_role", "arn")
	// The object is returned by aws_iam_role's own list call - exactly how
	// the real bug reproduces - carrying a marker for the sibling type
	// aws_iam_service_linked_role.
	cloud.ownWithARN("aws_iam_role", "AWSServiceRoleForElasticBeanstalk", arn, `aws_iam_service_linked_role.app`)
	// aws_iam_service_linked_role's own declared-type scan is registered too
	// (with no objects of its own) purely so its list call succeeds with
	// zero results rather than hitting #293's separate "no list route at
	// all" refusal, which needs its own Tagging-index plumbing this test
	// does not set up and is not what this test is about: the point here is
	// the aws_iam_role scan's cross-type marker correction, not #293's
	// fallback mechanism.
	cloud.listable("aws_iam_service_linked_role")

	cfg := loadConfig(t, "testdata/iam-service-linked-role-sibling")
	res, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
	})
	if diags.HasErrors() {
		t.Fatalf("the role/service-linked-role sibling pair was reported as an error:\n%s\n%s", res, renderDiags(diags))
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 0 {
		t.Fatalf("the sibling pair was reported as a malformed marker:\n%s", res)
	}

	b, ok := res.BindingFor(mustAddr(t, "aws_iam_service_linked_role.app"))
	if !ok {
		t.Fatalf("aws_iam_service_linked_role.app did not bind at all:\n%s", res)
	}
	if b.ImportID != arn {
		t.Errorf("bound to import ID %q, want the role's ARN %q (aws_iam_role's own bare-name importID must not be carried forward)", b.ImportID, arn)
	}
	if b.IdentityAttr != "arn" {
		t.Errorf("bound via identity attribute %q, want \"arn\"", b.IdentityAttr)
	}
	if b.TypeName != "aws_iam_service_linked_role" {
		t.Errorf("bound under TypeName %q, want aws_iam_service_linked_role (the marker's own type)", b.TypeName)
	}
}

// TestDiscoverIAMServiceLinkedRoleSiblingRefusesWithoutARN is the fix's
// safety net: when the listed object carries no readable arn attribute at
// all (should not happen for a real aws_iam_role object, but never assumed),
// [importIdentityFromResource] must return false and the caller's existing
// malformed-marker refusal must stand, exactly as it did before this fix -
// never a silent bind with an empty or wrong import ID.
func TestDiscoverIAMServiceLinkedRoleSiblingRefusesWithoutARN(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_iam_role")
	cloud.listable("aws_iam_service_linked_role")
	// Deliberately no cloud.withAttr("aws_iam_role", "arn") - the listed object
	// has no arn attribute to recompose an identity from.
	cloud.own("aws_iam_role", "AWSServiceRoleForElasticBeanstalk", `aws_iam_service_linked_role.app`)

	cfg := loadConfig(t, "testdata/iam-service-linked-role-sibling")
	res, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
	})
	if !diags.HasErrors() {
		t.Fatalf("a role/service-linked-role pair with no arn attribute produced no error:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 1 {
		t.Fatalf("want one malformed-marker problem, got:\n%s", res)
	}
	if _, ok := res.BindingFor(mustAddr(t, "aws_iam_service_linked_role.app")); ok {
		t.Errorf("aws_iam_service_linked_role.app bound despite no recoverable identity:\n%s", res)
	}
}

// TestDiscoverIAMRoleGenuineMismatchStillMalformed is the fix's other half,
// the same shape TestDiscoverDefaultRouteTableGenuineMismatchStillMalformed
// asserts for #325: widening the check for the real role/service-linked-role
// pair must not weaken it for a marker naming a genuinely unrelated type.
func TestDiscoverIAMRoleGenuineMismatchStillMalformed(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_iam_role")
	cloud.listable("aws_iam_service_linked_role")
	cloud.own("aws_iam_role", "role-confused", `aws_s3_bucket.somewhere`)

	cfg := loadConfig(t, "testdata/iam-service-linked-role-sibling")
	res, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
	})
	if !diags.HasErrors() {
		t.Fatalf("a genuinely cross-type marker produced no error:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 1 {
		t.Fatalf("want one malformed-marker problem, got:\n%s", res)
	}
}
