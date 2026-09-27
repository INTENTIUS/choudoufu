// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

// GitHub issue #1656: the label release is the second write this package
// makes, a merge patch whose body names each released key with a null
// value and nothing else.

var crontabRef = ObjectRef{APIVersion: "stable.example.com/v1", Kind: "CronTab", Namespace: "smoke-crd", Name: "my-crontab"}

func labelledCrontab() *unstructured.Unstructured {
	live := &unstructured.Unstructured{Object: crontabManifest(int64(3))}
	live.SetLabels(map[string]string{"app": "cron", "tofu-estate": "smoke-crd"})
	live.SetResourceVersion("41")
	return live
}

func TestDeleteLabelsSendsANullMergePatchAndNothingElse(t *testing.T) {
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{crontabGVR: "CronTabList"}, labelledCrontab())
	var seen []clienttesting.PatchActionImpl
	dyn.PrependReactor("patch", "crontabs", func(action clienttesting.Action) (bool, runtime.Object, error) {
		seen = append(seen, action.(clienttesting.PatchActionImpl))
		return false, nil, nil // let the tracker apply it
	})
	c := NewWith(crontabDiscovery(), dyn)

	got, rejected, err := c.DeleteLabels(context.Background(), crontabRef, []string{"tofu-estate"}, "", true)
	if err != nil || rejected != "" {
		t.Fatalf("dry run: rejected=%q err=%v", rejected, err)
	}
	if got == nil {
		t.Fatal("dry run returned no object")
	}
	if len(seen) != 1 {
		t.Fatalf("patches sent = %d, want 1", len(seen))
	}
	p := seen[0]
	if p.GetPatchType() != types.MergePatchType {
		t.Errorf("patch type = %s, want a JSON merge patch", p.GetPatchType())
	}
	if p.GetNamespace() != "smoke-crd" || p.GetResource() != crontabGVR || p.GetName() != "my-crontab" {
		t.Errorf("patched %s %s/%s, want crontabs smoke-crd/my-crontab", p.GetResource(), p.GetNamespace(), p.GetName())
	}
	var body map[string]any
	if err := json.Unmarshal(p.GetPatch(), &body); err != nil {
		t.Fatalf("patch body is not JSON: %s", p.GetPatch())
	}
	want := map[string]any{"metadata": map[string]any{"labels": map[string]any{"tofu-estate": nil}}}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("patch body = %s, want exactly {\"metadata\":{\"labels\":{\"tofu-estate\":null}}}", p.GetPatch())
	}
	if len(p.PatchOptions.DryRun) != 1 || p.PatchOptions.DryRun[0] != metav1.DryRunAll {
		t.Errorf("DryRun option = %v, want [%s]", p.PatchOptions.DryRun, metav1.DryRunAll)
	}
	if p.PatchOptions.FieldManager != DefaultFieldManager {
		t.Errorf("field manager = %q, want %q when none is named", p.PatchOptions.FieldManager, DefaultFieldManager)
	}

	// The real write carries no dryRun and removes the label on the
	// stored object, leaving every other label where it was.
	written, rejected, err := c.DeleteLabels(context.Background(), crontabRef, []string{"tofu-estate"}, "custom", false)
	if err != nil || rejected != "" {
		t.Fatalf("write: rejected=%q err=%v", rejected, err)
	}
	if len(seen) != 2 || len(seen[1].PatchOptions.DryRun) != 0 || seen[1].PatchOptions.FieldManager != "custom" {
		t.Fatalf("second patch = %+v, want no dryRun under manager custom", seen[len(seen)-1].PatchOptions)
	}
	if got := written.GetLabels(); !reflect.DeepEqual(got, map[string]string{"app": "cron"}) {
		t.Errorf("labels after release = %v, want app=cron alone", got)
	}
	stored, found, err := c.ReadObject(context.Background(), crontabRef)
	if err != nil || !found {
		t.Fatalf("read back: found=%v err=%v", found, err)
	}
	if _, still := stored.GetLabels()["tofu-estate"]; still {
		t.Errorf("the stored object still carries tofu-estate: %v", stored.GetLabels())
	}
}

func TestDeleteLabelsReturnsTheServersVerdict(t *testing.T) {
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{crontabGVR: "CronTabList"}, labelledCrontab())
	dyn.PrependReactor("patch", "crontabs", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "stable.example.com", Resource: "crontabs"}, "my-crontab",
			errors.New("bob may not remove tofu-estate"))
	})
	c := NewWith(crontabDiscovery(), dyn)
	_, rejected, err := c.DeleteLabels(context.Background(), crontabRef, []string{"tofu-estate"}, "", true)
	if err != nil || !strings.Contains(rejected, "bob may not remove tofu-estate") {
		t.Errorf("a 403 was not returned as the server's verdict: rejected=%q err=%v", rejected, err)
	}

	dyn.PrependReactor("patch", "crontabs", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	if _, rejected, err := c.DeleteLabels(context.Background(), crontabRef, []string{"tofu-estate"}, "", true); err == nil {
		t.Errorf("connection refused returned a verdict: %q", rejected)
	}
}

func TestDeleteLabelsRefusesAnEmptyRequest(t *testing.T) {
	c := NewWith(crontabDiscovery(), fakedynamic.NewSimpleDynamicClient(runtime.NewScheme()))
	if _, _, err := c.DeleteLabels(context.Background(), crontabRef, nil, "", true); err == nil {
		t.Error("a release naming no key was sent")
	}
	if _, _, err := c.DeleteLabels(context.Background(), crontabRef, []string{""}, "", true); err == nil {
		t.Error("a release naming an empty key was sent")
	}
	if _, _, err := c.DeleteLabels(context.Background(), ObjectRef{Kind: "CronTab", Name: "x"}, []string{"tofu-estate"}, "", true); err == nil {
		t.Error("a release with no apiVersion was sent")
	}
}
