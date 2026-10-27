// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/pkg/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #2006: kyverno's for_each scale-down of ClusterPolicies
// planned "No changes" and left the removed key's object on the cluster,
// where stock destroys it. kyverno.io's preferred version is v2 (its
// CleanupPolicy, PolicyException and UpdateRequest are served there), and
// ClusterPolicy is served at v1 only. The sweep listed each group at the
// group's preferred version alone, so it never listed a ClusterPolicy and
// the removed shard was neither an orphan nor anything else. metallb.io
// has the same shape: IPAddressPool at v1beta1, BGPPeer at the preferred
// v1beta2.
//
// This drives the real [kubesweep.Client], discovery included, over the
// fake discovery and dynamic clients: what is under test is which version
// of the group a kind is listed at.
func TestKubernetesSweepListsAKindTheGroupsPreferredVersionDoesNotServe(t *testing.T) {
	shard := func(key string) addrs.AbsResourceInstance {
		return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_manifest", Name: "policies"}.Instance(addrs.StringKey(key)).Absolute(addrs.RootModuleInstance)
	}
	verbs := []string{"get", "list", "delete"}
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	// The fake serves a group's first resource list as its preferred
	// version, and its versions in this order: v2, v1, v2beta1, as
	// kyverno's API server reports them.
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "kyverno.io/v2", APIResources: []metav1.APIResource{
			{Name: "cleanuppolicies", Kind: "CleanupPolicy", Namespaced: true, Verbs: verbs},
		}},
		{GroupVersion: "kyverno.io/v1", APIResources: []metav1.APIResource{
			{Name: "clusterpolicies", Kind: "ClusterPolicy", Verbs: verbs},
			{Name: "clusterpolicies/status", Kind: "ClusterPolicy", Verbs: []string{"get", "update"}},
		}},
		// Served again at a later version: one resource, listed once,
		// at the version the server ranks first.
		{GroupVersion: "kyverno.io/v2beta1", APIResources: []metav1.APIResource{
			{Name: "clusterpolicies", Kind: "ClusterPolicy", Verbs: verbs},
			{Name: "cleanuppolicies", Kind: "CleanupPolicy", Namespaced: true, Verbs: verbs},
		}},
	}

	v1 := schema.GroupVersionResource{Group: "kyverno.io", Version: "v1", Resource: "clusterpolicies"}
	policy := func(name string, addr addrs.AbsResourceInstance) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("kyverno.io/v1")
		u.SetKind("ClusterPolicy")
		u.SetName(name)
		u.SetLabels(map[string]string{"tofu-estate": "kyv"})
		u.SetAnnotations(map[string]string{kubesweep.AddressAnnotation: markers.EscapeAddress(addr.String())})
		return u
	}
	listKinds := map[schema.GroupVersionResource]string{
		v1: "ClusterPolicyList",
		{Group: "kyverno.io", Version: "v2", Resource: "cleanuppolicies"}:      "CleanupPolicyList",
		{Group: "kyverno.io", Version: "v2beta1", Resource: "clusterpolicies"}: "ClusterPolicyList",
		{Group: "kyverno.io", Version: "v2beta1", Resource: "cleanuppolicies"}: "CleanupPolicyList",
	}
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds,
		policy("choudoufu-shard-0", shard("shard-0")),
		policy("choudoufu-shard-1", shard("shard-1")),
		policy("choudoufu-shard-2", shard("shard-2")),
	)

	// local.policy_shards went 3 -> 2: shard-2 is no longer declared.
	req := Request{
		Estate:   "kyv",
		Sweepers: []Sweeper{KubernetesSweep{Client: kubesweep.NewWith(disc, dyn), Types: []string{"kubernetes_manifest"}, ManifestType: "kubernetes_manifest"}},
		Resolutions: []identity.Resolution{
			{Addr: shard("shard-0"), Class: identity.ClassConcrete, ImportID: kubesweep.ManifestImportID("kyverno.io/v1", "ClusterPolicy", "", "choudoufu-shard-0")},
			{Addr: shard("shard-1"), Class: identity.ClassConcrete, ImportID: kubesweep.ManifestImportID("kyverno.io/v1", "ClusterPolicy", "", "choudoufu-shard-1")},
		},
	}
	res := &Result{}
	res.Resolutions = append(res.Resolutions, req.Resolutions...)
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	wantAddr := shard("shard-2").String()
	wantID := kubesweep.ManifestImportID("kyverno.io/v1", "ClusterPolicy", "", "choudoufu-shard-2")
	if len(res.Orphans) != 1 {
		t.Fatalf("orphans = %+v, want exactly ClusterPolicy choudoufu-shard-2: the removed key's object has to be listed to be destroyed", res.Orphans)
	}
	o := res.Orphans[0]
	addr, ok := UnescapeAddress(o.Normalized)
	if !ok || addr.String() != wantAddr || o.TypeName != "kubernetes_manifest" || o.ImportID != wantID {
		t.Errorf("orphan = %s %s at %q, want kubernetes_manifest %s at %s", o.TypeName, o.ImportID, o.Normalized, wantID, wantAddr)
	}
	verified := map[string]bool{}
	for _, a := range res.VerifiedDeclared {
		verified[a.String()] = true
	}
	for _, key := range []string{"shard-0", "shard-1"} {
		if !verified[shard(key).String()] {
			t.Errorf("%s was not verified by the sweep: its ClusterPolicy was not listed", shard(key))
		}
	}
}
