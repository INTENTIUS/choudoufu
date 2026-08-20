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
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// The type these tests use is aws_eip_association, and naming it here is
// deliberate and allowed: it is the largest single markerless cause among
// the three estates issue #270 measures, and a test may name a type where
// production control flow may not. Nothing under internal/live/ names it.

const locatedTestType = "aws_eip_association"

var locatedTestProvider = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("aws"),
}

// locatedTypeSchema is a caricature of the real type's schema with the two
// properties the located path turns on: a string "id" (so
// identity.LocatedType admits it and write-back has something to record)
// and NO tags attribute, which is what makes it markerless in the first
// place and what makes checkOwnership's !markerCapable branch admit the
// object with no marker to check.
func locatedTypeSchema() providers.Schema {
	return providers.Schema{
		Version: 0,
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":            {Type: cty.String, Computed: true},
				"allocation_id": {Type: cty.String, Optional: true},
				"instance_id":   {Type: cty.String, Optional: true},
			},
		},
	}
}

// locatedTypeProvider serves the schema above and answers an import by ID
// with the object that ID names, which is the whole conversation the
// located path has with a provider. importedIDs records what it was asked
// for, so a test can assert the identity actually came out of the store
// rather than out of configuration.
func locatedTypeProvider(importedIDs *[]string) providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{locatedTestType: locatedTypeSchema()},
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
					"id":            cty.StringVal(req.Target.ID),
					"allocation_id": cty.StringVal("eipalloc-for-" + req.Target.ID),
					"instance_id":   cty.StringVal("i-for-" + req.Target.ID),
				}),
			}},
		}
	}
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: req.PriorState}
	}
	return p
}

// writeLocatedFixture writes a one-resource module declaring a markerless
// type, and returns its directory.
func writeLocatedFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `
resource "` + locatedTestType + `" "bastion" {
  allocation_id = "eipalloc-declared"
  instance_id   = "i-declared"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return dir
}

// TestOrderWorkRoutesLocatedExplicitly is acceptance criterion (b), and the
// one thing the previous agent stopped to say out loud.
//
// orderWork's default arm sends every class it does not name to
// needsDiscovery. Marker discovery is a TAG SWEEP; a located type has no
// tag, by definition. So a located resolution reaching that arm would lint
// clean, produce a needs-discovery omission the sweep can never satisfy,
// and then fail at apply against internal/live/stamp's unmarked-apply
// refusal - a plan refusal traded for an apply refusal, which is forbidden.
//
// The mutation this is written to catch is the removal of the
// ClassRecordLocated case: delete it and the resolution falls through to
// default, needsDiscovery gains an entry, and located loses one. Both
// halves are asserted, because asserting only that located is non-empty
// would still pass if the value were also duplicated into needsDiscovery.
func TestOrderWorkRoutesLocatedExplicitly(t *testing.T) {
	located := mustAddr(t, locatedTestType+`.bastion`)
	concrete := mustAddr(t, `aws_s3_bucket.logs`)
	discovery := mustAddr(t, `aws_vpc.main`)

	gotConcrete, _, gotDiscovery, _, gotRecord, gotLocated := orderWork([]identity.Resolution{
		{Addr: located, Class: identity.ClassRecordLocated},
		{Addr: concrete, Class: identity.ClassConcrete, ImportID: "logs"},
		{Addr: discovery, Class: identity.ClassNeedsDiscovery, Reason: "server-assigned"},
	})

	if len(gotLocated) != 1 || gotLocated[0].Addr.String() != located.String() {
		t.Fatalf("located = %v, want exactly %s.\n"+
			"A ClassRecordLocated resolution must have its own case in orderWork. Falling through to the default arm sends it to marker discovery, which is a tag sweep, and a located type has no tag: the run would lint clean and then hit internal/live/stamp's unmarked-apply refusal.",
			addrsOf(gotLocated), located)
	}
	for _, r := range gotDiscovery {
		if r.Addr.String() == located.String() {
			t.Errorf("%s is in needsDiscovery as well as located; marker discovery can never satisfy it", located)
		}
	}
	if len(gotDiscovery) != 1 || gotDiscovery[0].Addr.String() != discovery.String() {
		t.Errorf("needsDiscovery = %v, want exactly %s", addrsOf(gotDiscovery), discovery)
	}
	if len(gotConcrete) != 1 || gotConcrete[0].Addr.String() != concrete.String() {
		t.Errorf("concrete = %v, want exactly %s", addrsOf(gotConcrete), concrete)
	}
	if len(gotRecord) != 0 {
		t.Errorf("recordBacked = %v, want empty; a located instance's object is in the cloud and must not be hydrated from a record", addrsOf(gotRecord))
	}
}

func addrsOf(rs []identity.Resolution) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Addr.String())
	}
	return out
}

// TestBuildMaterializesLocatedFromTheStore is acceptance criterion (a): the
// identity comes from a store lookup and everything downstream of
// wanted.importID is the ordinary path.
//
// The load-bearing assertion is not that the instance materialized - a
// wrong identity materializes just as happily as a right one, which is the
// defect shape this repository has shipped six times. It is that the
// provider was asked to import the string the STORE held, and that the
// object in the prior state is the one that string names.
func TestBuildMaterializesLocatedFromTheStore(t *testing.T) {
	cfg := loadConfig(t, writeLocatedFixture(t))
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const estate = "test-estate"
	const wantID = "eipassoc-0f1e2d3c4b5a69780"

	store := localHintStore(t)
	located := NewLocatedStore(store, estate)
	wantVersion, err := located.Put(context.Background(), addr, LocatedRecord{ImportID: wantID}, "")
	if err != nil {
		t.Fatalf("seeding the located record: %s", err)
	}

	var imported []string
	provs := SingleProvider(locatedTestProvider, locatedTypeProvider(&imported))

	res, diags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordLocated}},
		provs, Options{LocatedStore: located})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{locatedTestType + `.bastion`})

	if len(imported) != 1 || imported[0] != wantID {
		t.Fatalf("the provider was asked to import %v, want exactly [%q].\n"+
			"The identity must come from the store. If it came from configuration, every estate would import the same object whatever it owns.",
			imported, wantID)
	}

	inst := res.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object for the located instance")
	}
	obj, err := inst.Current.Decode(locatedTypeSchema().Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding the materialized object: %s", err)
	}
	if got := obj.Value.GetAttr("id").AsString(); got != wantID {
		t.Errorf("the prior state holds id %q, want %q", got, wantID)
	}

	if len(res.LocatedVersions) != 1 {
		t.Fatalf("LocatedVersions = %v, want one entry so write-back can open a conditional Put", res.LocatedVersions)
	}
	if got := res.LocatedVersions[0]; got.Addr.String() != addr.String() || got.Version != wantVersion {
		t.Errorf("LocatedVersions[0] = %+v, want {%s %s}", got, addr, wantVersion)
	}
	if len(res.RecordVersions) != 0 {
		t.Errorf("RecordVersions = %v, want empty; a located version must not be filed under the record namespace's versions", res.RecordVersions)
	}
}

// TestBuildLocatedWithNoStoreConfigured is acceptance criterion (d)'s
// projection half: the run stops, by name, rather than guessing.
//
// This is the backstop that makes tools/estate-plan's eventual demotion of
// the markerless-type refusal sound. Without it, a configuration that
// declared a live block and forgot the record_store would silently reach
// apply with no way to say which object anything is.
func TestBuildLocatedWithNoStoreConfigured(t *testing.T) {
	cfg := loadConfig(t, writeLocatedFixture(t))
	addr := mustAddr(t, locatedTestType+`.bastion`)

	provs := SingleProvider(locatedTestProvider, locatedTypeProvider(nil))
	res, diags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordLocated}},
		provs, Options{})

	if !diags.HasErrors() {
		t.Fatal("a located instance with no record store produced no error. The demotion of the markerless-type refusal is only safe while this stops the run.")
	}
	var found bool
	for _, d := range diags {
		if d.Description().Summary == SummaryLocatedNoStore {
			found = true
			if !strings.Contains(d.Description().Detail, "record_store") {
				t.Errorf("the refusal does not name record_store, which is the missing thing:\n%s", d.Description().Detail)
			}
		}
	}
	if !found {
		t.Errorf("no %q diagnostic; got %v", SummaryLocatedNoStore, diags)
	}
	if len(res.Materialized) != 0 {
		t.Errorf("Materialized = %v, want empty", res.Materialized)
	}
}

// TestBuildLocatedRecordLostProposesCreate is acceptance criterion (f)'s
// plan half, and the recovery story issue #270 rests its safety argument
// on.
//
// With no located record the instance reads UNBOUND. The required outcome
// is a CREATE - never a destroy - and specifically the object must not
// enter the prior state at all, because an entry in the prior state with no
// configuration is what the plan engine destroys. The store half of this
// (that a lost record is indistinguishable from one never written) is
// pinned by TestLocatedStore_lostRecordReadsUnbound; this is the half that
// says what the plan then does about it.
func TestBuildLocatedRecordLostProposesCreate(t *testing.T) {
	cfg := loadConfig(t, writeLocatedFixture(t))
	addr := mustAddr(t, locatedTestType+`.bastion`)

	// A store with nothing in it: the shape a lost record leaves behind.
	located := NewLocatedStore(localHintStore(t), "test-estate")

	var imported []string
	provs := SingleProvider(locatedTestProvider, locatedTypeProvider(&imported))
	res, diags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordLocated}},
		provs, Options{LocatedStore: located})
	assertNoErrors(t, diags)

	if len(imported) != 0 {
		t.Errorf("the provider was asked to import %v with no record to import by; it must be asked nothing", imported)
	}
	if res.State.ResourceInstance(addr) != nil {
		t.Error("the instance entered the prior state with no located record. Anything in the prior state is something the plan engine may propose destroying, and a lost record must never be able to drive a destroy.")
	}
	if len(res.Materialized) != 0 {
		t.Errorf("Materialized = %v, want empty", res.Materialized)
	}

	var absent *Omission
	for i := range res.Omitted {
		if res.Omitted[i].Addr.String() == addr.String() {
			absent = &res.Omitted[i]
		}
	}
	if absent == nil {
		t.Fatalf("no omission for %s; got %v", addr, res.Omitted)
	}
	if absent.Reason != ReasonAbsent {
		t.Errorf("omission reason = %q, want %q. ReasonAbsent is what makes the plan propose a CREATE; any other reason is a different, and worse, answer.", absent.Reason, ReasonAbsent)
	}
}

// TestWriteBackLocatedRoundTrip is the pair the whole mechanism needs:
// write-back records the applied object's identity, and a projection built
// afterwards finds the same object again from the record alone.
//
// It is the unit-level shadow of live/e2e/record-store's floci crossing.
// The crossing is what proves it against a real provider and a real cloud
// API; this proves the two halves of the code agree with each other, which
// is the part a unit test can settle.
func TestWriteBackLocatedRoundTrip(t *testing.T) {
	cfg := loadConfig(t, writeLocatedFixture(t))
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const estate = "test-estate"
	const appliedID = "eipassoc-00112233445566778"

	store := localHintStore(t)
	located := NewLocatedStore(store, estate)

	// The state an apply finished with: the object exists and carries the
	// identity the cloud minted.
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

	diags := WriteBack(context.Background(), WriteBackRequest{
		LocatedStore: located,
		FinalState:   final,
		Schemas:      schemas,
	})
	assertNoErrors(t, diags)

	gotRec, _, exists, err := located.Get(context.Background(), addr)
	if err != nil {
		t.Fatalf("reading back the located record: %s", err)
	}
	if !exists {
		t.Fatal("write-back recorded nothing. Without a record, every later run reads unbound and proposes creating a second object - the mechanism would be inert and the estate would accumulate duplicates.")
	}
	if gotRec.ImportID != appliedID {
		t.Fatalf("recorded identity %q, want %q", gotRec.ImportID, appliedID)
	}

	// And the read side finds it. This is the crossing, in miniature: the
	// projection below shares nothing with the apply above except the
	// store.
	var imported []string
	provs := SingleProvider(locatedTestProvider, locatedTypeProvider(&imported))
	res, buildDiags := BuildWith(context.Background(), cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordLocated}},
		provs, Options{LocatedStore: NewLocatedStore(store, estate)})
	assertNoErrors(t, buildDiags)
	if len(imported) != 1 || imported[0] != appliedID {
		t.Fatalf("the replan imported %v, want [%q]", imported, appliedID)
	}
	assertMaterialized(t, res, []string{locatedTestType + `.bastion`})
}

// TestWriteBackLocatedDeletesAWithdrawnInstance pins the other direction:
// an instance the apply destroyed leaves no located record behind.
//
// Deleting the RECORD is not deleting the OBJECT - the object was destroyed
// by the ordinary apply, through the provider - and this only removes the
// note saying where it was.
func TestWriteBackLocatedDeletesAWithdrawnInstance(t *testing.T) {
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const estate = "test-estate"

	store := localHintStore(t)
	located := NewLocatedStore(store, estate)
	version, err := located.Put(context.Background(), addr, LocatedRecord{ImportID: "eipassoc-gone"}, "")
	if err != nil {
		t.Fatalf("seeding: %s", err)
	}

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{locatedTestType: locatedTypeSchema()}},
	}}

	diags := WriteBack(context.Background(), WriteBackRequest{
		LocatedStore:    located,
		LocatedVersions: []RecordVersion{{Addr: addr, Version: version}},
		FinalState:      states.NewState(), // the apply destroyed it
		Schemas:         schemas,
	})
	assertNoErrors(t, diags)

	if _, _, exists, err := located.Get(context.Background(), addr); err != nil || exists {
		t.Errorf("the located record survived the instance's destruction (exists=%v err=%v)", exists, err)
	}
}

// TestWriteBackLocatedIgnoresNonLocatedTypes is the guard against the
// write side and the read side drifting apart. Write-back re-asks
// identity.LocatedType rather than remembering a verdict, so a type the
// plan side would never materialize from a record must get no record
// written for it either.
//
// Mutation to check: make writeBackLocated skip the LocatedType call and
// write for every type in the final state. This test goes red; nothing else
// in the package does.
func TestWriteBackLocatedIgnoresNonLocatedTypes(t *testing.T) {
	// null_resource is not markerless and never located.
	addr := mustAddr(t, `null_resource.trigger`)
	const estate = "test-estate"

	store := localHintStore(t)
	located := NewLocatedStore(store, estate)

	final := states.NewState()
	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("abc123"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})
	src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: val}).
		Encode(nullResourceSchema().Block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	final.EnsureModule(addrs.RootModuleInstance).
		SetResourceInstanceCurrent(addr.Resource, src, nullProvider, addrs.NoKey)

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		nullProvider.Provider: {ResourceTypes: map[string]providers.Schema{"null_resource": nullResourceSchema()}},
	}}

	diags := WriteBack(context.Background(), WriteBackRequest{
		LocatedStore: located,
		FinalState:   final,
		Schemas:      schemas,
	})
	assertNoErrors(t, diags)

	if _, _, exists, err := located.Get(context.Background(), addr); err != nil || exists {
		t.Errorf("a non-located type got a located record written for it (exists=%v err=%v). The write side and the read side must admit the same set, or a type ends up written to a record nothing reads.", exists, err)
	}
}
