// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1391. A migrated kubernetes_manifest lost the one fact a
// stock state file was keeping for it: which metadata.labels and
// metadata.annotations keys its configuration last declared.
//
// The apply write-back records that set (#1211,
// projection.ManifestDeclaredKeys), and the plan's removal analysis is
// (recorded) minus (currently declared). live-import wrote no such set at
// all, so after a migration and the state-file delete the adopt page asks
// for, a label deleted from the configuration was proposed for removal by
// nothing and kept on the live object for ever - where stock reads its
// last-applied manifest and removes it. The read side degrades silently by
// design ("A MISSING record proposes removing nothing"), so nothing said so.
//
// Proving these red, done before the green run: drop the manifestKeys
// argument from the projection.RecordResidueForInstance call in stamp.go's
// recordResidueFor, and TestApprove_SeedsManifestDeclaredKeys reports no
// key set recorded at all - the defect verbatim.
//
// Both tests assert on the RENDERED KEY SET the store ends up holding and
// not on a "did it write" boolean, for untaggable_residue_test.go's reason:
// the wrong set produces a plan that is empty and wrong, and no
// verdict-level check sees that.

// crontabStateWithMetadata is crontabState with metadata.labels and
// metadata.annotations declared inside the manifest, as stock terraform
// records them for a block that wrote them.
func crontabStateWithMetadata() *states.State {
	const attrs = `{
  "manifest": {
    "value": {"apiVersion":"stable.example.com/v1","kind":"CronTab","metadata":{"name":"my-crontab","namespace":"smoke-crd","labels":{"team":"a","tier":"batch"},"annotations":{"owner":"platform"}},"spec":{"cronSpec":"* * * * */5"}},
    "type": ["object",{"apiVersion":"string","kind":"string","metadata":["object",{"name":"string","namespace":"string","labels":["object",{"team":"string","tier":"string"}],"annotations":["object",{"owner":"string"}]}],"spec":["object",{"cronSpec":"string"}]}]
  },
  "object": {
    "value": {"apiVersion":"stable.example.com/v1","kind":"CronTab","metadata":{"name":"my-crontab","namespace":"smoke-crd","labels":{"team":"a","tier":"batch"},"annotations":{"owner":"platform"}},"spec":{"cronSpec":"* * * * */5"}},
    "type": ["object",{"apiVersion":"string","kind":"string","metadata":["object",{"name":"string","namespace":"string","labels":["object",{"team":"string","tier":"string"}],"annotations":["object",{"owner":"string"}]}],"spec":["object",{"cronSpec":"string"}]}]
  },
  "field_manager": []
}`
	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_manifest", Name: "crontab"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{AttrsJSON: []byte(attrs), Status: states.ObjectReady},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")},
		addrs.NoKey,
	)
	return state
}

// crontabReadWithoutMetadata is what the provider hands back from
// ReadResource in this fixture: the same object with metadata.labels and
// metadata.annotations MISSING from `manifest`, and one key under a name
// the state never declared.
//
// It is deliberately not the state's own object. Whether
// hashicorp/kubernetes carries `manifest` through its read unchanged has
// never been checked, and for a stampable instance the residue carrier's
// applied value IS that read (ratify.go's own doc comment). So a seed taken
// from applied would record "no annotations, and a label the configuration
// never wrote", which is the wrong answer in both directions - and this
// fixture is the only thing in the suite that can tell the two sources
// apart.
func crontabReadWithoutMetadata() cty.Value {
	manifest := cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("stable.example.com/v1"),
		"kind":       cty.StringVal("CronTab"),
		"metadata": cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal("my-crontab"),
			"namespace": cty.StringVal("smoke-crd"),
			"labels":    cty.ObjectVal(map[string]cty.Value{"read-only-key": cty.StringVal("x")}),
		}),
		"spec": cty.ObjectVal(map[string]cty.Value{"cronSpec": cty.StringVal("* * * * */5")}),
	})
	return cty.ObjectVal(map[string]cty.Value{
		"manifest":      manifest,
		"object":        manifest,
		"field_manager": cty.ListValEmpty(cty.Object(map[string]cty.Type{"name": cty.String, "force_conflicts": cty.Bool})),
	})
}

// declaredKeysProvider serves the manifest schema and answers ReadResource
// with read, so a test can make the live read differ from the state.
type declaredKeysProvider struct {
	*tofu.MockProvider
}

func newDeclaredKeysProvider(read cty.Value) *declaredKeysProvider {
	c := &declaredKeysProvider{MockProvider: &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"kubernetes_manifest": manifestSchemaFixture()},
		},
	}}
	c.ConfigureProviderCalled = true
	c.ReadResourceFn = func(providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: read}
	}
	c.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}
	c.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return c
}

func (c *declaredKeysProvider) ConfiguredProvider(context.Context, addrs.AbsProviderConfig) (providers.Interface, error) {
	return c, nil
}

func declaredKeysStore(t *testing.T) *projection.RecordStore {
	t.Helper()
	backend, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	return projection.NewRecordEnvelopeStore(backend, projection.RecordKeyPrefix(testEstate))
}

func recordedManifestKeys(t *testing.T, store *projection.RecordStore, addr addrs.AbsResourceInstance) map[string][]string {
	t.Helper()
	keys, found, err := store.GetManifestDeclaredKeys(context.Background(), addr)
	if err != nil {
		t.Fatalf("reading the manifest declared keys for %s: %s", addr, err)
	}
	if !found {
		return nil
	}
	return keys
}

func sameKeys(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestApprove_SeedsManifestDeclaredKeys is #1391's direct counterpart: after
// an approved migration of a kubernetes_manifest entry the estate's record
// holds the metadata key sets the STATE's manifest declared, marker key
// included, which is the only thing a later live-plan can propose a removal
// from.
func TestApprove_SeedsManifestDeclaredKeys(t *testing.T) {
	addr := mustAddr(t, "kubernetes_manifest.crontab")
	store := declaredKeysStore(t)
	cloud := newDeclaredKeysProvider(crontabReadWithoutMetadata())
	cluster := &fakeCluster{object: liveCronTab(map[string]string{"team": "a", "tier": "batch"})}

	rat, diags := Ratify(context.Background(), Request{
		Estate:      testEstate,
		State:       crontabStateWithMetadata(),
		Providers:   cloud,
		Clusters:    cluster,
		RecordStore: store,
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify: %s", diags.Err())
	}
	if got := recordedManifestKeys(t, store, addr); got != nil {
		t.Fatalf("Ratify recorded %v; Ratify must never write", got)
	}

	rep, sdiags := rat.Approve(context.Background())
	if sdiags.HasErrors() {
		t.Fatalf("Approve: %s", sdiags.Err())
	}
	if out := onlyOutcome(t, rep); out.Outcome != OutcomeStamped {
		t.Fatalf("outcome = %s (%s), want STAMPED", out.Outcome, out.Detail)
	}

	keys := recordedManifestKeys(t, store, addr)
	if keys == nil {
		t.Fatalf("no metadata key set was recorded for %s - this is #1391's exact symptom: delete the state file as the adopt page says, remove a label from the configuration, and the next plan proposes removing nothing", addr)
	}
	if !sameKeys(keys[markers.LabelSurfaceAttr], "team", "tier", markers.TagEstate) {
		t.Errorf("recorded labels = %v, want the state's own team and tier plus the marker key this migration writes", keys[markers.LabelSurfaceAttr])
	}
	if !sameKeys(keys[markers.AnnotationSurfaceAttr], "owner") {
		t.Errorf("recorded annotations = %v, want the state's own owner", keys[markers.AnnotationSurfaceAttr])
	}
	for _, k := range keys[markers.LabelSurfaceAttr] {
		if k == "read-only-key" {
			t.Errorf("recorded labels = %v: the set came from the provider's READ, not from the state's recorded manifest. A read is not what the last apply declared", keys[markers.LabelSurfaceAttr])
		}
	}

	// A second run over the now-labelled object rewrites the same set rather
	// than failing on the version it never read (live-import is documented
	// idempotent).
	rat2, _ := Ratify(context.Background(), Request{Estate: testEstate, State: crontabStateWithMetadata(), Providers: cloud, Clusters: cluster, RecordStore: store})
	if _, d := rat2.Approve(context.Background()); d.HasErrors() {
		t.Fatalf("second Approve: %s", d.Err())
	}
	again := recordedManifestKeys(t, store, addr)
	if !sameKeys(again[markers.LabelSurfaceAttr], "team", "tier", markers.TagEstate) || !sameKeys(again[markers.AnnotationSurfaceAttr], "owner") {
		t.Errorf("a second migration left %v, want the first run's set unchanged", again)
	}
}

// TestApprove_TypedKubernetesEntryGetsNoManifestKeys is the other half of
// #1391's acceptance, and the guard on the blast radius: a typed
// kubernetes_* resource carries its labels in a metadata BLOCK the provider
// reads back like any other argument, so there is no "what did we declare"
// question to answer and no key set to record. The seed is keyed on
// markers.ManifestSurface over the provider's own schema, never on a type
// name, so this is what every non-manifest type in every estate gets.
func TestApprove_TypedKubernetesEntryGetsNoManifestKeys(t *testing.T) {
	addr := mustAddr(t, "kubernetes_config_map.app")
	store := declaredKeysStore(t)
	cloud := newLabelCloudProvider(map[string]string{"app": "web", "team": "a"})

	rat, diags := Ratify(context.Background(), Request{
		Estate:      testEstate,
		State:       configMapState(map[string]string{"app": "web", "team": "a"}),
		Providers:   cloud,
		RecordStore: store,
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify: %s", diags.Err())
	}
	rep, sdiags := rat.Approve(context.Background())
	if sdiags.HasErrors() {
		t.Fatalf("Approve: %s", sdiags.Err())
	}
	if out := onlyOutcome(t, rep); out.Outcome != OutcomeStamped {
		t.Fatalf("outcome = %s (%s), want STAMPED", out.Outcome, out.Detail)
	}
	if got := recordedManifestKeys(t, store, addr); got != nil {
		t.Errorf("a typed kubernetes_config_map recorded metadata key sets %v; only the manifest shape has a declared-key question, and recording one here would put the removal analysis in front of a type whose labels the provider reads back", got)
	}
}
