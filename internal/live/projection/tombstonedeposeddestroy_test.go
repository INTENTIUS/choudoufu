// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is GitHub issue #938, and it is the half tombstonedeposed_test.go
// (issue #901) left open one apply later.
//
// #901 is right that the apply which CRASHES mid-replace must record
// nothing: its deposed object is alive. The object is destroyed by the NEXT
// apply, and that apply has neither of the two facts [supersedeIdentity]
// needs - the address's recorded identity has named the new object since the
// crashed apply, so [identitySuperseded] is false, and its plan schedules a
// deposed destroy rather than a replace, so [WriteBackRequest.ReplacedAddrs]
// is empty. The identity was destroyed by this estate's own apply and was
// recorded nowhere, so the plan after it saw the terminated object's
// lingering tag as a second live claimant and refused the address:
//
//	Error: Two live resources claiming one address
//
//	2 live aws_instance resources carry estate "ec2-reference" and address
//	"aws_instance.main" at once: i-1fbf304b2bb047c88, i-65a7966e4fd5a1a42.
//
// That is reference-ec2-vpc's day2_crash stage, and HANDOFF's row 1:
// choudoufu refuses where stock proceeds.
//
// Every assertion is on a value read back through [RecordStore.GetTombstones]
// and [RecordStore.GetDeposed], the same discipline the two files beside this
// one state: the record is the whole of what discovery's
// pruneSupersededEntry has to work from.

// supersedeApplyDestroyingDeposed runs the write-back of the apply that
// CLOSES a crash window: the address's current object is unchanged
// (currentID, the object the crashed apply's create committed), the deposed
// keys in stillDeposed are whatever the final state still carries, and
// destroyed is what this run's PLAN scheduled a deposed destroy of -
// [WriteBackRequest.DestroyedDeposed], which backend/local's
// destroyedDeposedInstances derives from the plan.
//
// ReplacedAddrs is deliberately nil: this apply replaced nothing, which is
// exactly why issue #854's signal cannot carry this case.
func supersedeApplyDestroyingDeposed(t *testing.T, rs *RecordStore, addr addrs.AbsResourceInstance, currentID string, stillDeposed map[string]string, destroyed []string, version string) string {
	t.Helper()
	ctx := context.Background()

	encode := func(id string) *states.ResourceInstanceObjectSrc {
		t.Helper()
		obj := cty.ObjectVal(map[string]cty.Value{
			"id":            cty.StringVal(id),
			"allocation_id": cty.StringVal("eipalloc-declared"),
			"instance_id":   cty.StringVal("i-declared"),
		})
		src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: obj}).
			Encode(locatedTypeSchema().Block.ImpliedType(), 0, 0)
		if err != nil {
			t.Fatalf("encoding %s: %s", id, err)
		}
		return src
	}

	final := states.NewState()
	ms := final.EnsureModule(addrs.RootModuleInstance)
	ms.SetResourceInstanceCurrent(addr.Resource, encode(currentID), locatedTestProvider, addrs.NoKey)
	for dk, id := range stillDeposed {
		ms.SetResourceInstanceDeposed(addr.Resource, states.DeposedKey(dk), encode(id), locatedTestProvider, addrs.NoKey)
	}

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{locatedTestType: locatedTypeSchema()}},
	}}

	var destroys []DeposedDestroy
	for _, dk := range destroyed {
		destroys = append(destroys, DeposedDestroy{Addr: addr, Key: states.DeposedKey(dk)})
	}

	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:            rs,
		EnvelopeVersions: []RecordVersion{{Addr: addr, Version: version}},
		FinalState:       final,
		Schemas:          schemas,
		ReplacedAddrs:    nil,
		DestroyedDeposed: destroys,
	}))

	_, next, _, exists, err := rs.GetIdentity(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("reading back the record written for %s: exists=%v err=%v", addr, exists, err)
	}
	return next
}

// TestWriteBackDestroyingADeposedObjectRecordsItAsDestroyed is issue #938's
// headline, and reference-ec2-vpc's day2_crash in miniature: the whole
// three-apply sequence the stage runs, against one record store.
//
// The record must come out of the third apply saying two things: nothing is
// deposed at this address any more, and eipassoc-old was destroyed by this
// estate. The second is what lets the plan after it tell the terminated
// object's lingering tag from a second live claimant.
func TestWriteBackDestroyingADeposedObjectRecordsItAsDestroyed(t *testing.T) {
	ctx := context.Background()
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const oldID = "eipassoc-00112233445566778"
	const newID = "eipassoc-99887766554433221"
	const deposedKey = "deadbeef"

	located := newTestLocatedStore(localHintStore(t), "test-estate")

	// H0: the apply that first recorded the address.
	version := supersedeApplyReplacing(t, located.rs, addr, oldID, "", nil)

	// H1: the create_before_destroy replace, interrupted after the create
	// committed and before the destroy dispatched. Records no tombstone -
	// GitHub issue #901, and TestWriteBackReplaceWithAFailedDestroyLeg-
	// TombstonesNothing is that assertion.
	version = supersedeApplyDeposing(t, located.rs, addr, newID, deposedKey, oldID, version)
	if got := tombstonedIDs(t, located.rs, addr); len(got) != 0 {
		t.Fatalf("the crashed apply recorded %v as destroyed; issue #901 says it must record none, and this test's subject is the apply AFTER it", got)
	}

	// H2: the recovery apply. Its plan proposes exactly one thing - destroy
	// the deposed object - and it succeeds, so the final state carries no
	// deposed object at all.
	supersedeApplyDestroyingDeposed(t, located.rs, addr, newID, nil, []string{deposedKey}, version)

	rec, _, _, exists, err := located.rs.GetIdentity(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("reading the current identity after the recovery apply: exists=%v err=%v", exists, err)
	}
	if rec.ImportID != newID {
		t.Errorf("the address's current identity is %q, want the object the crashed apply's create committed, %q", rec.ImportID, newID)
	}

	if got := deposedIDs(t, located.rs, addr); len(got) != 0 {
		t.Errorf("the record still holds deposed objects %v after the apply that destroyed them, want none", got)
	}

	// The headline.
	got := tombstonedIDs(t, located.rs, addr)
	if len(got) != 1 || got[0] != oldID {
		t.Fatalf("after the apply that destroyed the deposed object the record names %v as destroyed by this estate, want exactly [%q].\n"+
			"Recording nothing here is GitHub issue #938: the identity was terminated by this estate's own apply, its tags stay readable through the tagging API for a time afterwards, and with no entry the next plan reads that shadow as a second live claimant and refuses the address.", got, oldID)
	}
}

// TestWriteBackDeposedDestroyThatFailedAgainRecordsNothing is issue #901's
// suppression, one apply later: the recovery apply's plan scheduled the
// destroy and the destroy leg failed AGAIN, so the object is still deposed
// in the final state, still running and still billed.
//
// A plan verb says a destroy was scheduled, never that it ran - the exact
// distinction #901 exists for - so the final state's own deposed map is what
// settles it, and this must record nothing.
func TestWriteBackDeposedDestroyThatFailedAgainRecordsNothing(t *testing.T) {
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const oldID = "eipassoc-00112233445566778"
	const newID = "eipassoc-99887766554433221"
	const deposedKey = "deadbeef"

	located := newTestLocatedStore(localHintStore(t), "test-estate")

	version := supersedeApplyReplacing(t, located.rs, addr, oldID, "", nil)
	version = supersedeApplyDeposing(t, located.rs, addr, newID, deposedKey, oldID, version)

	// Planned, and still there afterwards.
	supersedeApplyDestroyingDeposed(t, located.rs, addr, newID,
		map[string]string{deposedKey: oldID}, []string{deposedKey}, version)

	if got := deposedIDs(t, located.rs, addr); len(got) != 1 || got[deposedKey] != oldID {
		t.Fatalf("the record holds deposed objects %v, want exactly {%q: %q}: the destroy failed, the object is alive, and the next apply's whole job is still to destroy it", got, deposedKey, oldID)
	}
	if got := tombstonedIDs(t, located.rs, addr); len(got) != 0 {
		t.Fatalf("a deposed destroy that failed again recorded %v as destroyed, want none: %q is deposed and still running, and GitHub issue #901 is that a scheduled destroy is not a performed one", got, oldID)
	}
}

// TestWriteBackDeposedObjectGoneWithNoPlannedDestroyRecordsNothing is the
// plan gate, and the control that keeps this mechanism from becoming
// "whatever left the deposed map is dead".
//
// [diffDeposedForWrite]'s own doc comment names both populations: a key that
// left ri.Deposed was destroyed by this apply "or by a human working around
// the estate". Only the plan tells them apart, and an entry written from the
// record alone would be the same statement about a possibly-live object that
// GitHub issue #854 removed from the current-identity side.
func TestWriteBackDeposedObjectGoneWithNoPlannedDestroyRecordsNothing(t *testing.T) {
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const oldID = "eipassoc-00112233445566778"
	const newID = "eipassoc-99887766554433221"
	const deposedKey = "deadbeef"

	located := newTestLocatedStore(localHintStore(t), "test-estate")

	version := supersedeApplyReplacing(t, located.rs, addr, oldID, "", nil)
	version = supersedeApplyDeposing(t, located.rs, addr, newID, deposedKey, oldID, version)

	// The object is out of the final state, and this run's plan scheduled
	// no destroy of it.
	supersedeApplyDestroyingDeposed(t, located.rs, addr, newID, nil, nil, version)

	if got := deposedIDs(t, located.rs, addr); len(got) != 0 {
		t.Errorf("the record still holds deposed objects %v; diffDeposedForWrite clears a key the final state no longer carries either way", got)
	}
	if got := tombstonedIDs(t, located.rs, addr); len(got) != 0 {
		t.Fatalf("a deposed object that left the final state with no planned destroy recorded %v as destroyed, want none: a human working around the estate produces exactly this record evidence, and %q may well still be running", got, oldID)
	}
}

// TestWriteBackDeposedDestroyRecordsOnlyTheKeyThePlanNamed keeps the entry
// keyed to the deposed key the plan actually named rather than to "this
// address had a deposed destroy in it".
//
// Two objects are deposed at one address - a second crash window opened
// before the first was closed - and the plan destroys one of them. The
// other is alive, and the direction this mechanism is allowed to fail in is
// recording nothing about it.
func TestWriteBackDeposedDestroyRecordsOnlyTheKeyThePlanNamed(t *testing.T) {
	ctx := context.Background()
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const firstID = "eipassoc-00112233445566778"
	const secondID = "eipassoc-11111111111111111"
	const newID = "eipassoc-99887766554433221"

	located := newTestLocatedStore(localHintStore(t), "test-estate")

	version := supersedeApplyReplacing(t, located.rs, addr, firstID, "", nil)

	// Both deposed objects recorded at once, through the same writer a
	// crashed apply goes through.
	encode := func(id string) *states.ResourceInstanceObjectSrc {
		t.Helper()
		obj := cty.ObjectVal(map[string]cty.Value{
			"id":            cty.StringVal(id),
			"allocation_id": cty.StringVal("eipalloc-declared"),
			"instance_id":   cty.StringVal("i-declared"),
		})
		src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: obj}).
			Encode(locatedTypeSchema().Block.ImpliedType(), 0, 0)
		if err != nil {
			t.Fatalf("encoding %s: %s", id, err)
		}
		return src
	}
	final := states.NewState()
	ms := final.EnsureModule(addrs.RootModuleInstance)
	ms.SetResourceInstanceCurrent(addr.Resource, encode(newID), locatedTestProvider, addrs.NoKey)
	ms.SetResourceInstanceDeposed(addr.Resource, states.DeposedKey("aaaa"), encode(firstID), locatedTestProvider, addrs.NoKey)
	ms.SetResourceInstanceDeposed(addr.Resource, states.DeposedKey("bbbb"), encode(secondID), locatedTestProvider, addrs.NoKey)
	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{locatedTestType: locatedTypeSchema()}},
	}}
	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:            located.rs,
		EnvelopeVersions: []RecordVersion{{Addr: addr, Version: version}},
		FinalState:       final,
		Schemas:          schemas,
		ReplacedAddrs:    []addrs.AbsResourceInstance{addr},
	}))
	_, version, _, _, err := located.rs.GetIdentity(ctx, addr)
	if err != nil {
		t.Fatalf("reading back the two-deposed record: %s", err)
	}

	// The recovery apply destroys "aaaa" only; "bbbb" is still deposed.
	supersedeApplyDestroyingDeposed(t, located.rs, addr, newID,
		map[string]string{"bbbb": secondID}, []string{"aaaa"}, version)

	if got := deposedIDs(t, located.rs, addr); len(got) != 1 || got["bbbb"] != secondID {
		t.Fatalf("the record holds deposed objects %v, want exactly {\"bbbb\": %q}", got, secondID)
	}
	got := tombstonedIDs(t, located.rs, addr)
	if len(got) != 1 || got[0] != firstID {
		t.Fatalf("the record names %v as destroyed, want exactly [%q]: only the key the plan destroyed, never the one still deposed", got, firstID)
	}
}
