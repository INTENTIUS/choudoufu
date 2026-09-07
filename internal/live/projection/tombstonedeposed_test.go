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

// This file is GitHub issue #901: the one case GitHub issue #854 named and
// left, on the write side.
//
// A `create_before_destroy` replace commits its create, deposes the old
// object, and then FAILS to destroy it. The apply's own final state is then
// the create-before-destroy shape with the destroy leg still outstanding:
// Current names the new object, and ri.Deposed still holds the old one,
// alive, running and billed. The planned action for the address was
// CreateThenDelete either way, so it is in [WriteBackRequest.ReplacedAddrs],
// and #854's plan-derived gate admits it.
//
// The record's identity really did move, and this really was a replace, so
// both of [supersedeIdentity]'s two facts hold - and the conclusion they
// license, "this estate's apply destroyed that identity", is false. The
// deposed object is the third state neither fact distinguishes: superseded
// at the address, not destroyed in the cloud.
//
// Everything is asserted through [RecordStore.GetTombstones],
// [RecordStore.GetDeposed] and [RecordStore.GetIdentity] by value, the same
// discipline supersedeidentity_test.go states: what the read half
// ([discovery.pruneSupersededEntry]) can see is the record, and nothing
// else.

// supersedeApplyDeposing runs one apply's write-back for the located test
// type at addr with the state a create_before_destroy replace whose destroy
// leg FAILED leaves behind: currentID as the object the create committed,
// and deposedID still sitting in ri.Deposed under deposedKey. The plan
// scheduled a replace for this address, which is what [supersedeIdentity]
// requires (GitHub issue #854) - the whole point of this shape is that the
// #854 gate is satisfied and is not enough.
//
// It is [supersedeApplyReplacing]'s body with the deposed object added, and
// returns the envelope version it wrote.
func supersedeApplyDeposing(t *testing.T, rs *RecordStore, addr addrs.AbsResourceInstance, currentID, deposedKey, deposedID, version string) string {
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
	ms.SetResourceInstanceDeposed(addr.Resource, states.DeposedKey(deposedKey), encode(deposedID), locatedTestProvider, addrs.NoKey)

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{locatedTestType: locatedTypeSchema()}},
	}}

	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:            rs,
		EnvelopeVersions: []RecordVersion{{Addr: addr, Version: version}},
		FinalState:       final,
		Schemas:          schemas,
		ReplacedAddrs:    []addrs.AbsResourceInstance{addr},
	}))

	_, next, _, exists, err := rs.GetIdentity(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("reading back the record written for %s: exists=%v err=%v", addr, exists, err)
	}
	return next
}

// deposedIDs is every deposed identity the store records for addr, by
// deposed key, so a test asserts on what the read half's own
// deposedCandidates would find rather than on a count.
func deposedIDs(t *testing.T, rs *RecordStore, addr addrs.AbsResourceInstance) map[string]string {
	t.Helper()
	deposed, _, _, err := rs.GetDeposed(context.Background(), addr)
	if err != nil {
		t.Fatalf("GetDeposed: %s", err)
	}
	out := make(map[string]string, len(deposed))
	for dk, rec := range deposed {
		out[dk] = rec.ImportID
	}
	return out
}

// TestWriteBackReplaceWithAFailedDestroyLegTombstonesNothing is issue
// #901's headline. The address's record must come out of this apply saying
// two things and not a third: it owns eipassoc-new now, eipassoc-old is
// deposed at it - and NOTHING is recorded as destroyed, because nothing
// was.
//
// The tombstone's own meaning is "this estate's apply destroyed this
// identity" ([supersedeIdentity]). Writing one here states that about a
// live object, and the only thing standing between that statement and a
// live claimant being pruned out of a collision is the ORDER of two legs in
// [discovery.pruneSupersededEntry], one file over.
func TestWriteBackReplaceWithAFailedDestroyLegTombstonesNothing(t *testing.T) {
	ctx := context.Background()
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const oldID = "eipassoc-00112233445566778"
	const newID = "eipassoc-99887766554433221"
	const deposedKey = "deadbeef"

	located := newTestLocatedStore(localHintStore(t), "test-estate")

	// The apply that first recorded the address. A create replaces nothing.
	version := supersedeApplyReplacing(t, located.rs, addr, oldID, "", nil)

	// The create_before_destroy replace whose destroy leg failed.
	supersedeApplyDeposing(t, located.rs, addr, newID, deposedKey, oldID, version)

	rec, _, _, exists, err := located.rs.GetIdentity(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("reading the current identity after the replace: exists=%v err=%v", exists, err)
	}
	if rec.ImportID != newID {
		t.Errorf("the address's current identity is %q, want the object the create committed, %q", rec.ImportID, newID)
	}

	// (b) The deposed object is still recorded as a deposed object, which
	// is how [discovery.pruneSupersededEntry] knows to keep it as a live
	// claimant rather than pruning it as a tag shadow, and how
	// matchDeposedClaimant finds it to plan its destroy. A fix that
	// suppressed the tombstone by dropping the deposed entry would satisfy
	// the assertion below and lose the object.
	if got := deposedIDs(t, located.rs, addr); len(got) != 1 || got[deposedKey] != oldID {
		t.Fatalf("the record holds deposed objects %v, want exactly {%q: %q} - the object whose destroy failed is alive and the next apply's whole job is to destroy it", got, deposedKey, oldID)
	}

	// (a) The headline.
	if got := tombstonedIDs(t, located.rs, addr); len(got) != 0 {
		t.Fatalf("a create_before_destroy replace whose destroy leg failed recorded %v as destroyed, want none. %q is deposed and still running: an entry naming it says this estate's apply destroyed a live object, and the only thing keeping that out of an operator's report is which leg of pruneSupersededEntry runs first.", got, oldID)
	}
}

// TestWriteBackReplaceStillTombstonesWhenTheDeposedObjectIsSomeoneElse is
// the control that keeps issue #901's suppression about THE IDENTITY BEING
// SUPERSEDED rather than about the address having any deposed object at
// all.
//
// The address carries a deposed object left over from an earlier crash
// window (eipassoc-stale), and this apply replaces eipassoc-old with
// eipassoc-new. eipassoc-old really was destroyed, and its shadow's tags
// outlive it, so the entry that lets the next plan tell that shadow from a
// live duplicate must still be written.
func TestWriteBackReplaceStillTombstonesWhenTheDeposedObjectIsSomeoneElse(t *testing.T) {
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const oldID = "eipassoc-00112233445566778"
	const newID = "eipassoc-99887766554433221"
	const staleID = "eipassoc-55555555555555555"

	located := newTestLocatedStore(localHintStore(t), "test-estate")

	version := supersedeApplyReplacing(t, located.rs, addr, oldID, "", nil)
	supersedeApplyDeposing(t, located.rs, addr, newID, "cafef00d", staleID, version)

	got := tombstonedIDs(t, located.rs, addr)
	if len(got) != 1 || got[0] != oldID {
		t.Fatalf("a replace at an address carrying an unrelated deposed object recorded %v as destroyed, want exactly [%q]: the destroyed object's lingering tag is otherwise indistinguishable from a second live resource", got, oldID)
	}
}

// TestWriteBackReplaceSuppressesTheTombstoneForAnUnreadableDeposedObject
// pins the direction the suppression fails in when the deposed object's
// identity cannot be rendered at all.
//
// [diffDeposedForWrite] already documents that population: a deposed object
// [LocatedRecordFrom] cannot render an identity for still gets an entry,
// Provider alone. For the tombstone question that object is not "someone
// else" - it is unproven, and proving a live object dead is the direction
// this mechanism is not allowed to guess in ([supersedeIdentity]: "Writing
// no entry is the strictly louder direction"). The cost of suppressing is
// one refusal the next plan makes anyway on the deposed object's own
// account.
func TestWriteBackReplaceSuppressesTheTombstoneForAnUnreadableDeposedObject(t *testing.T) {
	ctx := context.Background()
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const oldID = "eipassoc-00112233445566778"
	const newID = "eipassoc-99887766554433221"

	located := newTestLocatedStore(localHintStore(t), "test-estate")
	version := supersedeApplyReplacing(t, located.rs, addr, oldID, "", nil)

	encode := func(v cty.Value) *states.ResourceInstanceObjectSrc {
		src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: v}).
			Encode(locatedTypeSchema().Block.ImpliedType(), 0, 0)
		if err != nil {
			t.Fatalf("encoding: %s", err)
		}
		return src
	}
	current := cty.ObjectVal(map[string]cty.Value{
		"id":            cty.StringVal(newID),
		"allocation_id": cty.StringVal("eipalloc-declared"),
		"instance_id":   cty.StringVal("i-declared"),
	})
	// A deposed object with no "id" at all: nothing to render an identity
	// from, so nothing to compare against the identity being superseded.
	unreadable := cty.ObjectVal(map[string]cty.Value{
		"id":            cty.NullVal(cty.String),
		"allocation_id": cty.StringVal("eipalloc-declared"),
		"instance_id":   cty.StringVal("i-declared"),
	})

	final := states.NewState()
	ms := final.EnsureModule(addrs.RootModuleInstance)
	ms.SetResourceInstanceCurrent(addr.Resource, encode(current), locatedTestProvider, addrs.NoKey)
	ms.SetResourceInstanceDeposed(addr.Resource, states.DeposedKey("deadbeef"), encode(unreadable), locatedTestProvider, addrs.NoKey)

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

	if got := tombstonedIDs(t, located.rs, addr); len(got) != 0 {
		t.Fatalf("a replace at an address carrying a deposed object whose identity could not be rendered recorded %v as destroyed, want none: that deposed object may BE %q, and nothing here can tell", got, oldID)
	}
}
