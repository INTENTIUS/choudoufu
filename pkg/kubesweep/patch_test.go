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

// TestPatchMarkersWritesTheLabelAndTheAddressAnnotation is GitHub issue
// #1639's cluster half against a fake clientset: one merge patch sets the
// tofu-estate label and the address annotation, leaves every other label
// and annotation and the spec alone, and the object read back afterwards
// carries both.
func TestPatchMarkersWritesTheLabelAndTheAddressAnnotation(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "stable.example.com", Version: "v1", Resource: "crontabs"}
	ct := &unstructured.Unstructured{}
	ct.SetAPIVersion("stable.example.com/v1")
	ct.SetKind("CronTab")
	ct.SetNamespace("smoke-crd")
	ct.SetName("my-crontab")
	ct.SetLabels(map[string]string{"app": "cron"})
	ct.SetAnnotations(map[string]string{"owner": "team-a"})
	_ = unstructured.SetNestedField(ct.Object, "my-awesome-cron-image", "spec", "image")
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "CronTabList"}, ct)
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "stable.example.com/v1", APIResources: []metav1.APIResource{
			{Name: "crontabs", Kind: "CronTab", Namespaced: true, Verbs: []string{"get", "list", "patch"}},
		}},
	}
	c := NewWith(disc, dyn)
	ref := ObjectRef{APIVersion: "stable.example.com/v1", Kind: "CronTab", Namespace: "smoke-crd", Name: "my-crontab"}

	const annotation = "choudoufu.intentius.io/tofu-address"
	_, rejected, err := c.PatchMarkers(context.Background(), ref,
		map[string]string{"tofu-estate": "smoke-crd"},
		map[string]string{annotation: "kubernetes_manifest.crontab"},
		"", false)
	if err != nil || rejected != "" {
		t.Fatalf("PatchMarkers: err=%v rejected=%q", err, rejected)
	}

	got, found, err := c.ReadObject(context.Background(), ref)
	if err != nil || !found {
		t.Fatalf("ReadObject: found=%v err=%v", found, err)
	}
	if a := got.GetAnnotations(); a[annotation] != "kubernetes_manifest.crontab" || a["owner"] != "team-a" || len(a) != 2 {
		t.Errorf("annotations read back = %v, want owner plus the address", a)
	}
	if l := got.GetLabels(); l["tofu-estate"] != "smoke-crd" || l["app"] != "cron" || len(l) != 2 {
		t.Errorf("labels read back = %v, want app plus tofu-estate", l)
	}
	if img, _, _ := unstructured.NestedString(got.Object, "spec", "image"); img != "my-awesome-cron-image" {
		t.Errorf("spec.image = %q; a marker patch touches nothing else", img)
	}
}

func TestPatchMarkersRefusesAnEmptyPatch(t *testing.T) {
	c := NewWith(&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}, nil)
	ref := ObjectRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "a", Name: "b"}
	if _, _, err := c.PatchMarkers(context.Background(), ref, nil, nil, "", true); err == nil {
		t.Fatal("an empty marker patch was sent")
	}
}
