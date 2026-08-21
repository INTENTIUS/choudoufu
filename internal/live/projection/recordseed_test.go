// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
)

// GitHub issue #340. [SeedRecordForInstance] is the migrate-time write for a
// record-backed instance, and the only claim that matters about it is that
// what a migration writes is exactly what the next plan reads. So the
// positive test does not decode the payload itself - it runs the real
// [BuildWith] over the seeded store and asserts the materialized value.

const seedPrefix = "tofu-records/seed-estate"

// TestSeedRecordForInstanceIsWhatBuildReads closes the seam between the two
// halves of the record lifecycle. A wrong key prefix, a wrong encoder or a
// wrong address encoding would each leave this store holding a record no
// plan can find, and every one of those failures is silent: the plan would
// simply propose creating the resource, which is what a migrated estate
// looked like before this existed.
func TestSeedRecordForInstanceIsWhatBuildReads(t *testing.T) {
	cfg := loadConfig(t, writeNullResourceFixture(t))
	addr := mustAddr(t, `null_resource.trigger`)

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}

	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("seeded-by-a-migration"),
		"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}),
	})
	wrote, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("SeedRecordForInstance: %s", err)
	}
	if !wrote {
		t.Fatal("SeedRecordForInstance reported no write into an empty store")
	}

	provs := SingleProvider(nullProvider, nullResourceProvider())
	res, diags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordBacked}},
		provs, Options{RecordStore: store, RecordKeyPrefix: seedPrefix})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`null_resource.trigger`})

	inst := res.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object for the record-backed instance: the plan cannot see what the migration wrote")
	}
	obj, err := inst.Current.Decode(nullResourceSchema().Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding the materialized object: %s", err)
	}
	if got := obj.Value.GetAttr("id").AsString(); got != "seeded-by-a-migration" {
		t.Errorf("id = %q, want %q", got, "seeded-by-a-migration")
	}
	if got := obj.Value.GetAttr("triggers").Index(cty.StringVal("input")).AsString(); got != "value" {
		t.Errorf("triggers.input = %q, want %q", got, "value")
	}
}

// TestSeedRecordForInstanceIsIdempotent: re-seeding the identical object is
// a no-op that reports no write and raises no error, which is what keeps a
// second live-import run over the same state file a no-op.
func TestSeedRecordForInstanceIsIdempotent(t *testing.T) {
	addr := mustAddr(t, `null_resource.trigger`)
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("stable"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})

	if wrote, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, val, nil, states.ObjectReady); err != nil || !wrote {
		t.Fatalf("the first seed: wrote = %v, err = %v", wrote, err)
	}
	wrote, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("the second seed returned an error: %s", err)
	}
	if wrote {
		t.Error("the second seed of an identical object reported a write; it must be a no-op")
	}
}

// TestSeedRecordForInstanceNeverOverwrites is the value-safety half. A
// record is the only carrier a record-backed resource's identity has, and
// the store's value can legitimately be newer than the state file a
// migration was pointed at. Overwriting would produce an empty plan built
// on a stale value, which nothing downstream can detect.
func TestSeedRecordForInstanceNeverOverwrites(t *testing.T) {
	addr := mustAddr(t, `null_resource.trigger`)
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	mk := func(id string) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{
			"id":       cty.StringVal(id),
			"triggers": cty.NullVal(cty.Map(cty.String)),
		})
	}

	if _, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, mk("the-live-value"), nil, states.ObjectReady); err != nil {
		t.Fatalf("seeding the first value: %s", err)
	}

	wrote, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, mk("a-stale-value"), nil, states.ObjectReady)
	if wrote {
		t.Error("a conflicting seed reported a write")
	}
	if err == nil {
		t.Fatal("a conflicting seed returned no error")
	}
	if !strings.Contains(err.Error(), addr.String()) {
		t.Errorf("the refusal does not name the address: %s", err)
	}

	raw, _, exists, err := store.Get(context.Background(), RecordKey(seedPrefix, addr))
	if err != nil || !exists {
		t.Fatalf("reading the record back: exists = %v, err = %v", exists, err)
	}
	got, _, _, err := decodeRecordPayload(raw)
	if err != nil {
		t.Fatalf("decoding the record: %s", err)
	}
	if id := got.GetAttr("id").AsString(); id != "the-live-value" {
		t.Errorf("the stored id is now %q; the refused seed overwrote it", id)
	}
}

// TestSeedRecordForInstanceWithNoStore pins the no-record_store case as a
// silent no-op rather than an error: a configuration that declares no store
// does not admit a record-backed type for planning either, so there is
// nothing to report.
func TestSeedRecordForInstanceWithNoStore(t *testing.T) {
	addr := mustAddr(t, `null_resource.trigger`)
	wrote, err := SeedRecordForInstance(context.Background(), nil, seedPrefix, addr,
		cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("x")}), nil, states.ObjectReady)
	if wrote || err != nil {
		t.Errorf("wrote = %v, err = %v; want false, nil", wrote, err)
	}
}

// TestSeedRecordForInstanceRefusesAPlannedObject inherits
// [encodeObjectStatus]'s refusal rather than folding an unexpected status
// into "ready" - the same guard [WriteBack] gets, reached from the migrate
// side.
func TestSeedRecordForInstanceRefusesAPlannedObject(t *testing.T) {
	addr := mustAddr(t, `null_resource.trigger`)
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	wrote, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr,
		cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("x")}), nil, states.ObjectPlanned)
	if wrote {
		t.Error("a planned object was recorded")
	}
	if err == nil {
		t.Fatal("a planned object was accepted")
	}
	if _, _, exists, getErr := store.Get(context.Background(), RecordKey(seedPrefix, addr)); getErr != nil || exists {
		t.Errorf("a record was created for a refused object: exists = %v, err = %v", exists, getErr)
	}
}
