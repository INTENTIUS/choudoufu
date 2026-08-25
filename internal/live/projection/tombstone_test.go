// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"
)

// This file is [gauntlet:corpus-ec2-instance-complete/day2_remove]'s own
// unit (maintainer ruling 2026-08-25): [WriteBack] used to hard-delete an
// address's whole record the moment it left the final state, which erased
// the very identity [classifyOrphans]'s collision guard needed to tell a
// live object's own lingering tag (real AWS keeps a terminated instance's
// tags listable for a time - live/GAUNTLET's day2_remove unit reproduced
// this directly against the emulator, not a floci gap) apart from a
// genuine second live claimant. [RecordStore.tombstone] replaces that
// delete with a small "destroyed by us" entry instead, and this file pins
// the write side of that: what gets carried forward, what gets cleared,
// and - the non-negotiable this unit was built under - that a tombstone
// never resurrects as something an ordinary plan-time read treats as a
// live record.

// TestRecordEnvelopeRoundTripsTombstone is [TestRecordEnvelopeRoundTripsDeposed]'s
// exact twin for Tombstone: write an envelope carrying a Tombstone entry
// through the real mergeEnvelope/getRaw path, read it back through
// GetTombstones, and confirm a merge that clears every other member but
// leaves Tombstone populated does not delete the key.
func TestRecordEnvelopeRoundTripsTombstone(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("tombstone-estate"))
	addr := locatedTestAddr(t, "aws_instance", "web")

	if _, _, keyExists, err := store.GetTombstones(ctx, addr); err != nil || keyExists {
		t.Fatalf("GetTombstones before any write: keyExists=%v err=%v, want false/nil", keyExists, err)
	}

	version, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Tombstone = map[string]*tombstoneFields{
			"i-terminated0123": {
				Identity: &identityPayload{ImportID: "i-terminated0123"},
				Provider: `provider["registry.opentofu.org/hashicorp/aws"]`,
				Time:     "2026-08-25T00:00:00Z",
			},
		}
	})
	if err != nil {
		t.Fatalf("mergeEnvelope: %s", err)
	}
	if version == "" {
		t.Fatal("mergeEnvelope reported no version for a real write")
	}

	got, gotVersion, keyExists, err := store.GetTombstones(ctx, addr)
	if err != nil {
		t.Fatalf("GetTombstones: %s", err)
	}
	if !keyExists {
		t.Fatal("GetTombstones reports the key does not exist right after writing it")
	}
	if gotVersion != version {
		t.Errorf("GetTombstones version = %q, want %q", gotVersion, version)
	}
	if len(got) != 1 {
		t.Fatalf("GetTombstones returned %d entries, want 1: %#v", len(got), got)
	}
	rec, ok := got["i-terminated0123"]
	if !ok {
		t.Fatalf("GetTombstones did not return the %q key: %#v", "i-terminated0123", got)
	}
	if rec.ImportID != "i-terminated0123" {
		t.Errorf("ImportID = %q, want %q", rec.ImportID, "i-terminated0123")
	}
	if rec.Provider != `provider["registry.opentofu.org/hashicorp/aws"]` {
		t.Errorf("Provider = %q, want the recorded provider string", rec.Provider)
	}

	// isEmpty() must count Tombstone: a merge that clears every other
	// member but leaves Tombstone populated must NOT delete the key -
	// this is the entire point of the mechanism (see tombstoneFields's
	// own doc comment).
	if _, err := store.mergeEnvelope(ctx, addr, version, func(env *recordEnvelope) {
		// no-op mutate: everything rides through unchanged.
	}); err != nil {
		t.Fatalf("no-op mergeEnvelope: %s", err)
	}
	if _, _, keyExists, err := store.GetTombstones(ctx, addr); err != nil || !keyExists {
		t.Fatalf("the key was deleted despite a non-empty Tombstone member (keyExists=%v err=%v)", keyExists, err)
	}
}

// TestTombstoneCarriesForwardExistingIdentity is [RecordStore.tombstone]'s
// headline case: an address whose record names a CURRENT identity
// (env.Identity, exactly what an ordinary taggable or located instance's
// apply writes - the "issue #364 unit A2" identity write writeback.go's
// own comment describes) is tombstoned. The destroyed identity must be
// carried forward into the new Tombstone entry BY VALUE - both the import
// ID and the provider - and the current Identity member must be gone.
func TestTombstoneCarriesForwardExistingIdentity(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("tombstone-estate"))
	addr := locatedTestAddr(t, "aws_instance", "web")

	const wantID = "i-3af829c3949cb18d8"
	const wantProvider = `provider["registry.opentofu.org/hashicorp/aws"]`
	version, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: wantID}
		env.Provider = wantProvider
	})
	if err != nil {
		t.Fatalf("seeding the current identity: %s", err)
	}

	if err := store.tombstone(ctx, addr, version); err != nil {
		t.Fatalf("tombstone: %s", err)
	}

	tombstones, _, keyExists, err := store.GetTombstones(ctx, addr)
	if err != nil {
		t.Fatalf("GetTombstones: %s", err)
	}
	if !keyExists {
		t.Fatal("the key was deleted entirely instead of tombstoned")
	}
	if len(tombstones) != 1 {
		t.Fatalf("GetTombstones returned %d entries, want 1: %#v", len(tombstones), tombstones)
	}
	rec, ok := tombstones[wantID]
	if !ok {
		t.Fatalf("GetTombstones has no entry keyed %q: %#v", wantID, tombstones)
	}
	if rec.ImportID != wantID {
		t.Errorf("tombstoned ImportID = %q, want %q", rec.ImportID, wantID)
	}
	if rec.Provider != wantProvider {
		t.Errorf("tombstoned Provider = %q, want %q", rec.Provider, wantProvider)
	}
}

// TestTombstoneNeverResurrectsAsALiveRecord is this unit's own
// non-negotiable: the ORDINARY (non-collision) destroy path must leave
// nothing an ordinary plan-time read treats as a live record. Every
// reader that answers "is there a CURRENT record here" -
// GetIdentity, GetResidue, GetDeposed - must report exactly what they
// report for an address with no record at all, even though the physical
// key still exists (holding nothing but the tombstone). A tombstone that
// leaked into any of these would mean a destroyed instance's own address
// still looks occupied to the next plan.
func TestTombstoneNeverResurrectsAsALiveRecord(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("tombstone-estate"))
	addr := locatedTestAddr(t, "aws_instance", "web")

	version, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "i-destroyed-instance"}
		env.Provider = `provider["registry.opentofu.org/hashicorp/aws"]`
	})
	if err != nil {
		t.Fatalf("seeding the current identity: %s", err)
	}

	if err := store.tombstone(ctx, addr, version); err != nil {
		t.Fatalf("tombstone: %s", err)
	}

	// The tombstone itself must be there - proven by the sibling test
	// above and re-asserted here so a future change that broke BOTH
	// halves at once would still fail this file, not silently pass it.
	if _, _, keyExists, err := store.GetTombstones(ctx, addr); err != nil || !keyExists {
		t.Fatalf("GetTombstones: keyExists=%v err=%v, want true/nil", keyExists, err)
	}

	// But nothing that reads for "is this address a live record right
	// now" may see anything.
	if rec, gotVersion, keyExists, identityFound, err := store.GetIdentity(ctx, addr); err != nil || identityFound {
		t.Errorf("GetIdentity resurrected the destroyed identity: rec=%#v version=%q keyExists=%v identityFound=%v err=%v",
			rec, gotVersion, keyExists, identityFound, err)
	}
	if attrs, gotVersion, keyExists, residueFound, err := store.GetResidue(ctx, addr); err != nil || residueFound {
		t.Errorf("GetResidue found residue on a tombstoned address: attrs=%#v version=%q keyExists=%v residueFound=%v err=%v",
			attrs, gotVersion, keyExists, residueFound, err)
	}
	if deposed, gotVersion, keyExists, err := store.GetDeposed(ctx, addr); err != nil || len(deposed) != 0 {
		t.Errorf("GetDeposed found a deposed object on a tombstoned address: deposed=%#v version=%q keyExists=%v err=%v",
			deposed, gotVersion, keyExists, err)
	}
}

// TestTombstoneWithNoIdentityActsLikeDelete covers the OTHER ordinary
// destroy path: an address whose record never carried a current identity
// at all (a record-backed kind=object instance, whose identity concept
// lives in its Object member, which tombstone never carries forward - or
// an untaggable, unrecordable instance with only a Provisioned taint).
// [RecordStore.tombstone] must reduce to exactly what the plain [delete]
// it replaced did: the key is gone, not left behind holding an empty,
// pointless envelope.
func TestTombstoneWithNoIdentityActsLikeDelete(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("tombstone-estate"))
	addr := locatedTestAddr(t, "aws_instance", "web")

	version, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Provisioned = &provisionedFields{Tainted: true}
	})
	if err != nil {
		t.Fatalf("seeding a provisioned-only envelope: %s", err)
	}

	if err := store.tombstone(ctx, addr, version); err != nil {
		t.Fatalf("tombstone: %s", err)
	}

	if _, _, keyExists, err := store.GetTombstones(ctx, addr); err != nil || keyExists {
		t.Errorf("the key still exists after tombstoning an envelope with no identity: keyExists=%v err=%v, want false/nil", keyExists, err)
	}
}

// TestTombstoneAccumulatesAcrossSuccessiveDestroys is the day2_replace-
// then-day2_remove shape this whole mechanism was built for: the SAME
// address is tombstoned twice, once for each of two DIFFERENT identities
// it held in turn (a replace destroys the old instance, then the block is
// removed and the new one is destroyed too). Both must be recoverable
// afterward, keyed separately - overwriting the first with the second
// would leave the first destroyed instance's own lingering tag
// unrecognized by [classifyOrphans]'s collision guard.
func TestTombstoneAccumulatesAcrossSuccessiveDestroys(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("tombstone-estate"))
	addr := locatedTestAddr(t, "aws_instance", "web")

	v1, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "i-old-replaced-away"}
	})
	if err != nil {
		t.Fatalf("seeding the first identity: %s", err)
	}
	if err := store.tombstone(ctx, addr, v1); err != nil {
		t.Fatalf("first tombstone: %s", err)
	}

	_, v2, _, err := store.GetTombstones(ctx, addr)
	if err != nil {
		t.Fatalf("reading the version after the first tombstone: %s", err)
	}
	if _, err := store.mergeEnvelope(ctx, addr, v2, func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "i-new-then-removed"}
	}); err != nil {
		t.Fatalf("seeding the second identity: %s", err)
	}
	_, v3, _, _, err := store.GetIdentity(ctx, addr)
	if err != nil {
		t.Fatalf("reading the version after seeding the second identity: %s", err)
	}
	if err := store.tombstone(ctx, addr, v3); err != nil {
		t.Fatalf("second tombstone: %s", err)
	}

	tombstones, _, keyExists, err := store.GetTombstones(ctx, addr)
	if err != nil {
		t.Fatalf("GetTombstones: %s", err)
	}
	if !keyExists {
		t.Fatal("the key was deleted after two successive tombstones")
	}
	if len(tombstones) != 2 {
		t.Fatalf("GetTombstones returned %d entries, want 2 (one per destroyed identity): %#v", len(tombstones), tombstones)
	}
	if _, ok := tombstones["i-old-replaced-away"]; !ok {
		t.Errorf("the first destroyed identity is missing: %#v", tombstones)
	}
	if _, ok := tombstones["i-new-then-removed"]; !ok {
		t.Errorf("the second destroyed identity is missing: %#v", tombstones)
	}
}
