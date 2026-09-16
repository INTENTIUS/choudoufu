// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// The manifest carrier (GitHub issue #1109): a custom resource is declared
// through kubernetes_manifest, whose whole object is one dynamic argument,
// and before manifest.go existed every such entry in a stock state file
// ratified UNTAGGABLE and migrated without its tofu-estate label.
//
// Proving these red, each done before the green run:
//
//   - make ratifyOne's manifested always false and
//     TestRatify_ManifestSurfaceTypeIsStamped reports UNTAGGABLE with
//     nothing patched, which is the defect verbatim.
//   - drop the another-estate branch from approveManifest and
//     TestApproveManifest_AnotherEstatesObjectIsRefused patches over
//     another estate's label.
//   - drop the changedOutsideManifestLabels branch and
//     TestApproveManifest_RefusesAWriteThatChangesMoreThanLabels writes
//     the label onto an object whose spec the server rewrote on the way
//     past.
//   - make manifestBookkeeping empty and
//     TestChangedOutsideManifestLabels_IgnoresTheServersOwnBookkeeping
//     refuses every label write ever made, because managedFields always
//     moves.

// manifestSchemaFixture is kubernetes_manifest narrowed to the shape
// markers.ManifestSurface admits: a required dynamic manifest, a computed
// dynamic object, a field_manager block, and no metadata block of its own.
func manifestSchemaFixture() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"manifest": {Type: cty.DynamicPseudoType, Required: true},
			"object":   {Type: cty.DynamicPseudoType, Computed: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"field_manager": {
				Nesting: configschema.NestingList, MaxItems: 1,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"name":            {Type: cty.String, Optional: true},
					"force_conflicts": {Type: cty.Bool, Optional: true},
				}},
			},
		},
	}}
}

// crontabObject is a kubernetes_manifest instance as the provider reads it
// back: the operator's manifest, and the live object beside it.
func crontabObject() cty.Value {
	manifest := cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("stable.example.com/v1"),
		"kind":       cty.StringVal("CronTab"),
		"metadata": cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal("my-crontab"),
			"namespace": cty.StringVal("smoke-crd"),
		}),
		"spec": cty.ObjectVal(map[string]cty.Value{
			"cronSpec": cty.StringVal("* * * * */5"),
		}),
	})
	return cty.ObjectVal(map[string]cty.Value{
		"manifest":      manifest,
		"object":        manifest,
		"field_manager": cty.ListValEmpty(cty.Object(map[string]cty.Type{"name": cty.String, "force_conflicts": cty.Bool})),
	})
}

// crontabStateJSON is what stock terraform records for the block above: a
// dynamic attribute is written with its own type beside its value, which
// is what makes the natural key readable out of a state file this fork
// never wrote.
const crontabStateJSON = `{
  "manifest": {
    "value": {"apiVersion":"stable.example.com/v1","kind":"CronTab","metadata":{"name":"my-crontab","namespace":"smoke-crd"},"spec":{"cronSpec":"* * * * */5"}},
    "type": ["object",{"apiVersion":"string","kind":"string","metadata":["object",{"name":"string","namespace":"string"}],"spec":["object",{"cronSpec":"string"}]}]
  },
  "object": {
    "value": {"apiVersion":"stable.example.com/v1","kind":"CronTab","metadata":{"name":"my-crontab","namespace":"smoke-crd"},"spec":{"cronSpec":"* * * * */5"}},
    "type": ["object",{"apiVersion":"string","kind":"string","metadata":["object",{"name":"string","namespace":"string"}],"spec":["object",{"cronSpec":"string"}]}]
  },
  "field_manager": []
}`

func crontabState() *states.State {
	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_manifest", Name: "crontab"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{AttrsJSON: []byte(crontabStateJSON), Status: states.ObjectReady},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")},
		addrs.NoKey,
	)
	return state
}

// liveCronTab is the object the fake cluster holds.
func liveCronTab(labels map[string]string) *unstructured.Unstructured {
	meta := map[string]any{
		"name":            "my-crontab",
		"namespace":       "smoke-crd",
		"resourceVersion": "812",
		"generation":      int64(1),
		"managedFields":   []any{map[string]any{"manager": "Terraform", "operation": "Apply"}},
	}
	if len(labels) > 0 {
		l := map[string]any{}
		for k, v := range labels {
			l[k] = v
		}
		meta["labels"] = l
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "stable.example.com/v1",
		"kind":       "CronTab",
		"metadata":   meta,
		"spec":       map[string]any{"cronSpec": "* * * * */5", "image": "my-awesome-cron-image"},
	}}
}

// fakeCluster stands in for an API server: it records every patch and
// answers a dry run with whatever mutate says the server would store.
type fakeCluster struct {
	object   *unstructured.Unstructured
	readErr  error
	absent   bool
	rejects  string
	mutate   func(*unstructured.Unstructured)
	patches  []fakePatch
	dryRuns  int
	realRuns int
}

type fakePatch struct {
	ref          kubesweep.ObjectRef
	key, value   string
	fieldManager string
	dryRun       bool
}

func (c *fakeCluster) ReadObject(_ context.Context, ref kubesweep.ObjectRef) (*unstructured.Unstructured, bool, error) {
	if c.readErr != nil {
		return nil, false, c.readErr
	}
	if c.absent {
		return nil, false, nil
	}
	return c.object.DeepCopy(), true, nil
}

func (c *fakeCluster) PatchLabel(_ context.Context, ref kubesweep.ObjectRef, key, value, fieldManager string, dryRun bool) (*unstructured.Unstructured, string, error) {
	c.patches = append(c.patches, fakePatch{ref: ref, key: key, value: value, fieldManager: fieldManager, dryRun: dryRun})
	if c.rejects != "" {
		return nil, c.rejects, nil
	}
	next := c.object.DeepCopy()
	labels := next.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[key] = value
	next.SetLabels(labels)
	// The server's own bookkeeping moves on any write, dry run included.
	next.SetResourceVersion("813")
	unstructured.SetNestedSlice(next.Object, []any{map[string]any{"manager": fieldManager, "operation": "Update"}}, "metadata", "managedFields")
	if c.mutate != nil {
		c.mutate(next)
	}
	if dryRun {
		c.dryRuns++
		return next, "", nil
	}
	c.realRuns++
	c.object = next
	return next.DeepCopy(), "", nil
}

func (c *fakeCluster) LabelPatcher(_ context.Context, _ addrs.AbsProviderConfig) (kubesweep.LabelPatcher, error) {
	return c, nil
}

// manifestCloudProvider is [labelCloudProvider]'s sibling for the manifest
// shape: it serves the schema, reads the object back and is never asked to
// plan or apply anything, because the manifest shape's marker write does
// not go through a provider at all.
type manifestCloudProvider struct {
	*tofu.MockProvider
	planned int
	applied int
}

func newManifestCloudProvider() *manifestCloudProvider {
	c := &manifestCloudProvider{MockProvider: &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"kubernetes_manifest": manifestSchemaFixture()},
		},
	}}
	c.ConfigureProviderCalled = true
	c.ReadResourceFn = func(providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: crontabObject()}
	}
	c.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		c.planned++
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}
	c.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		c.applied++
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return c
}

func (c *manifestCloudProvider) ConfiguredProvider(context.Context, addrs.AbsProviderConfig) (providers.Interface, error) {
	return c, nil
}

// TestRatify_ManifestSurfaceTypeIsStamped is the whole of GitHub issue
// #1109 end to end: a stock state holding a kubernetes_manifest entry
// ratifies VERIFIED with the natural key as its live id rather than
// UNTAGGABLE, -approve writes exactly the tofu-estate label through one
// merge patch, and the summary counts nothing skipped.
func TestRatify_ManifestSurfaceTypeIsStamped(t *testing.T) {
	cloud := newManifestCloudProvider()
	cluster := &fakeCluster{object: liveCronTab(nil)}

	rat, diags := Ratify(context.Background(), Request{
		Estate:    testEstate,
		State:     crontabState(),
		Providers: cloud,
		Clusters:  cluster,
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify: %s", diags.Err())
	}
	if len(rat.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(rat.Entries))
	}
	entry := rat.Entries[0]
	if entry.Status != StatusVerified {
		t.Fatalf("kubernetes_manifest.crontab ratified %s (%s), want VERIFIED: the manifest shape is a carrier (#1109)", entry.Status, entry.Detail)
	}
	wantID := "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=my-crontab"
	if entry.LiveID != wantID {
		t.Errorf("live id = %q, want the provider's own import id %q", entry.LiveID, wantID)
	}
	if len(cluster.patches) != 0 {
		t.Fatalf("Ratify patched the cluster %d time(s); it must never write", len(cluster.patches))
	}

	rep, sdiags := rat.Approve(context.Background())
	if sdiags.HasErrors() {
		t.Fatalf("Approve: %s", sdiags.Err())
	}
	if len(rep.Outcomes) != 1 || rep.Outcomes[0].Outcome != OutcomeStamped {
		t.Fatalf("Approve outcomes = %+v, want one STAMPED", rep.Outcomes)
	}
	for _, o := range rep.Outcomes {
		if o.Outcome == OutcomeSkipped || o.Outcome == OutcomeFailed {
			t.Errorf("an outcome reads %s: nothing may be skipped on a manifest migration (%s)", o.Outcome, o.Detail)
		}
	}
	if got := cluster.object.GetLabels()[markers.TagEstate]; got != testEstate {
		t.Errorf("live labels after approve = %v, want tofu-estate=%s", cluster.object.GetLabels(), testEstate)
	}
	if cloud.applied != 0 {
		t.Errorf("the provider applied %d time(s); the manifest shape's label is an API patch, never a write through the provider", cloud.applied)
	}
	if cluster.dryRuns != 1 || cluster.realRuns != 1 {
		t.Errorf("patches: %d dry run(s) and %d real, want exactly one of each", cluster.dryRuns, cluster.realRuns)
	}
	if len(cluster.patches) != 2 || !cluster.patches[0].dryRun || cluster.patches[1].dryRun {
		t.Fatalf("patch order = %+v, want the dry run first and the real write second", cluster.patches)
	}
	for _, p := range cluster.patches {
		if p.key != markers.TagEstate || p.value != testEstate {
			t.Errorf("patched %s=%s, want %s=%s and nothing else", p.key, p.value, markers.TagEstate, testEstate)
		}
		if p.ref.Kind != "CronTab" || p.ref.Namespace != "smoke-crd" || p.ref.Name != "my-crontab" || p.ref.APIVersion != "stable.example.com/v1" {
			t.Errorf("patched %+v, want the natural key out of the manifest", p.ref)
		}
	}
	if got, _, _ := unstructured.NestedString(cluster.object.Object, "spec", "image"); got != "my-awesome-cron-image" {
		t.Errorf("the object's spec.image is now %q; a label write touches nothing else", got)
	}

	// A second run over the now-labelled object writes nothing.
	rat2, _ := Ratify(context.Background(), Request{Estate: testEstate, State: crontabState(), Providers: cloud, Clusters: cluster})
	rep2, _ := rat2.Approve(context.Background())
	if len(rep2.Outcomes) != 1 || rep2.Outcomes[0].Outcome != OutcomeAlreadyStamped {
		t.Errorf("second approve outcomes = %+v, want one ALREADY_STAMPED", rep2.Outcomes)
	}
	if cluster.realRuns != 1 {
		t.Errorf("a second approve made %d real patches in total, want the first one only", cluster.realRuns)
	}
}

// manifestEligible is one manifest-shape instance ready for Approve.
func manifestEligible(cluster *fakeCluster, fieldManager string) *eligible {
	e := &eligible{
		residuable: residuable{
			provider: newManifestCloudProvider(),
			schema:   manifestSchemaFixture(),
			typeName: "kubernetes_manifest",
			applied:  crontabObject(),
		},
		manifested: true,
		manifestKey: markers.ManifestKey{
			APIVersion: "stable.example.com/v1",
			Kind:       "CronTab",
			Namespace:  "smoke-crd",
			Name:       "my-crontab",
		},
		fieldManager: fieldManager,
	}
	if cluster != nil {
		e.patcher = cluster
	} else {
		e.patcherErr = errors.New("no kubeconfig")
	}
	return e
}

func TestApproveManifest_AnotherEstatesObjectIsRefused(t *testing.T) {
	cluster := &fakeCluster{object: liveCronTab(map[string]string{markers.TagEstate: "other-team"})}
	out := approveOne(context.Background(), testEstate, mustAddr(t, "kubernetes_manifest.crontab"), manifestEligible(cluster, ""), "")
	if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, "other-team") {
		t.Fatalf("outcome = %s (%s), want FAILED naming other-team", out.Outcome, out.Detail)
	}
	if len(cluster.patches) != 0 {
		t.Errorf("patched %d time(s) on another estate's object, want 0", len(cluster.patches))
	}
}

func TestApproveManifest_RefusesAWriteThatChangesMoreThanLabels(t *testing.T) {
	cases := map[string]struct {
		mutate func(*unstructured.Unstructured)
		want   string
	}{
		"a mutating webhook rewrites the spec": {
			mutate: func(u *unstructured.Unstructured) {
				unstructured.SetNestedField(u.Object, "rewritten-by-the-webhook", "spec", "image")
			},
			want: "spec.image",
		},
		"a mutating webhook adds an annotation": {
			mutate: func(u *unstructured.Unstructured) {
				unstructured.SetNestedStringMap(u.Object, map[string]string{"injected": "yes"}, "metadata", "annotations")
			},
			want: "metadata.annotations",
		},
		"a mutating webhook drops a field": {
			mutate: func(u *unstructured.Unstructured) {
				unstructured.RemoveNestedField(u.Object, "spec", "cronSpec")
			},
			want: "spec.cronSpec",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cluster := &fakeCluster{object: liveCronTab(nil), mutate: tc.mutate}
			out := approveOne(context.Background(), testEstate, mustAddr(t, "kubernetes_manifest.crontab"), manifestEligible(cluster, ""), "")
			if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, tc.want) {
				t.Fatalf("outcome = %s (%s), want FAILED naming %s", out.Outcome, out.Detail, tc.want)
			}
			if cluster.realRuns != 0 {
				t.Errorf("the real patch was sent %d time(s) after the dry run said no, want 0", cluster.realRuns)
			}
			if got := cluster.object.GetLabels()[markers.TagEstate]; got != "" {
				t.Errorf("the object carries tofu-estate=%q after a refusal", got)
			}
		})
	}
}

func TestApproveManifest_ServerRejectionIsReportedInTheServersWords(t *testing.T) {
	cluster := &fakeCluster{object: liveCronTab(nil), rejects: `crontabs.stable.example.com "my-crontab" is forbidden: User "alice" cannot patch resource`}
	out := approveOne(context.Background(), testEstate, mustAddr(t, "kubernetes_manifest.crontab"), manifestEligible(cluster, ""), "")
	if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, `cannot patch resource`) {
		t.Fatalf("outcome = %s (%s), want FAILED quoting the server", out.Outcome, out.Detail)
	}
	if cluster.realRuns != 0 {
		t.Errorf("the real patch was sent after the dry run was refused")
	}
}

func TestApproveManifest_NoClusterClientSaysSoRatherThanSilentlySkipping(t *testing.T) {
	out := approveOne(context.Background(), testEstate, mustAddr(t, "kubernetes_manifest.crontab"), manifestEligible(nil, ""), "")
	if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, "no kubeconfig") {
		t.Fatalf("outcome = %s (%s), want FAILED carrying the reason no client could be built", out.Outcome, out.Detail)
	}
}

func TestApproveManifest_AbsentObjectIsRefused(t *testing.T) {
	cluster := &fakeCluster{absent: true}
	out := approveOne(context.Background(), testEstate, mustAddr(t, "kubernetes_manifest.crontab"), manifestEligible(cluster, ""), "")
	if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, "nothing to label") {
		t.Fatalf("outcome = %s (%s), want FAILED because the object is gone", out.Outcome, out.Detail)
	}
}

func TestApproveManifest_FieldManagerIsTheBlocksOrTheProvidersDefault(t *testing.T) {
	cluster := &fakeCluster{object: liveCronTab(nil)}
	if out := approveOne(context.Background(), testEstate, mustAddr(t, "kubernetes_manifest.crontab"), manifestEligible(cluster, "my-pipeline"), ""); out.Outcome != OutcomeStamped {
		t.Fatalf("outcome = %s (%s)", out.Outcome, out.Detail)
	}
	for _, p := range cluster.patches {
		if p.fieldManager != "my-pipeline" {
			t.Errorf("patched under field manager %q, want the block's own my-pipeline", p.fieldManager)
		}
	}

	// Unset, the caller passes "" and kubesweep substitutes the
	// provider's default, so the seam carries the empty string through.
	cluster2 := &fakeCluster{object: liveCronTab(nil)}
	approveOne(context.Background(), testEstate, mustAddr(t, "kubernetes_manifest.crontab"), manifestEligible(cluster2, ""), "")
	for _, p := range cluster2.patches {
		if p.fieldManager != "" {
			t.Errorf("patched under field manager %q, want the provider's default (empty, resolved by kubesweep)", p.fieldManager)
		}
	}
}

func TestManifestFieldManager_ReadsTheBlockTheStateRecorded(t *testing.T) {
	fmType := cty.Object(map[string]cty.Type{"name": cty.String, "force_conflicts": cty.Bool})
	with := cty.ObjectVal(map[string]cty.Value{
		"manifest": cty.NullVal(cty.DynamicPseudoType),
		"object":   cty.NullVal(cty.DynamicPseudoType),
		"field_manager": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":            cty.StringVal("my-pipeline"),
			"force_conflicts": cty.False,
		})}),
	})
	if got := manifestFieldManager(with); got != "my-pipeline" {
		t.Errorf("field manager = %q, want my-pipeline", got)
	}
	without := cty.ObjectVal(map[string]cty.Value{
		"manifest":      cty.NullVal(cty.DynamicPseudoType),
		"object":        cty.NullVal(cty.DynamicPseudoType),
		"field_manager": cty.ListValEmpty(fmType),
	})
	if got := manifestFieldManager(without); got != "" {
		t.Errorf("field manager = %q with no block, want the empty string", got)
	}
}

func TestChangedOutsideManifestLabels_IgnoresTheServersOwnBookkeeping(t *testing.T) {
	live := liveCronTab(map[string]string{"app": "web"})
	after := live.DeepCopy()
	after.SetLabels(map[string]string{"app": "web", markers.TagEstate: testEstate})
	after.SetResourceVersion("999")
	unstructured.SetNestedField(after.Object, int64(2), "metadata", "generation")
	unstructured.SetNestedSlice(after.Object, []any{map[string]any{"manager": "Terraform", "operation": "Update"}}, "metadata", "managedFields")
	if got := changedOutsideManifestLabels(live.Object, after.Object); len(got) != 0 {
		t.Fatalf("a plain label write reports %v as changed outside the labels; every write ever made would be refused", got)
	}
}

func TestChangedOutsideManifestLabels_CatchesTheShallowestDifference(t *testing.T) {
	live := liveCronTab(nil)
	after := live.DeepCopy()
	unstructured.SetNestedField(after.Object, "rewritten", "spec", "image")
	unstructured.SetNestedField(after.Object, int64(3), "spec", "replicas")
	got := changedOutsideManifestLabels(live.Object, after.Object)
	if len(got) != 2 || got[0] != "spec.image" || got[1] != "spec.replicas" {
		t.Fatalf("changed = %v, want [spec.image spec.replicas] in that order", got)
	}
}

// TestChangedOutsideManifestLabels_MetadataBookkeepingIsOnlyExemptAtTheTop
// pins that the exemption is the OBJECT's own metadata and not any map
// called metadata: a mask wider than its label is the shape that has
// caught real defects in this repository.
func TestChangedOutsideManifestLabels_MetadataBookkeepingIsOnlyExemptAtTheTop(t *testing.T) {
	live := liveCronTab(nil)
	unstructured.SetNestedStringMap(live.Object, map[string]string{"app": "web"}, "spec", "template", "metadata", "labels")
	after := live.DeepCopy()
	unstructured.SetNestedStringMap(after.Object, map[string]string{"app": "rewritten"}, "spec", "template", "metadata", "labels")
	got := changedOutsideManifestLabels(live.Object, after.Object)
	if len(got) != 1 || got[0] != "spec.template.metadata.labels.app" {
		t.Fatalf("changed = %v, want the pod template's own labels reported", got)
	}
}
