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
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #353's projection half: the one bit that says a create-time
// provisioner failed on a resource whose prior state is otherwise read back
// out of the cloud.
//
// Every test here drives the REAL [WriteBack] and the REAL [BuildWith], the
// same two functions a live apply and the next live plan actually call. A
// test that wrote its own payload struct and read it back would prove
// nothing, which is the lesson issue #216's own regression test states in
// as many words.

const provisionedTestType = "aws_instance"

var provisionedTestProvider = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("aws"),
}

// provisionedTypeSchema is an ordinary marker-carrying cloud type: a string
// "id" and a "tags" map. It is deliberately NOT a record-backed type - the
// whole point of this namespace is the class of resource whose state lives
// in the cloud and therefore has nowhere to keep a tainted bit.
func provisionedTypeSchema() providers.Schema {
	return providers.Schema{
		Version: 0,
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":            {Type: cty.String, Computed: true},
				"ami":           {Type: cty.String, Optional: true},
				"instance_type": {Type: cty.String, Optional: true},
				"tags":          {Type: cty.Map(cty.String), Optional: true},
			},
		},
	}
}

func provisionedTypeProvider() providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{provisionedTestType: provisionedTypeSchema()},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{
			ImportedResources: []providers.ImportedResource{{
				TypeName: req.TypeName,
				State: cty.ObjectVal(map[string]cty.Value{
					"id":            cty.StringVal(req.Target.ID),
					"ami":           cty.StringVal("ami-1234"),
					"instance_type": cty.StringVal("t3.micro"),
					"tags":          cty.MapValEmpty(cty.String),
				}),
			}},
		}
	}
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: req.PriorState}
	}
	return p
}

// writeProvisionedFixture writes a one-resource module, with or without a
// create-time provisioner on it. The two spellings differ in that block and
// nothing else, which is the mutation check built into the fixture's own
// shape: the gate on both halves of this mechanism reads exactly that
// block, so a gate that stopped reading it makes the two fixtures agree and
// a test fails whichever way it broke.
func writeProvisionedFixture(t *testing.T, withProvisioner bool) string {
	t.Helper()
	dir := t.TempDir()
	prov := ""
	if withProvisioner {
		prov = `
  provisioner "local-exec" {
    command = "echo provisioned"
  }
`
	}
	src := `
resource "` + provisionedTestType + `" "web" {
  ami           = "ami-1234"
  instance_type = "t3.micro"
` + prov + `}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return dir
}

// provisionedFinalState builds the state an apply leaves behind for the one
// instance, at the given status - ObjectTainted being exactly what
// internal/tofu's maybeTainted produces when a create-time provisioner
// fails, and what internal/engine/applying's ManagedApply produces for the
// same failure in the second apply engine.
func provisionedFinalState(t *testing.T, addr addrs.AbsResourceInstance, status states.ObjectStatus) *states.State {
	t.Helper()
	schema := provisionedTypeSchema()
	obj := &states.ResourceInstanceObject{
		Status: status,
		Value: cty.ObjectVal(map[string]cty.Value{
			"id":            cty.StringVal("i-0abcdef"),
			"ami":           cty.StringVal("ami-1234"),
			"instance_type": cty.StringVal("t3.micro"),
			"tags":          cty.MapValEmpty(cty.String),
		}),
	}
	src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), 0)
	if err != nil {
		t.Fatalf("encoding the applied object: %s", err)
	}
	st := states.NewState()
	st.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, provisionedTestProvider, addrs.NoKey)
	return st
}

func provisionedSchemas() *tofu.Schemas {
	return &tofu.Schemas{
		Providers: map[addrs.Provider]providers.ProviderSchema{
			provisionedTestProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{provisionedTestType: provisionedTypeSchema()},
			},
		},
	}
}

// TestFailedProvisionerTaintSurvivesWriteBackAndMaterialization is the
// feature, end to end through the two real functions.
//
// The claim: an apply whose create-time provisioner failed leaves a live,
// fully-marked cloud object behind, and the NEXT plan must see that object
// as tainted - which stock turns into a synthetic Replace, which re-runs
// the provisioner. Without the record the next plan reads the object back
// as healthy and the provisioner never runs again: a silent under-run, and
// one no verdict-level check can see, which is why this assertion is on
// inst.Current.Status and not on a count or a boolean.
func TestFailedProvisionerTaintSurvivesWriteBackAndMaterialization(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	dir := writeProvisionedFixture(t, true)
	cfg := loadConfig(t, dir)
	addr := mustAddr(t, provisionedTestType+`.web`)

	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)

	// Step 1: the apply. Its final state carries the object stock's
	// maybeTainted marked tainted when the provisioner failed.
	diags := WriteBack(ctx, WriteBackRequest{
		Store:      provisioned.rs,
		Config:     cfg,
		FinalState: provisionedFinalState(t, addr, states.ObjectTainted),
		Schemas:    provisionedSchemas(),
	})
	assertNoErrors(t, diags)

	// The wire format actually says so, rather than "whatever WriteBack
	// felt like".
	raw, _, exists, err := store.Get(ctx, RecordKey(RecordKeyPrefix(estate), addr))
	if err != nil || !exists {
		t.Fatalf("no provisioner record after write-back: exists=%v err=%v", exists, err)
	}
	if !strings.Contains(string(raw), `"tainted":true`) {
		t.Errorf("the persisted payload does not record the taint: %s", raw)
	}

	// Step 2: the next plan's projection, through the real builder.
	provs := SingleProvider(provisionedTestProvider, provisionedTypeProvider())
	res, diags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: "i-0abcdef"}},
		provs, Options{RecordStore: provisioned.rs})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{provisionedTestType + `.web`})

	inst := res.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object for the materialized instance")
	}
	// The exact field the stock plan graph consults:
	// node_resource_abstract_instance.go treats a tainted prior object as
	// if the object did not exist and then rewrites the change into a
	// synthetic Replace.
	if inst.Current.Status != states.ObjectTainted {
		t.Errorf("materialized instance status = %s, want ObjectTainted.\n"+
			"A failed create-time provisioner was written back and read back as healthy: the object is live, marked, and unprovisioned, and no later plan will ever say so.",
			inst.Current.Status)
	}

	if len(res.EnvelopeVersions) != 1 || res.EnvelopeVersions[0].Addr.String() != addr.String() {
		t.Errorf("EnvelopeVersions = %v, want one entry for %s so write-back can open a conditional Put or Delete", res.EnvelopeVersions, addr)
	}
	// GitHub issue #364 merged what used to be four separate version lists
	// (RecordVersions stays its own for kind=object; located, residue and
	// provisioned now share EnvelopeVersions) into two. A provisioner-taint
	// version must still never leak into the kind=object list.
	if len(res.RecordVersions) != 0 {
		t.Errorf("a provisioner-taint version leaked into the record-backed version list: %v", res.RecordVersions)
	}
}

// TestSucceedingProvisionerLeavesNoRecord is the negative control, and it
// is the half that keeps the mechanism from becoming a resource that
// replaces itself forever.
//
// Two directions, because only asserting the first would pass against an
// implementation that never writes anything at all: a clean apply writes
// nothing, and a clean apply CLEARS a record an earlier failure left.
func TestSucceedingProvisionerLeavesNoRecord(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	cfg := loadConfig(t, writeProvisionedFixture(t, true))
	addr := mustAddr(t, provisionedTestType+`.web`)

	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)

	// Direction one: a clean apply with nothing recorded before it.
	diags := WriteBack(ctx, WriteBackRequest{
		Store:      provisioned.rs,
		Config:     cfg,
		FinalState: provisionedFinalState(t, addr, states.ObjectReady),
		Schemas:    provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, err := provisioned.Get(ctx, addr); err != nil || exists {
		t.Fatalf("a successful provisioner left a record behind: exists=%v err=%v", exists, err)
	}

	// Direction two: a previous run's failure is on record, this run's
	// provisioner succeeds, and the record has to go - or the estate
	// proposes replacing this resource on every plan from here on.
	//
	// GitHub issue #364 unit A2: direction one's WriteBack already gave
	// this address a kind=identity envelope (an ordinary aws_instance now
	// gets one too), so seeding the stale taint has to start from THAT
	// version rather than from "" (no record yet) - the same conditional
	// write every other writer in this package makes.
	_, seedVersion, _, err := store.Get(ctx, RecordKey(RecordKeyPrefix(estate), addr))
	if err != nil {
		t.Fatalf("reading the identity envelope's version before seeding the stale taint: %s", err)
	}
	version, err := provisioned.Put(ctx, addr, seedVersion)
	if err != nil {
		t.Fatalf("seeding the stale record: %s", err)
	}
	diags = WriteBack(ctx, WriteBackRequest{
		Store:            provisioned.rs,
		EnvelopeVersions: []RecordVersion{{Addr: addr, Version: version}},
		Config:           cfg,
		FinalState:       provisionedFinalState(t, addr, states.ObjectReady),
		Schemas:          provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, err := provisioned.Get(ctx, addr); err != nil || exists {
		t.Errorf("a stale taint record survived a successful re-run: exists=%v err=%v.\n"+
			"The resource would be proposed for replacement on every plan from now on.", exists, err)
	}
}

// TestDestroyClearsTheProvisionerRecord: the instance is gone from the
// final state entirely, so nothing about it is worth remembering. The
// delete side walks the versions this run's plan read, which is exactly the
// set that can be conditionally deleted at all.
func TestDestroyClearsTheProvisionerRecord(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	cfg := loadConfig(t, writeProvisionedFixture(t, true))
	addr := mustAddr(t, provisionedTestType+`.web`)

	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)
	version, err := provisioned.Put(ctx, addr, "")
	if err != nil {
		t.Fatalf("seeding the record: %s", err)
	}

	diags := WriteBack(ctx, WriteBackRequest{
		Store:            provisioned.rs,
		EnvelopeVersions: []RecordVersion{{Addr: addr, Version: version}},
		Config:           cfg,
		FinalState:       states.NewState(),
		Schemas:          provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, _ := store.Get(ctx, RecordKey(RecordKeyPrefix(estate), addr)); exists {
		t.Error("the taint record survived the resource being destroyed")
	}
}

// TestNoProvisionerDeclaredFabricatesNoRecord is the gate, asserted from
// the write side.
//
// A tainted object with NO create-time provisioner in its configuration is
// a different thing entirely - a create the PROVIDER failed halfway through
// - and issue #353 deliberately did not widen to cover it. Widening would
// make every partially-created cloud resource force a replace on the next
// plan, which is a larger parity change that nothing here has measured.
//
// This is also the create-vs-adopt case: an instance an operator brought in
// with `live-import` has never had a provisioner run against it by this
// tool, and nothing may fabricate a record saying otherwise.
func TestNoProvisionerDeclaredFabricatesNoRecord(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	cfg := loadConfig(t, writeProvisionedFixture(t, false))
	addr := mustAddr(t, provisionedTestType+`.web`)

	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)

	diags := WriteBack(ctx, WriteBackRequest{
		Store:      provisioned.rs,
		Config:     cfg,
		FinalState: provisionedFinalState(t, addr, states.ObjectTainted),
		Schemas:    provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, _ := provisioned.Get(ctx, addr); exists {
		t.Error("a taint record was fabricated for a resource whose configuration declares no create-time provisioner")
	}
}

// TestDestroyTimeProvisionerNeedsNoRecord pins the ruling that the
// destroy-time case needs no storage at all, at the level the code actually
// decides it.
//
// Stock only runs a destroy-time provisioner when it is also calling the
// provider's delete, strictly before it. On failure the delete never
// happens and the live object survives with its marker intact, so the
// marker's continued existence already is the "still needs destroying"
// signal. A `when = destroy` block must therefore neither open the gate nor
// cause a record to be written.
func TestDestroyTimeProvisionerNeedsNoRecord(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	dir := t.TempDir()
	src := `
resource "` + provisionedTestType + `" "web" {
  ami           = "ami-1234"
  instance_type = "t3.micro"

  provisioner "local-exec" {
    when    = destroy
    command = "echo goodbye"
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	cfg := loadConfig(t, dir)
	addr := mustAddr(t, provisionedTestType+`.web`)

	// The fixture really does carry a provisioner, so this test cannot pass
	// by parsing nothing.
	modCfg, ok := identity.ConfigForModule(cfg, addr.Module)
	if !ok || modCfg.Module == nil {
		t.Fatal("the fixture did not load")
	}
	rc := modCfg.Module.ManagedResources[addr.Resource.Resource.String()]
	if rc == nil || rc.Managed == nil || len(rc.Managed.Provisioners) != 1 {
		t.Fatal("the fixture's destroy-time provisioner did not parse - the test setup is broken, not the code under test")
	}
	if rc.Managed.Provisioners[0].When != configs.ProvisionerWhenDestroy {
		t.Fatalf("the fixture's provisioner parsed as %v, want destroy-time", rc.Managed.Provisioners[0].When)
	}

	if declaresCreateProvisioners(cfg, addr) {
		t.Error("a `when = destroy` provisioner opened the create-time gate")
	}

	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)
	diags := WriteBack(ctx, WriteBackRequest{
		Store:      provisioned.rs,
		Config:     cfg,
		FinalState: provisionedFinalState(t, addr, states.ObjectTainted),
		Schemas:    provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, _ := provisioned.Get(ctx, addr); exists {
		t.Error("a destroy-time provisioner caused a taint record to be written; the marker is already the signal")
	}
}

// TestRecordBackedTaintStaysInItsOwnRecord: a record-backed type already
// carries its tainted bit inside its own record (recordPayload.Status,
// issue #216). Two sources for one fact could disagree, so this namespace
// stays out of it.
func TestRecordBackedTaintStaysInItsOwnRecord(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	dir := t.TempDir()
	src := `
resource "null_resource" "trigger" {
  provisioner "local-exec" {
    command = "echo provisioned"
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	cfg := loadConfig(t, dir)
	addr := mustAddr(t, `null_resource.trigger`)

	ti, ok := identity.LookupType("null_resource")
	if !ok || !ti.RecordBacked {
		t.Fatal("null_resource is not record-backed any more; this test's premise moved")
	}

	schema := nullResourceSchema()
	obj := &states.ResourceInstanceObject{
		Status: states.ObjectTainted,
		Value: cty.ObjectVal(map[string]cty.Value{
			"id":       cty.StringVal("half-created"),
			"triggers": cty.NullVal(cty.Map(cty.String)),
		}),
	}
	src2, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), 0)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	finalState := states.NewState()
	finalState.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src2, nullProvider, addrs.NoKey)

	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)
	diags := WriteBack(ctx, WriteBackRequest{
		Store:      provisioned.rs,
		Config:     cfg,
		FinalState: finalState,
		Schemas: &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
			nullProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{"null_resource": schema},
			},
		}},
	})
	assertNoErrors(t, diags)
	// GitHub issue #364: there is no separate "provisioned namespace" any
	// more for a second copy to land in - a record-backed instance's kind
	// object record legitimately lives at this very key, carrying its own
	// tainted bit (objectFields.Status). What must still be true is that
	// nothing ALSO wrote a Provisioned member alongside it: two sources for
	// one fact, which could disagree.
	env, _, exists, err := provisioned.rs.getRaw(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("the record-backed instance's own record is missing after write-back: exists=%v err=%v", exists, err)
	}
	if env.Kind != recordKindObject || env.Object == nil {
		t.Fatalf("the record for a record-backed instance is not kind=object: %+v", env)
	}
	if env.Object.Status != recordStatusTainted {
		t.Errorf("the record-backed instance's own object.status = %q, want %q - its tainted bit has no other carrier", env.Object.Status, recordStatusTainted)
	}
	if env.Provisioned != nil {
		t.Error("a record-backed instance got a second copy of its tainted bit in the envelope's Provisioned member; recordPayload.Status is the only carrier it should ever have")
	}
}

// TestProvisionedRecordForAnotherAddressIsRefused: a key copied, renamed or
// hand-edited into pointing at another resource must be refused rather than
// answered from. Answering would force a replacement of a live object that
// never had a provisioner fail on it - a destructive action taken on
// another resource's evidence.
func TestProvisionedRecordForAnotherAddressIsRefused(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)
	mine := mustAddr(t, provisionedTestType+`.web`)
	theirs := mustAddr(t, provisionedTestType+`.other`)

	// Write a legitimate record for `theirs`, then move its payload under
	// `mine`'s key - which is exactly what a hand-copied key looks like.
	if _, err := provisioned.Put(ctx, theirs, ""); err != nil {
		t.Fatalf("seeding: %s", err)
	}
	payload, _, _, err := store.Get(ctx, RecordKey(RecordKeyPrefix(estate), theirs))
	if err != nil {
		t.Fatalf("reading the seed: %s", err)
	}
	if _, err := store.PutIfAbsent(ctx, RecordKey(RecordKeyPrefix(estate), mine), payload); err != nil {
		t.Fatalf("planting the mismatched key: %s", err)
	}

	if _, _, _, err := provisioned.Get(ctx, mine); err == nil {
		t.Error("a record naming another address was accepted; it would force a replacement of the wrong resource")
	}
}

// TestUnreadableProvisionedRecordStopsTheRun: unlike a residue record,
// which is a visible nuisance when it cannot be read, an unreadable record
// HERE would be read as "no provisioner ever failed" - the exact silent
// under-run this namespace exists to prevent. So it is an error, the
// instance is omitted, and the run stops.
func TestUnreadableProvisionedRecordStopsTheRun(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	cfg := loadConfig(t, writeProvisionedFixture(t, true))
	addr := mustAddr(t, provisionedTestType+`.web`)

	store := localHintStore(t)
	if _, err := store.PutIfAbsent(ctx, RecordKey(RecordKeyPrefix(estate), addr), []byte(`{"formatVersion":"from-the-future"}`)); err != nil {
		t.Fatalf("planting the unreadable record: %s", err)
	}

	provs := SingleProvider(provisionedTestProvider, provisionedTypeProvider())
	res, diags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: "i-0abcdef"}},
		provs, Options{RecordStore: newTestProvisionedStore(store, estate).rs})

	if !diags.HasErrors() {
		t.Fatal("an unreadable provisioner record produced no error; the object would be reported healthy")
	}
	// GitHub issue #364 unit B moved where this run first meets the garbage
	// payload: applyRecordFirst's GetIdentity call now decodes the same
	// envelope ahead of the class-routed path that used to reach it only
	// through applyProvisionedTaint, so the decode failure surfaces as the
	// generic "Cannot read a persisted record" rather than
	// SummaryProvisionedUnreadable specifically. Both stop the run on the
	// same garbage record with zero materialized; which of the two readers
	// gets there first is no longer meaningful once every resolution reads
	// the record before its identity.Class is routed.
	const wantSummary = "Cannot read a persisted record"
	var found bool
	for _, d := range diags {
		if d.Description().Summary == wantSummary {
			found = true
		}
	}
	if !found {
		t.Errorf("no %q diagnostic; got %v", wantSummary, diags)
	}
	if len(res.Materialized) != 0 {
		t.Errorf("Materialized = %v, want empty - an instance whose provisioner history cannot be read must not enter the prior state", res.Materialized)
	}
}

// TestProvisionedStoreIsNotConsultedWithoutAProvisioner: the read gate is
// the same question as the write gate, and it is what keeps an estate with
// no provisioners anywhere from paying a store round trip per instance.
//
// Asserted by planting a record that WOULD taint the instance if it were
// read, on a fixture with no provisioner block, and requiring the plan to
// come out healthy.
func TestProvisionedStoreIsNotConsultedWithoutAProvisioner(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	cfg := loadConfig(t, writeProvisionedFixture(t, false))
	addr := mustAddr(t, provisionedTestType+`.web`)

	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)
	if _, err := provisioned.Put(ctx, addr, ""); err != nil {
		t.Fatalf("seeding: %s", err)
	}

	provs := SingleProvider(provisionedTestProvider, provisionedTypeProvider())
	res, diags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: "i-0abcdef"}},
		provs, Options{RecordStore: provisioned.rs})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{provisionedTestType + `.web`})

	inst := res.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object")
	}
	if inst.Current.Status != states.ObjectReady {
		t.Errorf("status = %s, want ObjectReady: a record for a resource with no provisioner block must be inert, not a standing order to replace it", inst.Current.Status)
	}
	// GitHub issue #364: EnvelopeVersions is no longer provisioned-specific
	// - fillResidueFor's own, unrelated, unconditional consultation of the
	// same shared key populates it regardless of whether a provisioner is
	// declared, so its emptiness can no longer stand in for
	// "applyProvisionedTaint never ran". The Status assertion above is the
	// one that actually proves the taint record was never consulted: if it
	// had been, this instance would have come back ObjectTainted.
}

func TestProvisionedRecordCarriesNoProvisionerContent(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)
	addr := mustAddr(t, provisionedTestType+`.web`)
	if _, err := provisioned.Put(ctx, addr, ""); err != nil {
		t.Fatalf("Put: %s", err)
	}
	payload, _, _, err := store.Get(ctx, RecordKey(RecordKeyPrefix(estate), addr))
	if err != nil {
		t.Fatalf("Get: %s", err)
	}
	got := string(payload)
	for _, forbidden := range []string{"hash", "digest", "command", "fingerprint", "sha", "content", "config"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Errorf("the payload carries %q: %s\n"+
				"This namespace stores one bit. A memory of the provisioner's CONTENT is the rejected design - stock has none, and live/RECEIPTS.md forbids the pattern by name.", forbidden, got)
		}
	}
}

// provisionedAbsentProvider is [provisionedTypeProvider] for the moment
// after an operator has deleted the half-built object by hand: the import
// lookup answers with an empty ImportedResources list, which build.go's
// importAndRead turns into statusAbsent and materialize turns into "the
// plan will propose creating it".
//
// It is the leg of the reproduction below that nothing else in this file
// exercises, and it is the leg where the old implementation went wrong:
// materialize returns at statusAbsent, so applyProvisionedTaint never runs
// and ProvisionedVersions comes back empty for an address that has a
// record.
func provisionedAbsentProvider() providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{provisionedTestType: provisionedTypeSchema()},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(_ providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{}
	}
	return p
}

// TestStaleTaintDoesNotSurviveAHealthyRecreate is GitHub issue #353's
// severe follow-on defect, reproduced in the three legs an operator
// actually walks and then asserted fixed.
//
// The claim being defended is the inverse of
// [TestFailedProvisionerTaintSurvivesWriteBackAndMaterialization]'s. That
// one says a real failure must survive. This one says an ALREADY-FIXED
// failure must not, because a taint record that outlives the failure it
// describes does not merely mislead: the next plan reads it, sets
// ObjectTainted on a live, healthy, correctly provisioned object, and says
// "must be replaced". A resource carrying real data is then destroyed, and
// the reason printed to the operator is a provisioner failure that was
// fixed two applies ago. HANDOFF.md ranks a wrong marker above a missing
// one for precisely this shape.
//
// The three legs, each through the real WriteBack and the real BuildWith:
//
//  1. The apply fails on the provisioner. A record is written. Correct.
//  2. The operator deletes the half-built object by hand. The next plan
//     reads it as ABSENT, so nothing consults the record and
//     ProvisionedVersions comes back empty - asserted here rather than
//     assumed, because that emptiness is the whole mechanism of the bug.
//  3. The apply re-creates the object and the provisioner SUCCEEDS. The
//     record has to be gone afterwards, and the plan after THAT has to
//     read the object as healthy.
//
// Before the fix, leg 3 deleted nothing: the healthy branch fell through to
// a delete loop that walked the (empty) ProvisionedVersions, so the record
// survived and the fourth leg's plan came back ObjectTainted.
func TestStaleTaintDoesNotSurviveAHealthyRecreate(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	cfg := loadConfig(t, writeProvisionedFixture(t, true))
	addr := mustAddr(t, provisionedTestType+`.web`)

	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)

	// Leg 1: the create-time provisioner failed, so the apply's final state
	// carries a tainted object and write-back records it.
	diags := WriteBack(ctx, WriteBackRequest{
		Store:      provisioned.rs,
		Config:     cfg,
		FinalState: provisionedFinalState(t, addr, states.ObjectTainted),
		Schemas:    provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, err := store.Get(ctx, RecordKey(RecordKeyPrefix(estate), addr)); err != nil || !exists {
		t.Fatalf("leg 1 wrote no record, so the rest of this test would prove nothing: exists=%v err=%v", exists, err)
	}

	// Leg 2: the operator deleted the half-built object. The plan reads
	// absence and proposes a plain create.
	//
	// GitHub issue #364 unit B changed what populates EnvelopeVersions here:
	// applyRecordFirst now reads the record for every resolution - this one
	// included, since leg 1's WriteBack wrote it an identity alongside its
	// taint bit - BEFORE materialize ever asks the provider whether the
	// object still exists, so the version is captured regardless of how
	// that read comes out. It is no longer proof that the taint record was
	// never consulted (recordEnvelopeVersion runs on the read, not on the
	// taint check specifically), but the version it captures is exactly
	// what leg 3's WriteBack needs to clear the record correctly, and that
	// is what the rest of this test still exercises end to end.
	absent, diags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: "i-0abcdef"}},
		SingleProvider(provisionedTestProvider, provisionedAbsentProvider()),
		Options{RecordStore: provisioned.rs})
	assertNoErrors(t, diags)
	assertOmitted(t, absent, map[string]Reason{provisionedTestType + `.web`: ReasonAbsent})
	if len(absent.EnvelopeVersions) != 1 || absent.EnvelopeVersions[0].Addr.String() != addr.String() {
		t.Fatalf("EnvelopeVersions = %v, want exactly one entry for %s: the record-first read (#364 unit B) captures the version whether or not the object is still there.",
			absent.EnvelopeVersions, addr)
	}

	// Leg 3: the apply creates the object and the provisioner succeeds this
	// time, so the final state is healthy - and this run's plan holds no
	// version for the record it has to clear.
	diags = WriteBack(ctx, WriteBackRequest{
		Store:            provisioned.rs,
		EnvelopeVersions: absent.EnvelopeVersions,
		Config:           cfg,
		FinalState:       provisionedFinalState(t, addr, states.ObjectReady),
		Schemas:          provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, err := provisioned.Get(ctx, addr); err != nil || exists {
		t.Errorf("the taint record survived a successful re-provision: exists=%v err=%v.\n"+
			"The provisioner ran and succeeded on a freshly created object. Nothing about that resource is tainted, and the record now says otherwise.", exists, err)
	}

	// The consequence, asserted where an operator would meet it: the next
	// plan reads the live object.
	res, diags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: "i-0abcdef"}},
		SingleProvider(provisionedTestProvider, provisionedTypeProvider()),
		Options{RecordStore: provisioned.rs})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{provisionedTestType + `.web`})
	inst := res.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object for the materialized instance")
	}
	if inst.Current.Status != states.ObjectReady {
		t.Errorf("materialized instance status = %s, want ObjectReady.\n"+
			"A live, healthy, correctly provisioned resource is about to be destroyed and re-created, and the reason the plan gives is a provisioner failure that was fixed an apply ago.",
			inst.Current.Status)
	}
}

// TestStaleTaintIsClearedWhenTheBlockComesBackAndSucceeds is the second way
// a plan never reads a record that exists: the resource block was not in
// the configuration at all, so the read gate
// ([resourceDeclaresCreateProvisioners] on a nil block) closed and nothing
// was consulted.
//
// The apply that puts the block back and provisions it cleanly has to leave
// no record, for TestStaleTaintDoesNotSurviveAHealthyRecreate's reason. The
// difference is only in why ProvisionedVersions is empty, and both ways
// have to be closed by the same gate or the fix is half a fix.
func TestStaleTaintIsClearedWhenTheBlockComesBackAndSucceeds(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	addr := mustAddr(t, provisionedTestType+`.web`)
	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)

	// A record stands from an older apply, from before the block was taken
	// out of the configuration.
	if _, err := provisioned.Put(ctx, addr, ""); err != nil {
		t.Fatalf("seeding the standing record: %s", err)
	}

	// An apply with the block absent from the configuration reads nothing
	// and settles nothing: the record is inert, which is the documented
	// behaviour and is parity with stock's own tainted bit surviving an
	// edit to the provisioner block.
	diags := WriteBack(ctx, WriteBackRequest{
		Store:      provisioned.rs,
		Config:     loadConfig(t, writeProvisionedFixture(t, false)),
		FinalState: provisionedFinalState(t, addr, states.ObjectReady),
		Schemas:    provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, _ := store.Get(ctx, RecordKey(RecordKeyPrefix(estate), addr)); !exists {
		t.Fatal("the record was swept by an apply whose configuration declares no create-time provisioner, which is a gate this mechanism does not have and must not grow: the read side is gated the same way, so sweeping here would mean writing and reading different sets")
	}

	// The block comes back, with its provisioner, and this time the
	// provisioner succeeds. The record must go.
	diags = WriteBack(ctx, WriteBackRequest{
		Store:      provisioned.rs,
		Config:     loadConfig(t, writeProvisionedFixture(t, true)),
		FinalState: provisionedFinalState(t, addr, states.ObjectReady),
		Schemas:    provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, err := provisioned.Get(ctx, addr); err != nil || exists {
		t.Errorf("a record from before the block was removed survived the block's return and a clean provision: exists=%v err=%v", exists, err)
	}
}

// TestTaintIsRecordedOverAStaleRecordFromAnEarlierIncarnation is the same
// hole seen from the write side.
//
// The address had a record from an earlier incarnation, this run's plan
// never read it (the object was absent), the re-create's provisioner failed
// AGAIN, and the taint still has to be recorded. A conditional Put made
// against priorVersion's "" would be rejected by the store as a conflict
// with the key that is already there, and the taint for a genuinely failed
// provisioner would be lost.
func TestTaintIsRecordedOverAStaleRecordFromAnEarlierIncarnation(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	cfg := loadConfig(t, writeProvisionedFixture(t, true))
	addr := mustAddr(t, provisionedTestType+`.web`)

	store := localHintStore(t)
	provisioned := newTestProvisionedStore(store, estate)
	if _, err := provisioned.Put(ctx, addr, ""); err != nil {
		t.Fatalf("seeding the standing record: %s", err)
	}

	diags := WriteBack(ctx, WriteBackRequest{
		Store:      provisioned.rs,
		Config:     cfg,
		FinalState: provisionedFinalState(t, addr, states.ObjectTainted),
		Schemas:    provisionedSchemas(),
	})
	assertNoErrors(t, diags)

	raw, _, exists, err := store.Get(ctx, RecordKey(RecordKeyPrefix(estate), addr))
	if err != nil || !exists {
		t.Fatalf("the taint went unrecorded because a record from an earlier incarnation was in the way: exists=%v err=%v", exists, err)
	}
	if !strings.Contains(string(raw), `"tainted":true`) {
		t.Errorf("the persisted payload does not record the taint: %s", raw)
	}
}

// TestProvisionedWriteConflictStopsTheRun pins Finding 4's answer: a
// concurrent writer that changed the record between this run's plan and its
// apply produces an ERROR, the same severity [writeBackConflictDiag] gives
// the identical *staterecord.VersionConflictError on the record-backed
// half, rather than a warning at the tail of a long apply.
//
// Both directions, because they lose different things and both are the
// silent under-run this namespace exists to prevent: a taint that cannot be
// written is a bit lost, and a stale record that cannot be cleared is a
// healthy resource destroyed on the next plan.
//
// The other writer is simulated by handing WriteBack a plan-time version
// the store does not hold, which is exactly what this run would be holding
// after somebody else rewrote the key. It is not simulated by writing the
// key twice: [staterecord.LocalStore]'s version is derived from the
// content, so re-writing the same one-bit payload produces the same version
// and no conflict at all - which is correct behaviour and useless for
// testing this.
func TestProvisionedWriteConflictStopsTheRun(t *testing.T) {
	const estate = "provisioned-estate"
	const otherWriter = "a-version-this-store-never-held"

	for _, tc := range []struct {
		name   string
		status states.ObjectStatus
	}{
		{
			name:   "the taint cannot be written",
			status: states.ObjectTainted,
		},
		{
			name:   "the stale record cannot be cleared",
			status: states.ObjectReady,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cfg := loadConfig(t, writeProvisionedFixture(t, true))
			addr := mustAddr(t, provisionedTestType+`.web`)

			store := localHintStore(t)
			provisioned := newTestProvisionedStore(store, estate)
			if _, err := provisioned.Put(ctx, addr, ""); err != nil {
				t.Fatalf("seeding: %s", err)
			}

			diags := WriteBack(ctx, WriteBackRequest{
				Store:            provisioned.rs,
				EnvelopeVersions: []RecordVersion{{Addr: addr, Version: otherWriter}},
				Config:           cfg,
				FinalState:       provisionedFinalState(t, addr, tc.status),
				Schemas:          provisionedSchemas(),
			})
			if !diags.HasErrors() {
				t.Fatalf("a rejected conditional write did not stop the run: %v", diags.ErrWithWarnings())
			}
			// GitHub issue #364: a provisioner-taint conflict now shares the
			// SAME merged-envelope write as the located and residue
			// concerns, so it is reported through the same generic
			// "Record store write conflict" diagnostic the record-backed
			// half already used - not a provisioner-specific message,
			// because the physical write it failed on is no longer a
			// provisioner-specific operation.
			var found bool
			for _, d := range diags {
				if d.Severity() != tfdiags.Error || d.Description().Summary != "Record store write conflict" {
					continue
				}
				found = true
				detail := d.Description().Detail
				if !strings.Contains(detail, "another writer changed it") {
					t.Errorf("the conflict is reported without naming both versions: %s", detail)
				}
				if !strings.Contains(detail, otherWriter) {
					t.Errorf("the conflict does not name the version this run expected (%q): %s", otherWriter, detail)
				}
			}
			if !found {
				t.Errorf("no %q error was raised: %v", "Record store write conflict", diags.ErrWithWarnings())
			}
		})
	}
}
