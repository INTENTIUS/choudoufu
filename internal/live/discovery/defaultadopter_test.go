// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"
)

// TestDefaultAdopterSiblings is issue #325's own coverage for the generic
// derivation [defaultAdopterSiblings] uses in place of a hand list of three
// string pairs: the "aws_default_" prefix on the type name, cross-checked
// against the two facts #305's ratified Reason text already states in prose
// for each of the three pairs it admitted - that the default type carries
// "the same import identity [its plain sibling] already carries in this
// table". A pair that merely starts with "aws_default_" but whose ratified
// row actually diverges (none exist among the admitted set today) must still
// fail this and fall through to the genuine cross-type refusal.
func TestDefaultAdopterSiblings(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"route table pair", "aws_route_table", "aws_default_route_table", true},
		{"route table pair, argument order reversed", "aws_default_route_table", "aws_route_table", true},
		{"security group pair", "aws_security_group", "aws_default_security_group", true},
		{"security group pair, reversed", "aws_default_security_group", "aws_security_group", true},
		{"network acl pair", "aws_network_acl", "aws_default_network_acl", true},
		{"network acl pair, reversed", "aws_default_network_acl", "aws_network_acl", true},

		// Genuine mismatches: a default type paired with the WRONG sibling,
		// two non-default types, two default types, and a default-prefixed
		// name whose plain form is not admitted at all
		// (aws_default_vpc/aws_default_subnet are real provider types -
		// confirmed against the offline doc cache - that #305 did not admit;
		// this must not treat them as siblings of anything by name alone).
		{"mismatched default pair", "aws_route_table", "aws_default_security_group", false},
		{"neither is a default type", "aws_s3_bucket", "aws_route_table", false},
		{"both are default types", "aws_default_route_table", "aws_default_security_group", false},
		{"unadmitted default type, plain sibling admitted", "aws_default_vpc", "aws_vpc", false},
		{"default type paired with an unrelated admitted type", "aws_default_route_table", "aws_route53_zone", false},
		{"identical type names", "aws_route_table", "aws_route_table", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := defaultAdopterSiblings(c.a, c.b); got != c.want {
				t.Errorf("defaultAdopterSiblings(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// TestDiscoverDefaultRouteTableAliasIsNotMalformed is issue #325's direct
// repro and fix confirmation. terraform-aws-modules/vpc declares both
// aws_route_table (for the VPC's non-default tables) and
// aws_default_route_table (adopting the VPC's default one, admitted by
// #305). AWS has no separate "default route table" list operation - a route
// table is a route table - so the aws_route_table type's own list call
// legitimately returns the object aws_default_route_table.default owns, and
// that object's marker names aws_default_route_table rather than
// aws_route_table.
//
// Before the fix, [scanType]'s marker-type-equality check required an exact
// string match and reported this as a malformed ownership marker. The
// estate's own aws_default_route_table.default is not declared by this
// package's shared fixture, so the corrected object is expected to surface
// as an honest orphan (nothing in THIS configuration claims it) rather than
// as an error - the same shape [TestDiscoverCrossTypeMarkerOnUndeclaredAddress]
// asserts for a genuine cross-type marker, except that this one must not be
// malformed at all.
func TestDiscoverDefaultRouteTableAliasIsNotMalformed(t *testing.T) {
	cloud := newFakeCloud()
	// The object is returned by aws_route_table's own list call - exactly
	// how the real bug reproduces - carrying a marker for the sibling type
	// aws_default_route_table.
	cloud.own("aws_route_table", "rtb-default-1", `aws_default_route_table.default`)

	res, diags := discoverFixture(t, cloud, Request{})
	if diags.HasErrors() {
		t.Fatalf("a default_route_table/route_table alias was reported as an error:\n%s\n%s", res, renderDiags(diags))
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 0 {
		t.Fatalf("the alias pair was reported as a malformed marker:\n%s", res)
	}

	// Not declared by this fixture, so it is exactly what an object this
	// estate owns but no configuration claims should be: an orphan, typed as
	// what its own marker says it is - aws_default_route_table, not
	// aws_route_table, which is the type whose list call happened to find
	// it. classifyOrphans' own o.Addr.Resource.Resource.Type != o.TypeName
	// guard depends on this.
	var found bool
	for _, o := range res.Orphans {
		if o.ImportID != "rtb-default-1" {
			continue
		}
		found = true
		if o.TypeName != "aws_default_route_table" {
			t.Errorf("orphan's TypeName is %q, want aws_default_route_table (the marker's own type)", o.TypeName)
		}
	}
	if !found {
		t.Fatalf("the aliased object did not appear as an orphan at all:\n%s", res)
	}
}

// TestDiscoverDefaultSecurityGroupAliasIsNotMalformed is the same shape as
// TestDiscoverDefaultRouteTableAliasIsNotMalformed for the other pair the
// issue names, aws_security_group/aws_default_security_group.
func TestDiscoverDefaultSecurityGroupAliasIsNotMalformed(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_security_group", "sg-default-1", `aws_default_security_group.default`)

	res, diags := discoverFixture(t, cloud, Request{})
	if diags.HasErrors() {
		t.Fatalf("a default_security_group/security_group alias was reported as an error:\n%s\n%s", res, renderDiags(diags))
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 0 {
		t.Fatalf("the alias pair was reported as a malformed marker:\n%s", res)
	}
	var found bool
	for _, o := range res.Orphans {
		if o.ImportID != "sg-default-1" {
			continue
		}
		found = true
		if o.TypeName != "aws_default_security_group" {
			t.Errorf("orphan's TypeName is %q, want aws_default_security_group", o.TypeName)
		}
	}
	if !found {
		t.Fatalf("the aliased object did not appear as an orphan at all:\n%s", res)
	}
}

// TestDiscoverDefaultAdopterDeclaredBothSidesNoFalseCollision is the shape
// found crossing a real estate (choudoufu's corpus-xancloud-iac,
// XanCloud/xancloud-iac's landing-zone-basic blueprint) the day #325 landed,
// which neither TestDiscoverDefaultRouteTableAliasIsNotMalformed nor
// TestDiscoverDefaultSecurityGroupAliasIsNotMalformed exercises: an estate
// that declares BOTH sides of a default-adopter pair, the ordinary shape
// terraform-aws-modules/vpc and this real module both use (an unrelated
// aws_security_group next to an aws_default_security_group adopting the
// VPC's own default one). AWS has one DescribeSecurityGroups list call, not
// two, so scanning BOTH declared types visits the shared default object
// TWICE - once under aws_security_group's own typeName (rebound to
// aws_default_security_group by defaultAdopterSiblings) and once under
// aws_default_security_group's own typeName (already matching) - and before
// claimantAlreadyPresent that produced ProblemCollision ("Two live resources
// claiming one address") printing the same live ID twice, not the one
// object it actually is.
func TestDiscoverDefaultAdopterDeclaredBothSidesNoFalseCollision(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_default_security_group")
	// The same live object, registered under BOTH declared types - exactly
	// how two independent scanType passes over one DescribeSecurityGroups
	// population both surface it.
	cloud.own("aws_security_group", "sg-shared-1", `aws_default_security_group.default`)
	cloud.own("aws_default_security_group", "sg-shared-1", `aws_default_security_group.default`)

	cfg := loadConfig(t, "testdata/default-adopter-dup")
	res, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
	})
	assertNoErrors(t, diags)
	if len(res.ProblemsOfKind(ProblemCollision)) != 0 {
		t.Fatalf("a live object discovered twice via two scan passes was reported as a genuine collision:\n%s", res)
	}

	b, ok := res.BindingFor(mustAddr(t, "aws_default_security_group.default"))
	if !ok {
		t.Fatalf("aws_default_security_group.default did not bind at all:\n%s", res)
	}
	if b.ImportID != "sg-shared-1" {
		t.Errorf("bound to %q, want sg-shared-1", b.ImportID)
	}
}

// TestDiscoverDefaultRouteTableGenuineMismatchStillMalformed is the fix's
// other half: widening the check for the real alias pairs must not weaken it
// for a marker that names a genuinely unrelated type. A route table carrying
// a marker for a default SECURITY GROUP - both "default_*" types, but not
// siblings of each other or of aws_route_table - must still be refused,
// exactly as it was before this fix, so this reuses
// TestDiscoverCrossTypeMarkerIsMalformed's own assertions rather than a
// weaker check.
func TestDiscoverDefaultRouteTableGenuineMismatchStillMalformed(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_route_table", "rtb-confused", `aws_default_security_group.default`)

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("a genuinely cross-type marker produced no error:\n%s", res)
	}
	problems := res.ProblemsOfKind(ProblemMalformedMarker)
	if len(problems) != 1 {
		t.Fatalf("want one malformed-marker problem, got:\n%s", res)
	}
	p := problems[0]
	if p.TypeName != "aws_route_table" {
		t.Errorf("the problem names type %q, want the live resource's own scanned type", p.TypeName)
	}
	for _, want := range []string{"aws_default_security_group", "aws_route_table"} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("the problem does not name %q:\n%s", want, p.Detail)
		}
	}
	if len(res.Orphans) != 0 {
		t.Errorf("a type-confused marker was also classified as an orphan:\n%s", res)
	}
}
