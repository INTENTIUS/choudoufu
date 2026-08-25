// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// ---------------------------------------------------------------------
// 1. Envelope round-trip and decode-shape validation
// ---------------------------------------------------------------------

// TestRecordEnvelopeRoundTripsDeposed writes an envelope carrying a
// Deposed entry through the real mergeEnvelope/getRaw path (not decodeEnvelope
// called on a hand-built literal) and reads it back through GetDeposed,
// pinning both fields deposedFields carries.
func TestRecordEnvelopeRoundTripsDeposed(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("deposed-estate"))
	addr := locatedTestAddr(t, "aws_instance", "web")

	if _, _, keyExists, err := store.GetDeposed(ctx, addr); err != nil || keyExists {
		t.Fatalf("GetDeposed before any write: keyExists=%v err=%v, want false/nil", keyExists, err)
	}

	version, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Deposed = map[string]*deposedFields{
			"deadbeef": {
				Identity: &identityPayload{ImportID: "i-old0123456789"},
				Provider: `provider["registry.opentofu.org/hashicorp/aws"]`,
			},
		}
	})
	if err != nil {
		t.Fatalf("mergeEnvelope: %s", err)
	}
	if version == "" {
		t.Fatal("mergeEnvelope reported no version for a real write")
	}

	got, gotVersion, keyExists, err := store.GetDeposed(ctx, addr)
	if err != nil {
		t.Fatalf("GetDeposed: %s", err)
	}
	if !keyExists {
		t.Fatal("GetDeposed reports the key does not exist right after writing it")
	}
	if gotVersion != version {
		t.Errorf("GetDeposed version = %q, want %q", gotVersion, version)
	}
	if len(got) != 1 {
		t.Fatalf("GetDeposed returned %d entries, want 1: %#v", len(got), got)
	}
	rec, ok := got["deadbeef"]
	if !ok {
		t.Fatalf("GetDeposed did not return the %q key: %#v", "deadbeef", got)
	}
	if rec.ImportID != "i-old0123456789" {
		t.Errorf("ImportID = %q, want %q", rec.ImportID, "i-old0123456789")
	}
	if rec.Provider != `provider["registry.opentofu.org/hashicorp/aws"]` {
		t.Errorf("Provider = %q, want the recorded provider string", rec.Provider)
	}

	// isEmpty() must count Deposed: a merge that clears every other member
	// but leaves Deposed populated must NOT delete the key.
	if _, err := store.mergeEnvelope(ctx, addr, version, func(env *recordEnvelope) {
		// no-op mutate: everything rides through unchanged.
	}); err != nil {
		t.Fatalf("no-op mergeEnvelope: %s", err)
	}
	if _, _, keyExists, err := store.GetDeposed(ctx, addr); err != nil || !keyExists {
		t.Fatalf("the key was deleted despite a non-empty Deposed member (keyExists=%v err=%v)", keyExists, err)
	}
}

// TestDecodeEnvelopeAcceptsV1WithNoDeposed is the A1-audit lesson applied
// to this unit: a v1 payload predates the whole envelope and carries no
// "deposed" key at all. It must still decode - byte for byte the same
// property TestDecodeEnvelopeAcceptsV1AsObject already pins for Kind and
// Object, extended to confirm Deposed comes back nil rather than decoding
// failing or a vacuous non-nil map appearing.
func TestDecodeEnvelopeAcceptsV1WithNoDeposed(t *testing.T) {
	val := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("v1-value")})
	of, err := encodeObjectFields(val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	v1 := struct {
		ValueType json.RawMessage `json:"value_type"`
		Attrs     json.RawMessage `json:"attrs"`
	}{ValueType: of.ValueType, Attrs: of.Attrs}
	raw, err := json.Marshal(v1)
	if err != nil {
		t.Fatalf("marshaling the v1 fixture: %s", err)
	}

	env, err := decodeEnvelope(raw)
	if err != nil {
		t.Fatalf("decodeEnvelope refused a v1 payload with no deposed key: %s", err)
	}
	if env.Deposed != nil {
		t.Errorf("Deposed = %#v, want nil for a v1 payload that never carried the field", env.Deposed)
	}
}

// TestDecodeEnvelopeAcceptsDeposedOnlyIdentityKey is the NEW shape this
// unit introduces: an ordinary taggable instance whose only recorded fact
// this pass is a deposed object (writeback.go's diffDeposedForWrite can be
// the SOLE reason mergeEnvelope writes a key at all). kind=identity with no
// Identity/Residue/Provisioned but a non-empty Deposed map must decode
// successfully, not be refused as "carries none of ..." - the validation
// switch in decodeEnvelope has to know about this member, and this test is
// what would catch it forgetting.
func TestDecodeEnvelopeAcceptsDeposedOnlyIdentityKey(t *testing.T) {
	env := recordEnvelope{
		FormatVersion: envelopeFormatVersion,
		Address:       "aws_instance.web",
		Kind:          recordKindIdentity,
		Deposed: map[string]*deposedFields{
			"deadbeef": {Provider: `provider["registry.opentofu.org/hashicorp/aws"]`},
		},
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshaling: %s", err)
	}

	got, err := decodeEnvelope(raw)
	if err != nil {
		t.Fatalf("decodeEnvelope refused a deposed-only kind=identity envelope: %s", err)
	}
	if len(got.Deposed) != 1 {
		t.Fatalf("Deposed has %d entries after decode, want 1", len(got.Deposed))
	}
	if got.Identity != nil || got.Residue != nil || got.Provisioned != nil {
		t.Errorf("decode populated a member the fixture never set: Identity=%v Residue=%v Provisioned=%v", got.Identity, got.Residue, got.Provisioned)
	}
}

// TestDecodeEnvelopeRefusesIdentityKindWithNothingAtAll pins the OTHER
// side of the switch this unit touched: a kind=identity envelope with
// NEITHER Deposed NOR any of the other three members must still be
// refused exactly as before - the new member widens what is accepted,
// never what a genuinely empty-looking payload means.
func TestDecodeEnvelopeRefusesIdentityKindWithNothingAtAll(t *testing.T) {
	env := recordEnvelope{
		FormatVersion: envelopeFormatVersion,
		Address:       "aws_instance.web",
		Kind:          recordKindIdentity,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshaling: %s", err)
	}
	if _, err := decodeEnvelope(raw); err == nil {
		t.Fatal("decodeEnvelope accepted a kind=identity envelope carrying nothing at all")
	}
}

// ---------------------------------------------------------------------
// 2. Write-back diffing: additions and removals
// ---------------------------------------------------------------------

// TestWriteBackRecordsANewDeposedObject is the addition half of
// diffDeposedForWrite, exercised through the real record-backed (kind=object)
// loop: a record-backed instance's final state carries both a current
// object and a freshly-deposed one, and write-back must record both facts
// in the ONE mergeEnvelope call.
func TestWriteBackRecordsANewDeposedObject(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("deposed-estate"))
	addr := mustAddr(t, `null_resource.trigger`)
	schema := nullResourceSchema()

	finalState := states.NewState()
	newVal := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("new-id"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})
	newSrc, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: newVal}).
		Encode(schema.Block.ImpliedType(), uint64(schema.Version), 0)
	if err != nil {
		t.Fatalf("encoding the current object: %s", err)
	}
	finalState.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, newSrc, nullProvider, addrs.NoKey)

	oldVal := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("old-id"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})
	oldSrc, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: oldVal}).
		Encode(schema.Block.ImpliedType(), uint64(schema.Version), 0)
	if err != nil {
		t.Fatalf("encoding the deposed object: %s", err)
	}
	finalState.EnsureModule(addr.Module).SetResourceInstanceDeposed(addr.Resource, states.DeposedKey("deadbeef"), oldSrc, nullProvider, addrs.NoKey)

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		nullProvider.Provider: {
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"null_resource": schema},
		},
	}}

	diags := WriteBack(ctx, WriteBackRequest{Store: store, FinalState: finalState, Schemas: schemas})
	assertNoErrors(t, diags)

	deposed, _, keyExists, err := store.GetDeposed(ctx, addr)
	if err != nil {
		t.Fatalf("GetDeposed: %s", err)
	}
	if !keyExists {
		t.Fatal("no record was written at all for the address")
	}
	if len(deposed) != 1 {
		t.Fatalf("Deposed has %d entries, want 1: %#v", len(deposed), deposed)
	}
	rec, ok := deposed["deadbeef"]
	if !ok {
		t.Fatalf("Deposed does not carry the %q key: %#v", "deadbeef", deposed)
	}
	if rec.Provider != providerString(nullProvider) {
		t.Errorf("recorded deposed Provider = %q, want %q", rec.Provider, providerString(nullProvider))
	}
	// LocatedRecordFrom's bare-"id" fallback (identity.LocatedIdentityPlanFor's
	// default branch) renders even a fixture type with no ratified row -
	// the same generic call every current-object writer in this package
	// already uses, exercised here for a deposed entry.
	if rec.ImportID != "old-id" {
		t.Errorf("recorded deposed identity = %+v, want ImportID %q", rec, "old-id")
	}
}

// TestWriteBackClearsADestroyedDeposedObject is the removal half: a record
// already names a deposed object for this address, and this pass's final
// state no longer has it in ri.Deposed at all - the ordinary, happy-path
// close of the crash window, when the follow-up apply destroys the old
// object. The stale entry must be removed, and - the shape this design
// specifically exists to reach - this must happen even when NOTHING else
// about the address changed this pass (deposedRecordedDiffers's own job).
func TestWriteBackClearsADestroyedDeposedObject(t *testing.T) {
	ctx := context.Background()
	addr := locatedTestAddr(t, locatedTestType, "bastion")
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("deposed-estate"))

	seedVersion, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "eipassoc-current"}
		env.Deposed = map[string]*deposedFields{
			"deadbeef": {Identity: &identityPayload{ImportID: "eipassoc-old"}},
		}
	})
	if err != nil {
		t.Fatalf("seeding: %s", err)
	}

	// The final state: the current object is unchanged (same identity,
	// same everything). For THIS fixture type identity still gets
	// re-touched every pass regardless (LocatedRecordFrom succeeds off the
	// bare "id" fallback, which sets touched unconditionally - see
	// writeBackRecordEnvelopes's own doc comment, "every stamped and
	// untaggable instance ... gets its identity recorded, best effort"),
	// so this integration-level test alone does not isolate
	// deposedRecordedDiffers's own contribution from that path.
	// TestDeposedRecordedDiffersDetectsChanges, below, is the isolated
	// proof that the pre-check itself is correct and load-bearing for the
	// population where nothing else re-touches (an ordinary taggable type
	// LocatedRecordFrom cannot render an identity for, which
	// writeBackRecordEnvelopes's own doc comment says is left alone).
	final := states.NewState()
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":            cty.StringVal("eipassoc-current"),
		"allocation_id": cty.StringVal("eipalloc-declared"),
		"instance_id":   cty.StringVal("i-declared"),
	})
	src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: applied}).
		Encode(locatedTypeSchema().Block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	final.EnsureModule(addrs.RootModuleInstance).SetResourceInstanceCurrent(addr.Resource, src, locatedTestProvider, addrs.NoKey)

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{locatedTestType: locatedTypeSchema()}},
	}}

	diags := WriteBack(ctx, WriteBackRequest{
		Store:            store,
		EnvelopeVersions: []RecordVersion{{Addr: addr, Version: seedVersion}},
		FinalState:       final,
		Schemas:          schemas,
	})
	assertNoErrors(t, diags)

	deposed, _, keyExists, err := store.GetDeposed(ctx, addr)
	if err != nil {
		t.Fatalf("GetDeposed after write-back: %s", err)
	}
	if !keyExists {
		t.Fatal("the whole key vanished - the current object's own identity should have survived")
	}
	if len(deposed) != 0 {
		t.Errorf("Deposed still carries %d entries after the deposed object was destroyed, want 0: %#v", len(deposed), deposed)
	}
	rec, _, _, found, err := store.GetIdentity(ctx, addr)
	if err != nil || !found {
		t.Fatalf("the current object's own identity did not survive: found=%v err=%v", found, err)
	}
	if rec.ImportID != "eipassoc-current" {
		t.Errorf("current identity = %q, want unchanged %q", rec.ImportID, "eipassoc-current")
	}
}

// ---------------------------------------------------------------------
// 3. Orphan-sweep exclusion
// ---------------------------------------------------------------------

// TestDeposedOnlyRecordIsInvisibleToOrphanDiscovery is HANDOFF's own
// warning made concrete for this unit: Deposed lives INSIDE a kind=identity
// envelope, never as a free-floating key of its own, so
// discoverOrphanedRecords - which proposes destroying only kind=object keys
// - must never treat a deposed-only record as an orphan to adopt or
// destroy. Same shape as TestAnIdentityKindRecordIsInvisibleToOrphanDiscovery,
// with a real kind=object record alongside it as the positive control.
func TestDeposedOnlyRecordIsInvisibleToOrphanDiscovery(t *testing.T) {
	ctx := context.Background()
	const estate = "orphan-deposed-estate"
	prefix := RecordKeyPrefix(estate)
	raw := localHintStore(t)
	store := NewRecordEnvelopeStore(raw, prefix)

	objectAddr := locatedTestAddr(t, "null_resource", "orphaned")
	deposedOnlyAddr := locatedTestAddr(t, "aws_instance", "orphaned")

	of, err := encodeObjectFields(cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("orphan-object-id"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	}), nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("encoding the object fixture: %s", err)
	}
	if _, err := store.mergeEnvelope(ctx, objectAddr, "", func(env *recordEnvelope) {
		env.Kind = recordKindObject
		env.Object = of
	}); err != nil {
		t.Fatalf("writing the kind=object fixture: %s", err)
	}

	if _, err := store.mergeEnvelope(ctx, deposedOnlyAddr, "", func(env *recordEnvelope) {
		env.Deposed = map[string]*deposedFields{
			"deadbeef": {Identity: &identityPayload{ImportID: "i-orphan-deposed"}},
		}
	}); err != nil {
		t.Fatalf("writing the deposed-only fixture: %s", err)
	}

	cfg := loadConfig(t, writeEmptyFixture(t))
	provs := SingleProvider(nullProvider, nullResourceProvider())

	res, diags := BuildWith(ctx, cfg, nil, provs, Options{RecordStore: store})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{objectAddr.String()})
	if res.Has(deposedOnlyAddr) {
		t.Errorf("%s materialized from a deposed-only kind=identity key; the orphan sweep must never treat a Deposed entry as delete authority", deposedOnlyAddr)
	}
}

// ---------------------------------------------------------------------
// 4. The crash-window (b) story - the money test
// ---------------------------------------------------------------------

// TestCrashWindowBWriteBackThenBuildRecoversTheDeposedObject is the design
// comment's own money test (issuecomment-5405599939, section 5), built as a
// unit test directly over the write-back and build seams rather than a full
// gauntlet harness crash simulation - the design's own suggested fallback
// when a real process kill is too big for a unit test, and this is that
// fallback.
//
// It reproduces window (b) - "graceful interrupt landing after the create
// node has committed the new object into Core's own in-memory state ...
// but before the destroy-deposed node runs" - in two acts:
//
//  1. WriteBack is handed a FinalState shaped exactly as
//     MaybeRestoreResourceInstanceDeposed leaves it: Current is the NEW
//     object, Deposed[dk] is the OLD one. Both facts must land in the one
//     record write-back makes.
//  2. The record is read back exactly as
//     internal/command/live_plan.go's collectDeposedRecords would read it,
//     turned into a [DeposedBinding] exactly as discovery's collision-
//     breaking branch would (this is the seam #361's design says
//     discovery.go owns; skipped here as its own package's job -
//     TestDiscoverDeposedDisambiguation in internal/live/discovery proves
//     that half), and handed to BuildWith. The built state's own
//     Instances[key].Deposed[dk] must carry the OLD object, live-read
//     through the provider - the second, independent verification the
//     design's safety argument rests on - which is what lets stock's own
//     completely unmodified node_resource_deposed.go graph machinery
//     propose destroying it on the next plan. Proving that LAST step (the
//     actual plan) is day2_crash's own gauntlet estate script, the next
//     unit; this test stops at the input stock's graph consumes.
func TestCrashWindowBWriteBackThenBuildRecoversTheDeposedObject(t *testing.T) {
	ctx := context.Background()
	const estate = "crash-window-b"
	addr := locatedTestAddr(t, locatedTestType, "bastion")
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix(estate))

	const oldID = "eipassoc-old-0000000000"
	const newID = "eipassoc-new-1111111111"
	const dk = states.DeposedKey("deadbeef")

	// ---- act 1: the crash. WriteBack sees both facts at once. ----
	final := states.NewState()
	newObj := cty.ObjectVal(map[string]cty.Value{
		"id":            cty.StringVal(newID),
		"allocation_id": cty.StringVal("eipalloc-declared"),
		"instance_id":   cty.StringVal("i-declared"),
	})
	newSrc, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: newObj}).
		Encode(locatedTypeSchema().Block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding the new object: %s", err)
	}
	final.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, newSrc, locatedTestProvider, addrs.NoKey)

	oldObj := cty.ObjectVal(map[string]cty.Value{
		"id":            cty.StringVal(oldID),
		"allocation_id": cty.StringVal("eipalloc-declared"),
		"instance_id":   cty.StringVal("i-declared"),
	})
	oldSrc, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: oldObj}).
		Encode(locatedTypeSchema().Block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding the old (deposed) object: %s", err)
	}
	final.EnsureModule(addr.Module).SetResourceInstanceDeposed(addr.Resource, dk, oldSrc, locatedTestProvider, addrs.NoKey)

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{locatedTestType: locatedTypeSchema()}},
	}}

	diags := WriteBack(ctx, WriteBackRequest{Store: store, FinalState: final, Schemas: schemas})
	assertNoErrors(t, diags)

	// Both facts landed, from the ONE mergeEnvelope call: the current
	// object's own identity, and the deposed one.
	currentRec, _, _, currentFound, err := store.GetIdentity(ctx, addr)
	if err != nil || !currentFound {
		t.Fatalf("the current object's identity was not recorded: found=%v err=%v", currentFound, err)
	}
	if currentRec.ImportID != newID {
		t.Fatalf("current identity = %q, want the NEW object %q", currentRec.ImportID, newID)
	}
	deposed, _, keyExists, err := store.GetDeposed(ctx, addr)
	if err != nil {
		t.Fatalf("GetDeposed: %s", err)
	}
	if !keyExists {
		t.Fatal("no record was written at all after the crash-window apply")
	}
	if len(deposed) != 1 {
		t.Fatalf("Deposed has %d entries, want exactly 1: %#v", len(deposed), deposed)
	}
	oldRec, ok := deposed[string(dk)]
	if !ok {
		t.Fatalf("Deposed does not carry key %q: %#v", dk, deposed)
	}
	if oldRec.ImportID != oldID {
		t.Fatalf("recorded deposed identity = %q, want the OLD object %q", oldRec.ImportID, oldID)
	}

	// ---- act 2: the next plan. Read the record back, bind it, build. ----
	binding := NewDeposedBinding(addr, dk.String(), oldRec)
	if binding.ImportID != oldID {
		t.Fatalf("NewDeposedBinding lost the identity: got %q, want %q", binding.ImportID, oldID)
	}

	var imported []string
	provs := SingleProvider(locatedTestProvider, locatedTypeProvider(&imported))
	cfg := loadConfig(t, writeLocatedFixture(t))

	res, buildDiags := BuildWith(ctx, cfg,
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordLocated, ImportID: newID}},
		provs, Options{
			RecordStore:     store,
			DeposedBindings: []DeposedBinding{binding},
		})
	assertNoErrors(t, buildDiags)

	// The money assertion: the built state's OWN Instances[key].Deposed[dk]
	// carries the old object, live-read through the provider (imported
	// names oldID, not merely echoed from the record) - exactly the input
	// stock's node_resource_deposed.go / transform_state.go graph
	// machinery needs to propose destroying it, unmodified.
	found := false
	for _, id := range imported {
		if id == oldID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the deposed object was never live-read through the provider (imported=%v) - a wrong-marker hazard, not a recovery", imported)
	}

	inst := res.State.ResourceInstance(addr)
	if inst == nil {
		t.Fatal("no resource instance at all in the built state")
	}
	if inst.Current == nil {
		t.Fatal("the current object went missing from the built state")
	}
	depObj, deposedPresent := inst.Deposed[dk]
	if !deposedPresent || depObj == nil {
		t.Fatalf("Instances[%s].Deposed[%s] is not populated in the built state - the deposed object was not folded in, so stock's own graph has nothing to propose destroying", addr, dk)
	}
	decoded, err := depObj.Decode(locatedTypeSchema().Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding the folded-in deposed object: %s", err)
	}
	if got := decoded.Value.GetAttr("id").AsString(); got != oldID {
		t.Fatalf("the folded-in deposed object has id %q, want the OLD object %q", got, oldID)
	}
}

// TestDeposedRecordedDiffersDetectsChanges is the isolated proof for
// deposedRecordedDiffers: writeBackRecordEnvelopes's cheap pre-check for
// "does this address need a write for a deposed-only change", independent
// of whatever else in that loop might also set touched. A deposed-only
// change has to be able to force a write even for the (common) population
// where identity/residue/taint never re-touch an address on their own -
// this pins that the key-set comparison itself gets addition, removal and
// the unchanged case all correct.
func TestDeposedRecordedDiffersDetectsChanges(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("diff-check-estate"))
	addr := locatedTestAddr(t, "aws_instance", "solo")

	stub := &states.ResourceInstanceObjectSrc{}

	// Nothing recorded, nothing live: no difference.
	if deposedRecordedDiffers(ctx, store, addr, nil) {
		t.Error("nothing recorded and nothing live reported a difference")
	}

	// Nothing recorded, one live entry: an addition.
	if !deposedRecordedDiffers(ctx, store, addr, map[states.DeposedKey]*states.ResourceInstanceObjectSrc{"deadbeef": stub}) {
		t.Error("a brand-new deposed entry was not detected as a difference")
	}

	// Seed one recorded entry.
	if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Deposed = map[string]*deposedFields{"deadbeef": {Identity: &identityPayload{ImportID: "i-old"}}}
	}); err != nil {
		t.Fatalf("seeding: %s", err)
	}

	// Same key set live: no difference.
	if deposedRecordedDiffers(ctx, store, addr, map[states.DeposedKey]*states.ResourceInstanceObjectSrc{"deadbeef": stub}) {
		t.Error("an unchanged key set reported a difference")
	}

	// Recorded but no longer live: a removal.
	if !deposedRecordedDiffers(ctx, store, addr, nil) {
		t.Error("a deposed entry that disappeared from live state was not detected as a difference")
	}

	// Recorded, but live now names a DIFFERENT key: still a difference
	// (a different deposed generation, not the same object).
	if !deposedRecordedDiffers(ctx, store, addr, map[states.DeposedKey]*states.ResourceInstanceObjectSrc{"beefdead": stub}) {
		t.Error("a changed deposed key set was not detected as a difference")
	}
}
