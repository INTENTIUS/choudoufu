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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// GitHub issue #1639: a same-estate rename of a Kubernetes object is a
// governed write now. The object carries its block address in
// metadata.annotations[markers.AddressAnnotation], so renaming the block
// rewrites that annotation, which label.go used to document as "nothing to
// write on the cluster".

// labelTestObjectWith is [labelTestObject] with annotations.
func labelTestObjectWith(labels, annotations map[string]string) cty.Value {
	obj := labelTestObject(labels)
	attrs := obj.AsValueMap()
	meta := attrs["metadata"].Index(cty.NumberIntVal(0)).AsValueMap()
	if annotations != nil {
		meta["annotations"] = labelTestLabels(annotations)
	}
	attrs["metadata"] = cty.ListVal([]cty.Value{cty.ObjectVal(meta)})
	return cty.ObjectVal(attrs)
}

func (c *labelTestCluster) annotations(t *testing.T) map[string]string {
	t.Helper()
	got, ok := markers.AnnotationsOf(c.object)
	if !ok {
		t.Fatal("the live object has no readable annotations")
	}
	return got
}

func TestMove_LabelSurfaceRenameRewritesTheAddressAnnotation(t *testing.T) {
	ctx := t.Context()
	old := mustAddr(t, labelTestType+".database")
	renamed := mustAddr(t, labelTestType+".database_renamed")
	cluster := newLabelTestCluster(t, labelTestSchema(), labelTestObjectWith(
		map[string]string{markers.TagEstate: "app", "tier": "db"},
		map[string]string{markers.AddressAnnotation: old.String(), "owner": "team-a"},
	))

	store := recordFallbackStore(t)
	if _, err := projection.SeedLocatedForInstance(ctx, store, old, labelTestProviderAddr, projection.LocatedRecord{ImportID: labelTestLiveID}); err != nil {
		t.Fatalf("seeding the record fixture: %s", err)
	}
	req := labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database_renamed"), old, renamed, "", "app")
	req.RecordStore = store
	res, diags := Move(ctx, req)
	if diags.HasErrors() {
		t.Fatalf("the rename was refused: %s", diags.Err())
	}
	if res.NothingToWrite {
		t.Fatal("NothingToWrite is true: the object carries its address in an annotation, so a rename has that to rewrite (#1639)")
	}
	if !res.Written || !res.Verified {
		t.Errorf("Written = %v, Verified = %v, want both true", res.Written, res.Verified)
	}
	if cluster.plans != 1 || cluster.applies != 1 {
		t.Errorf("planned %d and applied %d times, want 1 and 1", cluster.plans, cluster.applies)
	}
	ann := cluster.annotations(t)
	if ann[markers.AddressAnnotation] != renamed.String() || ann["owner"] != "team-a" || len(ann) != 2 {
		t.Errorf("live annotations = %v, want owner=team-a and the new address %q", ann, renamed.String())
	}
	if got := cluster.labels(t); got[markers.TagEstate] != "app" || got["tier"] != "db" || len(got) != 2 {
		t.Errorf("live labels = %v: a same-estate rename moved a label", got)
	}
	// The record store still follows the rename, as it did before.
	if rec, _, atNew, found, err := store.GetIdentity(ctx, renamed); err != nil || !atNew || !found || rec.ImportID != labelTestLiveID {
		t.Errorf("the record did not follow the rename to %s (err %v)", renamed, err)
	}
}

// An object stamped before #1639 carries the label and no annotation: the
// rename writes the annotation.
func TestMove_LabelSurfaceRenameAnnotatesAnUnannotatedObject(t *testing.T) {
	old := mustAddr(t, labelTestType+".database")
	renamed := mustAddr(t, labelTestType+".database_renamed")
	cluster := newLabelTestCluster(t, labelTestSchema(), labelTestObject(map[string]string{markers.TagEstate: "app"}))
	res, diags := Move(t.Context(), labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database_renamed"), old, renamed, "", "app"))
	if diags.HasErrors() {
		t.Fatalf("the rename was refused: %s", diags.Err())
	}
	if !res.Written || cluster.annotations(t)[markers.AddressAnnotation] != renamed.String() {
		t.Errorf("Written = %v, annotations = %v, want the new address written", res.Written, cluster.annotations(t))
	}
}

// The object already names the new address (a plan and apply of the renamed
// block got there first): nothing to write, and not a failure.
func TestMove_LabelSurfaceRenameAlreadyDoneWritesNothing(t *testing.T) {
	old := mustAddr(t, labelTestType+".database")
	renamed := mustAddr(t, labelTestType+".database_renamed")
	cluster := newLabelTestCluster(t, labelTestSchema(), labelTestObjectWith(
		map[string]string{markers.TagEstate: "app"},
		map[string]string{markers.AddressAnnotation: renamed.String()},
	))
	res, diags := Move(t.Context(), labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database_renamed"), old, renamed, "", "app"))
	if diags.HasErrors() {
		t.Fatalf("an already-done rename was refused: %s", diags.Err())
	}
	if res.Written || !res.Verified || !res.AlreadyMarked || cluster.applies != 0 {
		t.Errorf("Written = %v, Verified = %v, AlreadyMarked = %v, %d applies; want nothing written and the marker verified", res.Written, res.Verified, res.AlreadyMarked, cluster.applies)
	}
}

// The object names a third address: refused, never overwritten.
func TestMove_LabelSurfaceRenameRefusesAThirdAddress(t *testing.T) {
	old := mustAddr(t, labelTestType+".database")
	renamed := mustAddr(t, labelTestType+".database_renamed")
	cluster := newLabelTestCluster(t, labelTestSchema(), labelTestObjectWith(
		map[string]string{markers.TagEstate: "app"},
		map[string]string{markers.AddressAnnotation: labelTestType + ".somebody_else"},
	))
	_, diags := Move(t.Context(), labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database_renamed"), old, renamed, "", "app"))
	if !diags.HasErrors() {
		t.Fatal("a rename over an annotation naming a third address was not refused")
	}
	if got := RefusalFrom(diags); got != RefusalNothingAtOldAddress {
		t.Errorf("refusal code = %q, want %q: %s", got, RefusalNothingAtOldAddress, diags.Err())
	}
	if cluster.applies != 0 {
		t.Errorf("a refused rename wrote: %d applies", cluster.applies)
	}
}

// A cross-estate move writes the address beside the new label.
func TestMove_LabelSurfaceMoveWritesTheAddressAnnotation(t *testing.T) {
	addr := mustAddr(t, labelTestType+".database")
	cluster := newLabelTestCluster(t, labelTestSchema(), labelTestObject(map[string]string{markers.TagEstate: "app"}))
	res, diags := Move(t.Context(), labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database"), addr, addr, "app", "data"))
	if diags.HasErrors() {
		t.Fatalf("the move was refused: %s", diags.Err())
	}
	if !res.Written || !res.Verified {
		t.Errorf("Written = %v, Verified = %v", res.Written, res.Verified)
	}
	if got := cluster.annotations(t)[markers.AddressAnnotation]; got != addr.String() {
		t.Errorf("address annotation = %q, want %q", got, addr.String())
	}
	if got := cluster.labels(t)[markers.TagEstate]; got != "data" {
		t.Errorf("tofu-estate = %q, want data", got)
	}
}

// A same-estate rename refuses a plan that would change another
// annotation: the address annotation is the only one the write may move.
func TestMove_LabelSurfaceRenameRefusesAPlanMovingAnotherAnnotation(t *testing.T) {
	old := mustAddr(t, labelTestType+".database")
	renamed := mustAddr(t, labelTestType+".database_renamed")
	cluster := newLabelTestCluster(t, labelTestSchema(), labelTestObjectWith(
		map[string]string{markers.TagEstate: "app"},
		map[string]string{"owner": "team-a"},
	))
	cluster.planHook = func(v cty.Value) cty.Value {
		m := v.AsValueMap()
		elem := m["metadata"].Index(cty.NumberIntVal(0)).AsValueMap()
		elem["annotations"] = cty.MapVal(map[string]cty.Value{
			"owner":                   cty.StringVal("tampered"),
			markers.AddressAnnotation: cty.StringVal(renamed.String()),
		})
		m["metadata"] = cty.ListVal([]cty.Value{cty.ObjectVal(elem)})
		return cty.ObjectVal(m)
	}
	_, diags := Move(t.Context(), labelTestRequest(t, cluster, labelTestConfig(t, labelTestType, "database_renamed"), old, renamed, "", "app"))
	if got := RefusalFrom(diags); got != RefusalPlanChangesMoreThanTags {
		t.Fatalf("refusal code = %q, want %q: %v", got, RefusalPlanChangesMoreThanTags, diags.Err())
	}
	if cluster.applies != 0 {
		t.Errorf("a refused rename wrote: %d applies", cluster.applies)
	}
}

// manifestCluster is an API server holding one CronTab, for the manifest
// rename: it answers ReadObject and PatchMarkers the way a server would,
// and counts the patches.
type manifestCluster struct {
	object   *unstructured.Unstructured
	mutate   func(*unstructured.Unstructured)
	dryRuns  int
	realRuns int
	labelled int
}

func (c *manifestCluster) LabelPatcher(context.Context, addrs.AbsProviderConfig) (kubesweep.LabelPatcher, error) {
	return c, nil
}

func (c *manifestCluster) ReadObject(context.Context, kubesweep.ObjectRef) (*unstructured.Unstructured, bool, error) {
	if c.object == nil {
		return nil, false, nil
	}
	return c.object.DeepCopy(), true, nil
}

func (c *manifestCluster) PatchMarkers(_ context.Context, _ kubesweep.ObjectRef, labels, annotations map[string]string, _ string, dryRun bool) (*unstructured.Unstructured, string, error) {
	next := c.object.DeepCopy()
	if len(labels) > 0 {
		c.labelled++
	}
	ann := next.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	for k, v := range annotations {
		ann[k] = v
	}
	next.SetAnnotations(ann)
	next.SetResourceVersion("2")
	if c.mutate != nil {
		c.mutate(next)
	}
	if dryRun {
		c.dryRuns++
		return next, "", nil
	}
	c.realRuns++
	c.object = next
	return next.DeepCopy(), "", nil
}

const manifestTestImportID = "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-crd,name=my-crontab"

func liveCronTab(labels, annotations map[string]string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "stable.example.com/v1",
		"kind":       "CronTab",
		"metadata":   map[string]any{"name": "my-crontab", "namespace": "smoke-crd", "resourceVersion": "1"},
		"spec":       map[string]any{"image": "cron"},
	}}
	u.SetLabels(labels)
	if annotations != nil {
		u.SetAnnotations(annotations)
	}
	return u
}

func manifestRenameRequest(t *testing.T, cluster *manifestCluster) (Request, addrs.AbsResourceInstance) {
	t.Helper()
	old := mustAddr(t, labelTestType+".database")
	renamed := mustAddr(t, labelTestType+".database_renamed")
	provider := newLabelTestCluster(t, manifestTestSchema(), cty.NullVal(manifestTestSchema().Block.ImpliedType()))
	req := labelTestRequest(t, provider, labelTestConfig(t, labelTestType, "database_renamed"), old, renamed, "", "app")
	req.Resolutions[0].ImportID = manifestTestImportID
	if cluster != nil {
		req.Clusters = cluster
	}
	return req, renamed
}

func TestMove_ManifestSurfaceRenameRewritesTheAddressAnnotation(t *testing.T) {
	cluster := &manifestCluster{object: liveCronTab(map[string]string{markers.TagEstate: "app"}, map[string]string{"owner": "team-a"})}
	req, renamed := manifestRenameRequest(t, cluster)
	res, diags := Move(t.Context(), req)
	if diags.HasErrors() {
		t.Fatalf("the rename was refused: %s", diags.Err())
	}
	if res.NothingToWrite || !res.Written || !res.Verified {
		t.Errorf("NothingToWrite = %v, Written = %v, Verified = %v; want a verified write", res.NothingToWrite, res.Written, res.Verified)
	}
	if cluster.dryRuns != 1 || cluster.realRuns != 1 || cluster.labelled != 0 {
		t.Errorf("patches: %d dry, %d real, %d with a label; want one of each and no label", cluster.dryRuns, cluster.realRuns, cluster.labelled)
	}
	ann := cluster.object.GetAnnotations()
	if ann[markers.AddressAnnotation] != renamed.String() || ann["owner"] != "team-a" {
		t.Errorf("live annotations = %v, want owner and the new address", ann)
	}
	if res.LiveID != manifestTestImportID || res.Path != PathIdentity {
		t.Errorf("LiveID = %q, Path = %s", res.LiveID, res.Path)
	}
}

func TestMove_ManifestSurfaceRenameRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		object *unstructured.Unstructured
		mutate func(*unstructured.Unstructured)
		noKube bool
		want   string
	}{
		"no cluster client":            {object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil), noKube: true, want: "No cluster client"},
		"no object at the natural key": {object: nil, want: "serves no object"},
		"another estate's object":      {object: liveCronTab(map[string]string{markers.TagEstate: "other"}, nil), want: "Live resource owned by another estate"},
		"an annotation naming a third address": {
			object: liveCronTab(map[string]string{markers.TagEstate: "app"}, map[string]string{markers.AddressAnnotation: "kubernetes_manifest.x"}),
			want:   "Live object carries another address",
		},
		"a dry run that rewrites the spec": {
			object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil),
			mutate: func(u *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(u.Object, "tampered", "spec", "image")
			},
			want: "spec.image",
		},
		"a dry run that rewrites a label": {
			object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil),
			mutate: func(u *unstructured.Unstructured) {
				u.SetLabels(map[string]string{markers.TagEstate: "app", "injected": "yes"})
			},
			want: "metadata.labels",
		},
	} {
		t.Run(name, func(t *testing.T) {
			cluster := &manifestCluster{object: tc.object, mutate: tc.mutate}
			var req Request
			if tc.noKube {
				req, _ = manifestRenameRequest(t, nil)
			} else {
				req, _ = manifestRenameRequest(t, cluster)
			}
			_, diags := Move(t.Context(), req)
			if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), tc.want) {
				t.Fatalf("diags = %v, want an error saying %q", diags.Err(), tc.want)
			}
			if cluster.realRuns != 0 {
				t.Errorf("a refused rename patched the object %d time(s)", cluster.realRuns)
			}
		})
	}
}

func TestMove_ManifestSurfaceRenameAlreadyDone(t *testing.T) {
	renamedAddr := labelTestType + ".database_renamed"
	cluster := &manifestCluster{object: liveCronTab(map[string]string{markers.TagEstate: "app"}, map[string]string{markers.AddressAnnotation: renamedAddr})}
	req, _ := manifestRenameRequest(t, cluster)
	res, diags := Move(t.Context(), req)
	if diags.HasErrors() {
		t.Fatalf("an already-done rename was refused: %s", diags.Err())
	}
	if !res.AlreadyMarked || !res.Verified || res.Written || cluster.dryRuns+cluster.realRuns != 0 {
		t.Errorf("AlreadyMarked = %v, Verified = %v, Written = %v, %d patches; want nothing sent", res.AlreadyMarked, res.Verified, res.Written, cluster.dryRuns+cluster.realRuns)
	}
}
