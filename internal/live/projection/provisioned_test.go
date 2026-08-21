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
	provisioned := NewProvisionedStore(store, estate)

	// Step 1: the apply. Its final state carries the object stock's
	// maybeTainted marked tainted when the provisioner failed.
	diags := WriteBack(ctx, WriteBackRequest{
		ProvisionedStore: provisioned,
		Config:           cfg,
		FinalState:       provisionedFinalState(t, addr, states.ObjectTainted),
		Schemas:          provisionedSchemas(),
	})
	assertNoErrors(t, diags)

	// The wire format actually says so, rather than "whatever WriteBack
	// felt like".
	raw, _, exists, err := store.Get(ctx, ProvisionedKey(estate, addr))
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
		provs, Options{ProvisionedStore: provisioned})
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

	if len(res.ProvisionedVersions) != 1 || res.ProvisionedVersions[0].Addr.String() != addr.String() {
		t.Errorf("ProvisionedVersions = %v, want one entry for %s so write-back can open a conditional Put or Delete", res.ProvisionedVersions, addr)
	}
	if len(res.RecordVersions) != 0 || len(res.LocatedVersions) != 0 || len(res.ResidueVersions) != 0 {
		t.Errorf("a provisioner-taint version leaked into another namespace's version list: record=%v located=%v residue=%v",
			res.RecordVersions, res.LocatedVersions, res.ResidueVersions)
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
	provisioned := NewProvisionedStore(store, estate)

	// Direction one: a clean apply with nothing recorded before it.
	diags := WriteBack(ctx, WriteBackRequest{
		ProvisionedStore: provisioned,
		Config:           cfg,
		FinalState:       provisionedFinalState(t, addr, states.ObjectReady),
		Schemas:          provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, err := store.Get(ctx, ProvisionedKey(estate, addr)); err != nil || exists {
		t.Fatalf("a successful provisioner left a record behind: exists=%v err=%v", exists, err)
	}

	// Direction two: a previous run's failure is on record, this run's
	// provisioner succeeds, and the record has to go - or the estate
	// proposes replacing this resource on every plan from here on.
	version, err := provisioned.Put(ctx, addr, "")
	if err != nil {
		t.Fatalf("seeding the stale record: %s", err)
	}
	diags = WriteBack(ctx, WriteBackRequest{
		ProvisionedStore:    provisioned,
		ProvisionedVersions: []RecordVersion{{Addr: addr, Version: version}},
		Config:              cfg,
		FinalState:          provisionedFinalState(t, addr, states.ObjectReady),
		Schemas:             provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, err := store.Get(ctx, ProvisionedKey(estate, addr)); err != nil || exists {
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
	provisioned := NewProvisionedStore(store, estate)
	version, err := provisioned.Put(ctx, addr, "")
	if err != nil {
		t.Fatalf("seeding the record: %s", err)
	}

	diags := WriteBack(ctx, WriteBackRequest{
		ProvisionedStore:    provisioned,
		ProvisionedVersions: []RecordVersion{{Addr: addr, Version: version}},
		Config:              cfg,
		FinalState:          states.NewState(),
		Schemas:             provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, _ := store.Get(ctx, ProvisionedKey(estate, addr)); exists {
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
	provisioned := NewProvisionedStore(store, estate)

	diags := WriteBack(ctx, WriteBackRequest{
		ProvisionedStore: provisioned,
		Config:           cfg,
		FinalState:       provisionedFinalState(t, addr, states.ObjectTainted),
		Schemas:          provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, _ := store.Get(ctx, ProvisionedKey(estate, addr)); exists {
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
	provisioned := NewProvisionedStore(store, estate)
	diags := WriteBack(ctx, WriteBackRequest{
		ProvisionedStore: provisioned,
		Config:           cfg,
		FinalState:       provisionedFinalState(t, addr, states.ObjectTainted),
		Schemas:          provisionedSchemas(),
	})
	assertNoErrors(t, diags)
	if _, _, exists, _ := store.Get(ctx, ProvisionedKey(estate, addr)); exists {
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
	provisioned := NewProvisionedStore(store, estate)
	diags := WriteBack(ctx, WriteBackRequest{
		ProvisionedStore: provisioned,
		Config:           cfg,
		FinalState:       finalState,
		Schemas: &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
			nullProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{"null_resource": schema},
			},
		}},
	})
	assertNoErrors(t, diags)
	if _, _, exists, _ := store.Get(ctx, ProvisionedKey(estate, addr)); exists {
		t.Error("a record-backed instance got a second copy of its tainted bit in the provisioned namespace")
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
	provisioned := NewProvisionedStore(store, estate)
	mine := mustAddr(t, provisionedTestType+`.web`)
	theirs := mustAddr(t, provisionedTestType+`.other`)

	// Write a legitimate record for `theirs`, then move its payload under
	// `mine`'s key - which is exactly what a hand-copied key looks like.
	if _, err := provisioned.Put(ctx, theirs, ""); err != nil {
		t.Fatalf("seeding: %s", err)
	}
	payload, _, _, err := store.Get(ctx, ProvisionedKey(estate, theirs))
	if err != nil {
		t.Fatalf("reading the seed: %s", err)
	}
	if _, err := store.PutIfAbsent(ctx, ProvisionedKey(estate, mine), payload); err != nil {
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
	if _, err := store.PutIfAbsent(ctx, ProvisionedKey(estate, addr), []byte(`{"formatVersion":"from-the-future"}`)); err != nil {
		t.Fatalf("planting the unreadable record: %s", err)
	}

	provs := SingleProvider(provisionedTestProvider, provisionedTypeProvider())
	res, diags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: "i-0abcdef"}},
		provs, Options{ProvisionedStore: NewProvisionedStore(store, estate)})

	if !diags.HasErrors() {
		t.Fatal("an unreadable provisioner record produced no error; the object would be reported healthy")
	}
	var found bool
	for _, d := range diags {
		if d.Description().Summary == SummaryProvisionedUnreadable {
			found = true
		}
	}
	if !found {
		t.Errorf("no %q diagnostic; got %v", SummaryProvisionedUnreadable, diags)
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
	provisioned := NewProvisionedStore(store, estate)
	if _, err := provisioned.Put(ctx, addr, ""); err != nil {
		t.Fatalf("seeding: %s", err)
	}

	provs := SingleProvider(provisionedTestProvider, provisionedTypeProvider())
	res, diags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassConcrete, ImportID: "i-0abcdef"}},
		provs, Options{ProvisionedStore: provisioned})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{provisionedTestType + `.web`})

	inst := res.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object")
	}
	if inst.Current.Status != states.ObjectReady {
		t.Errorf("status = %s, want ObjectReady: a record for a resource with no provisioner block must be inert, not a standing order to replace it", inst.Current.Status)
	}
	if len(res.ProvisionedVersions) != 0 {
		t.Errorf("ProvisionedVersions = %v, want empty; the store must not be consulted for an instance with no create-time provisioner", res.ProvisionedVersions)
	}
}

// TestProvisionedKeysAreInvisibleToOrphanDiscovery is issue #353's version
// of the central safety property located.go and residue.go each carry, and
// the reason a fifth namespace root exists rather than a subdirectory of an
// existing one.
//
// builder.discoverOrphanedRecords lists [Options.RecordKeyPrefix] and
// materializes every key it can decode as an UNDECLARED prior-state entry,
// which makes the plan propose DESTROYING it. A key here says only that a
// shell command failed against a live object the record namespace has no
// authority over, so one reaching that listing is a cloud deletion driven
// by a note about `echo`.
//
// Proven three ways, exactly as the other two are: lexically, functionally
// through the real List call, and by decoding.
func TestProvisionedKeysAreInvisibleToOrphanDiscovery(t *testing.T) {
	const estate = "my-estate"
	ctx := context.Background()

	recordPrefix := RecordKeyPrefix(estate)
	provisionedPrefix := ProvisionedKeyPrefix(estate)

	if strings.HasPrefix(provisionedPrefix, recordPrefix+"/") || provisionedPrefix == recordPrefix {
		t.Fatalf("ProvisionedKeyPrefix(%q) = %q lives under RecordKeyPrefix %q; orphan discovery would list it", estate, provisionedPrefix, recordPrefix)
	}
	if strings.HasPrefix(recordPrefix, provisionedNamespaceRoot+"/") {
		t.Fatalf("RecordKeyPrefix(%q) = %q lives under the provisioned namespace %q", estate, recordPrefix, provisionedNamespaceRoot)
	}
	for _, other := range []string{hintNamespaceRoot, locatedNamespaceRoot, residueNamespaceRoot, "tofu-receipts"} {
		if provisionedNamespaceRoot == other || strings.HasPrefix(provisionedNamespaceRoot, other+"/") {
			t.Errorf("the provisioned namespace %q collides with %q", provisionedNamespaceRoot, other)
		}
	}

	store := localHintStore(t)
	recAddr := locatedTestAddr(t, "terraform_data", "seed")
	recordKey := RecordKey(recordPrefix, recAddr)
	if _, err := store.PutIfAbsent(ctx, recordKey, []byte(`{"value_type":"\"string\"","attrs":"\"x\""}`)); err != nil {
		t.Fatalf("writing the record fixture: %s", err)
	}
	provAddr := locatedTestAddr(t, provisionedTestType, "web")
	provisioned := NewProvisionedStore(store, estate)
	if _, err := provisioned.Put(ctx, provAddr, ""); err != nil {
		t.Fatalf("Put: %s", err)
	}

	keys, err := store.List(ctx, recordPrefix)
	if err != nil {
		t.Fatalf("List(%q): %s", recordPrefix, err)
	}
	if len(keys) != 1 || keys[0] != recordKey {
		t.Errorf("List(%q) = %v, want exactly the one record key %q.\n"+
			"A provisioner-taint key reaching orphan discovery is a cloud deletion driven by a note that a shell command failed.", recordPrefix, keys, recordKey)
	}

	if got, ok := RecordAddr(recordPrefix, ProvisionedKey(estate, provAddr)); ok {
		t.Errorf("RecordAddr decoded a provisioner key into %s under the record prefix; it must refuse it", got)
	}

	// And the payload cannot be half-read as a resource record either,
	// which is the third independent stop.
	payload, _, _, err := store.Get(ctx, ProvisionedKey(estate, provAddr))
	if err != nil {
		t.Fatalf("reading the planted key: %s", err)
	}
	if _, _, _, err := decodeRecordPayload(payload); err == nil {
		t.Error("decodeRecordPayload accepted a provisioner payload; it must refuse it outright")
	}
}

// TestProvisionedRecordCarriesNoProvisionerContent is the pin against the
// design issue #353 explicitly rejected.
//
// An earlier sketch proposed storing a hash of the provisioner's
// configuration and re-running when it changed. Stock has no such memory
// and never re-runs a provisioner because its command changed, so a diff
// like that would violate "match stock and go no further" - and it is by
// name the terraform_data.triggers_replace pseudo-receipt pattern
// live/RECEIPTS.md forbids. The payload is asserted by content, not by
// intention, so a future field that reintroduces it fails here.
func TestProvisionedRecordCarriesNoProvisionerContent(t *testing.T) {
	ctx := context.Background()
	const estate = "provisioned-estate"

	store := localHintStore(t)
	provisioned := NewProvisionedStore(store, estate)
	addr := mustAddr(t, provisionedTestType+`.web`)
	if _, err := provisioned.Put(ctx, addr, ""); err != nil {
		t.Fatalf("Put: %s", err)
	}
	payload, _, _, err := store.Get(ctx, ProvisionedKey(estate, addr))
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
