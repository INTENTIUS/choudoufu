// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// fakeHoldingFamily is a third family whose only answer that matters here
// is [substrate.ControllerHeld]: it recognises a tag key and an
// annotation no real family knows. It carries no surface, so it cannot
// change any surface question. Everything else is AWS's, by embedding.
type fakeHoldingFamily struct {
	substrate.Substrate
}

const (
	fakeHoldTag        = "fake.example/held-by"
	fakeHoldAnnotation = "fake.example/held-by"
)

func (fakeHoldingFamily) Name() string                { return "fakehold" }
func (fakeHoldingFamily) Surfaces() []markers.Surface { return nil }
func (fakeHoldingFamily) ControllerHeld(ev substrate.HoldEvidence) (substrate.Hold, bool) {
	if v, ok := ev.Tags[fakeHoldTag]; ok {
		return substrate.Hold{Controller: "FakeCtl", HeldBy: "fake controller " + v}, true
	}
	if v, ok := ev.Annotations[fakeHoldAnnotation]; ok {
		return substrate.Hold{Controller: "FakeCtl", HeldBy: "fake controller " + v}, true
	}
	return substrate.Hold{}, false
}

func withFakeHoldingFamily(t *testing.T) {
	t.Helper()
	orig := substrate.All
	substrate.All = append(append([]substrate.Substrate(nil), orig...), fakeHoldingFamily{Substrate: substrate.AWS})
	t.Cleanup(func() { substrate.All = orig })
}

// TestControllerHeldAsksTheFamilyOnTheTagLeg (GitHub issue #1706): the AWS
// leg's controller-held pass asks the families, not markers.ControllerHeld
// directly, so a family's own controller is recognised with no change to
// the leg. A resource carrying only the fake family's tag is withheld from
// removal and reported with the fake family's HeldBy.
func TestControllerHeldAsksTheFamilyOnTheTagLeg(t *testing.T) {
	withFakeHoldingFamily(t)
	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable("aws_cloudwatch_log_group")
	cloud.obj("aws_cloudwatch_log_group", "/estate/fake", map[string]string{
		TagEstate:   estateName,
		TagAddress:  `aws_cloudwatch_log_group.fake`,
		fakeHoldTag: "ns/thing",
	})
	cloud.obj("aws_security_group", "sg-fake", map[string]string{fakeHoldTag: "ns/other"})

	res, diags := discoverFixture(t, cloud, Request{Sweep: true, CollectUnclaimed: true})
	assertNoErrors(t, diags)

	if _, ok := removalsByAddr(res)[`aws_cloudwatch_log_group.fake`]; ok {
		t.Errorf("a resource the fake family holds is proposed for destroy:\n%s", res)
	}
	for _, u := range res.Unclaimed {
		if u.ImportID == "sg-fake" {
			t.Errorf("a resource the fake family holds is in the unclaimed population:\n%s", res)
		}
	}
	got := map[string]ControllerHeldResource{}
	for _, c := range res.ControllerHeld {
		got[c.ImportID] = c
	}
	for id, want := range map[string]string{"/estate/fake": "fake controller ns/thing", "sg-fake": "fake controller ns/other"} {
		c, ok := got[id]
		if !ok {
			t.Errorf("%s is not reported as controller-held:\n%s", id, res)
			continue
		}
		if c.Controller != "FakeCtl" || c.HeldBy != want {
			t.Errorf("%s held = %q by %q, want FakeCtl by %q", id, c.Controller, c.HeldBy, want)
		}
	}
}

// TestControllerHeldAsksTheFamilyOnTheKubernetesLeg (GitHub issue #1706):
// the Kubernetes leg names a held object's holder by asking the families
// with the object's annotations, not by copying the name kubesweep wrote.
// The fake family is asked first, so on the object carrying its annotation
// beside Helm's it answers, and the leg must report that answer; the object
// carrying Helm's alone still reads as Helm's. Which objects are held at all
// stays kubesweep's decision, release-secret check included (#1625).
func TestControllerHeldAsksTheFamilyOnTheKubernetesLeg(t *testing.T) {
	orig := substrate.All
	substrate.All = append([]substrate.Substrate{fakeHoldingFamily{Substrate: substrate.AWS}}, orig...)
	t.Cleanup(func() { substrate.All = orig })

	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	secretGVR := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	both := helmConfigMap("smoke-k8s", "both", "web")
	ann := both.GetAnnotations()
	ann[fakeHoldAnnotation] = "ns/cr"
	both.SetAnnotations(ann)
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "ConfigMapList", secretGVR: "SecretList"},
		helmConfigMap("smoke-k8s", "web-greeting", "web"),
		both,
		helmReleaseSecret("smoke-k8s", "web"),
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
	got := map[string]ControllerHeldResource{}
	for _, c := range res.ControllerHeld {
		got[c.ImportID] = c
	}
	if c := got["smoke-k8s/web-greeting"]; c.Controller != "Helm" || c.HeldBy != "Helm release smoke-k8s/web" {
		t.Errorf("web-greeting held = %q by %q, want Helm by Helm release smoke-k8s/web", c.Controller, c.HeldBy)
	}
	if c := got["smoke-k8s/both"]; c.Controller != "FakeCtl" || c.HeldBy != "fake controller ns/cr" {
		t.Errorf("both held = %q by %q, want FakeCtl by fake controller ns/cr: the leg must report the families' answer", c.Controller, c.HeldBy)
	}
}
