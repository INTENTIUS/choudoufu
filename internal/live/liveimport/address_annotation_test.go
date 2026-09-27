// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1639: live-import's adoption of a Kubernetes object writes
// the block address into metadata.annotations[markers.AddressAnnotation]
// beside the tofu-estate label, on both carriers: the labels-only plan of
// a typed metadata block, and the merge patch of a manifest-declared one.

// configMapObjectWith is [configMapObject] with annotations too.
func configMapObjectWith(labels, annotations map[string]string) cty.Value {
	obj := configMapObject(labels)
	attrs := obj.AsValueMap()
	meta := attrs["metadata"].Index(cty.NumberIntVal(0)).AsValueMap()
	if annotations != nil {
		meta["annotations"] = labelMap(annotations)
	}
	attrs["metadata"] = cty.ListVal([]cty.Value{cty.ObjectVal(meta)})
	return cty.ObjectVal(attrs)
}

func TestApproveLabel_WritesTheAddressAnnotation(t *testing.T) {
	addr := mustAddr(t, `kubernetes_config_map.app["a:b"]`)
	e, p := configMapEligible(map[string]string{"app": "web"})
	out := approveOne(context.Background(), testEstate, addr, e, "")
	if out.Outcome != OutcomeStamped {
		t.Fatalf("outcome = %s (%s), want STAMPED", out.Outcome, out.Detail)
	}
	ann, ok := markers.AnnotationsOf(p.appliedObject)
	if !ok {
		t.Fatal("the applied object has no readable annotations")
	}
	want := markers.EscapeAddress(addr.String())
	if ann[markers.AddressAnnotation] != want || len(ann) != 1 {
		t.Errorf("applied annotations = %v, want only %s=%q", ann, markers.AddressAnnotation, want)
	}
	if labels, _ := markers.LabelsOf(p.appliedObject); labels[markers.TagEstate] != testEstate || labels["app"] != "web" || len(labels) != 2 {
		t.Errorf("applied labels = %v, want app=web plus tofu-estate", labels)
	}
}

func TestApproveLabel_LabelledObjectWithoutTheAnnotationGetsIt(t *testing.T) {
	addr := mustAddr(t, "kubernetes_config_map.app")
	e, p := configMapEligible(map[string]string{markers.TagEstate: testEstate})
	out := approveOne(context.Background(), testEstate, addr, e, "")
	if out.Outcome != OutcomeStamped {
		t.Fatalf("outcome = %s (%s), want STAMPED: the label alone is not every marker the object should carry", out.Outcome, out.Detail)
	}
	if ann, _ := markers.AnnotationsOf(p.appliedObject); ann[markers.AddressAnnotation] != addr.String() {
		t.Errorf("applied annotations = %v, want the address", ann)
	}
}

func TestApproveLabel_FullyMarkedObjectIsAlreadyStamped(t *testing.T) {
	addr := mustAddr(t, "kubernetes_config_map.app")
	e, p := configMapEligible(nil)
	e.applied = configMapObjectWith(map[string]string{markers.TagEstate: testEstate}, map[string]string{markers.AddressAnnotation: addr.String()})
	out := approveOne(context.Background(), testEstate, addr, e, "")
	if out.Outcome != OutcomeAlreadyStamped || p.applyCount != 0 {
		t.Fatalf("outcome = %s (%s), %d applies; want ALREADY_STAMPED and none", out.Outcome, out.Detail, p.applyCount)
	}
}

func TestApproveLabel_AnnotationNamingAnotherAddressIsRefused(t *testing.T) {
	addr := mustAddr(t, "kubernetes_config_map.app")
	e, p := configMapEligible(nil)
	e.applied = configMapObjectWith(nil, map[string]string{markers.AddressAnnotation: "kubernetes_config_map.other"})
	out := approveOne(context.Background(), testEstate, addr, e, "")
	if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, "kubernetes_config_map.other") || !strings.Contains(out.Detail, "live-mv") {
		t.Fatalf("outcome = %s (%s), want FAILED naming the other address and live-mv", out.Outcome, out.Detail)
	}
	if p.applyCount != 0 {
		t.Errorf("applied %d times over another address's annotation, want 0", p.applyCount)
	}
}

func TestApproveManifest_WritesTheAddressAnnotation(t *testing.T) {
	addr := mustAddr(t, "kubernetes_manifest.crontab")
	cluster := &fakeCluster{object: liveCronTab(nil)}
	out := approveOne(context.Background(), testEstate, addr, manifestEligible(cluster, ""), "")
	if out.Outcome != OutcomeStamped {
		t.Fatalf("outcome = %s (%s), want STAMPED", out.Outcome, out.Detail)
	}
	if got := cluster.object.GetAnnotations()[markers.AddressAnnotation]; got != addr.String() {
		t.Errorf("live annotations = %v, want %s=%q", cluster.object.GetAnnotations(), markers.AddressAnnotation, addr.String())
	}
	if got := cluster.object.GetLabels()[markers.TagEstate]; got != testEstate {
		t.Errorf("live labels = %v, want tofu-estate", cluster.object.GetLabels())
	}
	if cluster.dryRuns != 1 || cluster.realRuns != 1 {
		t.Errorf("patches: %d dry run(s) and %d real, want one of each: the label and the annotation go in one patch", cluster.dryRuns, cluster.realRuns)
	}
}

func TestApproveManifest_LabelledObjectWithoutTheAnnotationGetsIt(t *testing.T) {
	addr := mustAddr(t, "kubernetes_manifest.crontab")
	cluster := &fakeCluster{object: liveCronTab(map[string]string{markers.TagEstate: testEstate})}
	out := approveOne(context.Background(), testEstate, addr, manifestEligible(cluster, ""), "")
	if out.Outcome != OutcomeStamped {
		t.Fatalf("outcome = %s (%s), want STAMPED", out.Outcome, out.Detail)
	}
	if got := cluster.object.GetAnnotations()[markers.AddressAnnotation]; got != addr.String() {
		t.Errorf("live annotations = %v, want the address", cluster.object.GetAnnotations())
	}
}

func TestApproveManifest_AnnotationNamingAnotherAddressIsRefused(t *testing.T) {
	addr := mustAddr(t, "kubernetes_manifest.crontab")
	live := liveCronTab(nil)
	live.SetAnnotations(map[string]string{markers.AddressAnnotation: "kubernetes_manifest.other"})
	cluster := &fakeCluster{object: live}
	out := approveOne(context.Background(), testEstate, addr, manifestEligible(cluster, ""), "")
	if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, "kubernetes_manifest.other") {
		t.Fatalf("outcome = %s (%s), want FAILED naming the other address", out.Outcome, out.Detail)
	}
	if cluster.realRuns != 0 {
		t.Errorf("patched %d times over another address's annotation, want 0", cluster.realRuns)
	}
}
