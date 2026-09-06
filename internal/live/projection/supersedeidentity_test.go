// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is GitHub issue #670's write half: a replace destroys the
// object an address's record names and creates another at the SAME address,
// so the address never leaves the final state and
// [RecordStore.tombstone] - which only runs for an address that did - never
// sees it. Without an entry, the object the apply destroyed is merely
// unrecorded, and the next plan cannot tell its lingering tag from a
// second, genuinely live claimant carrying the same marker.
//
// Every assertion here is on a rendered identity read back through
// [RecordStore.GetTombstones] and [RecordStore.GetIdentity], never on a
// predicate: the value in the record is the whole of what the read half
// ([discovery.pruneSupersededEntry]) has to work from.

// supersedeApply runs one apply's write-back for the located test type at
// addr, with appliedID as the identity the object came out of the apply
// carrying, and returns the envelope version it wrote. The apply's plan
// scheduled a replace FOR THIS ADDRESS, which is what the write side now
// requires before it records anything as destroyed (GitHub issue #854).
// It is TestWriteBackLocatedRoundTrip's own body, parameterized so a test
// can run several applies in a row against one store the way an estate
// does.
func supersedeApply(t *testing.T, rs *RecordStore, addr addrs.AbsResourceInstance, appliedID, version string) string {
	t.Helper()
	return supersedeApplyReplacing(t, rs, addr, appliedID, version, []addrs.AbsResourceInstance{addr})
}

// supersedeApplyReplacing is [supersedeApply] with the plan's own replace
// set spelled out: exactly the addresses whose planned action was
// DeleteThenCreate or CreateThenDelete, which is what
// [WriteBackRequest.ReplacedAddrs] carries and what backend/local's
// replacedInstances derives from the plan. Passing nil is an apply that
// replaced nothing - an import block, a live-mv onto an address that
// already held a record, or an ordinary create-and-forget - and it is the
// distinction GitHub issue #854 exists for.
func supersedeApplyReplacing(t *testing.T, rs *RecordStore, addr addrs.AbsResourceInstance, appliedID, version string, replaced []addrs.AbsResourceInstance) string {
	t.Helper()
	ctx := context.Background()

	final := states.NewState()
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":            cty.StringVal(appliedID),
		"allocation_id": cty.StringVal("eipalloc-declared"),
		"instance_id":   cty.StringVal("i-declared"),
	})
	src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: applied}).
		Encode(locatedTypeSchema().Block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding the applied object: %s", err)
	}
	final.EnsureModule(addrs.RootModuleInstance).
		SetResourceInstanceCurrent(addr.Resource, src, locatedTestProvider, addrs.NoKey)

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{locatedTestType: locatedTypeSchema()}},
	}}

	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:            rs,
		EnvelopeVersions: []RecordVersion{{Addr: addr, Version: version}},
		FinalState:       final,
		Schemas:          schemas,
		ReplacedAddrs:    replaced,
	}))

	_, next, _, exists, err := rs.GetIdentity(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("reading back the record written for %s: exists=%v err=%v", addr, exists, err)
	}
	return next
}

// tombstonedIDs is every destroyed identity the store records for addr, by
// value and in a stable order, so a test asserts on what an operator (and
// the read half) would actually find rather than on a count.
func tombstonedIDs(t *testing.T, rs *RecordStore, addr addrs.AbsResourceInstance) []string {
	t.Helper()
	tombstones, _, _, err := rs.GetTombstones(context.Background(), addr)
	if err != nil {
		t.Fatalf("GetTombstones: %s", err)
	}
	out := make([]string, 0, len(tombstones))
	for _, rec := range tombstones {
		out = append(out, rec.ImportID)
	}
	sort.Strings(out)
	return out
}

// TestWriteBackReplaceTombstonesTheDestroyedIdentity is the headline, and
// corpus-ec2-instance-complete's day2_replace in miniature. One apply
// records eipassoc-old; the next apply of the SAME address comes out
// carrying eipassoc-new, which is what a destroy-then-create replace leaves
// in the final state. The record must then say two things at once: the
// address owns eipassoc-new now, and this estate destroyed eipassoc-old.
//
// Both are asserted by value. Recording only the first is the state before
// this issue, and it is what let a live duplicate be pruned as though it
// were a terminated shadow.
func TestWriteBackReplaceTombstonesTheDestroyedIdentity(t *testing.T) {
	ctx := context.Background()
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const estate = "test-estate"
	const oldID = "eipassoc-00112233445566778"
	const newID = "eipassoc-99887766554433221"

	located := newTestLocatedStore(localHintStore(t), estate)

	version := supersedeApply(t, located.rs, addr, oldID, "")
	if got := tombstonedIDs(t, located.rs, addr); len(got) != 0 {
		t.Fatalf("the FIRST apply of an address tombstoned %v; a create destroys nothing and must record nothing as destroyed", got)
	}

	supersedeApply(t, located.rs, addr, newID, version)

	rec, _, _, exists, err := located.rs.GetIdentity(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("reading the current identity after the replace: exists=%v err=%v", exists, err)
	}
	if rec.ImportID != newID {
		t.Errorf("the address's current identity is %q, want the object the replace created, %q", rec.ImportID, newID)
	}

	got := tombstonedIDs(t, located.rs, addr)
	if len(got) != 1 || got[0] != oldID {
		t.Fatalf("the replace recorded %v as destroyed, want exactly [%q]. With nothing recorded, the destroyed object's lingering tag is indistinguishable from a second live resource and the next plan either refuses forever or prunes a live object.", got, oldID)
	}

	tombstones, _, _, err := located.rs.GetTombstones(ctx, addr)
	if err != nil {
		t.Fatalf("GetTombstones: %s", err)
	}
	if rec := tombstones[oldID]; rec.Provider != locatedTestProvider.String() {
		t.Errorf("the tombstone for %s names provider %q, want the provider that managed the destroyed object, %q", oldID, rec.Provider, locatedTestProvider.String())
	}
}

// TestWriteBackUnchangedIdentityTombstonesNothing is the control that makes
// the rule "the recorded identity was replaced", not "an apply happened".
// A no-op apply, and every ordinary update, re-writes the SAME identity;
// recording that as a destruction would put every address's own live object
// into its own tombstone list, which the read half would then prune out of
// any collision it turned up in - the exact failure this issue is fixing,
// inverted.
func TestWriteBackUnchangedIdentityTombstonesNothing(t *testing.T) {
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const sameID = "eipassoc-00112233445566778"

	located := newTestLocatedStore(localHintStore(t), "test-estate")

	version := supersedeApply(t, located.rs, addr, sameID, "")
	supersedeApply(t, located.rs, addr, sameID, version)

	if got := tombstonedIDs(t, located.rs, addr); len(got) != 0 {
		t.Fatalf("re-applying the same object recorded %v as destroyed; the object the address owns right now would then be pruned out of any collision it appears in", got)
	}
}

// TestWriteBackReplaceTombstonesAreBounded pins
// [maxTombstonesPerAddress]. Before this issue an address accumulated one
// tombstone in its whole life, when its last occupant was destroyed; a
// replace can now happen on every apply forever, and the list rides in the
// envelope every plan reads for that instance. The bound is what keeps that
// cheap, and it must evict the OLDEST - the entries whose objects the cloud
// stopped listing several replaces ago - never the newest, which are the
// only ones that can still have a tag shadow in the air.
func TestWriteBackReplaceTombstonesAreBounded(t *testing.T) {
	addr := mustAddr(t, locatedTestType+`.bastion`)
	located := newTestLocatedStore(localHintStore(t), "test-estate")

	// One clock tick per apply, so "oldest" is a fact about the recorded
	// Time rather than about map iteration order. tombstoneClock is the
	// seam tombstoneFields.Time's own doc comment promises nothing reads
	// back to decide anything.
	base := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	tick := 0
	restore := tombstoneClock
	tombstoneClock = func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * time.Minute)
	}
	t.Cleanup(func() { tombstoneClock = restore })

	const applies = maxTombstonesPerAddress + 4
	version := ""
	for i := 0; i < applies; i++ {
		version = supersedeApply(t, located.rs, addr, fmt.Sprintf("eipassoc-%02d", i), version)
	}

	got := tombstonedIDs(t, located.rs, addr)
	// applies-1 destroyed objects, capped at maxTombstonesPerAddress, and
	// the ones kept are the last ones destroyed. Asserted by value: a cap
	// that evicted the newest would be worse than no cap at all.
	want := make([]string, 0, maxTombstonesPerAddress)
	for i := applies - 1 - maxTombstonesPerAddress; i < applies-1; i++ {
		want = append(want, fmt.Sprintf("eipassoc-%02d", i))
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("after %d applies the record holds %v as destroyed, want %v", applies, got, want)
	}

	rec, _, _, exists, err := located.rs.GetIdentity(context.Background(), addr)
	if err != nil || !exists {
		t.Fatalf("reading the current identity: exists=%v err=%v", exists, err)
	}
	if want := fmt.Sprintf("eipassoc-%02d", applies-1); rec.ImportID != want {
		t.Errorf("the current identity is %q, want %q - the live object must never be evicted into, or confused with, the tombstone list", rec.ImportID, want)
	}
}

// TestWriteBackImportOfADifferentObjectTombstonesNothing is GitHub issue
// #854's headline, and the case the record alone cannot see.
//
// The record for an address names eipassoc-old. An `import` block then
// points that same address at eipassoc-imported - a SECOND, GENUINELY LIVE
// object - and the apply that follows re-records the address accordingly.
// The record evidence is byte for byte what a replace leaves behind: the
// identity changed, and the address is still in the final state. The one
// thing that differs is the plan, which scheduled no replace, and therefore
// no destroy.
//
// Nothing may be recorded as destroyed here. eipassoc-old is running, and an
// entry naming it would be read back by
// [discovery.pruneSupersededEntry], which would drop it out of any collision
// it turns up in and describe it, in the operator's own report, as
// "destroyed by an earlier apply of this estate".
func TestWriteBackImportOfADifferentObjectTombstonesNothing(t *testing.T) {
	ctx := context.Background()
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const oldID = "eipassoc-00112233445566778"
	const importedID = "eipassoc-44556677889900112"

	located := newTestLocatedStore(localHintStore(t), "test-estate")

	// The apply that first recorded the address. A create replaces nothing.
	version := supersedeApplyReplacing(t, located.rs, addr, oldID, "", nil)

	// The import. The plan's replace set is empty: an `import` block plans
	// a Create or a NoOp carrying an Importing side, never a replace.
	supersedeApplyReplacing(t, located.rs, addr, importedID, version, nil)

	rec, _, _, exists, err := located.rs.GetIdentity(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("reading the current identity after the import: exists=%v err=%v", exists, err)
	}
	if rec.ImportID != importedID {
		t.Errorf("the address's current identity is %q, want the imported object %q - the import must still re-point the record", rec.ImportID, importedID)
	}

	if got := tombstonedIDs(t, located.rs, addr); len(got) != 0 {
		t.Fatalf("an import at an address that already held a record recorded %v as destroyed, want none. %q is alive: the next plan would drop it out of the collision it belongs in and tell the operator this estate destroyed it.", got, oldID)
	}
}

// TestWriteBackMvOntoAnOccupiedAddressTombstonesNothing is the same defect
// on the `live-mv` path, and it pins the gate as PER ADDRESS rather than per
// run.
//
// The estate's apply really does replace something - aws_eip.other - so the
// plan's replace set is not empty. The address under test is not in it: its
// record changed hands because a live-mv brought another object's marker to
// it while the object it used to name kept running. An implementation that
// asked "did this run replace anything" rather than "was THIS address
// replaced" would record eipassoc-previous as destroyed here.
func TestWriteBackMvOntoAnOccupiedAddressTombstonesNothing(t *testing.T) {
	addr := mustAddr(t, locatedTestType+`.bastion`)
	other := mustAddr(t, locatedTestType+`.other`)
	const previousID = "eipassoc-11223344556677889"
	const movedID = "eipassoc-99887766554433221"

	located := newTestLocatedStore(localHintStore(t), "test-estate")

	version := supersedeApplyReplacing(t, located.rs, addr, previousID, "", nil)
	// A replace happened in this apply, at a DIFFERENT address.
	supersedeApplyReplacing(t, located.rs, addr, movedID, version, []addrs.AbsResourceInstance{other})

	if got := tombstonedIDs(t, located.rs, addr); len(got) != 0 {
		t.Fatalf("a live-mv onto an address that already held a record recorded %v as destroyed while the run's only replace was at %s, want none recorded at %s", got, other, addr)
	}
}

// TestWriteBackReplaceWithNoPlanSignalTombstonesNothing pins the direction
// this fails in when the signal is absent rather than false.
//
// A caller that hands [WriteBack] no replace set at all gets no tombstone,
// even for an apply that really did replace the object. That is the loud
// direction: with nothing recorded, the next plan keeps the extra claimant
// and refuses the address, exactly as it did before tombstones existed. The
// opposite default - "no signal, so fall back to the record" - is the bug
// this issue is fixing, and it would silently reappear for any future caller
// that forgot the field.
func TestWriteBackReplaceWithNoPlanSignalTombstonesNothing(t *testing.T) {
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const oldID = "eipassoc-00112233445566778"
	const newID = "eipassoc-99887766554433221"

	located := newTestLocatedStore(localHintStore(t), "test-estate")

	version := supersedeApplyReplacing(t, located.rs, addr, oldID, "", nil)
	supersedeApplyReplacing(t, located.rs, addr, newID, version, nil)

	if got := tombstonedIDs(t, located.rs, addr); len(got) != 0 {
		t.Fatalf("a write-back with no plan replace set recorded %v as destroyed; absence of the signal must refuse, never assume", got)
	}
}
