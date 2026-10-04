// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	fakedynamic "k8s.io/client-go/dynamic/fake"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1858: the cross-estate move's half of #1764. A
// `live-mv -from-estate` on a kubernetes_manifest object is the two
// requests kubesweep.Client.PatchMarkers makes - the merge patch writing
// tofu-estate and the address annotation, then the JSON patch handing
// both to the block's field manager's Apply entry (#1704). Killed between
// them, the object carries the destination estate and its manager's
// Update entry still owns the label. Before the fix the rerun answered
// "Live object already in this estate" and the hand-off never finished,
// so the destination's next apply conflicted on the label.
//
// The metadata-block move (kubernetes_config_map and the other typed
// resources) has no such window: it is one provider plan and one provider
// apply, with no ownership request after it, so a kill lands the label
// and the annotation together or neither.
// TestMove_LabelSurfaceMoveIsOneWrite pins that.

// moveCrashAddress is the moved block's address, kept across the move;
// fieldManagerTestConfig declares it.
const moveCrashAddress = labelTestType + ".database_renamed"

// moveCrashRequest is the move of moveCrashAddress from estate "app" to
// "data" over the fault cluster, with the block declaring
// field_manager { name = declared } when declared is not empty.
func moveCrashRequest(t *testing.T, client *kubesweep.Client, declared string) Request {
	t.Helper()
	addr := mustAddr(t, moveCrashAddress)
	provider := newLabelTestCluster(t, manifestTestSchema(), cty.NullVal(manifestTestSchema().Block.ImpliedType()))
	req := labelTestRequest(t, provider, fieldManagerTestConfig(t, declared), addr, addr, "app", "data")
	req.Resolutions[0].ImportID = manifestTestImportID
	req.Clusters = kubeClusters{client}
	return req
}

// moveCrashProviderApply is the provider's server-side apply of the
// CronTab in estate, as the block at moveCrashAddress, under manager,
// force_conflicts unset: the greenfield create in the source estate, and
// the destination's next apply after the move.
func moveCrashProviderApply(dyn *fakedynamic.FakeDynamicClient, manager, estate string) (*unstructured.Unstructured, error) {
	ct := liveCronTab(map[string]string{markers.TagEstate: estate}, map[string]string{markers.AddressAnnotation: moveCrashAddress})
	ct.SetResourceVersion("")
	return dyn.Resource(faultCronTabGVR).Namespace("smoke-crd").Apply(context.Background(), ct.GetName(), ct, metav1.ApplyOptions{FieldManager: manager})
}

// readCrashObject reads the CronTab as the cluster holds it now.
func readCrashObject(t *testing.T, client *kubesweep.Client) *unstructured.Unstructured {
	t.Helper()
	ref, _ := manifestTestRef()
	obj, found, err := client.ReadObject(t.Context(), ref)
	if err != nil || !found {
		t.Fatalf("reading the CronTab: found = %v, err = %v", found, err)
	}
	return obj
}

// manifestTestRef is manifestTestImportID as an object reference.
func manifestTestRef() (kubesweep.ObjectRef, bool) {
	apiVersion, kind, namespace, name, ok := kubesweep.ParseManifestImportID(manifestTestImportID)
	return kubesweep.ObjectRef{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name}, ok
}

// assertLabelOwnedBy fails unless manager's Apply entry, and nothing
// else, owns the tofu-estate label on obj.
func assertLabelOwnedBy(t *testing.T, obj *unstructured.Unstructured, manager string) {
	t.Helper()
	owned := false
	for _, e := range obj.GetManagedFields() {
		if !strings.Contains(string(e.FieldsV1.Raw), "f:"+markers.TagEstate) {
			continue
		}
		if e.Manager == manager && e.Operation == metav1.ManagedFieldsOperationApply {
			owned = true
			continue
		}
		t.Errorf("%s/%s also owns the tofu-estate label: %s", e.Manager, e.Operation, e.FieldsV1.Raw)
	}
	if !owned {
		t.Errorf("%s/Apply does not own the tofu-estate label", manager)
	}
}

// killMoveBetweenItsWrites runs the move with the fault injected and
// checks it died where the fault says: the label and the annotation
// written, the hand-off not.
func killMoveBetweenItsWrites(t *testing.T, client *kubesweep.Client, dyn *fakedynamic.FakeDynamicClient, req Request, manager string) {
	t.Helper()
	fired := crashBetweenMarkerWrites(dyn)
	_, diags := Move(t.Context(), req)
	if !fired() {
		t.Fatal("the injected fault never fired: the run did not reach the ownership write")
	}
	if !diags.HasErrors() {
		t.Fatal("the killed move reported success")
	}
	if msg := diags.Err().Error(); !strings.Contains(msg, "Rerun the same live-mv") {
		t.Errorf("the killed move must say that rerunning live-mv finishes it: %s", msg)
	}
	killed := readCrashObject(t, client)
	if got := killed.GetLabels()[markers.TagEstate]; got != "data" {
		t.Fatalf("after the kill tofu-estate = %q, want the merge patch to have landed (data)", got)
	}
	if got := killed.GetAnnotations()[markers.AddressAnnotation]; got != moveCrashAddress {
		t.Fatalf("after the kill the address annotation = %q, want %q", got, moveCrashAddress)
	}
	held, err := kubesweep.MarkersHeldByUpdate(killed, manager, []string{markers.TagEstate}, []string{markers.AddressAnnotation})
	if err != nil || !held {
		t.Fatalf("after the kill %s's Update entry does not hold a marker (held = %v, err = %v): the fault did not leave the state this test is about", manager, held, err)
	}
}

// TestMove_ManifestMoveCrashBetweenItsWrites is the fault. The first
// live-mv -from-estate dies between the label patch and the ownership
// write; the operator reruns it, as the error says; then the destination
// estate's provider applies the object, its label now reading "data". A
// move that was never killed lets that apply through
// (TestMove_ManifestSurfaceMoveUnderTheBlocksFieldManager).
func TestMove_ManifestMoveCrashBetweenItsWrites(t *testing.T) {
	for _, tc := range []struct{ name, declared, manager string }{
		{"default manager", "", kubesweep.DefaultFieldManager},
		{"block's field_manager", "my-pipeline", "my-pipeline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, dyn := faultCluster(t)
			if _, err := moveCrashProviderApply(dyn, tc.manager, "app"); err != nil {
				t.Fatalf("greenfield create: %v", err)
			}
			req := moveCrashRequest(t, client, tc.declared)
			killMoveBetweenItsWrites(t, client, dyn, req, tc.manager)

			patches := countPatches(dyn)
			res, diags := Move(t.Context(), req)
			if diags.HasErrors() {
				t.Fatalf("the rerun was refused: %s", diags.Err())
			}
			if !res.Written || !res.Verified {
				t.Errorf("rerun: Written = %v, Verified = %v; want the rerun to finish the write and verify it", res.Written, res.Verified)
			}
			// The dry run, the merge patch, and the hand-off.
			if n := patches(); n != 3 {
				t.Errorf("the rerun sent %d patch request(s), want 3: a dry run, the marker patch and the ownership write", n)
			}

			after, err := moveCrashProviderApply(dyn, tc.manager, "data")
			if err != nil {
				t.Fatalf("after a killed live-mv -from-estate and its rerun, the destination's apply conflicts: %v", err)
			}
			if got := after.GetLabels()[markers.TagEstate]; got != "data" {
				t.Errorf("after the destination's apply tofu-estate = %q", got)
			}
			assertLabelOwnedBy(t, after, tc.manager)
			assertAnnotationOwnedBy(t, after, tc.manager)

			// A third run finds the move finished and says so, writing
			// nothing.
			patches = countPatches(dyn)
			_, diags = Move(t.Context(), req)
			if got := RefusalFrom(diags); got != RefusalNewAddressClaimed {
				t.Errorf("a run after the finished move: refusal %q, want %q: %v", got, RefusalNewAddressClaimed, diags.Err())
			}
			if n := patches(); n != 0 {
				t.Errorf("a run after the finished move sent %d patch(es), want none", n)
			}
		})
	}
}

// TestMove_ManifestMoveCrashRerunDryRun: -dry-run over the killed state
// sends the server's dry run of the finishing write and stops, as a first
// move's -dry-run does. Nothing is persisted, so the hand-off is still
// owed afterwards.
func TestMove_ManifestMoveCrashRerunDryRun(t *testing.T) {
	client, dyn := faultCluster(t)
	if _, err := moveCrashProviderApply(dyn, kubesweep.DefaultFieldManager, "app"); err != nil {
		t.Fatalf("greenfield create: %v", err)
	}
	req := moveCrashRequest(t, client, "")
	killMoveBetweenItsWrites(t, client, dyn, req, kubesweep.DefaultFieldManager)

	patches := countPatches(dyn)
	dry := req
	dry.DryRun = true
	res, diags := Move(t.Context(), dry)
	if diags.HasErrors() {
		t.Fatalf("the dry rerun was refused: %s", diags.Err())
	}
	if res.Written || res.Verified {
		t.Errorf("Written = %v, Verified = %v under -dry-run", res.Written, res.Verified)
	}
	if n := patches(); n != 1 {
		t.Errorf("the dry rerun sent %d patch request(s), want exactly the server's dry run", n)
	}
	held, err := kubesweep.MarkersHeldByUpdate(readCrashObject(t, client), kubesweep.DefaultFieldManager, []string{markers.TagEstate}, []string{markers.AddressAnnotation})
	if err != nil || !held {
		t.Errorf("-dry-run finished the hand-off (held = %v, err = %v); it must persist nothing", held, err)
	}
}

// TestMove_ManifestMoveCrashRerunJudgedByTheDryRun: the finishing write is
// held to the first write's terms. A webhook that would rewrite the spec
// answers the dry run, the rerun is refused in the usual words, and
// nothing is sent for real.
func TestMove_ManifestMoveCrashRerunJudgedByTheDryRun(t *testing.T) {
	cluster := &manifestCluster{
		object: liveCronTab(map[string]string{markers.TagEstate: "data"}, map[string]string{markers.AddressAnnotation: labelTestType + ".database"}),
		mutate: func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(u.Object, "tampered", "spec", "image")
		},
	}
	cluster.object.SetManagedFields(killedMoveManagedFields(kubesweep.DefaultFieldManager))
	_, diags := Move(t.Context(), manifestMoveRequest(t, cluster, "data"))
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), "spec.image") {
		t.Fatalf("diags = %v, want the dry run's spec.image refusal", diags.Err())
	}
	if cluster.dryRuns != 1 || cluster.realRuns != 0 {
		t.Errorf("patches: %d dry, %d real; want the dry run and nothing after it", cluster.dryRuns, cluster.realRuns)
	}
}

// TestMove_ManifestMoveAlreadyInTheEstateStaysRefused is the boundary of
// the resume: an object in the destination estate is finished only when
// it is exactly what the killed move left. Each case here is not, and
// keeps the "already in this estate" refusal with nothing sent.
func TestMove_ManifestMoveAlreadyInTheEstateStaysRefused(t *testing.T) {
	addr := labelTestType + ".database"
	for name, tc := range map[string]struct {
		annotations map[string]string
		manager     string
	}{
		// No Update entry holds a marker: the move finished, or the object
		// was created in the destination.
		"no Update entry holds a marker": {map[string]string{markers.AddressAnnotation: addr}, ""},
		// The killed move wrote the annotation with the label; an object
		// with none was labelled some other way.
		"no address annotation": {nil, kubesweep.DefaultFieldManager},
		// Same: the killed move would have written this block's address.
		"another address": {map[string]string{markers.AddressAnnotation: labelTestType + ".other"}, kubesweep.DefaultFieldManager},
		// Another manager's Update entry is not this block's hand-off.
		"held by another manager's Update": {map[string]string{markers.AddressAnnotation: addr}, "kubectl-label"},
	} {
		t.Run(name, func(t *testing.T) {
			cluster := &manifestCluster{object: liveCronTab(map[string]string{markers.TagEstate: "data"}, tc.annotations)}
			if tc.manager != "" {
				cluster.object.SetManagedFields(killedMoveManagedFields(tc.manager))
			}
			_, diags := Move(t.Context(), manifestMoveRequest(t, cluster, "data"))
			if got := RefusalFrom(diags); got != RefusalNewAddressClaimed {
				t.Errorf("refusal %q, want %q: %v", got, RefusalNewAddressClaimed, diags.Err())
			}
			if cluster.dryRuns != 0 || cluster.realRuns != 0 {
				t.Errorf("patches: %d dry, %d real; want none", cluster.dryRuns, cluster.realRuns)
			}
		})
	}
}

// killedMoveManagedFields is the managedFields a killed move leaves on an
// object: manager's Update entry owning the tofu-estate label and the
// address annotation that the merge patch wrote.
func killedMoveManagedFields(manager string) []metav1.ManagedFieldsEntry {
	raw := `{"f:metadata":{"f:annotations":{"f:` + markers.AddressAnnotation + `":{}},"f:labels":{"f:` + markers.TagEstate + `":{}}}}`
	return []metav1.ManagedFieldsEntry{{
		Manager:    manager,
		Operation:  metav1.ManagedFieldsOperationUpdate,
		APIVersion: "stable.example.com/v1",
		FieldsType: "FieldsV1",
		FieldsV1:   &metav1.FieldsV1{Raw: []byte(raw)},
	}}
}

// panicClusters is a Clusters that fails the test when asked for a client.
type panicClusters struct{ t *testing.T }

func (p panicClusters) LabelPatcher(context.Context, addrs.AbsProviderConfig) (kubesweep.LabelPatcher, error) {
	p.t.Errorf("the metadata-block move asked for a cluster client; its write is the provider's apply alone")
	return nil, nil
}

// TestMove_LabelSurfaceMoveIsOneWrite pins why the metadata-block move
// needs no resume: its write is one provider plan and one provider apply,
// and nothing is sent after it - no cluster client is asked for, so there
// is no ownership request a kill could fall before. A rerun after it is
// the "already in this estate" refusal with nothing written, which is the
// whole of the move's state.
func TestMove_LabelSurfaceMoveIsOneWrite(t *testing.T) {
	cluster := newLabelTestCluster(t, labelTestSchema(), labelTestObject(map[string]string{markers.TagEstate: "app"}))
	addr := mustAddr(t, labelTestType+".database")
	req := labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database"), addr, addr, "app", "data")
	req.Clusters = panicClusters{t}
	res, diags := Move(t.Context(), req)
	if diags.HasErrors() {
		t.Fatalf("the move was refused: %s", diags.Err())
	}
	if !res.Written || !res.Verified {
		t.Errorf("Written = %v, Verified = %v, want both true", res.Written, res.Verified)
	}
	if cluster.plans != 1 || cluster.applies != 1 {
		t.Errorf("planned %d and applied %d times, want one of each and nothing else", cluster.plans, cluster.applies)
	}
	if got := cluster.labels(t)[markers.TagEstate]; got != "data" {
		t.Errorf("tofu-estate = %q after the move", got)
	}

	rerun := newLabelTestCluster(t, labelTestSchema(), cluster.object)
	req = labelTestRequest(t, rerun, labelTestConfig(t, labelTestType, "database"), addr, addr, "app", "data")
	req.Clusters = panicClusters{t}
	_, diags = Move(t.Context(), req)
	if got := RefusalFrom(diags); got != RefusalNewAddressClaimed {
		t.Errorf("the rerun's refusal is %q, want %q: %v", got, RefusalNewAddressClaimed, diags.Err())
	}
	if rerun.applies != 0 {
		t.Errorf("the rerun wrote %d time(s)", rerun.applies)
	}
}
