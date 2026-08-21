// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang/marks"
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
	seeded, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("SeedRecordForInstance: %s", err)
	}
	if seeded != SeedWritten {
		t.Fatalf("SeedRecordForInstance = %v into an empty store, want SeedWritten", seeded)
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

	if seeded, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, val, nil, states.ObjectReady); err != nil || seeded != SeedWritten {
		t.Fatalf("the first seed: result = %v, err = %v", seeded, err)
	}
	seeded, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("the second seed returned an error: %s", err)
	}
	if seeded != SeedUnchanged {
		t.Errorf("the second seed of an identical object = %v; it must be SeedUnchanged", seeded)
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

	seeded, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, mk("a-stale-value"), nil, states.ObjectReady)
	if seeded.Wrote() {
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
	seeded, err := SeedRecordForInstance(context.Background(), nil, seedPrefix, addr,
		cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("x")}), nil, states.ObjectReady)
	if seeded != SeedUnchanged || err != nil {
		t.Errorf("result = %v, err = %v; want SeedUnchanged, nil", seeded, err)
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
	seeded, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr,
		cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("x")}), nil, states.ObjectPlanned)
	if seeded.Wrote() {
		t.Error("a planned object was recorded")
	}
	if err == nil {
		t.Fatal("a planned object was accepted")
	}
	if _, _, exists, getErr := store.Get(context.Background(), RecordKey(seedPrefix, addr)); getErr != nil || exists {
		t.Errorf("a record was created for a refused object: exists = %v, err = %v", exists, getErr)
	}
}

// ---------------------------------------------------------------------------
// GitHub issue #344: a pre-sensitivity record versus its own re-migration.
// ---------------------------------------------------------------------------

// preSensitivityRecord writes the record a migration produced before
// recordPayload.SensitiveAttrs existed: the same encoder, handed the same
// object with no marks on it. That equivalence is not an assumption -
// [encodeRecordPayload] omits the field entirely when nothing is sensitive,
// which is the property recordPayload.SensitiveAttrs's own doc comment
// states and TestRecordWithNoSensitivityIsByteIdenticalToTheOldFormat checks - and
// it is asserted here rather than trusted.
func preSensitivityRecord(t *testing.T, store staterecord.Store, addr addrs.AbsResourceInstance, val cty.Value, private []byte, status states.ObjectStatus) {
	t.Helper()
	unmarked, _ := val.UnmarkDeep()
	payload, err := encodeRecordPayload(unmarked, private, status)
	if err != nil {
		t.Fatalf("encoding the pre-sensitivity record: %s", err)
	}
	if strings.Contains(string(payload), "sensitive_attributes") {
		t.Fatalf("the fixture is not the pre-sensitivity shape: %s", payload)
	}
	if _, err := store.PutIfVersion(context.Background(), RecordKey(seedPrefix, addr), payload, ""); err != nil {
		t.Fatalf("writing the pre-sensitivity record: %s", err)
	}
}

// seedFixture is the object every #344 case re-seeds, with triggers marked
// sensitive - the config-derived case recordPayload.SensitiveAttrs names
// (a null_resource whose triggers come from a `sensitive = true` variable).
func seedFixture(id string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal(id),
		"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}).Mark(marks.Sensitive),
	})
}

func seedStore(t *testing.T) staterecord.Store {
	t.Helper()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	return store
}

// TestSeedRecordUpgradesAPreSensitivityRecord is #344's whole positive case.
// An estate migrated before choudoufu persisted sensitivity, migrated again
// after, must not report a stale-record conflict for a record that is not
// stale - and the rewrite must leave the value exactly where it was and add
// only the marks.
func TestSeedRecordUpgradesAPreSensitivityRecord(t *testing.T) {
	addr := mustAddr(t, `null_resource.trigger`)
	store := seedStore(t)
	val := seedFixture("stable")
	preSensitivityRecord(t, store, addr, val, nil, states.ObjectReady)

	seeded, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("re-seeding a pre-sensitivity record refused: %s", err)
	}
	if seeded != SeedMarksAdded {
		t.Fatalf("result = %v, want SeedMarksAdded", seeded)
	}

	raw, _, exists, err := store.Get(context.Background(), RecordKey(seedPrefix, addr))
	if err != nil || !exists {
		t.Fatalf("reading the record back: exists = %v, err = %v", exists, err)
	}
	got, _, gotStatus, err := decodeRecordPayload(raw)
	if err != nil {
		t.Fatalf("decoding the rewritten record: %s", err)
	}
	if gotStatus != states.ObjectReady {
		t.Errorf("status = %v, want ObjectReady", gotStatus)
	}
	if !got.RawEquals(val) {
		t.Errorf("the rewritten record decodes to\n %#v\nwant\n %#v", got, val)
	}
	unmarked, pvms := got.UnmarkDeepWithPaths()
	if unmarked.GetAttr("id").AsString() != "stable" {
		t.Errorf("the value was changed by the upgrade: %#v", unmarked)
	}
	if len(pvms) != 1 || !pvms[0].Path.Equals(cty.GetAttrPath("triggers")) {
		t.Errorf("the rewritten record carries %v, want exactly .triggers sensitive", pvms)
	}

	// Third run: now byte-identical, so back to the plain no-op. The upgrade
	// has to be a one-time step, not a rewrite on every migration.
	again, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, val, nil, states.ObjectReady)
	if err != nil || again != SeedUnchanged {
		t.Errorf("the third seed = %v, err = %v; want SeedUnchanged, nil", again, err)
	}
}

// TestSeedRecordRefusesEverythingButAddedSensitivity is the negative control
// set, and it is the half that matters: #340 refuses a byte-different record
// on purpose, and a loosening that admitted anything beyond "same value, more
// marks" would replace a correct stored value with a stale one and produce an
// EMPTY plan that is wrong.
//
// Each case starts from a store that already holds a record, and asserts both
// that the seed is refused AND that the stored bytes are untouched - the
// second half being the one that would still pass if the refusal were
// reported but the write happened anyway.
func TestSeedRecordRefusesEverythingButAddedSensitivity(t *testing.T) {
	addr := mustAddr(t, `null_resource.trigger`)
	marked := seedFixture("stable")
	unmarkedFixture, _ := marked.UnmarkDeep()

	cases := []struct {
		name string
		// stored is written into the store first, exactly as given.
		store func(t *testing.T, s staterecord.Store)
		// then this object is seeded over it.
		val     cty.Value
		private []byte
		status  states.ObjectStatus
		wantErr string
	}{
		{
			// The one the whole guard exists for: the stored value is
			// genuinely different, and it may be NEWER than the state file
			// this migration was given.
			name: "a different value with the same sensitivity still conflicts",
			store: func(t *testing.T, s staterecord.Store) {
				seedOK(t, s, addr, seedFixture("newer"), nil, states.ObjectReady)
			},
			val:     marked,
			status:  states.ObjectReady,
			wantErr: "the stored value differs",
		},
		{
			// The same difference, reached through the sensitivity door: an
			// unmarked record whose value ALSO differs must not be admitted
			// just because the marks would grow.
			name: "a different value that would also gain sensitivity still conflicts",
			store: func(t *testing.T, s staterecord.Store) {
				preSensitivityRecord(t, s, addr, seedFixture("newer"), nil, states.ObjectReady)
			},
			val:     marked,
			status:  states.ObjectReady,
			wantErr: "the stored value differs",
		},
		{
			// The direction that would LOSE sensitivity. Strict subset is
			// what makes the rewrite one-way; without it this would quietly
			// unmark a recorded secret.
			name:    "dropping sensitivity conflicts",
			store:   func(t *testing.T, s staterecord.Store) { seedOK(t, s, addr, marked, nil, states.ObjectReady) },
			val:     unmarkedFixture,
			status:  states.ObjectReady,
			wantErr: "already records at least as much sensitivity",
		},
		{
			// Same count, different path: a swap is not a superset. Caught by
			// the length half of the strict-subset test.
			name: "swapping one sensitive path for another conflicts",
			store: func(t *testing.T, s staterecord.Store) {
				seedOK(t, s, addr, cty.ObjectVal(map[string]cty.Value{
					"id":       cty.StringVal("stable").Mark(marks.Sensitive),
					"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}),
				}), nil, states.ObjectReady)
			},
			val:     marked,
			status:  states.ObjectReady,
			wantErr: "already records at least as much sensitivity",
		},
		{
			// More paths than before, but not the ones that were there. This
			// is the case the length check alone would wave through, and it
			// would drop a mark from a recorded secret while adding two
			// elsewhere - which is the same loss the "dropping sensitivity"
			// case is refused for, hidden inside a growth.
			name: "growing the set while dropping one of its members conflicts",
			store: func(t *testing.T, s staterecord.Store) {
				seedOK(t, s, addr, cty.ObjectVal(map[string]cty.Value{
					"id":       cty.StringVal("stable"),
					"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}),
					"extra":    cty.StringVal("e").Mark(marks.Sensitive),
				}), nil, states.ObjectReady)
			},
			val: cty.ObjectVal(map[string]cty.Value{
				"id":       cty.StringVal("stable").Mark(marks.Sensitive),
				"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}).Mark(marks.Sensitive),
				"extra":    cty.StringVal("e"),
			}),
			status:  states.ObjectReady,
			wantErr: `marks .extra sensitive and this run would not`,
		},
		{
			name: "different provider-private bytes conflict",
			store: func(t *testing.T, s staterecord.Store) {
				preSensitivityRecord(t, s, addr, marked, []byte("was"), states.ObjectReady)
			},
			val:     marked,
			private: []byte("now"),
			status:  states.ObjectReady,
			wantErr: "provider-private bytes differ",
		},
		{
			// #216's tainted bit has no other carrier, so a status change is
			// never an incidental difference.
			name: "a different object status conflicts",
			store: func(t *testing.T, s staterecord.Store) {
				preSensitivityRecord(t, s, addr, marked, nil, states.ObjectTainted)
			},
			val:     marked,
			status:  states.ObjectReady,
			wantErr: "object status differs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := seedStore(t)
			tc.store(t, store)
			before, _, _, err := store.Get(context.Background(), RecordKey(seedPrefix, addr))
			if err != nil {
				t.Fatalf("reading the stored record: %s", err)
			}

			seeded, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, tc.val, tc.private, tc.status)
			if err == nil {
				t.Fatalf("the seed was accepted; result = %v", seeded)
			}
			if seeded.Wrote() {
				t.Errorf("a refused seed reported %v", seeded)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("the refusal says %q, want it to name %q", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), addr.String()) {
				t.Errorf("the refusal does not name the address: %s", err)
			}

			after, _, _, err := store.Get(context.Background(), RecordKey(seedPrefix, addr))
			if err != nil {
				t.Fatalf("re-reading the stored record: %s", err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("a refused seed changed the stored record:\n before %s\n after  %s", before, after)
			}
		})
	}
}

// seedOK writes a record through the real seed path and fails the test if it
// does not create one, so a fixture that silently conflicts cannot be
// mistaken for the state a case meant to set up.
func seedOK(t *testing.T, store staterecord.Store, addr addrs.AbsResourceInstance, val cty.Value, private []byte, status states.ObjectStatus) {
	t.Helper()
	seeded, err := SeedRecordForInstance(context.Background(), store, seedPrefix, addr, val, private, status)
	if err != nil || seeded != SeedWritten {
		t.Fatalf("setting up the stored record: result = %v, err = %v", seeded, err)
	}
}

// TestSeedRecordUpgradeLosesAConcurrentWriteRace holds the one thing the
// upgrade shares with every other write to this store: the decision is made
// about bytes read a moment ago, and a writer that replaced them in between
// must win. The conditional put is against the version the read returned, so
// the rewrite cannot clobber a record this comparison never saw.
func TestSeedRecordUpgradeLosesAConcurrentWriteRace(t *testing.T) {
	addr := mustAddr(t, `null_resource.trigger`)
	store := seedStore(t)
	val := seedFixture("stable")
	preSensitivityRecord(t, store, addr, val, nil, states.ObjectReady)

	racing := &raceOnGet{Store: store, key: RecordKey(seedPrefix, addr), t: t, addr: addr}
	seeded, err := SeedRecordForInstance(context.Background(), racing, seedPrefix, addr, val, nil, states.ObjectReady)
	if err == nil {
		t.Fatalf("the upgrade won a race it should have lost; result = %v", seeded)
	}
	if seeded.Wrote() {
		t.Errorf("a lost race reported %v", seeded)
	}

	raw, _, _, getErr := store.Get(context.Background(), RecordKey(seedPrefix, addr))
	if getErr != nil {
		t.Fatalf("reading the record back: %s", getErr)
	}
	got, _, _, decErr := decodeRecordPayload(raw)
	if decErr != nil {
		t.Fatalf("decoding: %s", decErr)
	}
	if id := got.GetAttr("id").AsString(); id != "written-by-the-other-writer" {
		t.Errorf("the stored id is %q; the lost race clobbered the other writer", id)
	}
}

// raceOnGet is a store that lets a second writer land between the seed's Get
// and its PutIfVersion - the window the conditional put exists to close.
type raceOnGet struct {
	staterecord.Store
	key  string
	t    *testing.T
	addr addrs.AbsResourceInstance
	done bool
}

func (r *raceOnGet) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	payload, version, exists, err := r.Store.Get(ctx, key)
	if key == r.key && !r.done {
		r.done = true
		other, encErr := encodeRecordPayload(cty.ObjectVal(map[string]cty.Value{
			"id":       cty.StringVal("written-by-the-other-writer"),
			"triggers": cty.NullVal(cty.Map(cty.String)),
		}), nil, states.ObjectReady)
		if encErr != nil {
			r.t.Fatalf("encoding the racing write: %s", encErr)
		}
		if _, putErr := r.Store.PutIfVersion(ctx, key, other, version); putErr != nil {
			r.t.Fatalf("the racing write: %s", putErr)
		}
	}
	return payload, version, exists, err
}
