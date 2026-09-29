// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"sort"
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

// helmReleaseSecret is the Helm release history Secret [kubesweep.Client]
// checks for before calling an annotated object held (GitHub issue #1625).
func helmReleaseSecret(ns, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Secret")
	u.SetNamespace(ns)
	u.SetName("sh.helm.release.v1." + name + ".v1")
	u.SetLabels(map[string]string{"owner": "helm", "name": name, "status": "deployed"})
	return u
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
	secretGVR := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "ConfigMapList", secretGVR: "SecretList"},
		helmConfigMap("smoke-k8s", "web-greeting", "web"),
		helmReleaseSecret("smoke-k8s", "web"),
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
	// One list for both substrates (the 2026-09-26 ruling on #1604): a
	// Helm-held object is reported where an ACK-made bucket is.
	if len(res.ControllerHeld) != 1 {
		t.Fatalf("held = %+v, want the one Helm release object", res.ControllerHeld)
	}
	h := res.ControllerHeld[0]
	if h.TypeName != "kubernetes_config_map_v1" || h.Kind != "ConfigMap" || h.ImportID != "smoke-k8s/web-greeting" || h.Controller != "Helm" || h.HeldBy != "Helm release smoke-k8s/web" {
		t.Errorf("held = %+v, want kubernetes_config_map_v1 ConfigMap smoke-k8s/web-greeting held by Helm release smoke-k8s/web", h)
	}
	if res.KubernetesOwnerSkipped != 1 {
		t.Errorf("owner-skipped = %d, want 1: a held object is counted with the rest of what the sweep set aside", res.KubernetesOwnerSkipped)
	}
}

// TestKubernetesSweepStopsHoldingWhenReleaseSecretIsGone (GitHub issue
// #1625): the same object, the same annotation, but no release secret in
// the cluster at all - the release Helm named is gone. The sweep no longer
// holds it: it is an ordinary orphan, the same as the stray ConfigMap
// beside it, because helm's own field manager (not a control-plane one)
// wrote its content.
func TestKubernetesSweepStopsHoldingWhenReleaseSecretIsGone(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	secretGVR := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	stray := &unstructured.Unstructured{}
	stray.SetAPIVersion("v1")
	stray.SetKind("ConfigMap")
	stray.SetNamespace("smoke-k8s")
	stray.SetName("stray")
	stray.SetLabels(map[string]string{"tofu-estate": "smoke-k8s"})
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "ConfigMapList", secretGVR: "SecretList"},
		helmConfigMap("smoke-k8s", "web-greeting", "web"),
		// No helmReleaseSecret fixture: the release's history is gone.
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
	sort.Strings(orphans)
	if len(orphans) != 2 || orphans[0] != "smoke-k8s/stray" || orphans[1] != "smoke-k8s/web-greeting" {
		t.Fatalf("orphans = %v, want smoke-k8s/stray and smoke-k8s/web-greeting: no release names the second one any more", orphans)
	}
	if len(res.ControllerHeld) != 0 {
		t.Errorf("held = %+v, want none: the annotation names a release that no longer exists", res.ControllerHeld)
	}
	if res.KubernetesOwnerSkipped != 0 {
		t.Errorf("owner-skipped = %d, want 0", res.KubernetesOwnerSkipped)
	}
}
