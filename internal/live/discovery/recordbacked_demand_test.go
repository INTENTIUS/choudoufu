// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestDiscover_recordBackedAddrsSkipTheWholeDemand is edge 3 of the
// plan-node seam (rfc/20260823-foundation-order-ruling.md, ruling 3;
// GitHub issue #388): when the only needs-discovery resolution in a
// configuration is listed in Request.RecordBackedAddrs, the sweep's
// per-instance binding demand ends up empty and, with no Sweep requested
// either, Discover makes NO provider calls at all - the same
// nothing-to-do short circuit TestDiscoverNothingToDo proves for a
// configuration with no needs-discovery instances in the first place.
// This is the demand actually shrinking, not merely the bound result
// being discarded afterward.
func TestDiscover_recordBackedAddrsSkipTheWholeDemand(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))
	all := resolveOrFail(t, cfg).All()

	// Keep everything that is not ClassNeedsDiscovery, plus exactly one
	// needs-discovery instance (aws_vpc.main, the fixture's only VPC) -
	// mirroring TestDiscoverNothingToDo's own filtering, but leaving one
	// needs-discovery entry in to be excluded via RecordBackedAddrs rather
	// than absent altogether.
	var resolutions []identity.Resolution
	var vpc identity.Resolution
	foundVPC := false
	for _, r := range all {
		if r.Class != identity.ClassNeedsDiscovery {
			resolutions = append(resolutions, r)
			continue
		}
		if r.Addr.String() == `aws_vpc.main` {
			vpc = r
			foundVPC = true
			continue
		}
		// Every OTHER needs-discovery instance is dropped from this
		// request entirely (not merely excluded), so the demand this test
		// is measuring is exactly the one entry the assertion cares about.
	}
	if !foundVPC {
		t.Fatal("fixture no longer declares aws_vpc.main; this test's premise is stale")
	}
	resolutions = append(resolutions, vpc)

	cloud := newFakeCloud()
	// A live object DOES exist and even carries a valid marker - if the
	// demand were not actually shrunk, this is what a stray call would
	// find and bind.
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)

	res, diags := Discover(context.Background(), Request{
		Estate:            estateName,
		Config:            cfg,
		Resolutions:       resolutions,
		RecordBackedAddrs: map[string]bool{`aws_vpc.main`: true},
		Provider:          cloud,
	})
	assertNoErrors(t, diags)

	if len(cloud.requests) != 0 {
		t.Errorf("the provider was called %d times for a demand that should have been empty: %v", len(cloud.requests), cloud.requests)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("something bound despite an empty demand: %v", res.Bindings)
	}
	if len(res.Unbound) != 0 {
		t.Errorf("the excluded address should not even be reported unbound (it was never in the demand): %v", res.Unbound)
	}
	for _, r := range res.Resolutions {
		if r.Addr.String() == `aws_vpc.main` && r.Class != identity.ClassNeedsDiscovery {
			t.Errorf("the excluded resolution was rewritten even though it was never bound: %s", r)
		}
	}
}

// TestDiscover_recordBackedAddrsStillDeclaredNotOrphaned is edge 3's other
// half: excluding one instance of a type from the binding demand must not
// make it look like an orphan when the sweep (or, as here, an ordinary
// config-driven scan of a SIBLING instance of the same type) lists the
// live object that carries its marker, and must not stop a sibling
// instance of the same type from binding normally. The estate-wide orphan
// sweep itself is untouched by any of this - it is not what this test
// exercises, and RecordBackedAddrs plays no part in it (see
// Request.RecordBackedAddrs's own doc comment): what is exercised here is
// the ordinary declared/orphan distinction ([declared.declares]) that the
// sweep also depends on, proven still correct for an excluded address.
func TestDiscover_recordBackedAddrsStillDeclaredNotOrphaned(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_eip", "eip-0", `aws_eip.pool[0]`)
	cloud.own("aws_eip", "eip-1", `aws_eip.pool[1]`)
	cloud.own("aws_eip", "eip-2", `aws_eip.pool[2]`)

	res, diags := discoverFixture(t, cloud, Request{
		CollectUnclaimed: true,
		RecordBackedAddrs: map[string]bool{
			`aws_eip.pool[0]`: true,
		},
	})
	assertNoErrors(t, diags)

	if _, ok := res.BindingFor(mustAddr(t, `aws_eip.pool[0]`)); ok {
		t.Errorf("the excluded instance was bound anyway:\n%s", res)
	}
	if _, ok := res.BindingFor(mustAddr(t, `aws_eip.pool[1]`)); !ok {
		t.Errorf("a sibling instance of the same type failed to bind despite the exclusion:\n%s", res)
	}
	if _, ok := res.BindingFor(mustAddr(t, `aws_eip.pool[2]`)); !ok {
		t.Errorf("a sibling instance of the same type failed to bind despite the exclusion:\n%s", res)
	}

	for _, o := range res.Orphans {
		if o.TypeName == "aws_eip" && o.ImportID == "eip-0" {
			t.Errorf("the excluded instance's live object was reported as an orphan, not merely unbound: %#v", o)
		}
	}
	if len(res.Orphans) != 0 {
		t.Errorf("no orphan should have been produced at all: %v", res.Orphans)
	}

	// Still needs-discovery, exactly as an unbound instance stays (see
	// TestDiscoverUnboundIsNotAnError) - not concrete, not silently
	// dropped from the merged resolution list.
	for _, r := range res.Resolutions {
		if r.Addr.String() == `aws_eip.pool[0]` && r.Class != identity.ClassNeedsDiscovery {
			t.Errorf("the excluded instance's resolution was rewritten: %s", r)
		}
	}
}

// TestDiscover_recordBackedWholeCountBlockStillMintsSlot is #388's
// flag-sweep-scout regression (GitHub issue #388 comment, corpus-iam-policy):
// a count block whose ENTIRE declared demand is record-backed - the shape a
// migrated single-member `count = 1` block takes once its identity lives in
// the estate record - must still be indexed for slot purposes, or
// [Result.SlotTable] has no entry for it and [projection.NodeResolver]'s
// AdjustConfigValue writes no tofu-slot tag, mismatching what migrate
// already wrote onto the live marker. It must ALSO make no provider call at
// all, exactly as [TestDiscover_recordBackedAddrsSkipTheWholeDemand] proves
// for a non-count resolution: minting the slot is bookkeeping over the
// declared configuration, never a reason to scan.
func TestDiscover_recordBackedWholeCountBlockStillMintsSlot(t *testing.T) {
	cloud := newFakeCloud()
	// A live object matching the marker exists - if the demand were not
	// actually shrunk to zero provider calls, this is what a stray scan
	// would find.
	cloud.own("aws_eip", "eipalloc-a", `aws_eip.pool:0`)

	cfg1 := loadCountConfig(t, 1)
	res, diags := Discover(context.Background(), Request{
		Estate:            countEstate,
		Config:            cfg1,
		Resolutions:       resolveOrFail(t, cfg1).All(),
		RecordBackedAddrs: map[string]bool{`aws_eip.pool[0]`: true},
		Provider:          cloud,
	})
	assertNoErrors(t, diags)

	if len(cloud.requests) != 0 {
		t.Errorf("the provider was called %d times for a demand that should have been entirely record-backed: %v", len(cloud.requests), cloud.requests)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("a record-backed instance was bound by a scan it should never have run: %v", res.Bindings)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("no orphan should have been produced: %v", res.Orphans)
	}
	// The #388 default-flip's own regression (corpus-iam-policy's
	// day2_remove stage): a record-backed entry with zero claimants is not
	// genuinely unbound - it was excluded from the scan, not left
	// unanswered by it - and reporting it Unbound anyway makes
	// classifyOrphans's rename guard read this block as "still has an
	// unclaimed declared instance," withholding a genuine removal on any
	// SIBLING block sharing the same blockKey (type.name, no module path).
	// See declaredEntry.recordBacked's own doc comment.
	if len(res.Unbound) != 0 {
		t.Errorf("a record-backed instance was reported Unbound: %v", res.Unbound)
	}
}

// TestDiscover_recordBackedMultiInstanceCountBlockStillMintsSlots is
// TestDiscover_recordBackedWholeCountBlockStillMintsSlot generalized past a
// single-member set: corpus-vpc-complete's 28 regressed objects are the same
// shape at a larger scale (a migrated estate whose record store answers
// every instance of a block at once - GitHub issue #388's
// statelessRecordBackedNeedsDiscoveryAddrs reads the WHOLE estate's record in
// one pass, so a block's members are never partly record-backed in
// practice). Every declared index must get exactly the slot equal to its own
// index, with zero provider calls and zero bindings, the same as the N=1
// case - this is what proves the fix generalizes rather than happening to
// work only for a trivial one-entry countBlock.entries slice.
func TestDiscover_recordBackedMultiInstanceCountBlockStillMintsSlots(t *testing.T) {
	cloud := newFakeCloud()
	// Live objects matching every marker exist - if the demand were not
	// actually shrunk to zero provider calls, a stray scan would find them.
	cloud.own("aws_eip", "eipalloc-a", `aws_eip.pool:0`)
	cloud.own("aws_eip", "eipalloc-b", `aws_eip.pool:1`)
	cloud.own("aws_eip", "eipalloc-c", `aws_eip.pool:2`)

	cfg3 := loadCountConfig(t, 3)
	res, diags := Discover(context.Background(), Request{
		Estate:      countEstate,
		Config:      cfg3,
		Resolutions: resolveOrFail(t, cfg3).All(),
		RecordBackedAddrs: map[string]bool{
			`aws_eip.pool[0]`: true,
			`aws_eip.pool[1]`: true,
			`aws_eip.pool[2]`: true,
		},
		Provider: cloud,
	})
	assertNoErrors(t, diags)

	if len(cloud.requests) != 0 {
		t.Errorf("the provider was called %d times for a demand that should have been entirely record-backed: %v", len(cloud.requests), cloud.requests)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("a record-backed instance was bound by a scan it should never have run: %v", res.Bindings)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("no orphan should have been produced: %v", res.Orphans)
	}
	assertSlotTable(t, res, map[string]string{
		"aws_eip.pool:0": "0", "aws_eip.pool:1": "1", "aws_eip.pool:2": "2",
	})
}

// TestDiscover_recordBackedWholeTypeStillCollectsUnclaimed is GitHub issue
// #388 edge 3's foreign-coverage regression, found live on
// corpus-ec2-instance-complete's test_plan stage: "Foreign resources:
// nothing was swept" the moment every declared instance of a type is
// record-backed, because decl.typeNames() - the config-driven scan's own
// demand - excludes a type with no non-record-backed member left in it
// (see decl.recordBacked's own doc comment), which used to mean the type
// never got listed with Request.CollectUnclaimed at all: it fell into
// [sweepTypes]' undeclared-type universe instead, and a sweep never
// collects unclaimed resources (see TypeScan.Sweep's own doc comment) -
// reasoning that is sound for a type the configuration never mentions and
// wrong for one it does, merely record-backed for this run.
//
// Unlike TestDiscover_recordBackedWholeCountBlockStillMintsSlot (which
// proves the OPPOSITE - zero provider calls - for the ordinary bind/apply
// shape with CollectUnclaimed unset), this test sets both Sweep and
// CollectUnclaimed, the shape live-plan's own stateless path always uses,
// and a genuinely-foreign sibling live object of the same type must still
// be found.
func TestDiscover_recordBackedWholeTypeStillCollectsUnclaimed(t *testing.T) {
	cloud := newFakeCloud()
	cloud.noFilter("aws_eip") // as the real AWS provider's aws_eip list schema
	cloud.own("aws_eip", "eipalloc-0", `aws_eip.pool:0`)
	cloud.own("aws_eip", "eipalloc-1", `aws_eip.pool:1`)
	cloud.own("aws_eip", "eipalloc-2", `aws_eip.pool:2`)
	// Carries no tags at all - a real, unmarked, foreign EIP sitting in the
	// same account. If the whole type's demand silently stopped being
	// listed, this is exactly what would go unseen.
	cloud.obj("aws_eip", "eipalloc-wild", nil)

	res, diags := discoverFixture(t, cloud, Request{
		CollectUnclaimed: true,
		Sweep:            true,
		RecordBackedAddrs: map[string]bool{
			`aws_eip.pool[0]`: true,
			`aws_eip.pool[1]`: true,
			`aws_eip.pool[2]`: true,
		},
	})
	assertNoErrors(t, diags)

	if len(res.Bindings) != 0 {
		t.Errorf("a record-backed instance was bound by a scan it should never have run: %v", res.Bindings)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("no orphan should have been produced for the record-backed instances' own live objects: %v", res.Orphans)
	}
	for _, u := range res.Unbound {
		if u.Resource.Resource.Type == "aws_eip" {
			t.Errorf("a record-backed aws_eip instance was reported Unbound: %s", u)
		}
	}

	if len(res.Unclaimed) != 1 || res.Unclaimed[0].ImportID != "eipalloc-wild" {
		t.Fatalf("the wild EIP was not collected even though the whole type is record-backed and CollectUnclaimed was set:\n%s", res)
	}

	scan, ok := res.ScanFor("aws_eip")
	if !ok {
		t.Fatalf("aws_eip was not scanned at all")
	}
	if !scan.Sweep {
		t.Errorf("expected aws_eip to have been routed through the sweep (every declared instance is record-backed): %s", scan)
	}
	if scan.Scope != ScopeAll {
		t.Errorf("aws_eip was not widened to ScopeAll despite CollectUnclaimed: %s", scan)
	}

	for _, r := range res.Resolutions {
		switch r.Addr.String() {
		case `aws_eip.pool[0]`, `aws_eip.pool[1]`, `aws_eip.pool[2]`:
			if r.Class != identity.ClassNeedsDiscovery {
				t.Errorf("the record-backed resolution for %s was rewritten even though it was never bound: %s", r.Addr, r)
			}
		}
	}
}

// TestDiscover_recordBackedCollisionOnCountBlockIsReported is GitHub issue
// #411: a manufactured marker collision on a ServerAssigned/ARN-identity
// type's fungible count member - a second live object carrying the exact
// same tofu-address/tofu-slot as the one the estate record already answers
// for - used to be silently dropped rather than refused. Root cause: a
// record-backed count entry ([declared.recordBacked], edge 3 of the plan-node
// seam, GitHub issue #388) was excluded from [declared.entryFor], so a live
// claimant naming its address fell through to declares()+displacedFrom, which
// answers "not displaced" unconditionally for a ClassNeedsDiscovery address -
// an invariant that used to make this code unreachable (see displaced.go's
// own doc comment) until RecordBackedAddrs started excluding a
// ClassNeedsDiscovery entry from entryFor's index without also excluding it
// from declares(). The claimant was neither bound, nor an orphan, nor a
// Problem: it vanished.
//
// The fixture mirrors corpus-iam-policy's real BREAK=replace reproduction
// (live/e2e/corpus-iam-policy/run.sh, PART F): a count = 1 block whose sole
// declared instance is record-backed (the shape a migrated single-member
// count set takes, per TestDiscover_recordBackedWholeCountBlockStillMintsSlot
// just above), with TWO live members carrying the identical tofu-address and
// tofu-slot - the state a skipped destroy in a create-before-destroy replace
// leaves behind. Sweep+CollectUnclaimed is what corpus-sqs-basic's
// real live-plan callers always set (statelessDiscover, internal/command/
// live_plan.go) and what makes a record-backed type's own live objects
// still get listed at all (see partitionSweepTypes and
// TestDiscover_recordBackedWholeTypeStillCollectsUnclaimed above) - without
// it this fixture would prove nothing, because the type would never be
// scanned in the first place.
//
// The expected outcome is the established matrix's fungible-set answer -
// corpus-sqs-basic's own "Two live resources claiming one slot"
// (ProblemDuplicateSlot) - not the scalar "Live resource displaced..."
// warning: a count block's collision is a set-membership question
// (count.go's slots.Match), never a single address's identity question
// (displaced.go).
func TestDiscover_recordBackedCollisionOnCountBlockIsReported(t *testing.T) {
	cloud := newFakeCloud()
	cloud.noFilter("aws_eip") // matches the real aws_eip list schema's own shape
	// Two live objects, the SAME tofu-address and tofu-slot - the exact
	// shape a manufactured (or crash-left-behind) collision takes.
	cloud.slotted("eipalloc-a", "0")
	cloud.slotted("eipalloc-collision", "0")

	cfg1 := loadCountConfig(t, 1)
	res, diags := Discover(context.Background(), Request{
		Estate:            countEstate,
		Config:            cfg1,
		Resolutions:       resolveOrFail(t, cfg1).All(),
		RecordBackedAddrs: map[string]bool{`aws_eip.pool[0]`: true},
		CollectUnclaimed:  true,
		Sweep:             true,
		Provider:          cloud,
	})
	if !diags.HasErrors() {
		t.Fatalf("a manufactured collision on a record-backed count instance produced no error:\n%s", res)
	}

	problems := res.ProblemsOfKind(ProblemDuplicateSlot)
	if len(problems) != 1 {
		t.Fatalf("want exactly one duplicate-slot problem, got %d:\n%s", len(problems), res)
	}
	if !strings.Contains(problems[0].Detail, "eipalloc-a") || !strings.Contains(problems[0].Detail, "eipalloc-collision") {
		t.Errorf("the problem does not name both claimants: %s", problems[0].Detail)
	}
	if problems[0].TypeName != "aws_eip" {
		t.Errorf("the problem names type %q, want aws_eip", problems[0].TypeName)
	}

	// The collision must not be quietly resolved into a Binding either -
	// this is the refusal shape, not a guess about which claimant is real.
	if len(res.Bindings) != 0 {
		t.Errorf("a contested record-backed set bound anyway:\n%s", res)
	}
	if len(res.Unbound) != 0 {
		t.Errorf("a contested record-backed set was reported Unbound instead of refused:\n%s", res)
	}
}
