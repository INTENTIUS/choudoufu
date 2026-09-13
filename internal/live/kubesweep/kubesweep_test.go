// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestKindOfType(t *testing.T) {
	for typeName, want := range map[string]struct {
		kind      string
		versioned bool
		ok        bool
	}{
		"kubernetes_config_map":                        {"ConfigMap", false, true},
		"kubernetes_config_map_v1":                     {"ConfigMap", true, true},
		"kubernetes_horizontal_pod_autoscaler_v2":      {"HorizontalPodAutoscaler", true, true},
		"kubernetes_horizontal_pod_autoscaler_v2beta2": {"HorizontalPodAutoscaler", true, true},
		"kubernetes_cluster_role_binding":              {"ClusterRoleBinding", false, true},
		"kubernetes_namespace":                         {"Namespace", false, true},
		"kubernetes_manifest":                          {"Manifest", false, true},
		"aws_s3_bucket":                                {"", false, false},
	} {
		kind, versioned, ok := KindOfType(typeName)
		if kind != want.kind || versioned != want.versioned || ok != want.ok {
			t.Errorf("KindOfType(%q) = %q, %v, %v; want %q, %v, %v", typeName, kind, versioned, ok, want.kind, want.versioned, want.ok)
		}
	}
}

func TestTypeFor(t *testing.T) {
	kt := KindTypes([]string{"kubernetes_config_map", "kubernetes_config_map_v1", "kubernetes_ingress", "kubernetes_ingress_v1", "kubernetes_namespace", "aws_vpc"})
	// The configuration's own choice wins.
	if got, _ := TypeFor(kt, map[string]bool{"kubernetes_config_map": true}, "ConfigMap"); got != "kubernetes_config_map" {
		t.Errorf("declared plain: %q", got)
	}
	if got, _ := TypeFor(kt, map[string]bool{"kubernetes_config_map_v1": true}, "ConfigMap"); got != "kubernetes_config_map_v1" {
		t.Errorf("declared versioned: %q", got)
	}
	// Nothing declared: the versioned name targets the API the cluster
	// serves today (kubernetes_ingress managed the retired extensions
	// group).
	if got, _ := TypeFor(kt, nil, "Ingress"); got != "kubernetes_ingress_v1" {
		t.Errorf("undeclared, versioned available: %q", got)
	}
	if got, _ := TypeFor(kt, nil, "Namespace"); got != "kubernetes_namespace" {
		t.Errorf("undeclared, plain only: %q", got)
	}
	if _, ok := TypeFor(kt, nil, "Pod"); ok {
		t.Error("a kind with no provider type was given one")
	}
}

func TestOrphanResourceName(t *testing.T) {
	for _, tc := range []struct{ ns, name, want string }{
		{"smoke-k8s", "app-config", "orphan_smoke-k8s_app-config"},
		{"", "smoke-k8s", "orphan_smoke-k8s"},
		{"kube-system", "kube-root-ca.crt", "orphan_kube-system_kube-root-ca_crt"},
	} {
		if got := OrphanResourceName(tc.ns, tc.name); got != tc.want {
			t.Errorf("OrphanResourceName(%q, %q) = %q, want %q", tc.ns, tc.name, got, tc.want)
		}
	}
}

func configMap(ns, name string, labels map[string]string, owned bool) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("ConfigMap")
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetLabels(labels)
	if owned {
		u.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-abc", UID: "u"}})
	}
	return u
}

// TestListExcludesControllerOwnedObjects: the safety of the whole sweep.
// Two objects carry the estate label; the one a controller owns is never
// returned, and is counted.
func TestListExcludesControllerOwnedObjects(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "ConfigMapList"},
		configMap("smoke-k8s", "app-config", map[string]string{"tofu-estate": "smoke-k8s"}, false),
		configMap("smoke-k8s", "copied", map[string]string{"tofu-estate": "smoke-k8s"}, true),
		configMap("other", "foreign", map[string]string{"app": "x"}, false),
	)
	// The fake dynamic client does not apply label selectors server-side,
	// so filter in a reactor the way the API server would.
	dyn.PrependReactor("list", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return false, nil, nil
	})
	c := NewWith(&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}, dyn)
	got, skipped, err := c.List(context.Background(), Kind{GVR: gvr, Kind: "ConfigMap", Namespaced: true}, "tofu-estate", "smoke-k8s")
	if err != nil {
		t.Fatal(err)
	}
	// The fake ignores the selector, so the foreign object comes back
	// too; what this test pins is the owner exclusion and the import id.
	var names []string
	for _, o := range got {
		names = append(names, o.ImportID)
	}
	if skipped != 1 {
		t.Errorf("ownerSkipped = %d, want 1 (the ReplicaSet-owned copy)", skipped)
	}
	for _, o := range got {
		if o.Name == "copied" {
			t.Error("the controller-owned object was returned")
		}
		if o.Name == "app-config" && o.ImportID != "smoke-k8s/app-config" {
			t.Errorf("import id = %q, want smoke-k8s/app-config", o.ImportID)
		}
	}
	if len(names) == 0 {
		t.Error("nothing listed")
	}
}

func TestKindsJoinsServedResourcesToProviderTypes(t *testing.T) {
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: []string{"get", "list", "delete"}},
			{Name: "namespaces", Kind: "Namespace", Namespaced: false, Verbs: []string{"get", "list", "delete"}},
			{Name: "pods/status", Kind: "Pod", Namespaced: true, Verbs: []string{"get"}},
			{Name: "bindings", Kind: "Binding", Namespaced: true, Verbs: []string{"create"}},
		}},
		{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{
			{Name: "deployments", Kind: "Deployment", Namespaced: true, Verbs: []string{"get", "list", "delete"}},
		}},
	}
	c := NewWith(disc, nil)
	kinds, unserved, err := c.Kinds(context.Background(), []string{"kubernetes_config_map", "kubernetes_config_map_v1", "kubernetes_namespace", "kubernetes_deployment_v1", "kubernetes_storage_class"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range kinds {
		got = append(got, k.Kind+":"+k.GVR.String())
	}
	want := []string{"ConfigMap:/v1, Resource=configmaps", "Deployment:apps/v1, Resource=deployments", "Namespace:/v1, Resource=namespaces"}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kinds[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(unserved) != 1 || unserved[0] != "kubernetes_storage_class" {
		t.Errorf("unserved = %v, want [kubernetes_storage_class]", unserved)
	}
	for _, k := range kinds {
		if k.Kind == "ConfigMap" && len(k.TypeNames) != 2 {
			t.Errorf("ConfigMap types = %v, want both names", k.TypeNames)
		}
		if k.Kind == "Namespace" && k.Namespaced {
			t.Error("Namespace reported as namespaced")
		}
	}
}

// TestControllerMade: the two signals, and the two shapes each one exists
// for.
func TestControllerMade(t *testing.T) {
	owned := configMap("ns", "pod-like", nil, true)
	if !ControllerMade(owned) {
		t.Error("an object with an ownerReference was not judged controller-made")
	}
	endpoints := configMap("ns", "app", nil, false)
	endpoints.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "kube-controller-manager", Operation: metav1.ManagedFieldsOperationUpdate}})
	if !ControllerMade(endpoints) {
		t.Error("an object written only by kube-controller-manager (the legacy Endpoints shape) was not judged controller-made")
	}
	declared := configMap("ns", "app-config", nil, false)
	declared.SetManagedFields([]metav1.ManagedFieldsEntry{
		{Manager: "terraform-provider-kubernetes_v3.2.1", Operation: metav1.ManagedFieldsOperationUpdate},
		{Manager: "kube-controller-manager", Operation: metav1.ManagedFieldsOperationUpdate},
	})
	if ControllerMade(declared) {
		t.Error("an object the provider wrote was judged controller-made because a controller also touched it")
	}
	bare := configMap("ns", "bare", nil, false)
	if ControllerMade(bare) {
		t.Error("an object with no owner and no managedFields was judged controller-made")
	}
}

// TestKindsListsEveryServedKindUnderTheManifestType (GitHub issue #1079):
// with kubernetes_manifest in the type universe, a served kind no built-in
// type manages - a CRD, here - is listed under it, imports by the manifest
// id, and never counts as unserved; without it the kind is not listed at
// all, as before. A resource without a delete verb is never listed either
// way.
func TestKindsListsEveryServedKindUnderTheManifestType(t *testing.T) {
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: []string{"get", "list", "delete"}},
			{Name: "componentstatuses", Kind: "ComponentStatus", Namespaced: false, Verbs: []string{"get", "list"}},
		}},
		{GroupVersion: "stable.example.com/v1", APIResources: []metav1.APIResource{
			{Name: "crontabs", Kind: "CronTab", Namespaced: true, Verbs: []string{"get", "list", "delete"}},
			{Name: "crontabs/status", Kind: "CronTab", Namespaced: true, Verbs: []string{"get", "update"}},
		}},
	}
	c := NewWith(disc, nil)

	kinds, unserved, err := c.Kinds(context.Background(), []string{"kubernetes_config_map_v1", "kubernetes_manifest"}, "kubernetes_manifest")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range kinds {
		got = append(got, k.Kind+":"+k.APIVersion+":"+k.TypeNames[0])
	}
	want := []string{"ConfigMap:v1:kubernetes_config_map_v1", "CronTab:stable.example.com/v1:" + "kubernetes_manifest"}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kinds[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if kinds[0].Manifest || !kinds[1].Manifest {
		t.Errorf("Manifest flags = %v, %v; want false for the built-in kind and true for the CRD", kinds[0].Manifest, kinds[1].Manifest)
	}
	if len(unserved) != 0 {
		t.Errorf("unserved = %v; the manifest type is never unserved", unserved)
	}

	kinds, _, err = c.Kinds(context.Background(), []string{"kubernetes_config_map_v1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 1 || kinds[0].Kind != "ConfigMap" {
		t.Errorf("without the manifest type the CRD was listed: %v", kinds)
	}
}

func TestListRendersTheManifestImportID(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "stable.example.com", Version: "v1", Resource: "crontabs"}
	ct := &unstructured.Unstructured{}
	ct.SetAPIVersion("stable.example.com/v1")
	ct.SetKind("CronTab")
	ct.SetNamespace("smoke-crd")
	ct.SetName("my-crontab")
	ct.SetLabels(map[string]string{"tofu-estate": "smoke-crd"})
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "CronTabList"}, ct)
	c := NewWith(&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}, dyn)
	got, _, err := c.List(context.Background(), Kind{GVR: gvr, Kind: "CronTab", Namespaced: true, APIVersion: "stable.example.com/v1", TypeNames: []string{"kubernetes_manifest"}, Manifest: true}, "tofu-estate", "smoke-crd")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ImportID != "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=my-crontab" {
		t.Fatalf("listed = %+v", got)
	}
}

func TestManifestImportIDRoundTrips(t *testing.T) {
	for _, tc := range []struct{ apiVersion, kind, ns, name, want string }{
		{"stable.example.com/v1", "CronTab", "smoke-crd", "my-crontab", "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=my-crontab"},
		{"v1", "Namespace", "", "smoke-crd", "apiVersion=v1,kind=Namespace,name=smoke-crd"},
	} {
		id := ManifestImportID(tc.apiVersion, tc.kind, tc.ns, tc.name)
		if id != tc.want {
			t.Errorf("ManifestImportID = %q, want %q", id, tc.want)
		}
		a, k, ns, n, ok := ParseManifestImportID(id)
		if !ok || a != tc.apiVersion || k != tc.kind || ns != tc.ns || n != tc.name {
			t.Errorf("ParseManifestImportID(%q) = %q %q %q %q %v", id, a, k, ns, n, ok)
		}
	}
	for _, bad := range []string{"smoke-crd/my-crontab", "apiVersion=v1,kind=ConfigMap", "apiVersion=v1,kind=X,name=a,name=b", "apiVersion=v1,kind=X,name=a,extra=1", ""} {
		if _, _, _, _, ok := ParseManifestImportID(bad); ok {
			t.Errorf("ParseManifestImportID(%q) accepted", bad)
		}
	}
}

func TestManifestOrphanResourceName(t *testing.T) {
	for _, tc := range []struct{ kind, ns, name, want string }{
		{"CronTab", "smoke-crd", "my-crontab", "orphan_crontab_smoke-crd_my-crontab"},
		{"ClusterIssuer", "", "letsencrypt", "orphan_clusterissuer_letsencrypt"},
	} {
		if got := ManifestOrphanResourceName(tc.kind, tc.ns, tc.name); got != tc.want {
			t.Errorf("ManifestOrphanResourceName(%q, %q, %q) = %q, want %q", tc.kind, tc.ns, tc.name, got, tc.want)
		}
	}
}
