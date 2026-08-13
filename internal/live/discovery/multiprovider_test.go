// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

func testProviderAddr(t *testing.T, alias string) addrs.AbsProviderConfig {
	t.Helper()
	return addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("aws"),
		Alias:    alias,
	}
}

// TestMergeSinglePassIsUnchanged pins issue #69's central safety property at
// the merge layer: an estate whose managed resources sit under exactly one
// provider configuration must come out of Merge exactly as it went in,
// because that is the shape every existing single-provider caller of
// Discover already depends on.
func TestMergeSinglePassIsUnchanged(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	res, diags := discoverFixture(t, cloud, Request{})
	assertNoErrors(t, diags)

	provider := testProviderAddr(t, "")
	merged, providerOf, mergeDiags := Merge(estateName, []Pass{{Provider: provider, Result: res}})
	assertNoErrors(t, mergeDiags)

	if merged != res {
		t.Error("a single pass must come back unchanged, not rebuilt field by field")
	}
	for _, r := range res.Resolutions {
		if !r.Undeclared {
			continue
		}
		if providerOf[r.Addr.String()].String() != provider.String() {
			t.Errorf("%s not attributed to the single pass's provider", r.Addr)
		}
	}
}

// TestMergeUnionsDisjointPasses is the ordinary multi-provider case: two
// provider configurations, each finding a different orphan, neither
// clashing with the other. Both must appear in the merged result, and
// neither's removal is disturbed.
func TestMergeUnionsDisjointPasses(t *testing.T) {
	cloudA := newFakeCloud()
	cloudA.own("aws_vpc", "vpc-gone-a", `aws_vpc.retired`)
	resA, diagsA := discoverFixture(t, cloudA, Request{})
	assertNoErrors(t, diagsA)

	cloudB := newFakeCloud()
	cloudB.own("aws_kms_key", "key-gone-b", `aws_kms_key.retired`)
	resB, diagsB := discoverFixture(t, cloudB, Request{})
	assertNoErrors(t, diagsB)

	providerA := testProviderAddr(t, "")
	providerB := testProviderAddr(t, "west")

	merged, providerOf, mergeDiags := Merge(estateName, []Pass{
		{Provider: providerA, Result: resA},
		{Provider: providerB, Result: resB},
	})
	assertNoErrors(t, mergeDiags)

	if len(merged.ProblemsOfKind(ProblemCollision)) != 0 {
		t.Fatalf("two orphans at different addresses were reported as colliding:\n%s", merged)
	}

	var removedA, removedB bool
	for _, o := range merged.Orphans {
		switch o.ImportID {
		case "vpc-gone-a":
			removedA = o.Removal
		case "key-gone-b":
			removedB = o.Removal
		}
	}
	if !removedA || !removedB {
		t.Errorf("both orphans should still be proposed for removal:\n%s", merged)
	}

	if providerOf[mustAddr(t, `aws_vpc.retired`).String()].String() != providerA.String() {
		t.Error("the VPC orphan was not attributed to the pass that found it")
	}
	if providerOf[mustAddr(t, `aws_kms_key.retired`).String()].String() != providerB.String() {
		t.Error("the KMS key orphan was not attributed to the pass that found it")
	}

	// Both passes ran over the identical estate config (discoverFixture
	// always loads estateDir), so they share the same base set of declared
	// resolutions. Naively concatenating each pass's own Resolutions list -
	// the bug this test would not otherwise catch - would double every one
	// of those declared resolutions; the merged count must be the base
	// count plus exactly the two distinct orphans, not the base counted
	// twice plus two.
	wantCount := len(resA.Resolutions) + 1 // resA already carries its own orphan; +1 for resB's distinct one
	if len(merged.Resolutions) != wantCount {
		t.Errorf("merged %d resolutions, want %d (declared resolutions must not be duplicated per pass):\n%s", len(merged.Resolutions), wantCount, merged)
	}
}

// TestMergeCrossProviderOrphanCollision is issue #69's requirement (3): two
// provider configurations (two regions, in practice) each independently
// find a live resource carrying this estate's marker for the *same*
// address. live/MARKERS.md's Ownership semantics make no exception for
// region - "at most one live resource per address per estate" - so this
// must come out as a named collision, not as two legitimate resources or as
// two independent destroys.
func TestMergeCrossProviderOrphanCollision(t *testing.T) {
	cloudA := newFakeCloud()
	cloudA.own("aws_vpc", "vpc-gone-east", `aws_vpc.retired`)
	resA, diagsA := discoverFixture(t, cloudA, Request{})
	assertNoErrors(t, diagsA)
	if len(resA.Removals()) != 1 {
		t.Fatalf("pass A did not propose its own removal on its own:\n%s", resA)
	}

	cloudB := newFakeCloud()
	cloudB.own("aws_vpc", "vpc-gone-west", `aws_vpc.retired`)
	resB, diagsB := discoverFixture(t, cloudB, Request{})
	assertNoErrors(t, diagsB)
	if len(resB.Removals()) != 1 {
		t.Fatalf("pass B did not propose its own removal on its own:\n%s", resB)
	}

	providerA := testProviderAddr(t, "")
	providerB := testProviderAddr(t, "west")

	merged, _, mergeDiags := Merge(estateName, []Pass{
		{Provider: providerA, Region: "us-east-1", Result: resA},
		{Provider: providerB, Region: "us-west-2", Result: resB},
	})
	if !mergeDiags.HasErrors() {
		t.Fatalf("a cross-provider marker collision produced no error")
	}

	problems := merged.ProblemsOfKind(ProblemCollision)
	if len(problems) != 1 {
		t.Fatalf("want exactly one collision problem, got %d:\n%s", len(problems), merged)
	}
	if problems[0].Marker != `aws_vpc.retired` {
		t.Errorf("collision problem names the wrong address: %s", problems[0])
	}
	// Both regions and both live IDs must be legible in the message an
	// operator actually reads - the maintainer ruling on issue #69's
	// cross-region collision question is that this must be reported loudly,
	// naming both sides, not silently resolved either way.
	for _, want := range []string{"us-east-1", "us-west-2", "vpc-gone-east", "vpc-gone-west"} {
		if !strings.Contains(problems[0].Detail, want) {
			t.Errorf("collision detail does not mention %q:\n%s", want, problems[0].Detail)
		}
	}
	t.Logf("collision message: %s", problems[0].Detail)

	// Neither side's removal survives the collision: this estate's honest
	// answer is "a human has to look", not "destroy one of them and hope".
	if len(merged.Removals()) != 0 {
		t.Errorf("a cross-provider collision must withhold removal from both sides:\n%s", merged)
	}
	for _, o := range merged.Orphans {
		if o.Removal {
			t.Errorf("%s is still marked for removal despite the collision", o)
		}
		if o.Withheld == "" {
			t.Errorf("%s has no reason recorded for withholding its removal", o)
		}
	}

	// The merged Resolutions list must not carry either side's synthetic
	// removal resolution - a plan built from this must not propose
	// destroying either live resource.
	for _, r := range merged.Resolutions {
		if r.Addr.String() == mustAddr(t, `aws_vpc.retired`).String() {
			t.Errorf("a resolution survived the collision for the colliding address: %s", r)
		}
	}
}

func TestMergeEmpty(t *testing.T) {
	merged, providerOf, diags := Merge(estateName, nil)
	assertNoErrors(t, diags)
	if merged.Estate != estateName {
		t.Errorf("empty merge lost the estate name: %q", merged.Estate)
	}
	if len(providerOf) != 0 {
		t.Errorf("empty merge produced provider attributions: %v", providerOf)
	}
}

// TestDiscoverScopeProviderLeavesAnotherProvidersDeclaredResourceAlone is
// the false-orphan hazard issue #69's design has to avoid: a resource type
// whose list operation is account-global rather than region-scoped
// (aws_s3_bucket chosen for the real alias-e2e fixture; also IAM and
// Route53 - see [Request.ScopeProvider]'s doc comment) hands *every* pass
// every account's population of the type, including objects declared under
// a *different* provider configuration. A pass scoped to provider A must
// recognize such an object as somebody else's declared, owned resource -
// not misread it as an orphan and propose destroying it.
//
// This is exercised directly against Discover (not Merge) because the
// property belongs to one pass in isolation: a single ScopeProvider'd call
// must never orphan an address it simply isn't responsible for, regardless
// of whether a second pass exists to claim it.
func TestDiscoverScopeProviderLeavesAnotherProvidersDeclaredResourceAlone(t *testing.T) {
	cloud := newFakeCloud()
	// A live VPC matching the estate's own declared aws_vpc.main address -
	// legitimately owned, exactly as if some other pass's provider
	// configuration had already claimed it.
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)

	cfg := loadConfig(t, estateDir(t))
	resolutions := resolveOrFail(t, cfg).All()

	// A provider configuration nothing in the fixture actually uses, so
	// every declared resource block is out of this call's scope - the
	// sharpest version of "this pass owns none of the estate's declared
	// resources", which is exactly the shape a pass whose own provider
	// legitimately owns nothing yet (or whose resources are all owned by
	// other passes) takes in the real multi-provider loop.
	outOfScope := testProviderAddr(t, "not-declared-anywhere")

	res, diags := Discover(context.Background(), Request{
		Estate:           estateName,
		Config:           cfg,
		Resolutions:      resolutions,
		Provider:         cloud,
		ScopeProvider:    outOfScope,
		CollectUnclaimed: true,
		Sweep:            true,
	})
	assertNoErrors(t, diags)

	if len(res.Orphans) != 0 {
		t.Errorf("a declared resource outside this pass's scope was misread as an orphan:\n%s", res)
	}
	if len(res.Removals()) != 0 {
		t.Errorf("a declared resource outside this pass's scope was proposed for removal:\n%s", res)
	}
	if _, ok := res.BindingFor(mustAddr(t, `aws_vpc.main`)); ok {
		t.Error("a scoped-out pass bound an address it has no business finding")
	}
	if containsAddr(res.Unbound, `aws_vpc.main`) {
		t.Error("a scoped-out pass reported another provider's address as unbound rather than simply not its business")
	}

	// The resolution rides through completely untouched: still
	// needs-discovery, exactly as [identity.Resolve] produced it, ready for
	// whichever pass's own scope actually owns it.
	for _, r := range res.Resolutions {
		if r.Addr.String() != mustAddr(t, `aws_vpc.main`).String() {
			continue
		}
		if r.Class != identity.ClassNeedsDiscovery {
			t.Errorf("aws_vpc.main was rewritten by a pass outside its scope: %s", r)
		}
	}
}

// TestMergeDedupesSweepCoverage: whether a type is listable or taggable at
// all is a fact about the provider's schema - identical for every provider
// configuration built from the same provider version - so two passes both
// sweeping the same type (found or not found) must not double it in the
// merged report. Real evidence this matters: TestAliasedProvidersAgainstFloci's
// two-provider sweep against a real AWS provider reports ~300 admitted
// types, and before this dedup, every one of them appeared twice.
//
// The two Results are hand-built rather than produced by a real Discover
// call: SweepCovered can only be populated for a type this package's fake
// cloud can list, and every type the fake cloud lists is also one the P0.1
// fixture declares - which the config-driven scan, not the sweep, would
// claim first. Merge does not care where a Result came from, only that its
// fields are internally consistent, so this is a fair test of Merge's own
// dedup logic in isolation.
func TestMergeDedupesSweepCoverage(t *testing.T) {
	resA := &Result{
		SweepGaps:    []SweepGap{{TypeName: "aws_db_instance", Reason: SweepGapNotListable, Detail: "not listable"}},
		SweepCovered: []string{"aws_eip", "aws_vpc"},
	}
	resB := &Result{
		SweepGaps:    []SweepGap{{TypeName: "aws_db_instance", Reason: SweepGapNotListable, Detail: "not listable"}},
		SweepCovered: []string{"aws_eip", "aws_kms_key"},
	}

	merged, _, mergeDiags := Merge(estateName, []Pass{
		{Provider: testProviderAddr(t, ""), Result: resA},
		{Provider: testProviderAddr(t, "west"), Result: resB},
	})
	assertNoErrors(t, mergeDiags)

	dbInstanceGaps := 0
	for _, g := range merged.SweepGaps {
		if g.TypeName == "aws_db_instance" {
			dbInstanceGaps++
		}
	}
	if dbInstanceGaps != 1 {
		t.Errorf("aws_db_instance's sweep gap appears %d times in the merged result, want 1:\n%s", dbInstanceGaps, merged)
	}

	eipCovered := 0
	for _, typeName := range merged.SweepCovered {
		if typeName == "aws_eip" {
			eipCovered++
		}
	}
	if eipCovered != 1 {
		t.Errorf("aws_eip (covered by both passes) appears %d times in SweepCovered, want 1:\n%s", eipCovered, merged)
	}
	// Each pass's own unique coverage still survives the dedup.
	for _, want := range []string{"aws_vpc", "aws_kms_key"} {
		found := false
		for _, typeName := range merged.SweepCovered {
			if typeName == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s (covered by only one pass) is missing from merged SweepCovered:\n%s", want, merged)
		}
	}
}
