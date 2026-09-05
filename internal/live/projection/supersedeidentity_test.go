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
// carrying, and returns the envelope version it wrote. It is
// TestWriteBackLocatedRoundTrip's own body, parameterized so a test can run
// several applies in a row against one store the way an estate does.
func supersedeApply(t *testing.T, rs *RecordStore, addr addrs.AbsResourceInstance, appliedID, version string) string {
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
