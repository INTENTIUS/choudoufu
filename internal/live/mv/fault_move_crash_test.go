// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

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

// GitHub issue #1110, fault 4's move half: a kill between the two writes
// live-mv makes on a manifest-declared object. kubesweep.Client.PatchMarkers
// is two requests - the merge patch that writes the address annotation, then
// the JSON patch that hands the annotation's ownership from the "Terraform"
// Update entry to the provider's "Terraform" Apply entry (#1704). A process
// that dies between them leaves the new address on the object and the
// ownership with the Update entry, which is the state #1704 fixed: the
// provider's next apply that changes the annotation fails with a field
// manager conflict.
//
// The injector is crashBetweenMarkerWrites: a reactor on the fake dynamic
// client that lets the marker merge patch through and fails the ownership
// JSON patch once, which is what a kill leaves on the server. Everything
// else runs against client-go's field-managed tracker, the API server's own
// managedfields code, so a conflict here is the conflict kind reports.

var faultCronTabGVR = schema.GroupVersionResource{Group: "stable.example.com", Version: "v1", Resource: "crontabs"}

// errKilled is what the injected fault answers in place of the second
// write. The process that sent it is gone; the error only ends the run.
var errKilled = errors.New("injected fault: the process was killed before the ownership write")

// kubeClusters hands one kubesweep.Client to Move as its Clusters.
type kubeClusters struct{ c *kubesweep.Client }

func (k kubeClusters) LabelPatcher(context.Context, addrs.AbsProviderConfig) (kubesweep.LabelPatcher, error) {
	return k.c, nil
}

// faultCluster is a field-managed fake API server serving CronTab.
func faultCluster(t *testing.T) (*kubesweep.Client, *fakedynamic.FakeDynamicClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	gv := schema.GroupVersion{Group: "stable.example.com", Version: "v1"}
	scheme.AddKnownTypeWithName(gv.WithKind("CronTab"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(gv.WithKind("CronTabList"), &unstructured.UnstructuredList{})
	tracker := clienttesting.NewFieldManagedObjectTracker(scheme, serializer.NewCodecFactory(scheme).UniversalDecoder(), managedfields.NewDeducedTypeConverter())
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{faultCronTabGVR: "CronTabList"})
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

// crashBetweenMarkerWrites is the injector. It fails the first real
// ownership write (a JSON patch; the marker write is a merge patch and the
// dry run carries DryRun) and reports through the returned func whether it
// fired.
func crashBetweenMarkerWrites(dyn *fakedynamic.FakeDynamicClient) (fired func() bool) {
	n := 0
	dyn.PrependReactor("patch", "crontabs", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if action.(clienttesting.PatchAction).GetPatchType() == types.JSONPatchType && n == 0 {
			n++
			return true, nil, errKilled
		}
		return false, nil, nil
	})
	return func() bool { return n > 0 }
}

// faultProviderApply is the provider's server-side apply of the CronTab
// with the given address annotation: an Apply under the provider's default
// manager, force_conflicts unset.
func faultProviderApply(dyn *fakedynamic.FakeDynamicClient, address string) error {
	ct := liveCronTab(map[string]string{markers.TagEstate: "app"}, map[string]string{markers.AddressAnnotation: address})
	ct.SetResourceVersion("")
	_, err := dyn.Resource(faultCronTabGVR).Namespace("smoke-crd").Apply(context.Background(), ct.GetName(), ct, metav1.ApplyOptions{FieldManager: kubesweep.DefaultFieldManager})
	return err
}

// rerunRecovers is what the fault test expects of the rerun (GitHub issue
// #1764). Before the fix the rerun read the new address on the object,
// took the "already marked" branch (manifest.go's reannotateManifest),
// reported the rename verified, and never re-sent the ownership write, so
// the Update entry kept the annotation and the provider's next rename
// failed with
//
//	Apply failed with 1 conflict: conflict with "Terraform" using
//	stable.example.com/v1: .metadata.annotations.choudoufu.intentius.io/tofu-address
//
// Flipping it to false is the red control: the test then fails because
// the rerun recovers.
const rerunRecovers = true

// countPatches counts every patch request the cluster is sent from here
// on, dry runs included.
func countPatches(dyn *fakedynamic.FakeDynamicClient) (count func() int) {
	n := 0
	dyn.PrependReactor("patch", "crontabs", func(clienttesting.Action) (bool, runtime.Object, error) {
		n++
		return false, nil, nil
	})
	return func() int { return n }
}

// faultRenameRequest is manifestRenameRequest over the fault cluster, with
// the renamed block declaring field_manager { name = declared } when
// declared is not empty.
func faultRenameRequest(t *testing.T, client *kubesweep.Client, declared string) (Request, addrs.AbsResourceInstance) {
	t.Helper()
	req, renamed := manifestRenameRequest(t, nil)
	if declared != "" {
		req.Config = fieldManagerTestConfig(t, declared)
	}
	req.Clusters = kubeClusters{client}
	return req, renamed
}

// TestMove_ManifestRenameCrashBetweenItsWrites is the fault. The first
// live-mv dies between the marker write and the ownership write; the
// operator reruns it, as the first run's error tells them to; then the
// provider's next rename (a moved block) applies under the block's
// manager. A run that was never killed lets that apply through
// (TestMove_ManifestRenameWithoutAFaultLeavesTheProviderFree).
func TestMove_ManifestRenameCrashBetweenItsWrites(t *testing.T) {
	for _, tc := range []struct{ name, declared, manager string }{
		{"default manager", "", kubesweep.DefaultFieldManager},
		{"block's field_manager", "my-pipeline", "my-pipeline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, dyn := faultCluster(t)
			if _, err := fieldManagerProviderApply(dyn, tc.manager, labelTestType+".database"); err != nil {
				t.Fatalf("greenfield create: %v", err)
			}
			fired := crashBetweenMarkerWrites(dyn)
			req, renamed := faultRenameRequest(t, client, tc.declared)

			_, diags := Move(t.Context(), req)
			if !fired() {
				t.Fatal("the injected fault never fired: the run did not reach the ownership write")
			}
			if !diags.HasErrors() {
				t.Fatal("the killed run reported success")
			}
			if msg := diags.Err().Error(); strings.Contains(msg, "the rename is done") || !strings.Contains(msg, "Rerun the same live-mv") {
				t.Errorf("the killed run must say the rename is not finished and that rerunning live-mv finishes it: %s", msg)
			}

			res, diags := Move(t.Context(), req)
			if diags.HasErrors() {
				t.Fatalf("the rerun was refused: %s", diags.Err())
			}
			if !res.Verified {
				t.Errorf("the rerun did not verify the new address")
			}
			after, err := fieldManagerProviderApply(dyn, tc.manager, renamed.String()+"_again")
			switch {
			case rerunRecovers && err != nil:
				t.Errorf("after a killed live-mv and its rerun, the provider's next rename conflicts (#1704 reopened by a crash): %v", err)
			case rerunRecovers:
				assertAnnotationOwnedBy(t, after, tc.manager)
			case err == nil:
				t.Errorf("the rerun now recovers from the crash: set rerunRecovers to true so this test guards the recovery")
			}
		})
	}
}

// TestMove_ManifestRenameWithoutAFaultLeavesTheProviderFree is the control:
// the same rename with no fault, then the same provider rename. A rerun of
// the clean rename in between reads managedFields, finds the annotation
// already with the Apply entry, and sends no patch at all.
func TestMove_ManifestRenameWithoutAFaultLeavesTheProviderFree(t *testing.T) {
	client, dyn := faultCluster(t)
	if err := faultProviderApply(dyn, labelTestType+".database"); err != nil {
		t.Fatalf("greenfield create: %v", err)
	}
	req, renamed := manifestRenameRequest(t, nil)
	req.Clusters = kubeClusters{client}
	if _, diags := Move(t.Context(), req); diags.HasErrors() {
		t.Fatalf("the rename was refused: %s", diags.Err())
	}

	patches := countPatches(dyn)
	res, diags := Move(t.Context(), req)
	if diags.HasErrors() {
		t.Fatalf("the rerun was refused: %s", diags.Err())
	}
	if !res.AlreadyMarked || res.Written || !res.Verified {
		t.Errorf("rerun: AlreadyMarked = %v, Written = %v, Verified = %v; want an already-marked, verified rerun that wrote nothing", res.AlreadyMarked, res.Written, res.Verified)
	}
	if n := patches(); n != 0 {
		t.Errorf("the rerun of a clean rename sent %d patch(es), want none", n)
	}

	if err := faultProviderApply(dyn, renamed.String()+"_again"); err != nil {
		t.Errorf("the provider's next rename conflicts after an unfaulted live-mv: %v", err)
	}
}
