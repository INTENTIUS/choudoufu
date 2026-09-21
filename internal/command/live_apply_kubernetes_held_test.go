// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1184: a delete the cluster has only accepted. These drive
// the runner's own two hooks - AfterPlan, where the plan's deletes are read,
// and AfterApply, where the cluster is asked - against a stub cluster, the
// way the backend calls them around a real apply.
// live/smoke/scenarios/k8s-a-held-delete-is-not-gone.sh is the same check
// against kind.

// heldStubSweeper is a cluster after an apply: fixed kinds, and per kind
// whatever is still there. It counts every request it is sent.
type heldStubSweeper struct {
	liveLsStubSweeper
	kindsCalls int
}

func (s *heldStubSweeper) Kinds(ctx context.Context, types []string, manifestType string) ([]kubesweep.Kind, []string, error) {
	s.kindsCalls++
	return s.liveLsStubSweeper.Kinds(ctx, types, manifestType)
}

// objectMetaShapeSchema is a built-in Kubernetes type by shape
// (identity.ObjectMetaShape): one metadata block with a name, a namespace,
// labels and a computed uid.
func objectMetaShapeSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{"data": {Type: cty.Map(cty.String), Optional: true}},
		BlockTypes: map[string]*configschema.NestedBlock{"metadata": {Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1, Block: configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"name":      {Type: cty.String, Optional: true},
				"namespace": {Type: cty.String, Optional: true},
				"labels":    {Type: cty.Map(cty.String), Optional: true},
				"uid":       {Type: cty.String, Computed: true},
			},
		}}},
	}}
}

func heldTestChange(provider addrs.AbsProviderConfig, typeName, name string, action plans.Action) *plans.ResourceInstanceChangeSrc {
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	return &plans.ResourceInstanceChangeSrc{Addr: addr, PrevRunAddr: addr, ProviderAddr: provider, ChangeSrc: plans.ChangeSrc{Action: action}}
}

// heldTestRun is one apply as the runner sees it: the plan's changes, the
// identities PriorState resolved, and the cluster as the apply left it.
func heldTestRun(t *testing.T, sweeper kubesweep.Sweeper, changes []*plans.ResourceInstanceChangeSrc, resolutions []identity.Resolution) tfdiags.Diagnostics {
	t.Helper()
	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		provider.Provider: {ResourceTypes: map[string]providers.Schema{
			"kubernetes_config_map": objectMetaShapeSchema(),
			"kubernetes_secret":     objectMetaShapeSchema(),
			"kubernetes_manifest":   *manifestShapeSchema(),
		}},
	}}
	planned := plans.NewChanges()
	sync := planned.SyncWrapper()
	for _, c := range changes {
		sync.AppendResourceInstanceChange(c)
	}
	provs := &statelessProviders{}
	provs.rememberKubernetesSweeper(provider, sweeper)
	r := &statelessRunner{
		kubeSweepers: provs.kubernetesSweepers(),
		resolver: &projection.NodeResolver{
			Estate:      "smoke-k8s",
			MarkerIndex: projection.NewMarkerIndex(resolutions),
		},
	}
	if diags := r.AfterPlan(context.Background(), nil, &plans.Plan{Changes: planned}, schemas); len(diags) != 0 {
		t.Fatalf("AfterPlan raised diagnostics over a plan of deletes: %v", diags)
	}
	return r.AfterApply(context.Background())
}

func heldTestResolution(typeName, name, importID string) identity.Resolution {
	return identity.Resolution{
		Addr:     addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
		Class:    identity.ClassConcrete,
		ImportID: importID,
	}
}

var (
	heldTestProvider = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	heldTestCM       = kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map"}}
	heldTestSecret   = kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, Kind: "Secret", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_secret"}}
	heldTestCronTab  = kubesweep.Kind{GVR: schema.GroupVersionResource{Group: "stable.example.com", Version: "v1", Resource: "crontabs"}, Kind: "CronTab", Namespaced: true, APIVersion: "stable.example.com/v1", TypeNames: []string{"kubernetes_manifest"}, Manifest: true}
	heldTestLabels   = map[string]string{"tofu-estate": "smoke-k8s"}
)

// TestHeldKubernetesDeleteIsNamedAfterApply is the issue's own measurement:
// an apply deletes two ConfigMaps - one a block removed from source, which
// the sweep planned at its synthetic orphan address, and one still declared,
// which is what `apply -destroy` deletes - and the cluster keeps one of
// them, terminating, under a finalizer. One warning, naming that object and
// its finalizer and nothing else; one list, of the one kind that had
// deletes.
func TestHeldKubernetesDeleteIsNamedAfterApply(t *testing.T) {
	sweeper := &heldStubSweeper{liveLsStubSweeper: liveLsStubSweeper{
		kinds: []kubesweep.Kind{heldTestCM, heldTestSecret, heldTestCronTab},
		objects: map[string][]kubesweep.Object{
			"ConfigMap": {
				// Deleted by this run and held.
				{Kind: "ConfigMap", Namespace: "smoke-k8s", Name: "held-config", ImportID: "smoke-k8s/held-config", Labels: heldTestLabels,
					DeletionTimestamp: "2026-09-16T08:23:38Z", Finalizers: []string{"smoke.choudoufu.io/hold", "backup.example.com/snapshot"}},
				// Terminating, but not by this run: someone else's delete.
				{Kind: "ConfigMap", Namespace: "smoke-k8s", Name: "bystander", ImportID: "smoke-k8s/bystander", Labels: heldTestLabels,
					DeletionTimestamp: "2026-09-16T08:00:00Z", Finalizers: []string{"other.example.com/hold"}},
				// This run updated it; it is simply there.
				{Kind: "ConfigMap", Namespace: "smoke-k8s", Name: "kept", ImportID: "smoke-k8s/kept", Labels: heldTestLabels},
			},
		},
	}}
	diags := heldTestRun(t, sweeper,
		[]*plans.ResourceInstanceChangeSrc{
			heldTestChange(heldTestProvider, "kubernetes_config_map", "orphan_smoke-k8s_held-config", plans.Delete),
			heldTestChange(heldTestProvider, "kubernetes_config_map", "app", plans.Delete),
			heldTestChange(heldTestProvider, "kubernetes_config_map", "kept", plans.Update),
		},
		[]identity.Resolution{
			heldTestResolution("kubernetes_config_map", "orphan_smoke-k8s_held-config", "smoke-k8s/held-config"),
			heldTestResolution("kubernetes_config_map", "app", "smoke-k8s/app-config"),
			heldTestResolution("kubernetes_config_map", "kept", "smoke-k8s/kept"),
		})

	if len(diags) != 1 {
		t.Fatalf("diagnostics = %v, want exactly one warning", diags)
	}
	if diags[0].Severity() != tfdiags.Warning {
		t.Errorf("severity = %v, want a warning: the apply did what it was asked and its exit code is the apply's", diags[0].Severity())
	}
	const wantSummary = "Delete accepted, object not gone"
	const wantDetail = "The API server accepted the delete of 1 object this run destroyed, and it is still in the cluster, terminating:\n" +
		"\n" +
		"  - ConfigMap smoke-k8s/held-config (kubernetes_config_map.orphan_smoke-k8s_held-config), finalizers: smoke.choudoufu.io/hold, backup.example.com/snapshot\n" +
		"\n" +
		"It stays until the controller that owns each finalizer removes it. This run's destroyed count includes it. It still carries the estate's label, so the next plan will propose destroying it again until it is gone."
	if got := diags[0].Description().Summary; got != wantSummary {
		t.Errorf("summary = %q, want %q", got, wantSummary)
	}
	if got := diags[0].Description().Detail; got != wantDetail {
		t.Errorf("detail:\n%s\nwant:\n%s", got, wantDetail)
	}
	if want := []string{"ConfigMap tofu-estate=smoke-k8s"}; len(sweeper.selectors) != 1 || sweeper.selectors[0] != want[0] {
		t.Errorf("lists = %v, want %v: one list, of the one kind that had deletes, by the estate's selector", sweeper.selectors, want)
	}
}

// TestHeldKubernetesDeletesAcrossKindsAndShapes: a delete through the
// manifest type is joined by its import id's kind and group, a replace's
// delete leg counts, and each kind with deletes is listed once however many
// deletes it had. An object with a deletionTimestamp and no finalizer (the
// server still finishing) is named as that, and one with no deletionTimestamp at all is not named.
func TestHeldKubernetesDeletesAcrossKindsAndShapes(t *testing.T) {
	sweeper := &heldStubSweeper{liveLsStubSweeper: liveLsStubSweeper{
		kinds: []kubesweep.Kind{heldTestCM, heldTestSecret, heldTestCronTab},
		objects: map[string][]kubesweep.Object{
			"ConfigMap": {
				{Kind: "ConfigMap", Namespace: "a", Name: "one", ImportID: "a/one", Labels: heldTestLabels, DeletionTimestamp: "2026-09-16T08:23:38Z"},
				// Planned for delete and still there with no
				// deletionTimestamp: nothing asked it to go, so it is
				// not a held delete and is not named.
				{Kind: "ConfigMap", Namespace: "a", Name: "two", ImportID: "a/two", Labels: heldTestLabels, Finalizers: []string{"never/asked"}},
			},
			"CronTab": {{Kind: "CronTab", Namespace: "a", Name: "tab", ImportID: "apiVersion=stable.example.com/v1,kind=CronTab,namespace=a,name=tab", Labels: heldTestLabels,
				DeletionTimestamp: "2026-09-16T08:23:39Z", Finalizers: []string{"stable.example.com/cleanup"}}},
		},
	}}
	diags := heldTestRun(t, sweeper,
		[]*plans.ResourceInstanceChangeSrc{
			heldTestChange(heldTestProvider, "kubernetes_config_map", "one", plans.DeleteThenCreate),
			heldTestChange(heldTestProvider, "kubernetes_config_map", "two", plans.Delete),
			heldTestChange(heldTestProvider, "kubernetes_manifest", "tab", plans.Delete),
		},
		[]identity.Resolution{
			heldTestResolution("kubernetes_config_map", "one", "a/one"),
			heldTestResolution("kubernetes_config_map", "two", "a/two"),
			heldTestResolution("kubernetes_manifest", "tab", "apiVersion=stable.example.com/v1,kind=CronTab,namespace=a,name=tab"),
		})
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %v, want exactly one warning for both objects", diags)
	}
	const wantDetail = "The API server accepted the delete of 2 objects this run destroyed, and they are still in the cluster, terminating:\n" +
		"\n" +
		"  - ConfigMap a/one (kubernetes_config_map.one), no finalizers: the server has not finished the delete yet\n" +
		"  - CronTab a/tab (kubernetes_manifest.tab), finalizers: stable.example.com/cleanup\n" +
		"\n" +
		"Each stays until the controller that owns each of its finalizers removes it. This run's destroyed count includes them. They still carry the estate's label, so the next plan will propose destroying them again until they are gone."
	if got := diags[0].Description().Detail; got != wantDetail {
		t.Errorf("detail:\n%s\nwant:\n%s", got, wantDetail)
	}
	if len(sweeper.selectors) != 2 || sweeper.selectors[0] != "ConfigMap tofu-estate=smoke-k8s" || sweeper.selectors[1] != "CronTab tofu-estate=smoke-k8s" {
		t.Errorf("lists = %v, want one for ConfigMap and one for CronTab and none for Secret", sweeper.selectors)
	}
}

// TestCompletedKubernetesDeletesSayNothing: the deletes all finished, so the
// one list the kind costs comes back without them and the run says nothing.
func TestCompletedKubernetesDeletesSayNothing(t *testing.T) {
	sweeper := &heldStubSweeper{liveLsStubSweeper: liveLsStubSweeper{
		kinds:   []kubesweep.Kind{heldTestCM, heldTestSecret},
		objects: map[string][]kubesweep.Object{"ConfigMap": {{Kind: "ConfigMap", Namespace: "smoke-k8s", Name: "kept", ImportID: "smoke-k8s/kept", Labels: heldTestLabels}}},
	}}
	diags := heldTestRun(t, sweeper,
		[]*plans.ResourceInstanceChangeSrc{
			heldTestChange(heldTestProvider, "kubernetes_config_map", "app", plans.Delete),
			heldTestChange(heldTestProvider, "kubernetes_config_map", "held", plans.Delete),
		},
		[]identity.Resolution{
			heldTestResolution("kubernetes_config_map", "app", "smoke-k8s/app-config"),
			heldTestResolution("kubernetes_config_map", "held", "smoke-k8s/held-config"),
		})
	if len(diags) != 0 {
		t.Errorf("diagnostics = %v, want none: both objects are gone", diags)
	}
	if len(sweeper.selectors) != 1 {
		t.Errorf("lists = %v, want exactly one", sweeper.selectors)
	}
}

// TestAnApplyThatDeletedNothingAsksTheClusterNothing: creates and updates
// only. No discovery, no list, no diagnostics - the check costs a run that
// deleted nothing not one request.
func TestAnApplyThatDeletedNothingAsksTheClusterNothing(t *testing.T) {
	sweeper := &heldStubSweeper{liveLsStubSweeper: liveLsStubSweeper{
		kinds: []kubesweep.Kind{heldTestCM},
		objects: map[string][]kubesweep.Object{"ConfigMap": {{Kind: "ConfigMap", Namespace: "smoke-k8s", Name: "app-config", ImportID: "smoke-k8s/app-config", Labels: heldTestLabels,
			DeletionTimestamp: "2026-09-16T08:23:38Z", Finalizers: []string{"x/y"}}}},
	}}
	diags := heldTestRun(t, sweeper,
		[]*plans.ResourceInstanceChangeSrc{
			heldTestChange(heldTestProvider, "kubernetes_config_map", "app", plans.Update),
			heldTestChange(heldTestProvider, "kubernetes_config_map", "new", plans.Create),
		},
		[]identity.Resolution{heldTestResolution("kubernetes_config_map", "app", "smoke-k8s/app-config")})
	if len(diags) != 0 {
		t.Errorf("diagnostics = %v, want none", diags)
	}
	if sweeper.kindsCalls != 0 || len(sweeper.selectors) != 0 {
		t.Errorf("the cluster was asked %d discovery and %d list requests, want none of either", sweeper.kindsCalls, len(sweeper.selectors))
	}
}

// TestHeldKubernetesDeleteCheckIsSilentWhenTheClusterCannotAnswer: the list
// fails. The apply succeeded and the next plan lists the same kind and says
// so if it cannot; this check raises nothing of its own, and never an error.
func TestHeldKubernetesDeleteCheckIsSilentWhenTheClusterCannotAnswer(t *testing.T) {
	for name, sweeper := range map[string]*heldStubSweeper{
		"list fails":      {liveLsStubSweeper: liveLsStubSweeper{kinds: []kubesweep.Kind{heldTestCM}, failKind: "ConfigMap"}},
		"discovery fails": {liveLsStubSweeper: liveLsStubSweeper{kindsErr: errors.New("connection refused")}},
	} {
		diags := heldTestRun(t, sweeper,
			[]*plans.ResourceInstanceChangeSrc{heldTestChange(heldTestProvider, "kubernetes_config_map", "app", plans.Delete)},
			[]identity.Resolution{heldTestResolution("kubernetes_config_map", "app", "smoke-k8s/app-config")})
		if len(diags) != 0 {
			t.Errorf("%s: diagnostics = %v, want none", name, diags)
		}
	}
}
