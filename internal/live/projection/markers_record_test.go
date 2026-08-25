// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #365, slice 2. An operator's `markers "record"` selection is
// the first way a TAGGABLE instance reaches this package with a record-held
// identity, and both halves of this file are failures that were reachable
// the moment that became possible - each of them silent, with clean plan
// verdicts throughout.
//
// The type here is aws_ebs_volume, and naming it is allowed for the reason
// located_build_test.go states for its own: a test may name a type where
// production control flow may not. What matters about it is that it is
// taggable and its exported `id` is proven whole, which is exactly the
// population the selection exists for.

const markersRecordTestType = "aws_ebs_volume"

// markersRecordTypeSchema is locatedTypeSchema's taggable twin. The tags map
// is the whole difference and it is the whole point: with it,
// markerCapable(schema.Block) is TRUE, so checkOwnership no longer
// short-circuits and every ownership question this file asks becomes live.
func markersRecordTypeSchema() providers.Schema {
	return providers.Schema{
		Version: 0,
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":                {Type: cty.String, Computed: true},
				"availability_zone": {Type: cty.String, Optional: true},
				"size":              {Type: cty.Number, Optional: true},
				"tags":              {Type: cty.Map(cty.String), Optional: true},
			},
		},
	}
}

// markersRecordProvider answers an import by ID with an object carrying NO
// tags at all - which is what a selected resource's live object looks like,
// because internal/live/stamp never wrote a marker into it.
func markersRecordProvider(importedIDs *[]string) providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{markersRecordTestType: markersRecordTypeSchema()},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		if importedIDs != nil {
			*importedIDs = append(*importedIDs, req.Target.ID)
		}
		return providers.ImportResourceStateResponse{
			ImportedResources: []providers.ImportedResource{{
				TypeName: req.TypeName,
				State: cty.ObjectVal(map[string]cty.Value{
					"id":                cty.StringVal(req.Target.ID),
					"availability_zone": cty.StringVal("us-east-1a"),
					"size":              cty.NumberIntVal(8),
					"tags":              cty.NullVal(cty.Map(cty.String)),
				}),
			}},
		}
	}
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: req.PriorState}
	}
	return p
}

// writeMarkersRecordFixture writes a one-resource module whose live block
// selects that resource, and returns its directory.
func writeMarkersRecordFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `
terraform {
  live {
    estate = "test-estate"

    record_store "local" {}

    strict {
      marker_repair = "never"

      markers "record" {
        types = ["` + markersRecordTestType + `"]
      }
    }
  }
}

resource "` + markersRecordTestType + `" "data" {
  availability_zone = "us-east-1a"
  size              = 8
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return dir
}

// TestMarkersRecordOwnershipAdmitsAnUntaggedLocatedObject is the failure
// that would have been the most expensive, and the one nothing in this
// repository was watching for.
//
// Before the selection existed, every located type was markerless, so
// checkOwnership's !markerCapable arm admitted it and the ownership rule
// never ran on a located instance. A SELECTED instance is taggable and
// carries no marker - by design, because the whole point is that no marker
// was written - so it reads as the declared_untagged quadrant, whose default
// verb is refuse. The object would be kept out of the prior state, the plan
// would propose creating it, and the account would grow one more of them per
// run, with every plan verdict clean.
//
// The record is the ownership proof here, and it is a better one than the
// tag: it was written by this estate's own apply, keyed by this exact
// address, and LocatedStore.Get has already refused a payload naming any
// other one.
//
// Ownership is set with a real Policy so that the DEFAULT verbs are the ones
// being applied - refuse for declared_untagged - rather than the nil-policy
// shortcut. A test that left Policy nil would pass on the broken code.
func TestMarkersRecordOwnershipAdmitsAnUntaggedLocatedObject(t *testing.T) {
	cfg := loadConfig(t, writeMarkersRecordFixture(t))
	addr := mustAddr(t, markersRecordTestType+`.data`)
	const estate = "test-estate"
	const liveID = "vol-0123456789abcdef0"

	store := localHintStore(t)
	located := newTestLocatedStore(store, estate)
	if _, err := located.Put(context.Background(), addr, LocatedRecord{ImportID: liveID}, ""); err != nil {
		t.Fatalf("seeding the located record: %s", err)
	}

	pol := policy.Build(nil, estate)

	var imported []string
	provs := SingleProvider(locatedTestProvider, markersRecordProvider(&imported))
	res, diags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordLocated}},
		provs, Options{
			RecordStore: located.rs,
			Ownership:   &Ownership{Estate: estate, Policy: pol},
		})
	assertNoErrors(t, diags)

	if len(res.Unowned) != 0 {
		t.Fatalf("the selected instance was refused as unowned, so the plan would propose creating a second live object: %v", res.Unowned)
	}
	if len(imported) != 1 || imported[0] != liveID {
		t.Fatalf("imported %v, want [%q] - the identity has to have come out of the record", imported, liveID)
	}
	assertMaterialized(t, res, []string{markersRecordTestType + `.data`})
}

// TestMarkersRecordWriteBackRecordsTheSelectedIdentity is the other half,
// and the one that makes the mechanism true rather than hypothetical.
//
// writeBackLocated decides what to write by re-asking the admission
// predicate rather than remembering the plan's verdict, which is right - but
// identity.LocatedType answers NO for a selected type, because a selected
// type is taggable and has a ratified row. Without the selection consulted
// here too, the plan side would read the store and the apply side would
// never write to it: every run would read unbound, propose a create, and
// leave the estate one more object per apply.
//
// The recorded identity is asserted BY VALUE, not by existence. A record
// that exists and holds the wrong string is the failure mode this whole
// slice is built to avoid, and "a record was written" cannot see it.
func TestMarkersRecordWriteBackRecordsTheSelectedIdentity(t *testing.T) {
	cfg := loadConfig(t, writeMarkersRecordFixture(t))
	addr := mustAddr(t, markersRecordTestType+`.data`)
	const estate = "test-estate"
	const appliedID = "vol-0123456789abcdef0"

	store := localHintStore(t)
	located := newTestLocatedStore(store, estate)

	final := states.NewState()
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":                cty.StringVal(appliedID),
		"availability_zone": cty.StringVal("us-east-1a"),
		"size":              cty.NumberIntVal(8),
		"tags":              cty.NullVal(cty.Map(cty.String)),
	})
	src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: applied}).
		Encode(markersRecordTypeSchema().Block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding the applied object: %s", err)
	}
	final.EnsureModule(addrs.RootModuleInstance).
		SetResourceInstanceCurrent(addr.Resource, src, locatedTestProvider, addrs.NoKey)

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{
			markersRecordTestType: markersRecordTypeSchema(),
		}},
	}}

	assertNoErrors(t, WriteBack(context.Background(), WriteBackRequest{
		Store:      located.rs,
		FinalState: final,
		Schemas:    schemas,
		Config:     cfg,
	}))

	rec, _, exists, err := located.Get(context.Background(), addr)
	if err != nil {
		t.Fatalf("reading back the located record: %s", err)
	}
	if !exists {
		t.Fatal("write-back recorded nothing for a selected instance. The plan side reads this key; if nothing writes it, every run reads unbound and proposes creating a second object.")
	}
	if rec.ImportID != appliedID {
		t.Fatalf("recorded identity %q, want %q", rec.ImportID, appliedID)
	}
}

// TestMarkersRecordWriteBackIgnoresAnUnselectedResource used to be the guard
// against the selection being read too widely on the write side: the same
// applied object, the same type, the same store, no selection in the
// configuration, and nothing written at all - because before GitHub issue
// #364 unit A2, an ordinary marker-found instance's identity had no reason
// to be in the record store, and a second, unreconciled answer to "which
// object is this" is exactly what issue #270 exists to prevent the plan
// side from ever reading.
//
// The 2026-08-23 foundation-order ruling widened the record to hold every
// instance's identity, read first by a later plan and verified against the
// marker rather than trusted on its own (unit B, not yet landed) - so an
// unselected, marker-bearing instance now DOES get its identity recorded,
// through the exact same generic derivation ([LocatedRecordFrom]) a
// selected instance's does. What the test still guards is that the two
// routes never conflict: an unselected resource is still found by its
// marker (nothing about ownership moved), and its record - now present -
// holds the correct value rather than a fragment or a guess. See
// TestApprove_UnselectedInstanceIsStampedNotLocated in
// internal/live/liveimport for this same reversal on the migrate side.
func TestMarkersRecordWriteBackIgnoresAnUnselectedResource(t *testing.T) {
	dir := t.TempDir()
	src := `
terraform {
  live {
    estate = "test-estate"
    record_store "local" {}
  }
}

resource "` + markersRecordTestType + `" "data" {
  availability_zone = "us-east-1a"
  size              = 8
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	cfg := loadConfig(t, dir)
	addr := mustAddr(t, markersRecordTestType+`.data`)
	const estate = "test-estate"

	store := localHintStore(t)
	located := newTestLocatedStore(store, estate)

	final := states.NewState()
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":                cty.StringVal("vol-unselected"),
		"availability_zone": cty.StringVal("us-east-1a"),
		"size":              cty.NumberIntVal(8),
		"tags":              cty.NullVal(cty.Map(cty.String)),
	})
	srcObj, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: applied}).
		Encode(markersRecordTypeSchema().Block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	final.EnsureModule(addrs.RootModuleInstance).
		SetResourceInstanceCurrent(addr.Resource, srcObj, locatedTestProvider, addrs.NoKey)

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{
			markersRecordTestType: markersRecordTypeSchema(),
		}},
	}}

	assertNoErrors(t, WriteBack(context.Background(), WriteBackRequest{
		Store:      located.rs,
		FinalState: final,
		Schemas:    schemas,
		Config:     cfg,
	}))

	rec, _, exists, err := located.Get(context.Background(), addr)
	if err != nil {
		t.Fatalf("reading back the identity record: %s", err)
	}
	if !exists {
		t.Fatal("no identity record was written for an unselected instance; GitHub issue #364 unit A2 records every instance's identity, not only a selected or located one's")
	}
	if rec.ImportID != "vol-unselected" {
		t.Errorf("identity record ImportID = %q, want %q (the applied object's own id)", rec.ImportID, "vol-unselected")
	}
}

// writeMarkersRecordSelectionOnlyFixture is writeMarkersRecordFixture's own
// selection, with NO resource block at all: the shape a plan sees the
// moment the block that used to declare markersRecordTestType.data - or,
// for corpus-sumaform-aws's real day2_remove unit, the module that called
// it - is deleted from configuration. The `markers "record"` selection
// itself is root-level and untouched by the deletion (this file's own
// header: "an operator's markers = record selection... is the first way a
// TAGGABLE instance reaches this package with a record-held identity"),
// which is what internal/live/discovery's recordOrphanReadSweep relies on
// to still find the type selected after its declaring block is gone.
func writeMarkersRecordSelectionOnlyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `
terraform {
  live {
    estate = "test-estate"

    record_store "local" {}

    strict {
      marker_repair = "never"

      markers "record" {
        types = ["` + markersRecordTestType + `"]
      }
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return dir
}

// TestMarkersRecordUndeclaredRecordRootedInstanceIsDestroyed is
// gauntlet:sumaform-clear's own day2_remove finding, at this package's own
// level rather than a full e2e run: an UNDECLARED resolution (the
// resource's block - or, in the real estate, the module that called it -
// removed from configuration) for a markers=record-selected TAGGABLE type
// carries the identical failure
// TestMarkersRecordOwnershipAdmitsAnUntaggedLocatedObject fixed for the
// DECLARED case, and it was not fixed by that change: an undeclared
// resolution never takes [builder.materializeLocated]'s route (that path is
// [identity.ClassRecordLocated] only, and internal/live/discovery's
// recordOrphanReadSweep - the leg that finds this instance at all, once it
// also stops skipping a selected taggable type per its own doc comment -
// produces a plain [identity.ClassConcrete] resolution instead), so without
// [identity.Resolution.RecordRooted] telling [builder.run]'s own
// undeclaredConcrete pass to trust it the same way, checkOwnership fell
// through to the ordinary taggable-type tag check, found no tag - there was
// never one to find, by the same selection that put the identity in the
// record instead - and silently omitted a real, correctly-identified live
// object instead of proposing its destroy.
//
// Asserted the same three ways TestUndeclaredInstanceIsMaterialized does:
// no error, nothing in res.Unowned (the failure mode this test guards would
// put it there instead), and a real state entry under the provider the run
// supplied - what the plan engine's destroy node actually reads.
func TestMarkersRecordUndeclaredRecordRootedInstanceIsDestroyed(t *testing.T) {
	cfg := loadConfig(t, writeMarkersRecordSelectionOnlyFixture(t))
	addr := mustAddr(t, markersRecordTestType+`.data`)
	const estate = "test-estate"
	const liveID = "vol-0123456789abcdef0"

	store := localHintStore(t)
	located := newTestLocatedStore(store, estate)
	if _, err := located.Put(context.Background(), addr, LocatedRecord{ImportID: liveID}, ""); err != nil {
		t.Fatalf("seeding the located record: %s", err)
	}

	pol := policy.Build(nil, estate)

	var imported []string
	provs := SingleProvider(locatedTestProvider, markersRecordProvider(&imported))
	res, diags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{
			Addr:         addr,
			Class:        identity.ClassConcrete,
			ImportID:     liveID,
			Undeclared:   true,
			RecordRooted: true,
		}},
		provs, Options{
			RecordStore: located.rs,
			Ownership:   &Ownership{Estate: estate, Policy: pol},
		})
	assertNoErrors(t, diags)

	if len(res.Unowned) != 0 {
		t.Fatalf("the record-rooted undeclared instance was refused as unowned, so its destroy would never be proposed: %v", res.Unowned)
	}
	if len(imported) != 1 || imported[0] != liveID {
		t.Fatalf("imported %v, want [%q] - the identity has to have come out of the record", imported, liveID)
	}
	assertMaterialized(t, res, []string{markersRecordTestType + `.data`})

	mod := res.State.Module(addrs.RootModuleInstance)
	if mod == nil {
		t.Fatal("the projection has no root module")
	}
	rs := mod.Resource(addr.Resource.Resource)
	if rs == nil {
		t.Fatalf("the undeclared, record-rooted instance is not in the state, so no destroy can be proposed for it:\n%s", res)
	}
	if rs.ProviderConfig.String() != locatedTestProvider.String() {
		t.Errorf("the undeclared instance is filed under provider %s, want %s", rs.ProviderConfig, locatedTestProvider)
	}
}

// TestMarkersRecordUndeclaredWithoutRecordRootedIsRefused is the previous
// test's control: identical fixture, identical seeded record, identical
// resolution, with only [identity.Resolution.RecordRooted] left false - the
// ordinary value for every resolution a marker-TAG-sourced leg (
// classifyOrphans, parentReadSweep, foldChildReadSweep) produces for an
// undeclared instance, as opposed to a record-store-sourced one
// (recordOrphanReadSweep). It proves RecordRooted is actually load-bearing
// here rather than a field nothing reads: with it false, this taggable
// type's live object - which the SAME markersRecordProvider mock returns
// with no tags, because a selected instance never carries one - is not
// trusted the way a genuine record-located identity is, checkOwnership's
// ordinary tag check runs, finds nothing, and the instance is correctly
// refused as unowned rather than destroyed on an unverified guess. This is
// not a claim that a real tag-sourced orphan of this type would reach this
// exact state (own.verified(addr) would ordinarily short-circuit it first,
// per checkOwnership's own doc comment) - it isolates RecordRooted's own
// effect, all else held equal.
func TestMarkersRecordUndeclaredWithoutRecordRootedIsRefused(t *testing.T) {
	cfg := loadConfig(t, writeMarkersRecordSelectionOnlyFixture(t))
	addr := mustAddr(t, markersRecordTestType+`.data`)
	const estate = "test-estate"
	const liveID = "vol-0123456789abcdef0"

	store := localHintStore(t)
	located := newTestLocatedStore(store, estate)
	if _, err := located.Put(context.Background(), addr, LocatedRecord{ImportID: liveID}, ""); err != nil {
		t.Fatalf("seeding the located record: %s", err)
	}

	pol := policy.Build(nil, estate)

	provs := SingleProvider(locatedTestProvider, markersRecordProvider(nil))
	res, diags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{
			Addr:       addr,
			Class:      identity.ClassConcrete,
			ImportID:   liveID,
			Undeclared: true,
			// RecordRooted deliberately left false.
		}},
		provs, Options{
			RecordStore: located.rs,
			Ownership:   &Ownership{Estate: estate, Policy: pol},
		})
	assertNoErrors(t, diags)

	if len(res.Unowned) != 1 {
		t.Fatalf("got %d unowned entries, want exactly 1 - without RecordRooted this taggable, untagged instance should be refused as unowned, not silently trusted or materialized: %v", len(res.Unowned), res.Unowned)
	}

	mod := res.State.Module(addrs.RootModuleInstance)
	if mod != nil {
		if rs := mod.Resource(addr.Resource.Resource); rs != nil {
			t.Fatalf("the instance was materialized into the state despite carrying no tag and no RecordRooted trust: %s", res)
		}
	}
}
