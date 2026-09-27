// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1639 (step 1 of #1605's ruling): the node stamp writes the
// block address into metadata.annotations[markers.AddressAnnotation] on
// both Kubernetes arms, beside the tofu-estate label, carrying the same
// escaped value the AWS tofu-address tag carries.

func configMapTestConfigWith(labels, annotations cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"data":      cty.MapVal(map[string]cty.Value{"greeting": cty.StringVal("hello")}),
		"id":        cty.NullVal(cty.String),
		"immutable": cty.NullVal(cty.Bool),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"annotations": annotations,
			"labels":      labels,
			"name":        cty.StringVal("app-config"),
			"namespace":   cty.StringVal("smoke-k8s"),
		})}),
	})
}

// forEachConfigMapAddr is kubernetes_config_map.app["a:b"], an address that
// is not a legal label value (the reason #1016 kept it off the labels) and
// is a legal annotation value once escaped.
func forEachConfigMapAddr() addrs.AbsResourceInstance {
	return addrs.AbsResourceInstance{Resource: addrs.Resource{
		Mode: addrs.ManagedResourceMode, Type: configMapTestType, Name: "app",
	}.Instance(addrs.StringKey("a:b"))}
}

func TestNodeResolver_AdjustConfigValue_labelArmStampsTheAddressAnnotation(t *testing.T) {
	addr := forEachConfigMapAddr()
	resolver := &NodeResolver{Estate: "smoke-k8s"}
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, configMapTestConfig(cty.NullVal(cty.Map(cty.String))), configMapTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	ann, ok := markers.AnnotationsOf(got)
	if !ok {
		t.Fatalf("no readable metadata.annotations on %#v", got)
	}
	want := markers.EscapeAddress(addr.String())
	if ann[markers.AddressAnnotation] != want {
		t.Fatalf("annotations = %v, want %s=%q", ann, markers.AddressAnnotation, want)
	}
	if len(ann) != 1 {
		t.Errorf("annotations = %v, want only the address annotation", ann)
	}
	// The estate label is still the only label: the address is an
	// annotation, never a label.
	if labels := requireLabels(t, got); len(labels) != 1 || labels[markers.TagEstate] != "smoke-k8s" {
		t.Errorf("labels = %v, want only %s=smoke-k8s", labels, markers.TagEstate)
	}
	if !got.Type().Equals(configMapTestConfig(cty.NullVal(cty.Map(cty.String))).Type()) {
		t.Errorf("the adjusted value changed type:\n got %#v\nwant %#v", got.Type(), configMapTestConfig(cty.NullVal(cty.Map(cty.String))).Type())
	}
}

func TestNodeResolver_AdjustConfigValue_labelArmKeepsOwnAnnotations(t *testing.T) {
	addr := configMapAddr(t)
	resolver := &NodeResolver{Estate: "smoke-k8s"}
	own := cty.MapVal(map[string]cty.Value{"team": cty.StringVal("web")})
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, configMapTestConfigWith(cty.NullVal(cty.Map(cty.String)), own), configMapTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	ann, _ := markers.AnnotationsOf(got)
	if ann["team"] != "web" || ann[markers.AddressAnnotation] != addr.String() || len(ann) != 2 {
		t.Fatalf("annotations = %v, want team=web and the address", ann)
	}
}

func TestNodeResolver_AdjustConfigValue_labelArmRefusesAnAnnotationNamingAnotherAddress(t *testing.T) {
	addr := configMapAddr(t)
	resolver := &NodeResolver{Estate: "smoke-k8s"}
	own := cty.MapVal(map[string]cty.Value{markers.AddressAnnotation: cty.StringVal("kubernetes_config_map.other")})
	_, diags := resolver.AdjustConfigValue(context.Background(), addr, configMapTestConfigWith(cty.NullVal(cty.Map(cty.String)), own), configMapTypeSchema())
	if !diags.HasErrors() {
		t.Fatalf("a hand-written address annotation naming another address was overwritten silently")
	}
	msg := diags.Err().Error()
	if !strings.Contains(msg, SummaryMarkerConflict) || !strings.Contains(msg, markers.AddressAnnotation) || !strings.Contains(msg, "live-mv") {
		t.Errorf("conflict message does not name the annotation and the rename: %s", msg)
	}

	// The same address, hand-written unescaped, is not a conflict.
	fe := forEachConfigMapAddr()
	same := cty.MapVal(map[string]cty.Value{markers.AddressAnnotation: cty.StringVal(fe.String())})
	if _, diags := resolver.AdjustConfigValue(context.Background(), fe, configMapTestConfigWith(cty.NullVal(cty.Map(cty.String)), same), configMapTypeSchema()); diags.HasErrors() {
		t.Errorf("the unescaped spelling of the instance's own address was refused: %s", diags.Err())
	}
}

func TestNodeResolver_AdjustConfigValue_untagOfTheAddressReleasesTheAnnotation(t *testing.T) {
	addr := configMapAddr(t)
	resolver := &NodeResolver{Estate: "smoke-k8s", PolicyUntag: map[string]string{addr.String(): markers.TagAddress}}
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, configMapTestConfig(cty.NullVal(cty.Map(cty.String))), configMapTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if ann, _ := markers.AnnotationsOf(got); ann[markers.AddressAnnotation] != "" {
		t.Errorf("annotations = %v: an untag of tofu-address still wrote the address", ann)
	}
	if labels := requireLabels(t, got); labels[markers.TagEstate] != "smoke-k8s" {
		t.Errorf("labels = %v: an untag of the address released the estate", labels)
	}
	rel := resolver.UntagReleases()
	if len(rel) != 1 || rel[0].Key != markers.TagAddress {
		t.Errorf("releases = %v, want the one tofu-address release", rel)
	}
}

func TestNodeResolver_AdjustConfigValue_manifestArmStampsTheAddressAnnotation(t *testing.T) {
	addr := manifestAddr(t)
	resolver := &NodeResolver{Estate: "smoke-crd"}
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, manifestTestConfig(manifestTestManifest(cty.NilVal)), manifestTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	ann, ok := markers.ManifestAnnotationsOf(got)
	if !ok {
		t.Fatalf("no readable manifest.metadata.annotations on %#v", got)
	}
	if ann[markers.AddressAnnotation] != addr.String() || len(ann) != 1 {
		t.Fatalf("annotations = %v, want only %s=%q", ann, markers.AddressAnnotation, addr.String())
	}
	if labels := requireManifestLabels(t, got); labels[markers.TagEstate] != "smoke-crd" {
		t.Errorf("labels = %v, want %s=smoke-crd", labels, markers.TagEstate)
	}
}

func TestNodeResolver_AdjustConfigValue_manifestArmKeepsOwnAnnotations(t *testing.T) {
	addr := manifestAddr(t)
	manifest := manifestTestManifest(cty.NilVal)
	attrs := manifest.AsValueMap()
	meta := attrs["metadata"].AsValueMap()
	meta["annotations"] = cty.ObjectVal(map[string]cty.Value{"team": cty.StringVal("web")})
	attrs["metadata"] = cty.ObjectVal(meta)
	resolver := &NodeResolver{Estate: "smoke-crd"}
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, manifestTestConfig(cty.ObjectVal(attrs)), manifestTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	ann, _ := markers.ManifestAnnotationsOf(got)
	if ann["team"] != "web" || ann[markers.AddressAnnotation] != addr.String() || len(ann) != 2 {
		t.Fatalf("annotations = %v, want team=web and the address", ann)
	}
	// Idempotent: a second pass over its own output changes nothing.
	again, diags := resolver.AdjustConfigValue(context.Background(), addr, got, manifestTypeSchema())
	if diags.HasErrors() || !again.RawEquals(got) {
		t.Errorf("a second stamp changed the value or raised %s", diags.Err())
	}
}

// The ignore_changes arms are unreachable for a Kubernetes type today (no
// kubernetes_* type is record-located, so no selection reaches one; see
// TestNodeResolver_AdjustIgnoreChanges_manifestProtectsTheLabel), so the
// paths they would protect are pinned through the helpers they return.
func TestAddressAnnotationPaths(t *testing.T) {
	label := cty.Path{
		cty.GetAttrStep{Name: "metadata"}, cty.IndexStep{Key: cty.NumberIntVal(0)}, cty.GetAttrStep{Name: "annotations"},
		cty.IndexStep{Key: cty.StringVal(markers.AddressAnnotation)},
	}
	if got := markers.LabelAnnotationPath(markers.AddressAnnotation); !got.Equals(label) {
		t.Errorf("LabelAnnotationPath = %#v, want %#v", got, label)
	}
	manifest := cty.Path{
		cty.GetAttrStep{Name: "manifest"}, cty.GetAttrStep{Name: "metadata"}, cty.GetAttrStep{Name: "annotations"},
		cty.IndexStep{Key: cty.StringVal(markers.AddressAnnotation)},
	}
	if got := markers.ManifestAnnotationPath(markers.AddressAnnotation); !got.Equals(manifest) {
		t.Errorf("ManifestAnnotationPath = %#v, want %#v", got, manifest)
	}
}

func TestStampManifestSeedCarriesTheAddressAnnotation(t *testing.T) {
	addr := manifestAddr(t)
	seed := map[string]cty.Value{"manifest": manifestTestManifest(cty.NilVal)}
	got := stampManifestSeed(addr, seed, "smoke-crd")
	ann, ok := markers.ManifestAnnotationsOf(cty.ObjectVal(got))
	if !ok || ann[markers.AddressAnnotation] != addr.String() {
		t.Fatalf("seed annotations = %v, want the address annotation", ann)
	}
}
