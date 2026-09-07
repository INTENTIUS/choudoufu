// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is GitHub issue #938's read half, and it is deliberately the
// only test in this package that builds its record by RUNNING
// [projection.WriteBack] rather than by seeding one.
//
// Every other superseded-claimant test in supersededclaimant_test.go seeds
// the record shape it wants and asserts what this package does with it, which
// is the right shape for testing this package - and it is exactly why #938
// went unnoticed until reference-ec2-vpc's day2_crash stage measured it. The
// two halves each did what its own test said, and the shape the write half
// actually produced after a crashed create_before_destroy replace was one no
// seeded test ever asked for: no deposed entry, and no tombstone either.
//
// So this joins the halves at the writer. The three applies below are the
// three the stage runs, the record between them is whatever WriteBack really
// wrote, and the assertion is on the claimant set an operator's next plan
// gets.

// crashWindowSchemas is the provider schema set the applies below decode
// through: one server-assigned, taggable type, the same shape
// [fakeCloud.GetProviderSchema] hands this package's own fixtures.
func crashWindowSchemas() *tofu.Schemas {
	schema := providers.Schema{
		Version: 1,
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"tags": {Type: cty.Map(cty.String), Optional: true},
		}},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Required: true},
			},
		},
	}
	return &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		recordOrphanProviderAddr.Provider: {ResourceTypes: map[string]providers.Schema{"aws_vpc": schema}},
	}}
}

// crashWindowObject encodes one applied object of that type.
func crashWindowObject(t *testing.T, id string) *states.ResourceInstanceObjectSrc {
	t.Helper()
	schemas := crashWindowSchemas()
	block := schemas.Providers[recordOrphanProviderAddr.Provider].ResourceTypes["aws_vpc"].Block
	src, err := (&states.ResourceInstanceObject{
		Status: states.ObjectReady,
		Value: cty.ObjectVal(map[string]cty.Value{
			"id":   cty.StringVal(id),
			"tags": cty.MapValEmpty(cty.String),
		}),
	}).Encode(block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding %s: %s", id, err)
	}
	return src
}

// crashWindowWriteBack runs one apply's write-back: current is the object
// the address came out of the apply owning, deposed is what the final state
// still carries as deposed, replaced and destroyedDeposed are what THIS
// run's plan scheduled. Returns the envelope version it wrote, for the next
// apply's conditional write.
func crashWindowWriteBack(t *testing.T, store *projection.RecordStore, addr addrs.AbsResourceInstance, current string, deposed map[string]string, replaced []addrs.AbsResourceInstance, destroyedDeposed []string, version string) string {
	t.Helper()
	ctx := context.Background()

	final := states.NewState()
	ms := final.EnsureModule(addrs.RootModuleInstance)
	ms.SetResourceInstanceCurrent(addr.Resource, crashWindowObject(t, current), recordOrphanProviderAddr, addrs.NoKey)
	for dk, id := range deposed {
		ms.SetResourceInstanceDeposed(addr.Resource, states.DeposedKey(dk), crashWindowObject(t, id), recordOrphanProviderAddr, addrs.NoKey)
	}

	var destroys []projection.DeposedDestroy
	for _, dk := range destroyedDeposed {
		destroys = append(destroys, projection.DeposedDestroy{Addr: addr, Key: states.DeposedKey(dk)})
	}

	diags := projection.WriteBack(ctx, projection.WriteBackRequest{
		Store:            store,
		EnvelopeVersions: []projection.RecordVersion{{Addr: addr, Version: version}},
		FinalState:       final,
		Schemas:          crashWindowSchemas(),
		ReplacedAddrs:    replaced,
		DestroyedDeposed: destroys,
	})
	assertNoErrors(t, diags)

	_, next, _, exists, err := store.GetIdentity(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("reading back the record written for %s: exists=%v err=%v", addr, exists, err)
	}
	return next
}

// TestDiscover_crashWindowClosedLeavesOneClaimant is
// [gauntlet:reference-ec2-vpc/day2_crash]'s H3 in miniature: the plan AFTER
// the one that closed the crash window.
//
// vpc-old was deposed by an interrupted create_before_destroy replace and
// terminated by the recovery apply. Its tags stay readable through the
// tagging API for a time after that - AWS's own documented behaviour for a
// terminated object, and what the emulator reproduces - so the sweep hands
// this pass TWO claimants for one declared address. Exactly one of them is
// live.
//
// The record is not seeded: it is whatever the three applies actually wrote.
// Before issue #938 the recovery apply wrote nothing at all about vpc-old,
// and this address refused with "Two live resources claiming one address"
// until AWS forgot the terminated object.
func TestDiscover_crashWindowClosedLeavesOneClaimant(t *testing.T) {
	cloud := newFakeCloud()
	// The lingering sighting of the terminated object, beside the live one.
	cloud.own("aws_vpc", "vpc-old", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-new", `aws_vpc.main`)

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estateName))
	addr := mustAddr(t, `aws_vpc.main`)

	// H0: the apply that created vpc-old.
	version := crashWindowWriteBack(t, store, addr, "vpc-old", nil, nil, nil, "")
	// H1: the create_before_destroy replace, interrupted after the create
	// committed and before the destroy dispatched.
	version = crashWindowWriteBack(t, store, addr, "vpc-new", map[string]string{"deadbeef": "vpc-old"},
		[]addrs.AbsResourceInstance{addr}, nil, version)
	// H2: the recovery apply, whose plan proposes exactly one thing -
	// destroy the deposed object - and which replaces nothing.
	crashWindowWriteBack(t, store, addr, "vpc-new", nil, nil, []string{"deadbeef"}, version)

	// What the record actually says now, by value, because it is the whole
	// of what this pass has to work from.
	tombstones, _, _, err := store.GetTombstones(context.Background(), addr)
	if err != nil {
		t.Fatalf("GetTombstones: %s", err)
	}
	var tombstoned []string
	for _, rec := range tombstones {
		tombstoned = append(tombstoned, rec.ImportID)
	}
	if fmt.Sprint(sortedStrings(tombstoned)) != fmt.Sprint([]string{"vpc-old"}) {
		t.Fatalf("after the recovery apply the record names %v as destroyed by this estate, want [vpc-old] - GitHub issue #938", sortedStrings(tombstoned))
	}
	deposed, _, _, err := store.GetDeposed(context.Background(), addr)
	if err != nil {
		t.Fatalf("GetDeposed: %s", err)
	}
	if len(deposed) != 0 {
		t.Fatalf("the record still holds deposed objects %v after the apply that destroyed them", deposed)
	}

	// H3: the plan after the recovery apply.
	res, diags := discoverFixture(t, cloud, Request{HintStore: raw})
	assertNoErrors(t, diags)

	if problems := res.ProblemsOfKind(ProblemCollision); len(problems) != 0 {
		t.Fatalf("the plan after the crash window closed still refused the address:\n%s", res)
	}
	b, bound := res.BindingFor(addr)
	if !bound {
		t.Fatalf("%s did not bind at all:\n%s", addr, res)
	}
	// The claimant set, by value. Binding vpc-old would be the wrong marker
	// HANDOFF ranks above a missing one: the plan would read and manage the
	// object the previous apply terminated while vpc-new stayed live and
	// unmanaged.
	if b.ImportID != "vpc-new" {
		t.Errorf("%s bound to %q, want the object the crashed apply's create committed, vpc-new", addr, b.ImportID)
	}
	var resolved string
	for _, r := range res.Resolutions {
		if r.Addr.String() == addr.String() {
			resolved = r.ImportID
		}
	}
	if resolved != "vpc-new" {
		t.Errorf("the merged resolution for %s carries import ID %q, want vpc-new - that value is what the plan reads", addr, resolved)
	}
	if got := displacedIDs(res); len(got) != 1 || got[0] != "vpc-old" {
		t.Errorf("the terminated object was not reported as displaced (got %v), so nothing in the run mentions it at all:\n%s", got, res)
	}
}

// TestDiscover_crashWindowStillOpenKeepsBothClaimants is the control beside
// it, and the one that keeps GitHub issue #901's suppression load-bearing
// through the read half.
//
// Same three applies, except the recovery apply's destroy leg FAILED: the
// object is still deposed and still running. Both claimants must survive, so
// that [matchDeposedClaimant] can plan the destroy that is still owed. A
// tombstone written here would be a live object recorded as destroyed, and
// the only thing standing between that and it being pruned out of the
// collision set is which leg of pruneSupersededEntry runs first.
func TestDiscover_crashWindowStillOpenKeepsBothClaimants(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-old", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-new", `aws_vpc.main`)

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estateName))
	addr := mustAddr(t, `aws_vpc.main`)

	version := crashWindowWriteBack(t, store, addr, "vpc-old", nil, nil, nil, "")
	version = crashWindowWriteBack(t, store, addr, "vpc-new", map[string]string{"deadbeef": "vpc-old"},
		[]addrs.AbsResourceInstance{addr}, nil, version)
	// The destroy was scheduled and did not happen.
	crashWindowWriteBack(t, store, addr, "vpc-new", map[string]string{"deadbeef": "vpc-old"}, nil, []string{"deadbeef"}, version)

	tombstones, _, _, err := store.GetTombstones(context.Background(), addr)
	if err != nil {
		t.Fatalf("GetTombstones: %s", err)
	}
	if len(tombstones) != 0 {
		t.Fatalf("a deposed destroy that did not happen recorded %v as destroyed: vpc-old is deposed and still running", tombstones)
	}

	res, _ := discoverFixture(t, cloud, Request{HintStore: raw})
	if got := displacedIDs(res); len(got) != 0 {
		t.Fatalf("a live deposed object was reported as displaced (%v), which means it was pruned out of the claimant set the next apply has to destroy it from:\n%s", got, res)
	}
}
