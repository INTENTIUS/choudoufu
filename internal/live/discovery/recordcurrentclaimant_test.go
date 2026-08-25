// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"testing"

	"github.com/intentius/choudoufu/internal/live/projection"
)

// This file is [gauntlet:corpus-ec2-instance-complete/day2_remove]'s own
// unit (2026-08-25): a day2_replace under the DEFAULT destroy-then-create
// ordering (never create_before_destroy - see recordCurrentClaimant's own
// doc comment for why [matchDeposedClaimant]'s Deposed-record leg cannot
// help here) leaves the terminated instance's tags visible via the tagging
// API for a time after the apply that destroyed it - confirmed directly
// against the emulator with no tofu in the loop
// (`aws ec2 describe-tags --filters Name=resource-id,Values=<terminated-id>`
// still returned the estate's own tofu-address tag) and matching AWS's own
// documented tag-propagation delay for a terminated instance, not a floci
// gap. Once the block that owned the address is removed, both the stale
// terminated claimant and the real current one turn up as orphans of the
// SAME address, and classifyOrphans's tag-only collision guard could not
// tell them apart - "Two live resources claiming one address", forever,
// even though the estate's own record store already knew which one was
// current the whole time (day2_replace's own apply had already overwritten
// it).

// TestClassifyOrphans_RecordCurrentClaimantSurvivesStaleDuplicate is the
// headline case: two live aws_instance objects carry the same estate and
// the same now-undeclared address, and the estate's own current identity
// record names one of them by import ID. The one the record names must be
// the sole survivor - proposed for removal exactly as a single, unambiguous
// orphan always is - and the other must be left untouched, with no
// [ProblemCollision] raised at all.
func TestClassifyOrphans_RecordCurrentClaimantSurvivesStaleDuplicate(t *testing.T) {
	addr := mustAddr(t, "aws_instance.this")
	marker := EscapeAddress(addr.String())

	rawStore, seedStore := recordOrphanHintStore(t)
	if _, err := projection.SeedLocatedForInstance(t.Context(), seedStore, addr, recordOrphanProviderAddr, projection.LocatedRecord{
		ImportID: "i-current-running",
	}); err != nil {
		t.Fatalf("seeding the current-identity record: %s", err)
	}

	result := &Result{
		Orphans: []OwnedResource{
			{TypeName: "aws_instance", ImportID: "i-stale-terminated", Marker: marker, Normalized: marker, Swept: true},
			{TypeName: "aws_instance", ImportID: "i-current-running", Marker: marker, Normalized: marker, Swept: true},
		},
	}

	diags := classifyOrphans(context.Background(), Request{Estate: estateName, HintStore: rawStore}, listclient.Schemas{}, result)
	assertNoErrors(t, diags)

	var stale, current *OwnedResource
	for i := range result.Orphans {
		switch result.Orphans[i].ImportID {
		case "i-stale-terminated":
			stale = &result.Orphans[i]
		case "i-current-running":
			current = &result.Orphans[i]
		}
	}
	if stale == nil || current == nil {
		t.Fatalf("expected both orphans to survive classification unchanged in identity: %+v", result.Orphans)
	}

	if !current.Removal {
		t.Errorf("i-current-running (the id the record names) was not proposed for removal: %+v", current)
	}
	if current.Withheld != "" {
		t.Errorf("i-current-running carries a Withheld reason despite being proposed for removal: %q", current.Withheld)
	}

	if stale.Removal {
		t.Errorf("i-stale-terminated (not the id the record names) was proposed for removal - a wrong destroy could hit either the wrong object or nothing at all if AWS has already forgotten it: %+v", stale)
	}
	if stale.Withheld == "" {
		t.Errorf("i-stale-terminated carries no Withheld explanation for why it was not proposed")
	}
}

// TestClassifyOrphans_RecordCurrentClaimantFallsBackWhenRecordMatchesNeither
// is the safe default matchDeposedClaimant's own doc comment insists on:
// a record that matches NEITHER claimant is not evidence for anything, so
// this must raise the ordinary collision exactly as it would with no record
// at all, and propose destroying neither.
func TestClassifyOrphans_RecordCurrentClaimantFallsBackWhenRecordMatchesNeither(t *testing.T) {
	addr := mustAddr(t, "aws_instance.this")
	marker := EscapeAddress(addr.String())

	rawStore, seedStore := recordOrphanHintStore(t)
	if _, err := projection.SeedLocatedForInstance(t.Context(), seedStore, addr, recordOrphanProviderAddr, projection.LocatedRecord{
		ImportID: "i-neither-claimant",
	}); err != nil {
		t.Fatalf("seeding the current-identity record: %s", err)
	}

	result := &Result{
		Orphans: []OwnedResource{
			{TypeName: "aws_instance", ImportID: "i-one", Marker: marker, Normalized: marker, Swept: true},
			{TypeName: "aws_instance", ImportID: "i-two", Marker: marker, Normalized: marker, Swept: true},
		},
	}

	diags := classifyOrphans(context.Background(), Request{Estate: estateName, HintStore: rawStore}, listclient.Schemas{}, result)
	if !diags.HasErrors() {
		t.Fatalf("a record matching neither claimant silently resolved the collision instead of falling back to the safe default:\n%s", renderDiags(diags))
	}
	for _, o := range result.Orphans {
		if o.Removal {
			t.Errorf("%s was proposed for removal despite an unresolved collision (record matched neither claimant): %+v", o.ImportID, o)
		}
	}
}

// TestClassifyOrphans_RecordCurrentClaimantNoHintStoreFallsBack is the
// existing-behavior control: with no record store at all
// (Request.HintStore nil, the ordinary case for an estate with no live
// block), two claimants for one address must still raise the ordinary
// collision exactly as before this leg existed.
func TestClassifyOrphans_RecordCurrentClaimantNoHintStoreFallsBack(t *testing.T) {
	addr := mustAddr(t, "aws_instance.this")
	marker := EscapeAddress(addr.String())

	result := &Result{
		Orphans: []OwnedResource{
			{TypeName: "aws_instance", ImportID: "i-one", Marker: marker, Normalized: marker, Swept: true},
			{TypeName: "aws_instance", ImportID: "i-two", Marker: marker, Normalized: marker, Swept: true},
		},
	}

	diags := classifyOrphans(context.Background(), Request{Estate: estateName}, listclient.Schemas{}, result)
	if !diags.HasErrors() {
		t.Fatalf("a two-claimant collision with no HintStore at all was silently resolved:\n%s", renderDiags(diags))
	}
	for _, o := range result.Orphans {
		if o.Removal {
			t.Errorf("%s was proposed for removal with no record store to consult: %+v", o.ImportID, o)
		}
	}
}
