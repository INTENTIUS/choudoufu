// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"errors"
	"strings"
	"testing"

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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1764, live-import's half: kubesweep.Client.PatchMarkers is
// two requests on a real write, the merge patch that writes the markers and
// the JSON patch that hands their ownership from the block manager's Update
// entry to its Apply entry (#1704). A live-import -approve killed between
// them leaves both markers on the object, still held by the Update entry.
// The rerun must not read "already stamped" off the values alone: the
// provider's next apply that changes a marker - a moved-block rename
// rewrites the address annotation - then conflicts with the Update entry.
//
// Everything runs against client-go's field-managed tracker (the API
// server's own managedfields code), as internal/live/mv's
// fault_move_crash_test.go does for live-mv.

var importFaultGVR = schema.GroupVersionResource{Group: "stable.example.com", Version: "v1", Resource: "crontabs"}

var errImportKilled = errors.New("injected fault: the process was killed before the ownership write")

func importFaultCluster(t *testing.T) (*kubesweep.Client, *fakedynamic.FakeDynamicClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	gv := schema.GroupVersion{Group: "stable.example.com", Version: "v1"}
	scheme.AddKnownTypeWithName(gv.WithKind("CronTab"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(gv.WithKind("CronTabList"), &unstructured.UnstructuredList{})
	tracker := clienttesting.NewFieldManagedObjectTracker(scheme, serializer.NewCodecFactory(scheme).UniversalDecoder(), managedfields.NewDeducedTypeConverter())
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{importFaultGVR: "CronTabList"})
	dyn.ReactionChain = nil
	dyn.AddReactor("*", "*", clienttesting.ObjectReaction(tracker))
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "stable.example.com/v1", APIResources: []metav1.APIResource{
			{Name: "crontabs", Kind: "CronTab", Namespaced: true, Verbs: []string{"get", "list", "patch"}},
		}},
	}
	return kubesweep.NewWith(disc, dyn), dyn
}

// crashBetweenImportWrites fails the first real ownership write (the JSON
// patch; the marker write is a merge patch) once, which is what a kill
// between the two leaves on the server.
func crashBetweenImportWrites(dyn *fakedynamic.FakeDynamicClient) (fired func() bool) {
	n := 0
	dyn.PrependReactor("patch", "crontabs", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if action.(clienttesting.PatchAction).GetPatchType() == types.JSONPatchType && n == 0 {
			n++
			return true, nil, errImportKilled
		}
		return false, nil, nil
	})
	return func() bool { return n > 0 }
}

// countPatches counts every patch request the cluster is sent from here on,
// dry runs included.
func countPatches(dyn *fakedynamic.FakeDynamicClient) (count func() int) {
	n := 0
	dyn.PrependReactor("patch", "crontabs", func(clienttesting.Action) (bool, runtime.Object, error) {
		n++
		return false, nil, nil
	})
	return func() int { return n }
}

// importFaultProviderApply is hashicorp/kubernetes applying the CronTab
// under manager, force_conflicts unset. labels and annotations are the
// markers the stamp puts into the manifest; nil for the stock apply that
// created the object before the migration.
func importFaultProviderApply(dyn *fakedynamic.FakeDynamicClient, manager string, labels, annotations map[string]string) error {
	ct := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "stable.example.com/v1",
		"kind":       "CronTab",
		"metadata":   map[string]any{"name": "my-crontab", "namespace": "smoke-crd"},
		"spec":       map[string]any{"cronSpec": "* * * * */5"},
	}}
	if labels != nil {
		ct.SetLabels(labels)
	}
	if annotations != nil {
		ct.SetAnnotations(annotations)
	}
	_, err := dyn.Resource(importFaultGVR).Namespace("smoke-crd").Apply(context.Background(), ct.GetName(), ct, metav1.ApplyOptions{FieldManager: manager})
	return err
}

var importFaultAddr = addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_manifest", Name: "crontab"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)

// importFaultEligible is manifestEligible over a real client.
func importFaultEligible(client *kubesweep.Client, declared string) *eligible {
	e := manifestEligible(nil, declared)
	e.patcher, e.patcherErr = client, nil
	return e
}

// importRerunRecovers records whether a rerun of a killed live-import
// hands the markers to the block manager's Apply entry. Before #1764's fix
// the rerun reported ALREADY_STAMPED and the provider's next rename failed
// with "conflict with \"Terraform\"" on the address annotation. Flipping
// it to false is the red control for the fix.
const importRerunRecovers = true

// TestApproveManifest_CrashBetweenItsWrites is the fault, under the
// provider's default manager and under a block that names its own.
func TestApproveManifest_CrashBetweenItsWrites(t *testing.T) {
	for _, tc := range []struct{ name, declared, manager string }{
		{"default manager", "", kubesweep.DefaultFieldManager},
		{"block's field_manager", "my-pipeline", "my-pipeline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, dyn := importFaultCluster(t)
			if err := importFaultProviderApply(dyn, tc.manager, nil, nil); err != nil {
				t.Fatalf("stock create: %v", err)
			}
			fired := crashBetweenImportWrites(dyn)

			first := approveManifest(t.Context(), testEstate, importFaultAddr, importFaultEligible(client, tc.declared))
			if !fired() {
				t.Fatal("the injected fault never fired: the run did not reach the ownership write")
			}
			if first.Outcome != OutcomeFailed {
				t.Fatalf("the killed run reported %s (%s), want FAILED", first.Outcome, first.Detail)
			}
			if !strings.Contains(first.Detail, "Rerun the same live-import -approve") {
				t.Errorf("the killed run does not name the command that finishes it: %s", first.Detail)
			}

			rerun := approveManifest(t.Context(), testEstate, importFaultAddr, importFaultEligible(client, tc.declared))
			if rerun.Outcome == OutcomeFailed || rerun.Outcome == OutcomeSkipped {
				t.Fatalf("the rerun reported %s: %s", rerun.Outcome, rerun.Detail)
			}

			err := importFaultProviderApply(dyn, tc.manager,
				map[string]string{markers.TagEstate: testEstate},
				map[string]string{markers.AddressAnnotation: "kubernetes_manifest.crontab_renamed"})
			switch {
			case importRerunRecovers && err != nil:
				t.Errorf("after a killed live-import and its rerun (%s: %s), the provider's next rename conflicts (#1764): %v", rerun.Outcome, rerun.Detail, err)
			case !importRerunRecovers && err == nil:
				t.Errorf("the rerun recovers: set importRerunRecovers to true")
			}
		})
	}
}

// TestApproveManifest_CleanRerunSendsNothing is the control: an adoption
// that was never killed leaves the markers with the Apply entry, and a
// rerun reads managedFields, finds nothing to hand over, and sends no
// patch at all.
func TestApproveManifest_CleanRerunSendsNothing(t *testing.T) {
	client, dyn := importFaultCluster(t)
	if err := importFaultProviderApply(dyn, kubesweep.DefaultFieldManager, nil, nil); err != nil {
		t.Fatalf("stock create: %v", err)
	}
	if out := approveManifest(t.Context(), testEstate, importFaultAddr, importFaultEligible(client, "")); out.Outcome != OutcomeStamped {
		t.Fatalf("the adoption reported %s: %s", out.Outcome, out.Detail)
	}
	patches := countPatches(dyn)
	rerun := approveManifest(t.Context(), testEstate, importFaultAddr, importFaultEligible(client, ""))
	if rerun.Outcome != OutcomeAlreadyStamped {
		t.Errorf("the rerun reported %s (%s), want ALREADY_STAMPED", rerun.Outcome, rerun.Detail)
	}
	if n := patches(); n != 0 {
		t.Errorf("the rerun over a cleanly adopted object sent %d patch(es), want none", n)
	}
	if err := importFaultProviderApply(dyn, kubesweep.DefaultFieldManager,
		map[string]string{markers.TagEstate: testEstate},
		map[string]string{markers.AddressAnnotation: "kubernetes_manifest.crontab_renamed"}); err != nil {
		t.Errorf("the provider's rename after a clean adoption conflicts: %v", err)
	}
}
