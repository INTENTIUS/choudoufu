// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// This file is [recordcurrentclaimant_test.go]'s own sibling for the case
// its own headline test cannot reach: a day2_remove (the block removed
// entirely, never replaced) leaves the address's record with no CURRENT
// identity at all, because [projection.RecordStore.tombstone] - not a
// plain delete - is what an ordinary destroy write-back now does (see
// that method's own doc comment; maintainer ruling 2026-08-25,
// gauntlet:corpus-ec2-instance-complete's own day2_remove unit). No current
// identity means [recordCurrentClaimant] returns ok=false exactly as it
// always has for an address with no record - the disambiguation here comes
// from [tombstoneGhostIndices] instead, consulted only when
// recordCurrentClaimant already gave up.

// TestClassifyOrphans_TombstoneGhostExcludedLeavesSoleSurvivor is the
// headline case this unit exists for: two live aws_instance objects carry
// the same estate and the same now-undeclared address. The record names no
// CURRENT identity (day2_remove's own shape - the address was removed, not
// replaced), but its Tombstone member names one of the two claimants as an
// identity THIS estate has already destroyed. That one must be excluded as
// a live claimant entirely (Withheld, not Removal), and the OTHER one -
// the genuine survivor, once the ghost is filtered out - must be the sole
// candidate proposed for removal, with no [ProblemCollision] raised.
func TestClassifyOrphans_TombstoneGhostExcludedLeavesSoleSurvivor(t *testing.T) {
	addr := mustAddr(t, "aws_instance.this")
	marker := EscapeAddress(addr.String())

	rawStore, seedStore := recordOrphanHintStore(t)
	if err := projection.SeedTombstoneForInstance(t.Context(), seedStore, addr, projection.TombstoneRecord{
		ImportID: "i-terminated-ghost",
	}); err != nil {
		t.Fatalf("seeding the tombstone: %s", err)
	}

	result := &Result{Verdicts: Verdicts{Orphans: []OwnedResource{
		{TypeName: "aws_instance", ImportID: "i-terminated-ghost", Marker: marker, Normalized: marker, Swept: true},
		{TypeName: "aws_instance", ImportID: "i-genuinely-live", Marker: marker, Normalized: marker, Swept: true},
	}}}

	diags := classifyOrphans(context.Background(), Request{Estate: estateName, HintStore: rawStore}, listclient.Schemas{}, result)
	assertNoErrors(t, diags)

	var ghost, survivor *OwnedResource
	for i := range result.Orphans {
		switch result.Orphans[i].ImportID {
		case "i-terminated-ghost":
			ghost = &result.Orphans[i]
		case "i-genuinely-live":
			survivor = &result.Orphans[i]
		}
	}
	if ghost == nil || survivor == nil {
		t.Fatalf("expected both orphans to survive classification unchanged in identity: %+v", result.Orphans)
	}

	if !survivor.Removal {
		t.Errorf("i-genuinely-live (the sole non-ghost claimant) was not proposed for removal: %+v", survivor)
	}
	if survivor.Withheld != "" {
		t.Errorf("i-genuinely-live carries a Withheld reason despite being proposed for removal: %q", survivor.Withheld)
	}

	if ghost.Removal {
		t.Errorf("i-terminated-ghost (a known-tombstoned identity) was proposed for removal - it is already gone, and a wrong destroy could hit an object AWS has recycled the id for: %+v", ghost)
	}
	if ghost.Withheld == "" {
		t.Errorf("i-terminated-ghost carries no Withheld explanation for why it was excluded")
	}
}

// TestClassifyOrphans_AllTombstonedClaimantsProposeNothing covers the
// other shape: every claimant of the address matches a tombstone entry (a
// day2_replace's old occupant AND the day2_remove that later destroyed its
// replacement, both still tag-visible at once). Nothing about this address
// is a collision and nothing is a survivor either - both are Withheld, and
// [ProblemCollision] is never raised for an address that is, in reality,
// already fully gone.
func TestClassifyOrphans_AllTombstonedClaimantsProposeNothing(t *testing.T) {
	addr := mustAddr(t, "aws_instance.this")
	marker := EscapeAddress(addr.String())

	rawStore, seedStore := recordOrphanHintStore(t)
	if err := projection.SeedTombstoneForInstance(t.Context(), seedStore, addr, projection.TombstoneRecord{
		ImportID: "i-old-replaced-away",
	}); err != nil {
		t.Fatalf("seeding the first tombstone: %s", err)
	}
	if err := projection.SeedTombstoneForInstance(t.Context(), seedStore, addr, projection.TombstoneRecord{
		ImportID: "i-new-then-removed",
	}); err != nil {
		t.Fatalf("seeding the second tombstone: %s", err)
	}

	result := &Result{Verdicts: Verdicts{Orphans: []OwnedResource{
		{TypeName: "aws_instance", ImportID: "i-old-replaced-away", Marker: marker, Normalized: marker, Swept: true},
		{TypeName: "aws_instance", ImportID: "i-new-then-removed", Marker: marker, Normalized: marker, Swept: true},
	}}}

	diags := classifyOrphans(context.Background(), Request{Estate: estateName, HintStore: rawStore}, listclient.Schemas{}, result)
	assertNoErrors(t, diags)

	for _, o := range result.Orphans {
		if o.Removal {
			t.Errorf("%s was proposed for removal despite matching a tombstone: %+v", o.ImportID, o)
		}
		if o.Withheld == "" {
			t.Errorf("%s carries no Withheld explanation for why nothing was proposed", o.ImportID)
		}
	}
}

// TestClassifyOrphans_TombstoneMatchesNeitherFallsBackToCollision is the
// safe-default twin [TestClassifyOrphans_RecordCurrentClaimantFallsBackWhenRecordMatchesNeither]
// already pins for a CURRENT record: a tombstone that matches NEITHER
// claimant is not evidence for anything, so a genuinely foreign object
// sharing this address's marker with a real live claimant must still raise
// the ordinary collision - a tombstone existing at all must never be read
// as "assume everything else here is fine".
func TestClassifyOrphans_TombstoneMatchesNeitherFallsBackToCollision(t *testing.T) {
	addr := mustAddr(t, "aws_instance.this")
	marker := EscapeAddress(addr.String())

	rawStore, seedStore := recordOrphanHintStore(t)
	if err := projection.SeedTombstoneForInstance(t.Context(), seedStore, addr, projection.TombstoneRecord{
		ImportID: "i-some-unrelated-destroyed-instance",
	}); err != nil {
		t.Fatalf("seeding the tombstone: %s", err)
	}

	result := &Result{Verdicts: Verdicts{Orphans: []OwnedResource{
		{TypeName: "aws_instance", ImportID: "i-one", Marker: marker, Normalized: marker, Swept: true},
		{TypeName: "aws_instance", ImportID: "i-two", Marker: marker, Normalized: marker, Swept: true},
	}}}

	diags := classifyOrphans(context.Background(), Request{Estate: estateName, HintStore: rawStore}, listclient.Schemas{}, result)
	if !diags.HasErrors() {
		t.Fatalf("a tombstone matching neither claimant silently resolved the collision instead of falling back to the safe default:\n%s", renderDiags(diags))
	}
	for _, o := range result.Orphans {
		if o.Removal {
			t.Errorf("%s was proposed for removal despite an unresolved collision (tombstone matched neither claimant): %+v", o.ImportID, o)
		}
	}
}
