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
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// regionChangeResolutions is testdata/region-change's declared population as
// a plan sees it after the repoint: aws_vpc.west still needs discovery,
// because a VPC's identity is assigned by EC2 and appears nowhere in
// configuration, and the other two are client-named.
func regionChangeResolutions(t *testing.T) []identity.Resolution {
	t.Helper()
	return []identity.Resolution{
		{Addr: mustAddr(t, "aws_vpc.west"), Class: identity.ClassNeedsDiscovery},
		{Addr: mustAddr(t, "aws_cloudwatch_log_group.west"), Class: identity.ClassConcrete, ImportID: "/app/logs"},
		{Addr: mustAddr(t, "aws_kms_key.east"), Class: identity.ClassConcrete, ImportID: "key-east"},
	}
}

// regionChangePass runs one provider-scoped pass over testdata/region-change.
func regionChangePass(t *testing.T, provider addrs.AbsProviderConfig, region string, cloud *fakeCloud) Pass {
	t.Helper()
	res, diags := Discover(context.Background(), Request{
		Estate:        estateName,
		Config:        loadConfig(t, "testdata/region-change"),
		Resolutions:   regionChangeResolutions(t),
		Provider:      cloud,
		ScopeProvider: provider,
		// The estate sweep is what reaches an object no declared instance
		// of this pass's own scope is waiting for, and it is on for every
		// real plan; without it neither pass would list aws_vpc for a
		// scope that declares none of it.
		Sweep:      true,
		SweepTypes: []string{"aws_vpc", "aws_kms_key"},
	})
	assertNoErrors(t, diags)
	return Pass{Provider: provider, Region: region, Result: res}
}

// TestRegionChangeStrandsTheOldRegionsObject is GitHub issue #906, measured
// at the layer that decides it.
//
// aws_vpc.west's block now names the east provider configuration; its live
// object is still in us-west-2 wearing this estate's marker for that address.
// The east pass lists us-east-1 and finds nothing, so on its own it plans a
// create. The west pass lists us-west-2, sees the object, and - because the
// address it is marked for is one the configuration still declares - used to
// drop it at [declared.declares]' branch and file it nowhere.
//
// The assertion is on the rendered refusal by value, not on a flag: what an
// operator has to be told is which live resource, in which region, and which
// provider configuration its address belongs to now.
func TestRegionChangeStrandsTheOldRegionsObject(t *testing.T) {
	east := testProviderAddr(t, "")
	west := testProviderAddr(t, "west")

	// us-east-1: aws_vpc.west's block points here now, and there is no VPC
	// here to find. The KMS key this configuration declares is here, which
	// is what the control below rests on.
	eastCloud := newFakeCloud()
	eastCloud.listable("aws_vpc")
	eastCloud.noFilter("aws_vpc")
	eastCloud.own("aws_kms_key", "key-east", "aws_kms_key.east")

	// us-west-2: the VPC the last apply built, still marked for the address
	// that has since moved to the east configuration.
	westCloud := newFakeCloud()
	westCloud.listable("aws_vpc")
	westCloud.noFilter("aws_vpc")
	westCloud.own("aws_vpc", "vpc-left-behind", "aws_vpc.west")

	merged, _, diags := Merge(estateName, []Pass{
		regionChangePass(t, east, "us-east-1", eastCloud),
		regionChangePass(t, west, "us-west-2", westCloud),
	}, false)

	if !diags.HasErrors() {
		t.Fatalf("a region change left vpc-left-behind in us-west-2 with this estate's marker for aws_vpc.west and the run raised no error at all; merged result:\n%s", merged)
	}

	problems := merged.ProblemsOfKind(ProblemOutOfScopeMarker)
	if len(problems) != 1 {
		t.Fatalf("want exactly one %s problem, got %d; merged result:\n%s", ProblemOutOfScopeMarker, len(problems), merged)
	}
	p := problems[0]
	if got, want := p.Addr.String(), "aws_vpc.west"; got != want {
		t.Errorf("problem names address %q, want %q", got, want)
	}
	if got, want := p.TypeName, "aws_vpc"; got != want {
		t.Errorf("problem names type %q, want %q", got, want)
	}
	for _, want := range []string{"vpc-left-behind", "us-west-2", "us-east-1"} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("the refusal does not name %q, so an operator cannot act on it:\n%s", want, p.Detail)
		}
	}
	// A refusal with an escape hatch has to name the escape hatch, the way
	// no_source_create's own does. The spelling comes from the schema, not
	// from a second copy of the sentence.
	if want := `strict { provider_change = "` + string(strict.ProviderChangeRecreate) + `" }`; !strings.Contains(p.Detail, want) {
		t.Errorf("the refusal does not name %q, the toggle that permits it:\n%s", want, p.Detail)
	}
	// The old promise. "Marker discovery will find it" is the sentence the
	// coverage line printed beside the create it proposed instead, and it
	// could never come true for this instance: discovery for aws_vpc.west
	// now lists us-east-1.
	if strings.Contains(p.Detail, "discovery will find it") {
		t.Errorf("the refusal repeats the recovery path that does not exist:\n%s", p.Detail)
	}

	// The control, in the same run: aws_kms_key.east's object is where its
	// own configuration looks, so nothing about it is stranded.
	for _, p := range merged.ProblemsOfKind(ProblemOutOfScopeMarker) {
		if p.Addr.String() == "aws_kms_key.east" {
			t.Errorf("an address whose own pass sighted its object was reported stranded:\n%s", p.Detail)
		}
	}
}

// TestSightingByBothPassesIsNotStranded is the false positive this finding
// has to be free of, and the reason it cannot be decided inside one pass.
//
// An account-global list operation hands every pass objects that belong, in
// configuration, to a different provider configuration - which from inside
// the pass that does not declare them looks exactly like the stranded object
// above. What separates them is whether the address's OWN pass saw it too,
// so here both passes sight aws_kms_key.east's one object and nothing may be
// refused.
func TestSightingByBothPassesIsNotStranded(t *testing.T) {
	east := testProviderAddr(t, "")
	west := testProviderAddr(t, "west")

	eastCloud := newFakeCloud()
	eastCloud.own("aws_kms_key", "key-east", "aws_kms_key.east")

	// The same physical key, answering the west pass's own list because the
	// type's list operation is not region-scoped.
	westCloud := newFakeCloud()
	westCloud.own("aws_kms_key", "key-east", "aws_kms_key.east")

	merged, _, diags := Merge(estateName, []Pass{
		regionChangePass(t, east, "us-east-1", eastCloud),
		regionChangePass(t, west, "us-west-2", westCloud),
	}, false)

	if problems := merged.ProblemsOfKind(ProblemOutOfScopeMarker); len(problems) != 0 {
		t.Errorf("an account-global sighting of a declared resource was refused as stranded: %s", problems[0].Detail)
	}
	if diags.HasErrors() {
		t.Errorf("the merge errored over an object both passes could see:\n%s", diags.Err())
	}
}

// TestRegionChangeToggleRecreatesAndWarns is the escape hatch the maintainer
// ruled for on 2026-09-06: `strict { provider_change = "recreate" }` selects
// stock OpenTofu's own outcome - plan the create under the new provider
// configuration, leave the old one's object where it is.
//
// What the toggle buys is the create, not silence. The same finding is still
// raised, at warning severity, and it must still say the three things an
// operator cannot recover without: which object, where it is, and that
// nothing will ever find it again. This is the only notice there will be.
func TestRegionChangeToggleRecreatesAndWarns(t *testing.T) {
	east := testProviderAddr(t, "")
	west := testProviderAddr(t, "west")

	eastCloud := newFakeCloud()
	eastCloud.listable("aws_vpc")
	eastCloud.noFilter("aws_vpc")
	eastCloud.own("aws_kms_key", "key-east", "aws_kms_key.east")

	westCloud := newFakeCloud()
	westCloud.listable("aws_vpc")
	westCloud.noFilter("aws_vpc")
	westCloud.own("aws_vpc", "vpc-left-behind", "aws_vpc.west")

	merged, _, diags := Merge(estateName, []Pass{
		regionChangePass(t, east, "us-east-1", eastCloud),
		regionChangePass(t, west, "us-west-2", westCloud),
	}, true)

	if diags.HasErrors() {
		t.Fatalf("provider_change = %q must let the plan proceed, and it errored:\n%s", recreateSetting, diags.Err())
	}
	if problems := merged.ProblemsOfKind(ProblemOutOfScopeMarker); len(problems) != 0 {
		t.Errorf("the refusal still fired under the toggle: %s", problems[0].Detail)
	}

	problems := merged.ProblemsOfKind(ProblemAbandonedByProviderChange)
	if len(problems) != 1 {
		t.Fatalf("want exactly one %s warning under the toggle, got %d; merged result:\n%s", ProblemAbandonedByProviderChange, len(problems), merged)
	}
	p := problems[0]
	if got, want := p.Addr.String(), "aws_vpc.west"; got != want {
		t.Errorf("warning names address %q, want %q", got, want)
	}
	for _, want := range []string{"vpc-left-behind", "us-west-2", "us-east-1", "no plan will propose anything for it"} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("the warning does not say %q, so it is not the notice it has to be:\n%s", want, p.Detail)
		}
	}
	if strings.Contains(p.Detail, "discovery will find it") {
		t.Errorf("the warning repeats the recovery path that does not exist:\n%s", p.Detail)
	}
	// Severity is read through the same call the operator's diagnostic is
	// built from, not asserted about the kind in the abstract.
	if got := problemDiag(&Result{}, p).Severity(); got != tfdiags.Warning {
		t.Errorf("the toggle's own finding renders at severity %v, want a warning - an error would mean the toggle bought nothing", got)
	}
}

// TestProviderChangeToggleDoesNotWeakenTheCollisionRefusal is the boundary of
// what the toggle is allowed to buy.
//
// Two live objects that BOTH already exist, in two regions, carrying one
// address's marker, is [crossProviderOrphanCollisions]' finding and a
// different question entirely: nothing in the configuration says which is the
// real owner, and no setting can answer it. The toggle permits creating a
// second object; it does not permit guessing between two that are already
// there. This is the same fixture and estate as
// TestMergeCrossProviderOrphanCollision, run with the toggle on.
func TestProviderChangeToggleDoesNotWeakenTheCollisionRefusal(t *testing.T) {
	cloudA := newFakeCloud()
	cloudA.own("aws_vpc", "vpc-gone-east", `aws_vpc.retired`)
	resA, diagsA := discoverFixture(t, cloudA, Request{})
	assertNoErrors(t, diagsA)

	cloudB := newFakeCloud()
	cloudB.own("aws_vpc", "vpc-gone-west", `aws_vpc.retired`)
	resB, diagsB := discoverFixture(t, cloudB, Request{})
	assertNoErrors(t, diagsB)

	merged, _, diags := Merge(estateName, []Pass{
		{Provider: testProviderAddr(t, ""), Region: "us-east-1", Result: resA},
		{Provider: testProviderAddr(t, "west"), Region: "us-west-2", Result: resB},
	}, true)

	if !diags.HasErrors() {
		t.Fatalf("provider_change = %q relaxed a cross-provider marker collision, which is a different finding and no toggle's to relax:\n%s", recreateSetting, merged)
	}
	if problems := merged.ProblemsOfKind(ProblemCollision); len(problems) != 1 {
		t.Fatalf("want exactly one collision problem under the toggle, got %d:\n%s", len(problems), merged)
	}
	if len(merged.Removals()) != 0 {
		t.Errorf("the toggle let a collision propose a removal after all:\n%s", merged)
	}
}

// TestRecreateSettingMatchesTheSchema pins [recreateSetting] - the spelling
// both of this file's messages tell an operator to write - against the schema
// that actually defines it. The constant is a literal here on purpose, so
// that internal/live/discovery takes a resolved bool from its caller rather
// than depending on internal/live/strict; this test is what keeps that from
// meaning the two can drift.
func TestRecreateSettingMatchesTheSchema(t *testing.T) {
	if got, want := recreateSetting, string(strict.ProviderChangeRecreate); got != want {
		t.Errorf("recreateSetting = %q, but the schema spells the setting %q; every message naming it is now wrong", got, want)
	}
	if strict.RecreatesOnProviderChange(strict.DefaultProviderChange) {
		t.Error("the schema's default now permits the repoint, so this package's own default (a false bool from a caller holding no configuration) no longer matches it")
	}
}
