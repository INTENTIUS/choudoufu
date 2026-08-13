// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// nullProvider is the provider configuration a null_resource fixture
// resolves to.
var nullProvider = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("null"),
}

// nullResourceSchema is a caricature of hashicorp/null's real schema, just
// enough for encode/decode round-tripping and dependency extraction: id
// (computed) and triggers (an author-supplied map).
func nullResourceSchema() providers.Schema {
	return providers.Schema{
		Version: 0,
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":       {Type: cty.String, Computed: true},
				"triggers": {Type: cty.Map(cty.String), Optional: true},
			},
		},
	}
}

// nullResourceProvider builds a [providers.Interface] that serves
// nullResourceSchema and nothing else - materializeRecord never calls
// ImportResourceState or ReadResource, so this deliberately leaves both
// unset; a test that reaches either would panic on tofu.MockProvider's own
// nil-function guard, which is the point.
func nullResourceProvider() providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"null_resource": nullResourceSchema()},
		},
	}
	p.ConfigureProviderCalled = true
	return p
}

// writeNullResourceFixture writes a one-resource module declaring
// null_resource.trigger, and returns its directory.
func writeNullResourceFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const src = `
resource "null_resource" "trigger" {
  triggers = {
    input = "value"
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return dir
}

// writeEmptyFixture writes a module with no resource blocks at all - the
// shape a configuration takes right after a record-backed resource's block
// is deleted from it.
func writeEmptyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("# intentionally empty\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return dir
}

// TestBuildHydratesRecordBacked is item 3's core claim: a
// ClassRecordBacked resolution with an existing record in the store
// hydrates into the projection's *states.State exactly like a live-read
// resource, bypassing importAndRead entirely.
func TestBuildHydratesRecordBacked(t *testing.T) {
	cfg := loadConfig(t, writeNullResourceFixture(t))
	addr := mustAddr(t, `null_resource.trigger`)

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/test-estate"
	key := RecordKey(prefix, addr)

	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("abc123"),
		"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}),
	})
	payload, err := encodeRecordPayload(val, nil)
	if err != nil {
		t.Fatalf("encoding the fixture payload: %s", err)
	}
	wantVersion, err := store.PutIfAbsent(context.Background(), key, payload)
	if err != nil {
		t.Fatalf("seeding the store: %s", err)
	}

	provs := SingleProvider(nullProvider, nullResourceProvider())
	resolutions := []identity.Resolution{{Addr: addr, Class: identity.ClassRecordBacked}}

	res, diags := BuildWith(context.Background(), cfg, resolutions, provs, Options{
		RecordStore:     store,
		RecordKeyPrefix: prefix,
	})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{`null_resource.trigger`})

	if len(res.RecordVersions) != 1 {
		t.Fatalf("RecordVersions = %v, want exactly one entry", res.RecordVersions)
	}
	if got := res.RecordVersions[0]; got.Addr.String() != addr.String() || got.Version != wantVersion {
		t.Errorf("RecordVersions[0] = %+v, want {%s %s}", got, addr, wantVersion)
	}

	inst := res.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object for the record-backed instance")
	}
	obj, err := inst.Current.Decode(nullResourceSchema().Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding the materialized object: %s", err)
	}
	if got := obj.Value.GetAttr("id"); got.AsString() != "abc123" {
		t.Errorf("id = %v, want abc123", got)
	}
}

// TestBuildRecordBackedAbsent is the create-not-yet-happened case: no
// record in the store, so the plan should propose creating it, the same
// ReasonAbsent a live-read resource with no cloud counterpart gets.
func TestBuildRecordBackedAbsent(t *testing.T) {
	cfg := loadConfig(t, writeNullResourceFixture(t))
	addr := mustAddr(t, `null_resource.trigger`)

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}

	provs := SingleProvider(nullProvider, nullResourceProvider())
	resolutions := []identity.Resolution{{Addr: addr, Class: identity.ClassRecordBacked}}

	res, diags := BuildWith(context.Background(), cfg, resolutions, provs, Options{
		RecordStore:     store,
		RecordKeyPrefix: "tofu-records/test-estate",
	})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, nil)
	assertOmitted(t, res, map[string]Reason{`null_resource.trigger`: ReasonAbsent})
	if len(res.RecordVersions) != 0 {
		t.Errorf("RecordVersions = %v, want none for an absent record", res.RecordVersions)
	}
}

// TestBuildRecordBackedWithNoStoreConfigured is the defensive path: a
// ClassRecordBacked resolution should never reach Build without a store,
// because internal/live/lint gates admission on one being configured. If
// it does anyway (a caller bypassing lint, or a future bug in that gate),
// Build must fail loudly rather than silently omit or panic.
func TestBuildRecordBackedWithNoStoreConfigured(t *testing.T) {
	cfg := loadConfig(t, writeNullResourceFixture(t))
	addr := mustAddr(t, `null_resource.trigger`)

	provs := SingleProvider(nullProvider, nullResourceProvider())
	resolutions := []identity.Resolution{{Addr: addr, Class: identity.ClassRecordBacked}}

	_, diags := BuildWith(context.Background(), cfg, resolutions, provs, Options{})
	if !diags.HasErrors() {
		t.Fatal("want an error when a record-backed resolution has no record store configured")
	}
	if !hasDiag(diags, "Record-backed instance with no record store", "no record store was configured") {
		t.Errorf("wrong diagnostics:\n%s", renderDiags(diags))
	}
}

// TestBuildDiscoversOrphanedRecords pins the fix a real end-to-end run
// caught: a record-backed resource has no marker and no cloud object, so
// once its resource block is removed from configuration, nothing but the
// record store's own key listing can find it again. Without this, "remove
// the block and re-apply" - the only way to destroy anything under live
// resource markers (choudoufu destroy is refused outright) - would report
// "No changes" forever and leak the record. This test builds a projection
// from a config with NO resource blocks at all and a store already holding
// a record at an address that config does not declare, and checks the
// orphan is still materialized (so the plan can propose destroying it).
func TestBuildDiscoversOrphanedRecords(t *testing.T) {
	cfg := loadConfig(t, writeEmptyFixture(t))
	addr := mustAddr(t, `null_resource.trigger`)

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/test-estate"
	key := RecordKey(prefix, addr)

	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("orphan-id"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})
	payload, err := encodeRecordPayload(val, nil)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	if _, err := store.PutIfAbsent(context.Background(), key, payload); err != nil {
		t.Fatalf("seeding the store: %s", err)
	}

	provs := SingleProvider(nullProvider, nullResourceProvider())
	// No resolutions at all: the resource block is gone, so identity
	// resolution would never produce one for this address either.
	res, diags := BuildWith(context.Background(), cfg, nil, provs, Options{
		RecordStore:     store,
		RecordKeyPrefix: prefix,
	})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{`null_resource.trigger`})
	if len(res.RecordVersions) != 1 || res.RecordVersions[0].Addr.String() != addr.String() {
		t.Errorf("RecordVersions = %v, want one entry for %s", res.RecordVersions, addr)
	}

	inst := res.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object for the orphaned record-backed instance")
	}
	obj, err := inst.Current.Decode(nullResourceSchema().Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding the materialized object: %s", err)
	}
	if got := obj.Value.GetAttr("id"); got.AsString() != "orphan-id" {
		t.Errorf("id = %v, want orphan-id", got)
	}
}

// TestRecordKeyRoundTrips is [RecordAddr]'s own contract:
// discoverOrphanedRecords depends on recovering the exact address a key was
// built from, for every shape an address can take - a for_each string key
// with characters SSM parameter names forbid included.
func TestRecordKeyRoundTrips(t *testing.T) {
	const prefix = "tofu-records/my-estate"
	for _, addrStr := range []string{
		`null_resource.trigger`,
		`terraform_data.replacement`,
		`random_pet.name["a b"]`,
		`null_resource.trigger["needs escaping / \"quotes\""]`,
		`module.child.null_resource.trigger`,
	} {
		t.Run(addrStr, func(t *testing.T) {
			addr := mustAddr(t, addrStr)
			key := RecordKey(prefix, addr)
			got, ok := RecordAddr(prefix, key)
			if !ok {
				t.Fatalf("RecordAddr(%q, %q) reported not-ok", prefix, key)
			}
			if got.String() != addr.String() {
				t.Errorf("round trip: got %s, want %s", got, addr)
			}
		})
	}

	if _, ok := RecordAddr(prefix, "some/other/namespace/key"); ok {
		t.Error("RecordAddr accepted a key outside its own prefix")
	}
	if _, ok := RecordAddr(prefix, prefix+"/null_resource/not-valid-base64!!"); ok {
		t.Error("RecordAddr accepted a key whose last segment is not valid base64")
	}
}

// TestWriteBackPersistsAndDeletes exercises item 4 end to end: a
// successful apply's final state persists via PutIfVersion using the
// version read at plan time, and an instance that was record-backed at
// plan time but has vanished from the final state (a destroy) gets its
// record deleted with the same version check.
func TestWriteBackPersistsAndDeletes(t *testing.T) {
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/test-estate"

	keptAddr := mustAddr(t, `null_resource.kept`)
	destroyedAddr := mustAddr(t, `null_resource.destroyed`)

	// Seed both with a prior record, as if a plan had just read them.
	oldVal := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("old-id"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})
	oldPayload, err := encodeRecordPayload(oldVal, nil)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	keptVersion, err := store.PutIfAbsent(ctx, RecordKey(prefix, keptAddr), oldPayload)
	if err != nil {
		t.Fatalf("seeding kept: %s", err)
	}
	destroyedVersion, err := store.PutIfAbsent(ctx, RecordKey(prefix, destroyedAddr), oldPayload)
	if err != nil {
		t.Fatalf("seeding destroyed: %s", err)
	}

	// The final state after apply: kept updated with a new value, destroyed
	// gone entirely.
	finalState := states.NewState()
	schema := nullResourceSchema()
	newVal := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("new-id"),
		"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("changed")}),
	})
	obj := &states.ResourceInstanceObject{Status: states.ObjectReady, Value: newVal}
	src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), 0)
	if err != nil {
		t.Fatalf("encoding the final object: %s", err)
	}
	finalState.EnsureModule(keptAddr.Module).SetResourceInstanceCurrent(keptAddr.Resource, src, nullProvider, addrs.NoKey)

	schemas := &tofu.Schemas{
		Providers: map[addrs.Provider]providers.ProviderSchema{
			nullProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{"null_resource": schema},
			},
		},
	}

	diags := WriteBack(ctx, WriteBackRequest{
		Store:     store,
		KeyPrefix: prefix,
		PriorVersions: []RecordVersion{
			{Addr: keptAddr, Version: keptVersion},
			{Addr: destroyedAddr, Version: destroyedVersion},
		},
		FinalState: finalState,
		Schemas:    schemas,
	})
	assertNoErrors(t, diags)

	// kept: the record now holds the new value.
	gotPayload, _, exists, err := store.Get(ctx, RecordKey(prefix, keptAddr))
	if err != nil || !exists {
		t.Fatalf("kept record missing after write-back: exists=%v err=%v", exists, err)
	}
	gotVal, _, err := decodeRecordPayload(gotPayload)
	if err != nil {
		t.Fatalf("decoding the written-back payload: %s", err)
	}
	if got := gotVal.GetAttr("id"); got.AsString() != "new-id" {
		t.Errorf("written-back id = %v, want new-id", got)
	}

	// destroyed: the record is gone.
	_, _, exists, err = store.Get(ctx, RecordKey(prefix, destroyedAddr))
	if err != nil {
		t.Fatalf("checking the destroyed record: %s", err)
	}
	if exists {
		t.Error("destroyed record still exists after write-back")
	}
}

// TestWriteBackVersionConflict is item 4's loud-failure requirement: a
// VersionConflictError from the store must surface as a run-stopping error
// diagnostic naming both versions, never a silent overwrite.
func TestWriteBackVersionConflict(t *testing.T) {
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/test-estate"
	addr := mustAddr(t, `null_resource.trigger`)

	oldVal := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("old-id"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})
	oldPayload, err := encodeRecordPayload(oldVal, nil)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	key := RecordKey(prefix, addr)
	planTimeVersion, err := store.PutIfAbsent(ctx, key, oldPayload)
	if err != nil {
		t.Fatalf("seeding: %s", err)
	}

	// Simulate a second writer racing this run: the record changes after
	// this run's plan read planTimeVersion.
	racingVal := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("raced-id"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})
	racingPayload, err := encodeRecordPayload(racingVal, nil)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	actualVersion, err := store.PutIfVersion(ctx, key, racingPayload, planTimeVersion)
	if err != nil {
		t.Fatalf("simulating the race: %s", err)
	}

	finalState := states.NewState()
	schema := nullResourceSchema()
	newVal := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("this-runs-id"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})
	obj := &states.ResourceInstanceObject{Status: states.ObjectReady, Value: newVal}
	src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), 0)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	finalState.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, nullProvider, addrs.NoKey)

	schemas := &tofu.Schemas{
		Providers: map[addrs.Provider]providers.ProviderSchema{
			nullProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{"null_resource": schema},
			},
		},
	}

	diags := WriteBack(ctx, WriteBackRequest{
		Store:         store,
		KeyPrefix:     prefix,
		PriorVersions: []RecordVersion{{Addr: addr, Version: planTimeVersion}},
		FinalState:    finalState,
		Schemas:       schemas,
	})
	if !diags.HasErrors() {
		t.Fatal("want an error diagnostic for a version conflict, got none")
	}
	if !hasDiag(diags, "Record store write conflict", planTimeVersion) || !hasDiag(diags, "Record store write conflict", actualVersion) {
		t.Errorf("conflict diagnostic does not name both versions (%s, %s):\n%s", planTimeVersion, actualVersion, renderDiags(diags))
	}

	// Nothing was overwritten: the racing writer's value is still there.
	gotPayload, _, exists, err := store.Get(ctx, key)
	if err != nil || !exists {
		t.Fatalf("record missing after a failed write-back: exists=%v err=%v", exists, err)
	}
	gotVal, _, err := decodeRecordPayload(gotPayload)
	if err != nil {
		t.Fatalf("decoding: %s", err)
	}
	if got := gotVal.GetAttr("id"); got.AsString() != "raced-id" {
		t.Errorf("record was overwritten: id = %v, want raced-id (the racing writer's value, untouched)", got)
	}
}

// TestRecordKeyPrefixDisjointFromReceipts pins item 5's namespace-safety
// requirement: RecordKeyPrefix's default namespace can never produce a key
// that starts with live/RECEIPTS.md's "/tofu-receipts/" segment, for any
// estate name the marker grammar (live/MARKERS.md) allows - including an
// estate literally named "tofu-receipts", which still lands one segment
// too deep to collide.
func TestRecordKeyPrefixDisjointFromReceipts(t *testing.T) {
	const receiptsPrefix = "/tofu-receipts/"

	estates := []string{
		"a",
		"my-estate",
		"tofu-receipts",
		"tofu-receipts-lookalike",
		"prod-us-east-1",
	}
	for _, estate := range estates {
		prefix := RecordKeyPrefix(estate)
		// The SSM parameter name shape a real store builds: a leading "/"
		// plus the prefix, exactly what staterecord.SSMStore.parameterName
		// does with its KeyPrefix.
		simulated := "/" + prefix + "/" + "some-key"
		if strings.HasPrefix(simulated, receiptsPrefix) {
			t.Errorf("RecordKeyPrefix(%q) = %q, whose simulated parameter name %q starts with the receipts namespace %q", estate, prefix, simulated, receiptsPrefix)
		}
		if !strings.HasPrefix(prefix, recordNamespaceRoot+"/") {
			t.Errorf("RecordKeyPrefix(%q) = %q, want it rooted at %q", estate, prefix, recordNamespaceRoot)
		}
	}

	if recordNamespaceRoot == "tofu-receipts" {
		t.Fatal("recordNamespaceRoot literally equals the receipts namespace segment")
	}
}

// TestRecordKeyIsSSMSafe pins RecordKey's own answer to the character-set
// problem its doc comment describes: an address containing characters SSM
// parameter names forbid must still produce a key built entirely from safe
// characters.
func TestRecordKeyIsSSMSafe(t *testing.T) {
	addr := mustAddr(t, `null_resource.trigger["needs escaping / \"quotes\""]`)
	key := RecordKey("tofu-records/test-estate", addr)
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-', r == '/':
			continue
		default:
			t.Fatalf("RecordKey(%s) = %q contains the SSM-unsafe character %q", addr, key, r)
		}
	}
}
