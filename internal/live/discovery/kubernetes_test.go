// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
)

// stubSweeper stands in for a cluster: fixed kinds, fixed objects per
// kind, and one kind whose list fails.
type stubSweeper struct {
	kinds    []kubesweep.Kind
	unserved []string
	objects  map[string][]kubesweep.Object // by kind
	failKind string
	listed   []string
}

func (s *stubSweeper) Kinds(_ context.Context, _ []string, _ string) ([]kubesweep.Kind, []string, error) {
	return s.kinds, s.unserved, nil
}

func (s *stubSweeper) List(_ context.Context, k kubesweep.Kind, key, value string) ([]kubesweep.Object, int, error) {
	s.listed = append(s.listed, k.Kind+" "+key+"="+value)
	if k.Kind == s.failKind {
		return nil, 0, errors.New("forbidden")
	}
	return s.objects[k.Kind], 1, nil
}

func k8sInstance(t *testing.T, typeName, name string) addrs.AbsResourceInstance {
	t.Helper()
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
}

// TestKubernetesSweepFilesOrphansAtASyntheticAddress (GitHub issue #1065):
// a listed object that no concrete resolution declares becomes an orphan
// whose Normalized value is an escaped address classifyOrphans can turn
// back into an instance; a declared one does not; a kind the cluster does
// not serve, and one whose list fails, are sweep gaps; every listed kind is
// a covered, server-side-filtered scan.
func TestKubernetesSweepFilesOrphansAtASyntheticAddress(t *testing.T) {
	cm := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", Namespaced: true, TypeNames: []string{"kubernetes_config_map", "kubernetes_config_map_v1"}}
	ns := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, Kind: "Namespace", TypeNames: []string{"kubernetes_namespace"}}
	sa := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}, Kind: "ServiceAccount", Namespaced: true, TypeNames: []string{"kubernetes_service_account"}}
	sweeper := &stubSweeper{
		kinds:    []kubesweep.Kind{cm, ns, sa},
		unserved: []string{"kubernetes_storage_class"},
		objects: map[string][]kubesweep.Object{
			"ConfigMap": {
				{Kind: "ConfigMap", Namespace: "smoke-k8s", Name: "app-config", ImportID: "smoke-k8s/app-config", Labels: map[string]string{"tofu-estate": "smoke-k8s"}},
				{Kind: "ConfigMap", Namespace: "smoke-k8s", Name: "doomed-config", ImportID: "smoke-k8s/doomed-config", Labels: map[string]string{"tofu-estate": "smoke-k8s"}},
			},
			"Namespace": {{Kind: "Namespace", Name: "smoke-k8s", ImportID: "smoke-k8s", Labels: map[string]string{"tofu-estate": "smoke-k8s"}}},
		},
		failKind: "ServiceAccount",
	}
	req := Request{
		Estate:          "smoke-k8s",
		Kubernetes:      sweeper,
		KubernetesTypes: []string{"kubernetes_config_map", "kubernetes_config_map_v1", "kubernetes_namespace", "kubernetes_service_account", "kubernetes_storage_class"},
		Resolutions: []identity.Resolution{
			{Addr: k8sInstance(t, "kubernetes_config_map", "app"), Class: identity.ClassConcrete, ImportID: "smoke-k8s/app-config"},
			{Addr: k8sInstance(t, "kubernetes_namespace", "app"), Class: identity.ClassConcrete, ImportID: "smoke-k8s"},
		},
	}
	res := &Result{}
	diags := sweepKubernetes(context.Background(), req, res)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	if len(res.Orphans) != 1 {
		t.Fatalf("orphans = %v, want exactly the undeclared ConfigMap", res.Orphans)
	}
	o := res.Orphans[0]
	if o.TypeName != "kubernetes_config_map" {
		t.Errorf("orphan type = %q, want the type the configuration declares for the kind", o.TypeName)
	}
	if o.ImportID != "smoke-k8s/doomed-config" || o.Marker != "smoke-k8s" || !o.Swept {
		t.Errorf("orphan = %+v", o)
	}
	addr, ok := UnescapeAddress(o.Normalized)
	if !ok {
		t.Fatalf("orphan's Normalized %q does not unescape to an address; classifyOrphans would withhold it as malformed", o.Normalized)
	}
	if addr.String() != "kubernetes_config_map.orphan_smoke-k8s_doomed-config" {
		t.Errorf("orphan address = %s", addr)
	}

	// Every listed kind's types are covered scans; the failed and the
	// unserved ones are gaps.
	covered := map[string]bool{}
	for _, c := range res.SweepCovered {
		covered[c] = true
	}
	for _, want := range []string{"kubernetes_config_map", "kubernetes_config_map_v1", "kubernetes_namespace"} {
		if !covered[want] {
			t.Errorf("%s not in SweepCovered %v", want, res.SweepCovered)
		}
	}
	gaps := map[string]SweepGapReason{}
	for _, g := range res.SweepGaps {
		gaps[g.TypeName] = g.Reason
	}
	if gaps["kubernetes_service_account"] != SweepGapListFailed {
		t.Errorf("service account gap = %v, want LIST_FAILED", gaps["kubernetes_service_account"])
	}
	if gaps["kubernetes_storage_class"] != SweepGapNotListable {
		t.Errorf("storage class gap = %v, want TYPE_NOT_LISTABLE", gaps["kubernetes_storage_class"])
	}
	for _, scan := range res.Scans {
		if scan.Source != SourceKubernetes || scan.Filtering != FilterServerSide || scan.Scope != ScopeEstate || !scan.Sweep {
			t.Errorf("scan %+v is not a server-side, estate-scoped Kubernetes sweep scan", scan)
		}
	}
	if res.KubernetesOwnerSkipped != 2 {
		t.Errorf("owner-skipped = %d, want 2 (one per successful list)", res.KubernetesOwnerSkipped)
	}
	if len(sweeper.listed) != 3 || sweeper.listed[0] != "ConfigMap tofu-estate=smoke-k8s" {
		t.Errorf("lists issued = %v; want one per kind, selected on the estate label", sweeper.listed)
	}
}

func TestKubernetesSweepDoesNothingWithoutASweeper(t *testing.T) {
	res := &Result{}
	if diags := sweepKubernetes(context.Background(), Request{Estate: "x"}, res); diags.HasErrors() || len(res.Orphans) != 0 || len(res.Scans) != 0 {
		t.Errorf("a request with no Kubernetes sweeper changed the result: %+v", res)
	}
}

// TestKubernetesSweepFilesManifestKindOrphans (GitHub issue #1079's third
// unit): a kind listed under kubernetes_manifest files its undeclared
// object at kubernetes_manifest.orphan_<kind>_<namespace>_<name> with the
// manifest import id; a CronTab a manifest block declares is not an
// orphan; a ConfigMap a manifest block declares is not an orphan of the
// built-in type either, since both meet on the natural key; and the
// manifest type is one covered scan however many kinds it listed.
func TestKubernetesSweepFilesManifestKindOrphans(t *testing.T) {
	cm := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map_v1"}}
	ct := kubesweep.Kind{GVR: schema.GroupVersionResource{Group: "stable.example.com", Version: "v1", Resource: "crontabs"}, Kind: "CronTab", Namespaced: true, APIVersion: "stable.example.com/v1", TypeNames: []string{"kubernetes_manifest"}, Manifest: true}
	ci := kubesweep.Kind{GVR: schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}, Kind: "ClusterIssuer", APIVersion: "cert-manager.io/v1", TypeNames: []string{"kubernetes_manifest"}, Manifest: true}
	labels := map[string]string{"tofu-estate": "smoke-crd"}
	sweeper := &stubSweeper{
		kinds: []kubesweep.Kind{cm, ct, ci},
		objects: map[string][]kubesweep.Object{
			"ConfigMap": {{Kind: "ConfigMap", Namespace: "smoke-crd", Name: "via-manifest", ImportID: "smoke-crd/via-manifest", Labels: labels}},
			"CronTab": {
				{Kind: "CronTab", Namespace: "smoke-crd", Name: "my-crontab", ImportID: "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=my-crontab", Labels: labels},
				{Kind: "CronTab", Namespace: "smoke-crd", Name: "doomed", ImportID: "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=doomed", Labels: labels},
			},
			"ClusterIssuer": {{Kind: "ClusterIssuer", Name: "letsencrypt", ImportID: "apiVersion=cert-manager.io/v1,kind=ClusterIssuer,name=letsencrypt", Labels: labels}},
		},
	}
	req := Request{
		Estate:                 "smoke-crd",
		Kubernetes:             sweeper,
		KubernetesTypes:        []string{"kubernetes_config_map_v1", "kubernetes_manifest"},
		KubernetesManifestType: "kubernetes_manifest",
		Resolutions: []identity.Resolution{
			{Addr: k8sInstance(t, "kubernetes_manifest", "crontab"), Class: identity.ClassConcrete, ImportID: "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=my-crontab"},
			{Addr: k8sInstance(t, "kubernetes_manifest", "cm"), Class: identity.ClassConcrete, ImportID: "apiVersion=v1,kind=ConfigMap,namespace=smoke-crd,name=via-manifest"},
		},
	}
	res := &Result{}
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	var got []string
	for _, o := range res.Orphans {
		addr, ok := UnescapeAddress(o.Normalized)
		if !ok {
			t.Fatalf("orphan %q does not unescape to an address", o.Normalized)
		}
		got = append(got, o.TypeName+"|"+addr.String()+"|"+o.ImportID+"|"+o.DisplayName)
	}
	want := []string{
		"kubernetes_manifest" + "|kubernetes_manifest.orphan_clusterissuer_letsencrypt|apiVersion=cert-manager.io/v1,kind=ClusterIssuer,name=letsencrypt|ClusterIssuer letsencrypt",
		"kubernetes_manifest" + "|kubernetes_manifest.orphan_crontab_smoke-crd_doomed|apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=doomed|CronTab smoke-crd/doomed",
	}
	if len(got) != len(want) {
		t.Fatalf("orphans = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("orphans[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	manifestScans := 0
	for _, s := range res.Scans {
		if s.TypeName == "kubernetes_manifest" {
			manifestScans++
			if s.Declared != 2 {
				t.Errorf("manifest scan Declared = %d, want 2 (both manifest blocks)", s.Declared)
			}
		}
	}
	if manifestScans != 1 {
		t.Errorf("manifest scans = %d, want exactly one for two listed kinds", manifestScans)
	}
	covered := 0
	for _, c := range res.SweepCovered {
		if c == "kubernetes_manifest" {
			covered++
		}
	}
	if covered != 1 {
		t.Errorf("kubernetes_manifest appears %d times in SweepCovered, want once", covered)
	}
}
