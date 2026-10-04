// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/managedfields"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/providers"
)

var fieldGranularCMGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

// fieldGranularCluster is a ConfigMap-serving fake whose tracker keeps
// managedFields the way the API server does (client-go's field-managed
// tracker), so a hand-over is measured on the same bookkeeping a real
// server keeps. The ConfigMap ns/shared exists with label team=a applied
// under applier.
func fieldGranularCluster(t *testing.T, applier string) (*kubesweep.Client, *fakedynamic.FakeDynamicClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	gv := schema.GroupVersion{Version: "v1"}
	scheme.AddKnownTypeWithName(gv.WithKind("ConfigMap"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(gv.WithKind("ConfigMapList"), &unstructured.UnstructuredList{})
	tracker := clienttesting.NewFieldManagedObjectTracker(scheme, serializer.NewCodecFactory(scheme).UniversalDecoder(), managedfields.NewDeducedTypeConverter())
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{fieldGranularCMGVR: "ConfigMapList"})
	dyn.ReactionChain = nil
	dyn.AddReactor("*", "*", clienttesting.ObjectReaction(tracker))
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: []string{"get", "list", "patch"}},
		}},
	}
	cm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "shared", "namespace": "ns", "labels": map[string]any{"team": "a"}},
	}}
	if _, err := dyn.Resource(fieldGranularCMGVR).Namespace("ns").Apply(context.Background(), "shared", cm, metav1.ApplyOptions{FieldManager: applier}); err != nil {
		t.Fatalf("seeding the ConfigMap under %s: %s", applier, err)
	}
	return kubesweep.NewWith(disc, dyn), dyn
}

var fieldGranularAddr = addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_labels", Name: "team"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)

func fieldGranularEligible(client *kubesweep.Client, stateManager string) *eligible {
	e := &eligible{fieldGranular: true, fieldManager: stateManager, fieldWriteOK: true}
	e.typeName = "kubernetes_labels"
	e.fieldWrite = discovery.FieldGranularWrite{
		Addr:   fieldGranularAddr,
		Object: kubesweep.ObjectRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns", Name: "shared"},
		Writes: []kubesweep.FieldWrite{{Root: []string{"f:metadata", "f:labels"}, Members: []string{kubesweep.MapMember("team")}}},
	}
	if client != nil {
		e.transferer = client
	}
	return e
}

func ownersOfTeam(t *testing.T, dyn *fakedynamic.FakeDynamicClient) []string {
	t.Helper()
	obj, err := dyn.Resource(fieldGranularCMGVR).Namespace("ns").Get(context.Background(), "shared", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, o := range kubesweep.FieldOwners(obj, kubesweep.FieldWrite{Root: []string{"f:metadata", "f:labels"}, Members: []string{"f:team"}}, "") {
		out = append(out, o.Manager)
	}
	return out
}

// TestApproveFieldGranularHandsTheFieldsOver (GitHub issue #1863): a stock
// instance's label, owned by "Terraform", is owned by the estate's field
// manager alone after -approve, with no value changed; a rerun writes
// nothing and says so.
func TestApproveFieldGranularHandsTheFieldsOver(t *testing.T) {
	client, dyn := fieldGranularCluster(t, kubesweep.DefaultFieldManager)

	out := approveFieldGranular(t.Context(), testEstate, fieldGranularAddr, fieldGranularEligible(client, kubesweep.DefaultFieldManager))
	if out.Outcome != OutcomeStamped {
		t.Fatalf("outcome = %s (%s), want STAMPED", out.Outcome, out.Detail)
	}
	if got := ownersOfTeam(t, dyn); len(got) != 1 || got[0] != "choudoufu:"+testEstate {
		t.Errorf("team is owned by %v after the hand-over, want choudoufu:%s alone", got, testEstate)
	}
	obj, _ := dyn.Resource(fieldGranularCMGVR).Namespace("ns").Get(context.Background(), "shared", metav1.GetOptions{})
	if obj.GetLabels()["team"] != "a" {
		t.Errorf("the label's value moved: %v", obj.GetLabels())
	}

	rerun := approveFieldGranular(t.Context(), testEstate, fieldGranularAddr, fieldGranularEligible(client, kubesweep.DefaultFieldManager))
	if rerun.Outcome != OutcomeAlreadyStamped {
		t.Errorf("rerun = %s (%s), want ALREADY_STAMPED", rerun.Outcome, rerun.Detail)
	}
}

// TestApproveFieldGranularNeverTakesAnotherEstatesFields: a state naming
// another estate's manager is refused by name, and nothing is written.
func TestApproveFieldGranularNeverTakesAnotherEstatesFields(t *testing.T) {
	client, dyn := fieldGranularCluster(t, "choudoufu:other")
	out := approveFieldGranular(t.Context(), testEstate, fieldGranularAddr, fieldGranularEligible(client, "choudoufu:other"))
	if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, `"other"`) {
		t.Errorf("outcome = %s (%s), want FAILED naming the estate other", out.Outcome, out.Detail)
	}
	if got := ownersOfTeam(t, dyn); len(got) != 1 || got[0] != "choudoufu:other" {
		t.Errorf("team is owned by %v, want choudoufu:other untouched", got)
	}
}

// TestApproveFieldGranularRefusesWhatItCannotDo: no cluster client, a
// recorded object this run cannot name, and fields nobody it may take them
// from owns are each FAILED with the reason, never a silent success.
func TestApproveFieldGranularRefusesWhatItCannotDo(t *testing.T) {
	client, _ := fieldGranularCluster(t, "kubectl-label")

	noClient := fieldGranularEligible(nil, kubesweep.DefaultFieldManager)
	noClient.patcherErr = errExampleNoClient
	if out := approveFieldGranular(t.Context(), testEstate, fieldGranularAddr, noClient); out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, errExampleNoClient.Error()) {
		t.Errorf("no client: %s (%s)", out.Outcome, out.Detail)
	}

	unnamed := fieldGranularEligible(client, kubesweep.DefaultFieldManager)
	unnamed.fieldWriteOK = false
	if out := approveFieldGranular(t.Context(), testEstate, fieldGranularAddr, unnamed); out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, "does not name the object") {
		t.Errorf("unnamed object: %s (%s)", out.Outcome, out.Detail)
	}

	// kubectl-label wrote the label: neither Terraform nor the estate owns
	// it, and a hand-over of nothing is not a migration.
	if out := approveFieldGranular(t.Context(), testEstate, fieldGranularAddr, fieldGranularEligible(client, kubesweep.DefaultFieldManager)); out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, "Something else holds them") {
		t.Errorf("fields held by another manager: %s (%s)", out.Outcome, out.Detail)
	}
}

var errExampleNoClient = &exampleErr{"this run was started without a Kubernetes cluster client"}

type exampleErr struct{ s string }

func (e *exampleErr) Error() string { return e.s }

// TestStateFieldManagerDefaultsToTheProviders: a state that names no
// field_manager wrote under the provider's default.
func TestStateFieldManagerDefaultsToTheProviders(t *testing.T) {
	for _, tc := range []struct {
		v    cty.Value
		want string
	}{
		{cty.ObjectVal(map[string]cty.Value{"field_manager": cty.NullVal(cty.String)}), kubesweep.DefaultFieldManager},
		{cty.ObjectVal(map[string]cty.Value{"field_manager": cty.StringVal("")}), kubesweep.DefaultFieldManager},
		{cty.ObjectVal(map[string]cty.Value{"field_manager": cty.StringVal("my-tool")}), "my-tool"},
		{cty.EmptyObjectVal, kubesweep.DefaultFieldManager},
	} {
		if got := stateFieldManager(tc.v); got != tc.want {
			t.Errorf("stateFieldManager(%#v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

// TestFieldGranularTypeReadsTheShapeOnTheKubernetesProvider: the carrier
// is chosen by schema shape on the Kubernetes family's provider, never by
// a type name, and not on another provider's.
func TestFieldGranularTypeReadsTheShapeOnTheKubernetesProvider(t *testing.T) {
	s := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"field_manager": {Type: cty.String, Optional: true},
			"force":         {Type: cty.Bool, Optional: true},
			"labels":        {Type: cty.Map(cty.String), Required: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{"metadata": {Nesting: configschema.NestingList, MaxItems: 1, Block: configschema.Block{
			Attributes: map[string]*configschema.Attribute{"name": {Type: cty.String, Required: true}},
		}}},
	}}
	k8s := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	aws := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}
	if !fieldGranularType(k8s, s) {
		t.Error("the field-granular shape on the kubernetes provider is not the carrier")
	}
	if fieldGranularType(aws, s) {
		t.Error("the shape on another provider was taken for the Kubernetes carrier")
	}
}
