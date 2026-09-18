// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1211's record half, end to end through the real
// write-back: an apply of a kubernetes_manifest writes what its
// configuration declared at metadata.labels and metadata.annotations into
// the instance's residue record, and the read side gets it back.
//
// This is the half neither rejected design had. The removal set is
// (recorded declared keys) \ (currently declared keys), so if nothing
// records the first term there is no fix - and if the record is written
// only when something else about the instance changed, the first term is
// whatever the FIRST apply declared, for ever.

const manifestKeysType = "kubernetes_manifest"

var manifestKeysProvider = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("kubernetes"),
}

// manifestKeysAppliedValue is one applied kubernetes_manifest object: the
// manifest the apply SENT, with the labels and annotations named, and an
// `object` the API server answered with that carries two more keys nobody
// declared.
func manifestKeysAppliedValue(labels, annotations map[string]string) cty.Value {
	meta := map[string]cty.Value{
		"name":      cty.StringVal("app-config"),
		"namespace": cty.StringVal("lbl1211"),
	}
	if labels != nil {
		meta["labels"] = priorObjectMap(labels)
	}
	if annotations != nil {
		meta["annotations"] = priorObjectMap(annotations)
	}
	manifest := cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("v1"),
		"kind":       cty.StringVal("ConfigMap"),
		"metadata":   cty.ObjectVal(meta),
	})
	live := cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("v1"),
		"kind":       cty.StringVal("ConfigMap"),
		"metadata": cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal("app-config"),
			"namespace": cty.StringVal("lbl1211"),
			"labels":    liveStringMap(map[string]string{"external": "keepme"}),
		}),
	})
	return cty.ObjectVal(map[string]cty.Value{
		"manifest":        manifest,
		"object":          live,
		"computed_fields": cty.NullVal(cty.List(cty.String)),
		"field_manager":   cty.ListValEmpty(cty.Object(map[string]cty.Type{"name": cty.String, "force_conflicts": cty.Bool})),
	})
}

func manifestKeysSchemas() *tofu.Schemas {
	return &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		manifestKeysProvider.Provider: {
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{manifestKeysType: manifestResidueSchema()},
		},
	}}
}

// manifestKeysFinalState puts one applied object at addr, the way an apply
// leaves it.
func manifestKeysFinalState(t *testing.T, addr addrs.AbsResourceInstance, val cty.Value) *states.State {
	t.Helper()
	schema := manifestResidueSchema()
	src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: val}).
		Encode(schema.Block.ImpliedType(), uint64(schema.Version), 0)
	if err != nil {
		t.Fatalf("encoding the applied object: %s", err)
	}
	st := states.NewState()
	st.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, manifestKeysProvider, addrs.NoKey)
	return st
}

// TestWriteBackRecordsTheDeclaredManifestKeys is arm 1's foundation: the
// keys the apply declared reach the record, and they are the manifest's
// own, not the live object's.
//
// Proven red by deleting the manifestDeclaredKeys call in
// writeBackRecordEnvelopes: found becomes false and every later assertion
// falls with it.
func TestWriteBackRecordsTheDeclaredManifestKeys(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("lbl1211"))
	addr := mustAddr(t, `kubernetes_manifest.cm`)

	applied := manifestKeysAppliedValue(
		map[string]string{"tier": "one", "squad": "blue", markers.TagEstate: "lbl1211"},
		map[string]string{"reviewed": "yes", "owner": "payments"},
	)
	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:      store,
		FinalState: manifestKeysFinalState(t, addr, applied),
		Schemas:    manifestKeysSchemas(),
	}))

	keys, found, err := store.GetManifestDeclaredKeys(ctx, addr)
	if err != nil {
		t.Fatalf("GetManifestDeclaredKeys: %s", err)
	}
	if !found {
		t.Fatal("an apply of a kubernetes_manifest recorded no declared key sets at all, so no later plan can tell a removal from a key that was never there")
	}
	if want := []string{"squad", "tier", markers.TagEstate}; !equalStrings(keys[markers.LabelSurfaceAttr], want) {
		t.Errorf("recorded labels = %v, want %v", keys[markers.LabelSurfaceAttr], want)
	}
	if want := []string{"owner", "reviewed"}; !equalStrings(keys[markers.AnnotationSurfaceAttr], want) {
		t.Errorf("recorded annotations = %v, want %v", keys[markers.AnnotationSurfaceAttr], want)
	}
	// The live object's own key is on `object`, not on `manifest`. If it
	// reached the record, the next plan would propose removing a key
	// kubectl wrote and the server would put it straight back.
	for _, list := range keys {
		for _, key := range list {
			if key == "external" {
				t.Error("a key only the LIVE object carries was recorded as declared; this record would churn the plan")
			}
		}
	}
}

// TestWriteBackRefreshesTheDeclaredManifestKeys is the convergence half.
// After the removal applies, the record must say what THIS apply
// declared - otherwise the next plan re-proposes the removal for ever,
// which is the churn both rejected designs were rejected for.
//
// Proven red twice. Deleting the manifestDeclaredKeys call in
// writeBackRecordEnvelopes makes the first apply record nothing; making
// the write conditional on setResidue != nil does the same, which is the
// measurement behind that condition being absent - a kubernetes_manifest
// with no config-only block produces no residue attributes at all, so a
// write gated on the classifier would never fire for the commonest shape
// there is.
func TestWriteBackRefreshesTheDeclaredManifestKeys(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("lbl1211"))
	addr := mustAddr(t, `kubernetes_manifest.cm`)
	schemas := manifestKeysSchemas()

	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:      store,
		FinalState: manifestKeysFinalState(t, addr, manifestKeysAppliedValue(map[string]string{"tier": "one", "squad": "blue"}, map[string]string{"reviewed": "yes"})),
		Schemas:    schemas,
	}))
	before, _, err := store.GetManifestDeclaredKeys(ctx, addr)
	if err != nil {
		t.Fatalf("GetManifestDeclaredKeys: %s", err)
	}
	if !equalStrings(before[markers.LabelSurfaceAttr], []string{"squad", "tier"}) {
		t.Fatalf("the first apply recorded %v, so the second apply has nothing to change", before[markers.LabelSurfaceAttr])
	}

	// The apply that removes squad, and deletes the last annotation with
	// its map - #1211's other shape.
	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:            store,
		FinalState:       manifestKeysFinalState(t, addr, manifestKeysAppliedValue(map[string]string{"tier": "one"}, nil)),
		Schemas:          schemas,
		EnvelopeVersions: []RecordVersion{},
	}))
	after, found, err := store.GetManifestDeclaredKeys(ctx, addr)
	if err != nil {
		t.Fatalf("GetManifestDeclaredKeys: %s", err)
	}
	if !found {
		t.Fatal("the second apply erased the record rather than refreshing it")
	}
	if !equalStrings(after[markers.LabelSurfaceAttr], []string{"tier"}) {
		t.Errorf("recorded labels = %v, want only tier - a record that still names squad re-proposes a removal that has already happened, for ever", after[markers.LabelSurfaceAttr])
	}
	if list, has := after[markers.AnnotationSurfaceAttr]; !has || len(list) != 0 {
		t.Errorf("recorded annotations = %v (present %v), want an empty set", list, has)
	}
}

// TestWriteBackLeavesANonManifestTypeAlone: the whole population that is
// not Kubernetes must be byte-identical to before this member existed. An
// ordinary type's envelope carries no manifest_metadata_keys at all.
func TestWriteBackLeavesANonManifestTypeAlone(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("lbl1211"))
	addr := mustAddr(t, `null_resource.trigger`)
	schema := nullResourceSchema()

	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("i-1"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})
	src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: val}).
		Encode(schema.Block.ImpliedType(), uint64(schema.Version), 0)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	st := states.NewState()
	st.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, nullProvider, addrs.NoKey)

	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:      store,
		FinalState: st,
		Schemas: &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
			nullProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{"null_resource": schema},
			},
		}},
	}))
	if _, found, err := store.GetManifestDeclaredKeys(ctx, addr); err != nil || found {
		t.Errorf("a null_resource recorded manifest key sets (found = %v, err = %v)", found, err)
	}
}

// TestResidueEnvelopeSurvivesManifestKeysOnly: a kubernetes_manifest with
// no `timeouts` and no `field_manager` block has no residue ATTRIBUTES at
// all, so the declared key sets are the only thing in its Residue member.
// That envelope has to encode, decode and read back, or the fix works only
// for the instances that happened to need issue #275 as well.
//
// Proven red by reverting residueFields.empty() to the Attributes-only
// check: decodeEnvelope normalizes the member to nil and the whole
// kind=identity envelope is then refused as carrying nothing.
func TestResidueEnvelopeSurvivesManifestKeysOnly(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("lbl1211"))
	addr := mustAddr(t, `kubernetes_manifest.cm`)

	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:      store,
		FinalState: manifestKeysFinalState(t, addr, manifestKeysAppliedValue(map[string]string{markers.TagEstate: "lbl1211"}, nil)),
		Schemas:    manifestKeysSchemas(),
	}))

	attrs, _, keyExists, residueFound, err := store.GetResidue(ctx, addr)
	if err != nil {
		t.Fatalf("GetResidue: %s", err)
	}
	if !keyExists {
		t.Fatal("no record key was written at all")
	}
	if residueFound || len(attrs) != 0 {
		t.Errorf("residue attributes = %v, want none - this fixture declares no config-only block", attrs)
	}
	keys, found, err := store.GetManifestDeclaredKeys(ctx, addr)
	if err != nil || !found {
		t.Fatalf("the manifest-keys-only envelope did not read back: found = %v, err = %v", found, err)
	}
	if !equalStrings(keys[markers.LabelSurfaceAttr], []string{markers.TagEstate}) {
		t.Errorf("recorded labels = %v, want only the marker", keys[markers.LabelSurfaceAttr])
	}
}
