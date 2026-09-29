// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

// TestListCarriesDeletionTimestampAndFinalizers (GitHub issue #1184): a
// terminating object is listed like any other, and what says it is
// terminating - metadata.deletionTimestamp, and the finalizers holding it -
// comes through the listing, so the post-apply check needs no second read.
func TestListCarriesDeletionTimestampAndFinalizers(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	held := configMap("smoke-k8s", "held-config", map[string]string{"tofu-estate": "smoke-k8s"}, false)
	at := metav1.NewTime(time.Date(2026, 9, 16, 8, 23, 38, 0, time.FixedZone("plus2", 2*60*60)))
	held.SetDeletionTimestamp(&at)
	held.SetFinalizers([]string{"smoke.choudoufu.io/hold"})
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "ConfigMapList"},
		held,
		configMap("smoke-k8s", "app-config", map[string]string{"tofu-estate": "smoke-k8s"}, false),
	)
	c := NewWith(&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}, dyn)
	got, _, err := c.List(context.Background(), Kind{GVR: gvr, Kind: "ConfigMap", Namespaced: true}, "tofu-estate", "smoke-k8s")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %v, want both objects: a terminating object is still a live, labelled one", got)
	}
	for _, o := range got {
		switch o.Name {
		case "held-config":
			if o.DeletionTimestamp != "2026-09-16T06:23:38Z" {
				t.Errorf("deletionTimestamp = %q, want 2026-09-16T06:23:38Z (RFC 3339, UTC)", o.DeletionTimestamp)
			}
			if len(o.Finalizers) != 1 || o.Finalizers[0] != "smoke.choudoufu.io/hold" {
				t.Errorf("finalizers = %v, want [smoke.choudoufu.io/hold]", o.Finalizers)
			}
		case "app-config":
			if o.DeletionTimestamp != "" || len(o.Finalizers) != 0 {
				t.Errorf("an object nobody asked to go reads deletionTimestamp=%q finalizers=%v, want neither", o.DeletionTimestamp, o.Finalizers)
			}
		}
	}
}

// TestKindsAnswersTheSameQuestionOnce (GitHub issue #1184): the post-apply
// check asks the client the question the sweep already asked, and must not
// pay API discovery for it a second time. A different question is asked
// afresh, and a failure is never remembered.
func TestKindsAnswersTheSameQuestionOnce(t *testing.T) {
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	disc.Resources = []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{
		{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: []string{"get", "list", "delete"}},
	}}}
	c := NewWith(disc, nil)
	types := []string{"kubernetes_config_map"}
	first, _, err := c.Kinds(context.Background(), types, "")
	if err != nil {
		t.Fatal(err)
	}
	asked := len(disc.Actions())
	if asked == 0 {
		t.Fatal("the first Kinds made no discovery request, so this test cannot see a second one")
	}
	second, _, err := c.Kinds(context.Background(), types, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(disc.Actions()) != asked {
		t.Errorf("the same question cost %d more discovery requests, want 0", len(disc.Actions())-asked)
	}
	if len(first) != 1 || len(second) != 1 || first[0].GVR != second[0].GVR {
		t.Errorf("answers differ: %v then %v", first, second)
	}
	second[0].Kind = "scribbled"
	if third, _, _ := c.Kinds(context.Background(), types, ""); third[0].Kind != "ConfigMap" {
		t.Error("a caller's edit to one answer reached the next")
	}
	if _, _, err := c.Kinds(context.Background(), types, "kubernetes_manifest"); err != nil {
		t.Fatal(err)
	}
	if len(disc.Actions()) == asked {
		t.Error("a different question was answered from the remembered one")
	}
}
