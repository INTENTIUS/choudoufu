// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/policy"
)

const policyEstate = "policy-unit"

// buildPolicy is [policy.Build] with only the given quadrant overridden, for
// tests that want to isolate one verb's effect.
func buildPolicy(t *testing.T, declaredTagged, declaredUntagged string) *policy.Policy {
	t.Helper()
	raw := &policy.Raw{}
	if declaredTagged != "" {
		raw.DeclaredTagged, raw.DeclaredTaggedSet = declaredTagged, true
	}
	if declaredUntagged != "" {
		raw.DeclaredUntagged, raw.DeclaredUntaggedSet = declaredUntagged, true
	}
	return policy.Build(raw, policyEstate)
}

// TestOwnershipPolicy_DeclaredUntaggedAdoptAdmits: GitHub issue #67's
// declared_untagged = "adopt" (and "converge") admits a client-named live
// resource this estate's ownership check would otherwise refuse. There is
// no guess here the way a content-matched bind candidate would be - the
// declared address names the resource explicitly - so admission is
// unconditional rather than an offer.
func TestOwnershipPolicy_DeclaredUntaggedAdoptAdmits(t *testing.T) {
	for _, verb := range []string{"adopt", "converge"} {
		t.Run(verb, func(t *testing.T) {
			cfg := loadConfig(t, "testdata/named")

			cloud := newFakeCloud()
			cloud.putTagged("aws_cloudwatch_log_group", "/somebody/logs", map[string]string{
				"id": "/somebody/logs", "name": "/somebody/logs",
			}, nil)

			pol := buildPolicy(t, "", verb)
			res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
				{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/somebody/logs"},
			}, cloud.providers(t), Options{Ownership: &Ownership{Estate: policyEstate, Policy: pol}})

			assertNoErrors(t, diags)
			if !res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
				t.Fatalf("declared_untagged = %q did not admit the resource:\n%s", verb, res)
			}
			if len(res.Unowned) != 0 {
				t.Errorf("an admitted resource must not also be reported unowned: %+v", res.Unowned)
			}
			if len(res.Policy) != 1 || res.Policy[0].Verb != policy.Verb(verb) || res.Policy[0].Tagged {
				t.Errorf("Policy outcomes = %+v, want one declared_untagged=%s entry", res.Policy, verb)
			}
		})
	}
}

// TestOwnershipPolicy_DeclaredUntaggedRefuseIsUnchanged: the default verb,
// explicit or omitted, refuses exactly as it always has.
func TestOwnershipPolicy_DeclaredUntaggedRefuseIsUnchanged(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/somebody/logs", map[string]string{
		"id": "/somebody/logs", "name": "/somebody/logs",
	}, nil)

	pol := buildPolicy(t, "", "refuse")
	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/somebody/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: policyEstate, Policy: pol}})

	assertNoErrors(t, diags)
	if res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatal("an explicit refuse must not admit the resource")
	}
	if len(res.Policy) != 0 {
		t.Errorf("the default verb, even set explicitly, must record no policy outcome: %+v", res.Policy)
	}
	if !hasDiag(diags, "Live resource outside this estate", "/somebody/logs") {
		t.Error("an explicit refuse must still carry the ordinary loud warning")
	}
}

// TestOwnershipPolicy_DeclaredUntaggedKeepIsQuiet: "keep" is documented as a
// quieter variant of refuse - same non-admission, no loud warning.
func TestOwnershipPolicy_DeclaredUntaggedKeepIsQuiet(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/somebody/logs", map[string]string{
		"id": "/somebody/logs", "name": "/somebody/logs",
	}, nil)

	pol := buildPolicy(t, "", "keep")
	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/somebody/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: policyEstate, Policy: pol}})

	assertNoErrors(t, diags)
	if res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatal("keep must not admit the resource")
	}
	if len(res.Unowned) != 1 {
		t.Fatalf("keep must still record the refusal on the result: %+v", res.Unowned)
	}
	if hasDiag(diags, "Live resource outside this estate", "/somebody/logs") {
		t.Error("keep must not carry the loud warning refuse always does")
	}
	if len(res.Policy) != 1 || res.Policy[0].Verb != policy.Keep {
		t.Errorf("Policy outcomes = %+v, want one declared_untagged=keep entry", res.Policy)
	}
}

// TestOwnershipPolicy_DeclaredTaggedKeepStillConverges: an already-owned
// resource under declared_tagged = "keep" is still admitted - ordinary
// convergence continues on every attribute but the policy tag (see
// internal/live/stamp for that exemption). Excluding it here would make
// the plan propose recreating something that already exists, which is not
// what "keep" means for a resource this estate already manages.
func TestOwnershipPolicy_DeclaredTaggedKeepStillConverges(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	}, map[string]string{
		markers.TagEstate:  policyEstate,
		markers.TagAddress: "aws_cloudwatch_log_group.app",
	})

	pol := buildPolicy(t, "keep", "")
	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
	}, cloud.providers(t), Options{Ownership: &Ownership{Estate: policyEstate, Policy: pol}})

	assertNoErrors(t, diags)
	if !res.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Fatalf("declared_tagged = \"keep\" must still admit an already-owned resource:\n%s", res)
	}
	if len(res.Policy) != 1 || !res.Policy[0].Tagged || res.Policy[0].Verb != policy.Keep {
		t.Errorf("Policy outcomes = %+v, want one declared_tagged=keep entry", res.Policy)
	}
}

// TestOwnershipPolicy_MarkerVerifiedInstanceIsGoverned is the regression for
// the bug the floci scenario test (TestPolicyMatrixAgainstFloci) caught: a
// needs-discovery instance marker discovery already bound - the common
// shape of declared_tagged in practice, a VPC or a subnet rather than a
// client-named bucket - short-circuits the tag read entirely (it is
// admitted on the strength of the marker binding, not by reading tags off
// the object again), and the first version of this policy hook only ever
// consulted policy on the tag-reading path below that shortcut. A verified
// instance is declared_tagged by construction (its marker only exists
// because its live tags carry this estate's tofu-estate), so its policy
// outcome has to be computed on the shortcut path too - and untag has to be
// possible for it, because it is the resource type that quadrant matters
// for most.
func TestOwnershipPolicy_MarkerVerifiedInstanceIsGoverned(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")
	rtb := mustAddr(t, `aws_route_table.main`)

	cloud := newFakeCloud()
	cloud.putTagged("aws_route_table", "rtb-1", map[string]string{
		"id": "rtb-1", "vpc_id": "vpc-1",
	}, nil)

	pol := buildPolicy(t, "untag", "")
	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-1"},
	}, cloud.providers(t), Options{Ownership: &Ownership{
		Estate:   policyEstate,
		Verified: map[string]bool{rtb.String(): true},
		Policy:   pol,
	}})

	assertNoErrors(t, diags)
	if !res.Has(rtb) {
		t.Fatalf("a marker-verified instance under declared_tagged = \"untag\" must still be admitted:\n%s", res)
	}
	if len(res.Policy) != 1 || !res.Policy[0].Tagged || res.Policy[0].Verb != policy.Untag {
		t.Fatalf("Policy outcomes = %+v, want one declared_tagged=untag entry for the verified instance", res.Policy)
	}
}

// TestOwnershipPolicy_ReconcileCandidateIsNotDeclaredTagged is the second
// regression the floci scenario test caught: a verified-but-undeclared
// instance - the shape a scoped reconciliation candidate or a sweep orphan
// takes once its resolution reaches the projection - must never be read as
// declared_tagged, even though [Ownership.verified] alone cannot tell it
// apart from a needs-discovery instance marker discovery bound. Getting
// this wrong handed every reconciliation candidate a declared_tagged =
// "untag" outcome it was never assigned, and (worse, for a caller that
// wires stamp's PolicyUntag off the same address string) could suppress a
// marker stamp never meant to run for it in the first place, since these
// addresses are synthetic and share no block with anything declared.
func TestOwnershipPolicy_ReconcileCandidateIsNotDeclaredTagged(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")
	strayAddr := mustAddr(t, `aws_cloudwatch_log_group.stray`)

	cloud := newFakeCloud()
	cloud.putTagged("aws_cloudwatch_log_group", "/stray/logs", map[string]string{
		"id": "/stray/logs", "name": "/stray/logs",
	}, nil)

	pol := buildPolicy(t, "untag", "")
	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: strayAddr, Class: identity.ClassConcrete, ImportID: "/stray/logs", Undeclared: true},
	}, cloud.providers(t), Options{Ownership: &Ownership{
		Estate:   policyEstate,
		Verified: map[string]bool{strayAddr.String(): true},
		Policy:   pol,
	}})

	assertNoErrors(t, diags)
	if !res.Has(strayAddr) {
		t.Fatalf("a verified undeclared instance must still be admitted (it is what makes it destroyable):\n%s", res)
	}
	if len(res.Policy) != 0 {
		t.Errorf("an undeclared instance must never produce a declared-quadrant policy outcome: %+v", res.Policy)
	}
}

// TestOwnershipPolicy_DefaultVerbsProduceNoOutcomes pins the byte-identical
// bar for the declared side: a policy naming only the two declared
// quadrants' own defaults (converge, refuse) behaves exactly as no policy
// at all, including in the new Policy report - the same property
// internal/live/discovery's orphan test pins for the undeclared side.
func TestOwnershipPolicy_DefaultVerbsProduceNoOutcomes(t *testing.T) {
	build := func(pol *policy.Policy) *Result {
		cfg := loadConfig(t, "testdata/named")
		cloud := newFakeCloud()
		cloud.putTagged("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
			"id": "/ours/logs", "name": "/ours/logs",
		}, map[string]string{
			markers.TagEstate:  policyEstate,
			markers.TagAddress: "aws_cloudwatch_log_group.app",
		})
		res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
			{Addr: mustAddr(t, `aws_cloudwatch_log_group.app`), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
		}, cloud.providers(t), Options{Ownership: &Ownership{Estate: policyEstate, Policy: pol}})
		assertNoErrors(t, diags)
		return res
	}

	withoutPolicy := build(nil)
	withDefaults := build(buildPolicy(t, "converge", "refuse"))

	if len(withoutPolicy.Policy) != 0 || len(withDefaults.Policy) != 0 {
		t.Errorf("default verbs must record no policy outcomes: nil=%v explicit=%v", withoutPolicy.Policy, withDefaults.Policy)
	}
	if withoutPolicy.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) != withDefaults.Has(mustAddr(t, `aws_cloudwatch_log_group.app`)) {
		t.Error("admission differs between no policy and an explicit-default policy")
	}
}
