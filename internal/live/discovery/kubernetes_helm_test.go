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

	"github.com/intentius/choudoufu/internal/live/kubesweep"
)

// fixedKindsClient is the real [kubesweep.Client] over a fake dynamic
// client, with Kinds answered from a fixed list: the fake discovery client
// serves no resource lists, and what this file tests is List's exclusion
// and the sweep's use of it, not API discovery.
type fixedKindsClient struct {
	*kubesweep.Client
	kinds []kubesweep.Kind
}

func (c fixedKindsClient) Kinds(context.Context, []string, string) ([]kubesweep.Kind, []string, error) {
	return c.kinds, nil, nil
}

// helmConfigMap is a ConfigMap as `helm install` leaves it: Helm's release
// annotations, the managed-by label, and a managedFields entry naming the
// helm client as the author of its content. The estate's label is on it
// because a chart value put it there, which is the shape
// live/kubernetes/COMPATIBILITY.md warned about (GitHub issue #1607).
func helmConfigMap(ns, name, release string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("ConfigMap")
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetLabels(map[string]string{
		"tofu-estate":                  "smoke-k8s",
		"app.kubernetes.io/managed-by": "Helm",
	})
	u.SetAnnotations(map[string]string{
		"meta.helm.sh/release-name":      release,
		"meta.helm.sh/release-namespace": ns,
	})
	u.SetManagedFields([]metav1.ManagedFieldsEntry{{
		Manager:   "helm",
		Operation: metav1.ManagedFieldsOperationUpdate,
		FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:data":{".":{},"f:greeting":{}},"f:metadata":{"f:annotations":{".":{},"f:meta.helm.sh/release-name":{},"f:meta.helm.sh/release-namespace":{}},"f:labels":{".":{},"f:app.kubernetes.io/managed-by":{},"f:tofu-estate":{}}}}`)},
	}})
	return u
}

// TestKubernetesSweepHoldsHelmReleaseObjects (GitHub issue #1607, ruled on
// #1604): an object carrying Helm's release annotation and the estate's
// label, which no block declares, is controller-held. The sweep does not
// propose it as an orphan, and reports it with its release. A plain
// undeclared object beside it is still an orphan, so the test can tell "the
// exclusion held" from "the sweep saw nothing".
func TestKubernetesSweepHoldsHelmReleaseObjects(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	stray := &unstructured.Unstructured{}
	stray.SetAPIVersion("v1")
	stray.SetKind("ConfigMap")
	stray.SetNamespace("smoke-k8s")
	stray.SetName("stray")
	stray.SetLabels(map[string]string{"tofu-estate": "smoke-k8s"})
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "ConfigMapList"},
		helmConfigMap("smoke-k8s", "web-greeting", "web"),
		stray,
	)
	cm := kubesweep.Kind{GVR: gvr, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map_v1"}}
	req := Request{
		Estate:   "smoke-k8s",
		Sweepers: []Sweeper{KubernetesSweep{Client: fixedKindsClient{Client: kubesweep.NewWith(&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}, dyn), kinds: []kubesweep.Kind{cm}}, Types: []string{"kubernetes_config_map_v1"}}},
	}
	res := &Result{}
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	var orphans []string
	for _, o := range res.Orphans {
		orphans = append(orphans, o.ImportID)
	}
	if len(orphans) != 1 || orphans[0] != "smoke-k8s/stray" {
		t.Fatalf("orphans = %v, want only smoke-k8s/stray: an object a Helm release holds is never proposed for removal", orphans)
	}
	if len(res.KubernetesHeld) != 1 {
		t.Fatalf("held = %+v, want the one Helm release object", res.KubernetesHeld)
	}
	h := res.KubernetesHeld[0]
	if h.Kind != "ConfigMap" || h.Namespace != "smoke-k8s" || h.Name != "web-greeting" || h.HeldBy != "Helm release smoke-k8s/web" {
		t.Errorf("held = %+v, want ConfigMap smoke-k8s/web-greeting held by Helm release smoke-k8s/web", h)
	}
	if res.KubernetesOwnerSkipped != 1 {
		t.Errorf("owner-skipped = %d, want 1: a held object is counted with the rest of what the sweep set aside", res.KubernetesOwnerSkipped)
	}
}
