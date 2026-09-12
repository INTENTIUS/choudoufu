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

func (s *stubSweeper) Kinds(_ context.Context, _ []string) ([]kubesweep.Kind, []string, error) {
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
