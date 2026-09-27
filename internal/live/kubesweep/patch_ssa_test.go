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
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/managedfields"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

// These tests are GitHub issue #1704: a kubernetes_manifest renamed through
// a moved block after live-import failed at apply with
//
//	Apply failed with 1 conflict: conflict with "Terraform" using
//	cert-manager.io/v1: .metadata.annotations.choudoufu.intentius.io/tofu-address
//
// because the marker patch is an Update under manager "Terraform", the
// provider's apply is an Apply under the same name, and managedFields keys
// ownership on manager AND operation. They run against client-go's
// field-managed object tracker, which runs the API server's own
// managedfields code, so a conflict here is the conflict kind reports.

const ssaTestAnnotation = "choudoufu.intentius.io/tofu-address"

// fieldManagedCluster is a fake dynamic client whose every request goes
// through a field-managed tracker, plus the discovery answer [Client]
// needs to resolve the CronTab kind.
func fieldManagedCluster(t *testing.T) (*Client, *fakedynamic.FakeDynamicClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	gv := schema.GroupVersion{Group: "stable.example.com", Version: "v1"}
	scheme.AddKnownTypeWithName(gv.WithKind("CronTab"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(gv.WithKind("CronTabList"), &unstructured.UnstructuredList{})
	tracker := clienttesting.NewFieldManagedObjectTracker(scheme, serializer.NewCodecFactory(scheme).UniversalDecoder(), managedfields.NewDeducedTypeConverter())
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{crontabGVR: "CronTabList"})
	dyn.ReactionChain = nil
	dyn.AddReactor("*", "*", clienttesting.ObjectReaction(tracker))
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "stable.example.com/v1", APIResources: []metav1.APIResource{
			{Name: "crontabs", Kind: "CronTab", Namespaced: true, Verbs: []string{"get", "list", "patch"}},
		}},
	}
	return NewWith(disc, dyn), dyn
}

// providerManifest is the object hashicorp/kubernetes applies for a
// kubernetes_manifest block: the whole manifest, with whatever markers the
// node stamp put into it.
func providerManifest(labels, annotations map[string]string) *unstructured.Unstructured {
	ct := &unstructured.Unstructured{}
	ct.SetAPIVersion("stable.example.com/v1")
	ct.SetKind("CronTab")
	ct.SetNamespace("smoke-crd")
	ct.SetName("my-crontab")
	l := map[string]string{"app": "cron"}
	for k, v := range labels {
		l[k] = v
	}
	ct.SetLabels(l)
	if len(annotations) > 0 {
		ct.SetAnnotations(annotations)
	}
	_ = unstructured.SetNestedField(ct.Object, "my-awesome-cron-image", "spec", "image")
	return ct
}

// providerApply is the provider's server-side apply: an Apply under
// [DefaultFieldManager], force_conflicts unset.
func providerApply(t *testing.T, dyn *fakedynamic.FakeDynamicClient, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	t.Helper()
	return dyn.Resource(crontabGVR).Namespace("smoke-crd").Apply(context.Background(), obj.GetName(), obj, metav1.ApplyOptions{FieldManager: DefaultFieldManager})
}

// TestPatchMarkersLeavesTheMarkersToTheProvidersApply walks #1704's day2
// sequence: stock creates the object by server-side apply, live-import
// writes the markers with PatchMarkers, and the rename's apply sends the
// same manifest with the address annotation changed. The rename must
// apply without force_conflicts, and the markers must end up owned by the
// provider's Apply entry alone.
func TestPatchMarkersLeavesTheMarkersToTheProvidersApply(t *testing.T) {
	c, dyn := fieldManagedCluster(t)
	if _, err := providerApply(t, dyn, providerManifest(nil, nil)); err != nil {
		t.Fatalf("stock create: %v", err)
	}
	ref := ObjectRef{APIVersion: "stable.example.com/v1", Kind: "CronTab", Namespace: "smoke-crd", Name: "my-crontab"}

	written, rejected, err := c.PatchMarkers(context.Background(), ref,
		map[string]string{"tofu-estate": "smoke-crd"},
		map[string]string{ssaTestAnnotation: "kubernetes_manifest.clusterissuer"},
		"", false)
	if err != nil || rejected != "" {
		t.Fatalf("PatchMarkers: err=%v rejected=%q", err, rejected)
	}
	if got := written.GetAnnotations()[ssaTestAnnotation]; got != "kubernetes_manifest.clusterissuer" {
		t.Fatalf("PatchMarkers returned annotation %q", got)
	}
	for _, e := range written.GetManagedFields() {
		if e.Manager == DefaultFieldManager && e.Operation == metav1.ManagedFieldsOperationUpdate {
			t.Errorf("after PatchMarkers, %s holds an Update entry (%s); the provider's Apply under the same name will conflict with it", e.Manager, e.FieldsV1.Raw)
		}
	}

	// The replan after live-import: the stamped manifest with the same
	// markers. Then the rename: the address annotation changes.
	if _, err := providerApply(t, dyn, providerManifest(
		map[string]string{"tofu-estate": "smoke-crd"},
		map[string]string{ssaTestAnnotation: "kubernetes_manifest.clusterissuer"})); err != nil {
		t.Fatalf("provider apply with unchanged markers: %v", err)
	}
	renamed, err := providerApply(t, dyn, providerManifest(
		map[string]string{"tofu-estate": "smoke-crd"},
		map[string]string{ssaTestAnnotation: "kubernetes_manifest.clusterissuer_review"}))
	if err != nil {
		t.Fatalf("the rename's apply conflicted (#1704): %v", err)
	}
	if got := renamed.GetAnnotations()[ssaTestAnnotation]; got != "kubernetes_manifest.clusterissuer_review" {
		t.Errorf("after the rename the annotation is %q", got)
	}
}

// TestPatchMarkersRenameThenProviderRename is live-mv's manifest path
// (#1639): a rename through PatchMarkers (annotation only), after which a
// second rename by the provider's apply must not conflict either.
func TestPatchMarkersRenameThenProviderRename(t *testing.T) {
	c, dyn := fieldManagedCluster(t)
	if _, err := providerApply(t, dyn, providerManifest(
		map[string]string{"tofu-estate": "smoke-crd"},
		map[string]string{ssaTestAnnotation: "kubernetes_manifest.a"})); err != nil {
		t.Fatalf("greenfield create: %v", err)
	}
	ref := ObjectRef{APIVersion: "stable.example.com/v1", Kind: "CronTab", Namespace: "smoke-crd", Name: "my-crontab"}
	if _, rejected, err := c.PatchMarkers(context.Background(), ref, nil,
		map[string]string{ssaTestAnnotation: "kubernetes_manifest.b"}, "", false); err != nil || rejected != "" {
		t.Fatalf("live-mv's PatchMarkers: err=%v rejected=%q", err, rejected)
	}
	if _, err := providerApply(t, dyn, providerManifest(
		map[string]string{"tofu-estate": "smoke-crd"},
		map[string]string{ssaTestAnnotation: "kubernetes_manifest.c"})); err != nil {
		t.Fatalf("the provider's rename after live-mv's conflicted (#1704): %v", err)
	}
}

// TestPatchMarkersTransfersOnlyTheMarkers pins the narrowness of the
// ownership transfer: a field some Update under the same manager name
// owned before the marker patch stays an Update field, so the provider's
// next apply does not start deleting it, and nothing another manager owns
// moves.
func TestPatchMarkersTransfersOnlyTheMarkers(t *testing.T) {
	c, dyn := fieldManagedCluster(t)
	if _, err := providerApply(t, dyn, providerManifest(nil, nil)); err != nil {
		t.Fatalf("stock create: %v", err)
	}
	res := dyn.Resource(crontabGVR).Namespace("smoke-crd")
	if _, err := res.Patch(context.Background(), "my-crontab", types.MergePatchType,
		[]byte(`{"metadata":{"annotations":{"hand-set":"x"}}}`), metav1.PatchOptions{FieldManager: DefaultFieldManager}); err != nil {
		t.Fatal(err)
	}
	if _, err := res.Patch(context.Background(), "my-crontab", types.MergePatchType,
		[]byte(`{"spec":{"cron":"* * * * */5"}}`), metav1.PatchOptions{FieldManager: "kubectl-edit"}); err != nil {
		t.Fatal(err)
	}
	ref := ObjectRef{APIVersion: "stable.example.com/v1", Kind: "CronTab", Namespace: "smoke-crd", Name: "my-crontab"}
	written, _, err := c.PatchMarkers(context.Background(), ref,
		map[string]string{"tofu-estate": "smoke-crd"},
		map[string]string{ssaTestAnnotation: "kubernetes_manifest.x"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]string{}
	for _, e := range written.GetManagedFields() {
		owners[e.Manager+"/"+string(e.Operation)] = string(e.FieldsV1.Raw)
	}
	upd := owners[DefaultFieldManager+"/Update"]
	if !strings.Contains(upd, "f:hand-set") || strings.Contains(upd, "tofu-address") || strings.Contains(upd, "tofu-estate") {
		t.Errorf("Terraform/Update = %s; want the hand-set annotation and no marker", upd)
	}
	app := owners[DefaultFieldManager+"/Apply"]
	if !strings.Contains(app, "f:choudoufu.intentius.io/tofu-address") || !strings.Contains(app, "f:tofu-estate") || strings.Contains(app, "hand-set") {
		t.Errorf("Terraform/Apply = %s; want both markers and not the hand-set annotation", app)
	}
	if kc := owners["kubectl-edit/Update"]; !strings.Contains(kc, "f:cron") {
		t.Errorf("kubectl-edit/Update = %s; another manager's field moved", kc)
	}
}

// TestDeleteMarkersLeavesNoOwnerBehind is #1656's release under the same
// tracker: removing the markers by merge patch must leave no manager owning
// them, so a later apply of the object (re-declared, re-adopted) sets them
// without a conflict.
func TestDeleteMarkersLeavesNoOwnerBehind(t *testing.T) {
	c, dyn := fieldManagedCluster(t)
	if _, err := providerApply(t, dyn, providerManifest(
		map[string]string{"tofu-estate": "smoke-crd"},
		map[string]string{ssaTestAnnotation: "kubernetes_manifest.a"})); err != nil {
		t.Fatal(err)
	}
	ref := ObjectRef{APIVersion: "stable.example.com/v1", Kind: "CronTab", Namespace: "smoke-crd", Name: "my-crontab"}
	got, rejected, err := c.DeleteMarkers(context.Background(), ref, []string{"tofu-estate"}, []string{ssaTestAnnotation}, "", false)
	if err != nil || rejected != "" {
		t.Fatalf("DeleteMarkers: err=%v rejected=%q", err, rejected)
	}
	for _, e := range got.GetManagedFields() {
		if raw := string(e.FieldsV1.Raw); strings.Contains(raw, "tofu-estate") || strings.Contains(raw, "tofu-address") {
			t.Errorf("%s/%s still owns a released marker: %s", e.Manager, e.Operation, raw)
		}
	}
	if _, err := providerApply(t, dyn, providerManifest(
		map[string]string{"tofu-estate": "other"},
		map[string]string{ssaTestAnnotation: "kubernetes_manifest.z"})); err != nil {
		t.Fatalf("an apply after release conflicted: %v", err)
	}
}

// TestPatchMarkersRetriesTheTransferOnAConflict is the race #1704's smoke
// run measured on kind: the three cert-manager Deployments' status moved
// between the marker patch and the ownership patch, the pinned
// resourceVersion answered 409, and their markers stayed with the Update
// entry. The transfer is recomputed from a fresh read and sent again.
func TestPatchMarkersRetriesTheTransferOnAConflict(t *testing.T) {
	c, dyn := fieldManagedCluster(t)
	if _, err := providerApply(t, dyn, providerManifest(nil, nil)); err != nil {
		t.Fatal(err)
	}
	conflicts := 0
	dyn.PrependReactor("patch", "crontabs", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if action.(clienttesting.PatchAction).GetPatchType() == types.JSONPatchType && conflicts == 0 {
			conflicts++
			return true, nil, apierrors.NewConflict(crontabGVR.GroupResource(), "my-crontab", errors.New("the object has been modified"))
		}
		return false, nil, nil
	})
	ref := ObjectRef{APIVersion: "stable.example.com/v1", Kind: "CronTab", Namespace: "smoke-crd", Name: "my-crontab"}
	written, rejected, err := c.PatchMarkers(context.Background(), ref,
		map[string]string{"tofu-estate": "smoke-crd"},
		map[string]string{ssaTestAnnotation: "kubernetes_manifest.a"}, "", false)
	if err != nil || rejected != "" {
		t.Fatalf("PatchMarkers: err=%v rejected=%q", err, rejected)
	}
	if conflicts != 1 {
		t.Fatalf("the injected conflict fired %d times", conflicts)
	}
	for _, e := range written.GetManagedFields() {
		if e.Manager == DefaultFieldManager && e.Operation == metav1.ManagedFieldsOperationUpdate {
			t.Errorf("after a retried transfer %s still holds an Update entry: %s", e.Manager, e.FieldsV1.Raw)
		}
	}
}
