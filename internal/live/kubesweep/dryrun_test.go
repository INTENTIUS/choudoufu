// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

var crontabGVR = schema.GroupVersionResource{Group: "stable.example.com", Version: "v1", Resource: "crontabs"}

func crontabDiscovery() *fakediscovery.FakeDiscovery {
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "stable.example.com/v1", APIResources: []metav1.APIResource{
			{Name: "crontabs", Kind: "CronTab", Namespaced: true, Verbs: []string{"get", "list", "create", "update", "delete"}},
			{Name: "crontabs/status", Kind: "CronTab", Namespaced: true, Verbs: []string{"get", "update"}},
		}},
	}
	return disc
}

func crontabManifest(replicas any) map[string]any {
	spec := map[string]any{"cronSpec": "* * * * */5", "image": "my-awesome-cron-image"}
	if replicas != nil {
		spec["replicas"] = replicas
	}
	return map[string]any{
		"apiVersion": "stable.example.com/v1",
		"kind":       "CronTab",
		"metadata":   map[string]any{"name": "my-crontab", "namespace": "smoke-crd", "labels": map[string]any{"tofu-estate": "smoke-crd"}},
		"spec":       spec,
	}
}

// TestDryRunCreateIsSubmittedWithDryRunAll (GitHub issue #1081, item 3):
// a planned create goes to the server as a POST carrying dryRun=All, the
// namespaced resource the kind is served as is the one addressed, and
// what the server's answer carries beyond the manifest - outside
// metadata - is counted as defaulted.
func TestDryRunCreateIsSubmittedWithDryRunAll(t *testing.T) {
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{crontabGVR: "CronTabList"})
	var seen clienttesting.CreateActionImpl
	dyn.PrependReactor("create", "crontabs", func(action clienttesting.Action) (bool, runtime.Object, error) {
		seen = action.(clienttesting.CreateActionImpl)
		answer := seen.Object.(*unstructured.Unstructured).DeepCopy()
		// What a server does on write: bookkeeping in metadata (never
		// counted), and one defaulted spec field (counted).
		answer.SetUID("u-1")
		answer.SetResourceVersion("17")
		unstructured.SetNestedField(answer.Object, int64(1), "spec", "replicas")
		unstructured.SetNestedField(answer.Object, "Pending", "status", "phase")
		return true, answer, nil
	})
	c := NewWith(crontabDiscovery(), dyn)
	got, err := c.DryRun(context.Background(), crontabManifest(nil), false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Accepted {
		t.Fatalf("not accepted: %q", got.Message)
	}
	if seen.GetNamespace() != "smoke-crd" || seen.GetResource() != crontabGVR {
		t.Errorf("submitted to %s in namespace %q, want crontabs in smoke-crd", seen.GetResource(), seen.GetNamespace())
	}
	if len(seen.CreateOptions.DryRun) != 1 || seen.CreateOptions.DryRun[0] != metav1.DryRunAll {
		t.Errorf("DryRun option = %v, want [%s]: without it the server would have persisted the object", seen.CreateOptions.DryRun, metav1.DryRunAll)
	}
	if labels := seen.Object.(*unstructured.Unstructured).GetLabels(); labels["tofu-estate"] != "smoke-crd" {
		t.Errorf("the submitted object lost the estate label: %v", labels)
	}
	if got.Defaulted != 1 {
		t.Errorf("Defaulted = %d, want 1 (spec.replicas; the uid, resourceVersion and status are not defaults)", got.Defaulted)
	}
}

// TestDryRunReturnsTheServersRejectionVerbatim: a status error the server
// answered with is its verdict, not a failure to reach it.
func TestDryRunReturnsTheServersRejectionVerbatim(t *testing.T) {
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{crontabGVR: "CronTabList"})
	dyn.PrependReactor("create", "crontabs", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInvalid(schema.GroupKind{Group: "stable.example.com", Kind: "CronTab"}, "my-crontab",
			field.ErrorList{field.Invalid(field.NewPath("spec", "replicas"), "string", "spec.replicas in body must be of type integer")})
	})
	c := NewWith(crontabDiscovery(), dyn)
	got, err := c.DryRun(context.Background(), crontabManifest("three"), false)
	if err != nil {
		t.Fatalf("a rejection came back as an error: %v", err)
	}
	if got.Accepted {
		t.Fatal("accepted what the server refused")
	}
	for _, want := range []string{`CronTab.stable.example.com "my-crontab" is invalid`, "spec.replicas", "must be of type integer"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("the message does not carry %q: %q", want, got.Message)
		}
	}
	// A 403 from the estate boundary's admission policy is a verdict too.
	dyn.PrependReactor("create", "crontabs", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "stable.example.com", Resource: "crontabs"}, "my-crontab",
			errors.New("tofu-estate=smoke-crd would move this object into estate smoke-crd and bob is not bound to it"))
	})
	got, err = c.DryRun(context.Background(), crontabManifest(nil), false)
	if err != nil || got.Accepted || !strings.Contains(got.Message, "bob is not bound to it") {
		t.Errorf("a 403 was not returned as the server's verdict: accepted=%v err=%v msg=%q", got.Accepted, err, got.Message)
	}
}

// TestDryRunReportsAClusterThatCannotAnswer: a transport failure, and a
// 5xx, are the cluster not answering, never a verdict.
func TestDryRunReportsAClusterThatCannotAnswer(t *testing.T) {
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{crontabGVR: "CronTabList"})
	dyn.PrependReactor("create", "crontabs", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	c := NewWith(crontabDiscovery(), dyn)
	if got, err := c.DryRun(context.Background(), crontabManifest(nil), false); err == nil {
		t.Errorf("connection refused returned a verdict: %+v", got)
	}
	dyn.PrependReactor("create", "crontabs", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(errors.New("etcd is down"))
	})
	if got, err := c.DryRun(context.Background(), crontabManifest(nil), false); err == nil {
		t.Errorf("a 500 returned a verdict: %+v", got)
	}
	// A group-version discovery cannot answer for is the same.
	disc := crontabDiscovery()
	disc.PrependReactor("get", "resource", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	if got, err := NewWith(disc, dyn).DryRun(context.Background(), crontabManifest(nil), false); err == nil {
		t.Errorf("discovery failing returned a verdict: %+v", got)
	}
}

// TestDryRunUpdateCarriesTheLiveResourceVersion: an update is a PUT, and
// a custom resource refuses an unconditional one, so the live object's
// resourceVersion is read first and sent - what kubectl replace does.
func TestDryRunUpdateCarriesTheLiveResourceVersion(t *testing.T) {
	live := &unstructured.Unstructured{Object: crontabManifest(int64(3))}
	live.SetResourceVersion("41")
	live.SetUID("u-1")
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{crontabGVR: "CronTabList"}, live)
	var seen clienttesting.UpdateActionImpl
	dyn.PrependReactor("update", "crontabs", func(action clienttesting.Action) (bool, runtime.Object, error) {
		seen = action.(clienttesting.UpdateActionImpl)
		return true, seen.Object, nil
	})
	c := NewWith(crontabDiscovery(), dyn)
	got, err := c.DryRun(context.Background(), crontabManifest(int64(5)), true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Accepted {
		t.Fatalf("not accepted: %q", got.Message)
	}
	if seen.Object == nil {
		t.Fatal("no update was submitted")
	}
	if rv := seen.Object.(*unstructured.Unstructured).GetResourceVersion(); rv != "41" {
		t.Errorf("submitted resourceVersion %q, want the live object's 41", rv)
	}
	if len(seen.UpdateOptions.DryRun) != 1 || seen.UpdateOptions.DryRun[0] != metav1.DryRunAll {
		t.Errorf("DryRun option = %v, want [%s]", seen.UpdateOptions.DryRun, metav1.DryRunAll)
	}
	// The server's no to the PUT itself is a rejection: the first live
	// run of this accepted a replicas: 0 the CRD's minimum forbids,
	// because the update's error was read from a shadowed variable.
	dyn.PrependReactor("update", "crontabs", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInvalid(schema.GroupKind{Group: "stable.example.com", Kind: "CronTab"}, "my-crontab",
			field.ErrorList{field.Invalid(field.NewPath("spec", "replicas"), int64(0), "spec.replicas in body should be greater than or equal to 1")})
	})
	got, err = c.DryRun(context.Background(), crontabManifest(int64(0)), true)
	if err != nil {
		t.Fatalf("the update's rejection came back as an error: %v", err)
	}
	if got.Accepted || !strings.Contains(got.Message, "greater than or equal to 1") {
		t.Errorf("the update's rejection was not returned as the server's verdict: accepted=%v msg=%q", got.Accepted, got.Message)
	}
	// An object that is gone by the time of the dry run is the server's
	// answer (404), not a cluster that cannot answer.
	got, err = c.DryRun(context.Background(), map[string]any{
		"apiVersion": "stable.example.com/v1", "kind": "CronTab",
		"metadata": map[string]any{"name": "vanished", "namespace": "smoke-crd"},
	}, true)
	if err != nil || got.Accepted || !strings.Contains(got.Message, "not found") {
		t.Errorf("a missing object on update: accepted=%v err=%v msg=%q", got.Accepted, err, got.Message)
	}
}

func TestCountAdded(t *testing.T) {
	sent := map[string]any{"spec": map[string]any{"image": "x"}, "metadata": map[string]any{"name": "n"}}
	got := map[string]any{
		"spec":     map[string]any{"image": "x", "replicas": int64(1), "template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}, "ports": []any{1, 2}}},
		"metadata": map[string]any{"name": "n", "uid": "u", "managedFields": []any{}},
		"status":   map[string]any{"phase": "Pending"},
	}
	// replicas, template.metadata.labels.a (a nested metadata is not the
	// object's own), template.ports (one leaf for a list).
	if n := countAdded(sent, got); n != 3 {
		t.Errorf("countAdded = %d, want 3", n)
	}
}
