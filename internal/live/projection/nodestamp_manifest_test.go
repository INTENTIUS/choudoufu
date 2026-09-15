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
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1079: the manifest branch of the node-path stamp. The
// schema is hashicorp/kubernetes 3.2.1's kubernetes_manifest, read off the
// provider rather than invented, and the manifest values are what an
// object constructor evaluates to inside a dynamic attribute: nested
// object types, never maps unless the operator wrote one.

func manifestTypeSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"manifest":        {Type: cty.DynamicPseudoType, Required: true},
			"object":          {Type: cty.DynamicPseudoType, Computed: true},
			"computed_fields": {Type: cty.List(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"field_manager": {Nesting: configschema.NestingList, MaxItems: 1, Block: configschema.Block{
				Attributes: map[string]*configschema.Attribute{"name": {Type: cty.String, Optional: true}},
			}},
			"wait": {Nesting: configschema.NestingList, MaxItems: 1, Block: configschema.Block{
				Attributes: map[string]*configschema.Attribute{"rollout": {Type: cty.Bool, Optional: true}},
			}},
		},
	}}
}

// manifestTestManifest is the CronTab of claim 24, its metadata built from
// the attributes given (labels omitted entirely when nil, the way an
// object constructor with no labels key evaluates).
func manifestTestManifest(labels cty.Value) cty.Value {
	meta := map[string]cty.Value{
		"name":      cty.StringVal("my-crontab"),
		"namespace": cty.StringVal("smoke-crd"),
	}
	if labels != cty.NilVal {
		meta["labels"] = labels
	}
	return cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("stable.example.com/v1"),
		"kind":       cty.StringVal("CronTab"),
		"metadata":   cty.ObjectVal(meta),
		"spec": cty.ObjectVal(map[string]cty.Value{
			"cronSpec": cty.StringVal("* * * * */5"),
			"image":    cty.StringVal("my-awesome-cron-image"),
		}),
	})
}

func manifestTestConfig(manifest cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"manifest":        manifest,
		"object":          cty.NullVal(cty.DynamicPseudoType),
		"computed_fields": cty.NullVal(cty.List(cty.String)),
		"field_manager":   cty.ListValEmpty(cty.Object(map[string]cty.Type{"name": cty.String})),
		"wait":            cty.ListValEmpty(cty.Object(map[string]cty.Type{"rollout": cty.Bool})),
	})
}

func manifestAddr(t *testing.T) addrs.AbsResourceInstance {
	t.Helper()
	return addrs.AbsResourceInstance{Resource: addrs.Resource{
		Mode: addrs.ManagedResourceMode, Type: "kubernetes_manifest", Name: "crontab",
	}.Instance(addrs.NoKey)}
}

func requireManifestLabels(t *testing.T, config cty.Value) map[string]string {
	t.Helper()
	labels, ok := markers.ManifestLabelsOf(config)
	if !ok {
		t.Fatalf("no manifest.metadata.labels on %#v", config)
	}
	return labels
}

func TestManifestSurfaceIsReadFromTheSchema(t *testing.T) {
	if !markers.ManifestSurface(manifestTypeSchema().Block) {
		t.Fatal("kubernetes_manifest's schema is not a manifest surface")
	}
	if _, labelled := markers.LabelSurface(manifestTypeSchema().Block); labelled {
		t.Fatal("a manifest surface must not also be a label surface")
	}
	if markers.ManifestSurface(configMapTypeSchema().Block) {
		t.Fatal("a metadata-block type is not a manifest surface")
	}
}

func TestNodeResolver_AdjustConfigValue_stampsAManifestWithoutLabels(t *testing.T) {
	resolver := &NodeResolver{Estate: "smoke-crd"}
	in := manifestTestConfig(manifestTestManifest(cty.NilVal))
	got, diags := resolver.AdjustConfigValue(context.Background(), manifestAddr(t), in, manifestTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected warnings: %s", diags.ErrWithWarnings())
	}
	labels := requireManifestLabels(t, got)
	if len(labels) != 1 || labels[markers.TagEstate] != "smoke-crd" {
		t.Fatalf("labels = %v, want only %s=smoke-crd", labels, markers.TagEstate)
	}
	// The label map is written in the object constructor's own shape, and
	// nothing else in the manifest moves.
	meta := got.GetAttr("manifest").GetAttr("metadata")
	if !meta.GetAttr("labels").Type().IsObjectType() {
		t.Errorf("labels written as %s, want an object like the rest of the constructor", meta.GetAttr("labels").Type().FriendlyName())
	}
	if !meta.GetAttr("name").RawEquals(cty.StringVal("my-crontab")) || !meta.GetAttr("namespace").RawEquals(cty.StringVal("smoke-crd")) {
		t.Errorf("metadata name/namespace were touched: %#v", meta)
	}
	if !got.GetAttr("manifest").GetAttr("spec").RawEquals(in.GetAttr("manifest").GetAttr("spec")) {
		t.Errorf("spec was touched")
	}
	if !got.GetAttr("object").RawEquals(in.GetAttr("object")) || !got.GetAttr("wait").RawEquals(in.GetAttr("wait")) {
		t.Errorf("attributes beside manifest were touched")
	}
}

func TestNodeResolver_AdjustConfigValue_keepsOwnManifestLabels(t *testing.T) {
	resolver := &NodeResolver{Estate: "smoke-crd"}
	for name, own := range map[string]cty.Value{
		"object": cty.ObjectVal(map[string]cty.Value{"app": cty.StringVal("cron"), "tier": cty.StringVal("batch")}),
		"map":    cty.MapVal(map[string]cty.Value{"app": cty.StringVal("cron"), "tier": cty.StringVal("batch")}),
	} {
		t.Run(name, func(t *testing.T) {
			got, diags := resolver.AdjustConfigValue(context.Background(), manifestAddr(t), manifestTestConfig(manifestTestManifest(own)), manifestTypeSchema())
			if diags.HasErrors() {
				t.Fatalf("unexpected diagnostics: %s", diags.Err())
			}
			labels := requireManifestLabels(t, got)
			want := map[string]string{"app": "cron", "tier": "batch", markers.TagEstate: "smoke-crd"}
			if len(labels) != len(want) {
				t.Fatalf("labels = %v, want %v", labels, want)
			}
			for k, v := range want {
				if labels[k] != v {
					t.Errorf("labels[%q] = %q, want %q", k, labels[k], v)
				}
			}
			gotLabels := got.GetAttr("manifest").GetAttr("metadata").GetAttr("labels")
			if gotLabels.Type().IsMapType() != own.Type().IsMapType() {
				t.Errorf("the container shape changed: wrote %s over %s", gotLabels.Type().FriendlyName(), own.Type().FriendlyName())
			}
		})
	}
}

func TestNodeResolver_AdjustConfigValue_manifestIsIdempotent(t *testing.T) {
	resolver := &NodeResolver{Estate: "smoke-crd"}
	once, diags := resolver.AdjustConfigValue(context.Background(), manifestAddr(t), manifestTestConfig(manifestTestManifest(cty.NilVal)), manifestTypeSchema())
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	twice, diags := resolver.AdjustConfigValue(context.Background(), manifestAddr(t), once, manifestTypeSchema())
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if !twice.RawEquals(once) {
		t.Fatalf("stamping a stamped manifest changed it:\n once  %#v\n twice %#v", once, twice)
	}
}

func TestNodeResolver_AdjustConfigValue_refusesAManifestNamingAnotherEstate(t *testing.T) {
	resolver := &NodeResolver{Estate: "smoke-crd"}
	other := cty.ObjectVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("someone-else")})
	in := manifestTestConfig(manifestTestManifest(other))
	got, diags := resolver.AdjustConfigValue(context.Background(), manifestAddr(t), in, manifestTypeSchema())
	if !diags.HasErrors() {
		t.Fatal("a manifest already labelled for another estate was stamped over")
	}
	if !strings.Contains(diags.Err().Error(), SummaryMarkerConflict) {
		t.Errorf("diagnostic = %s, want %s: the manifest refusal must be the tag refusal word for word", diags.Err(), SummaryMarkerConflict)
	}
	if !got.RawEquals(in) {
		t.Errorf("the configuration was changed alongside the refusal")
	}
}

func TestNodeResolver_AdjustConfigValue_manifestUntagWritesNothing(t *testing.T) {
	addr := manifestAddr(t)
	resolver := &NodeResolver{Estate: "smoke-crd", PolicyUntag: map[string]string{addr.String(): markers.TagEstate}}
	in := manifestTestConfig(manifestTestManifest(cty.NilVal))
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, in, manifestTypeSchema())
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if !got.RawEquals(in) {
		t.Fatalf("an untagged instance's manifest was changed: %#v", got)
	}
}

func TestNodeResolver_AdjustConfigValue_manifestRefusesAnIllegalLabelValue(t *testing.T) {
	resolver := &NodeResolver{Estate: strings.Repeat("a", 64)}
	_, diags := resolver.AdjustConfigValue(context.Background(), manifestAddr(t), manifestTestConfig(manifestTestManifest(cty.NilVal)), manifestTypeSchema())
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), SummaryMarkerNotALabel) {
		t.Fatalf("a 64-character estate was written as a label: %v", diags.ErrWithWarnings())
	}
}

func TestNodeResolver_AdjustConfigValue_manifestLeavesTheUnstampableAlone(t *testing.T) {
	resolver := &NodeResolver{Estate: "smoke-crd"}
	cases := map[string]struct {
		manifest cty.Value
		summary  string
	}{
		"unknown manifest": {cty.UnknownVal(cty.DynamicPseudoType), SummaryManifestUnresolved},
		"string manifest":  {cty.StringVal("apiVersion: v1"), SummaryManifestUnmergeable},
		"no metadata": {cty.ObjectVal(map[string]cty.Value{
			"apiVersion": cty.StringVal("v1"), "kind": cty.StringVal("Thing"),
		}), SummaryManifestUnmergeable},
		"unknown labels": {manifestTestManifest(cty.UnknownVal(cty.Map(cty.String))), SummaryManifestUnresolved},
		"list labels":    {manifestTestManifest(cty.ListVal([]cty.Value{cty.StringVal("x")})), SummaryManifestUnmergeable},
		"marked metadata": {cty.ObjectVal(map[string]cty.Value{
			"apiVersion": cty.StringVal("v1"), "kind": cty.StringVal("Thing"),
			"metadata": cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("x")}).Mark("sensitive"),
		}), SummaryManifestMarked},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := manifestTestConfig(tc.manifest)
			got, diags := resolver.AdjustConfigValue(context.Background(), manifestAddr(t), in, manifestTypeSchema())
			if diags.HasErrors() {
				t.Fatalf("a warning case raised an error: %s", diags.Err())
			}
			if len(diags) != 1 || diags[0].Description().Summary != tc.summary {
				t.Fatalf("diagnostics = %v, want one %q", diags.ErrWithWarnings(), tc.summary)
			}
			if !got.RawEquals(in) {
				t.Errorf("the configuration was changed alongside the warning: %#v", got)
			}
		})
	}
}

func TestNodeResolver_AdjustConfigValue_manifestKeepsMarks(t *testing.T) {
	resolver := &NodeResolver{Estate: "smoke-crd"}
	own := cty.ObjectVal(map[string]cty.Value{"app": cty.StringVal("cron")}).Mark("sensitive")
	in := manifestTestConfig(manifestTestManifest(own).Mark("outer"))
	got, diags := resolver.AdjustConfigValue(context.Background(), manifestAddr(t), in, manifestTypeSchema())
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	manifest := got.GetAttr("manifest")
	if !manifest.HasMark("outer") {
		t.Errorf("the manifest's own mark was dropped")
	}
	manifest, _ = manifest.Unmark()
	labels := manifest.GetAttr("metadata").GetAttr("labels")
	if !labels.HasMark("sensitive") {
		t.Errorf("the labels' own mark was dropped")
	}
	labels, _ = labels.Unmark()
	if !labels.GetAttr(markers.TagEstate).RawEquals(cty.StringVal("smoke-crd")) {
		t.Errorf("the marker was not written under the marks: %#v", labels)
	}
}

func TestNodeResolver_AdjustIgnoreChanges_manifestProtectsTheLabel(t *testing.T) {
	// kubernetes_manifest is not record-located, so no selection ever
	// reaches it and AdjustIgnoreChanges returns nil for it today; the
	// branch's path is pinned through the helper it returns, so that the
	// day a selection does reach the type the protected path is the one
	// an operator's own ignore_changes would name.
	resolver := &NodeResolver{Estate: "smoke-crd"}
	if got := resolver.AdjustIgnoreChanges(context.Background(), manifestAddr(t), manifestTypeSchema()); got != nil {
		t.Fatalf("an unselected instance got ignore paths: %v", got)
	}
	want := cty.Path{
		cty.GetAttrStep{Name: "manifest"}, cty.GetAttrStep{Name: "metadata"}, cty.GetAttrStep{Name: "labels"},
		cty.IndexStep{Key: cty.StringVal(markers.TagEstate)},
	}
	if !markers.ManifestLabelPath(markers.TagEstate).Equals(want) {
		t.Errorf("ManifestLabelPath = %#v, want %#v", markers.ManifestLabelPath(markers.TagEstate), want)
	}
}

func TestStampManifestSeedCarriesTheLabel(t *testing.T) {
	seed := map[string]cty.Value{
		"manifest":        manifestTestManifest(cty.NilVal),
		"computed_fields": cty.NullVal(cty.List(cty.String)),
	}
	got := stampManifestSeed(manifestAddr(t), seed, "smoke-crd")
	labels, ok := markers.ManifestLabelsOf(cty.ObjectVal(got))
	if !ok || labels[markers.TagEstate] != "smoke-crd" {
		t.Fatalf("the seed's manifest carries no marker: %v", got["manifest"])
	}
	if _, has := seed["manifest"].GetAttr("metadata").Type().AttributeTypes()["labels"]; has {
		t.Errorf("the caller's seed was mutated")
	}
	if !got["computed_fields"].RawEquals(seed["computed_fields"]) {
		t.Errorf("another seed entry was touched")
	}
	// No estate: the seed is returned as it was, nothing invented.
	if same := stampManifestSeed(manifestAddr(t), seed, ""); !same["manifest"].RawEquals(seed["manifest"]) {
		t.Errorf("a run with no estate stamped the seed")
	}
	// A seed already labelled for another estate is left for the node
	// stamp's own refusal.
	other := map[string]cty.Value{"manifest": manifestTestManifest(cty.ObjectVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("someone-else")}))}
	if same := stampManifestSeed(manifestAddr(t), other, "smoke-crd"); !same["manifest"].RawEquals(other["manifest"]) {
		t.Errorf("a conflicting seed was overwritten")
	}
}

func manifestReadValue(manifestLabels, liveLabels cty.Value) cty.Value {
	live := cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("stable.example.com/v1"),
		"kind":       cty.StringVal("CronTab"),
		"metadata": cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal("my-crontab"),
			"namespace": cty.StringVal("smoke-crd"),
			"labels":    liveLabels,
		}),
	})
	return cty.ObjectVal(map[string]cty.Value{
		"manifest":        manifestTestManifest(manifestLabels),
		"object":          live,
		"computed_fields": cty.NullVal(cty.List(cty.String)),
		"field_manager":   cty.ListValEmpty(cty.Object(map[string]cty.Type{"name": cty.String})),
		"wait":            cty.ListValEmpty(cty.Object(map[string]cty.Type{"rollout": cty.Bool})),
	})
}

func TestMirrorManifestMarkerFollowsTheLiveObject(t *testing.T) {
	stamped := cty.ObjectVal(map[string]cty.Value{"app": cty.StringVal("cron"), markers.TagEstate: cty.StringVal("smoke-crd")})
	block := manifestTypeSchema().Block
	cases := map[string]struct {
		live cty.Value
		want map[string]string
	}{
		"label stripped":       {cty.MapVal(map[string]cty.Value{"app": cty.StringVal("cron")}), map[string]string{"app": "cron"}},
		"no labels at all":     {cty.NullVal(cty.Map(cty.String)), map[string]string{"app": "cron"}},
		"label intact":         {cty.MapVal(map[string]cty.Value{"app": cty.StringVal("cron"), markers.TagEstate: cty.StringVal("smoke-crd")}), map[string]string{"app": "cron", markers.TagEstate: "smoke-crd"}},
		"label names another":  {cty.MapVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("other")}), map[string]string{"app": "cron", markers.TagEstate: "other"}},
		"controller added one": {cty.MapVal(map[string]cty.Value{"app": cty.StringVal("cron"), markers.TagEstate: cty.StringVal("smoke-crd"), "added": cty.StringVal("yes")}), map[string]string{"app": "cron", markers.TagEstate: "smoke-crd"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := manifestReadValue(stamped, tc.live)
			got := mirrorManifestMarker(in, block)
			labels, ok := markers.ManifestLabelsOf(got)
			if !ok {
				t.Fatalf("no manifest labels on %#v", got)
			}
			if len(labels) != len(tc.want) {
				t.Fatalf("labels = %v, want %v", labels, tc.want)
			}
			for k, v := range tc.want {
				if labels[k] != v {
					t.Errorf("labels[%q] = %q, want %q", k, labels[k], v)
				}
			}
			if !got.GetAttr("object").RawEquals(in.GetAttr("object")) {
				t.Errorf("the live object was touched")
			}
			if !got.GetAttr("manifest").GetAttr("spec").RawEquals(in.GetAttr("manifest").GetAttr("spec")) {
				t.Errorf("the manifest's spec was touched")
			}
		})
	}
}

func TestMirrorManifestMarkerLeavesTheRestAlone(t *testing.T) {
	block := manifestTypeSchema().Block
	// An unstamped prior manifest (nothing to mirror), a marked one, and
	// a type that is not a manifest surface all come back as they were.
	plain := manifestReadValue(cty.ObjectVal(map[string]cty.Value{"app": cty.StringVal("cron")}), cty.NullVal(cty.Map(cty.String)))
	if got := mirrorManifestMarker(plain, block); !got.RawEquals(plain) {
		t.Errorf("an unstamped prior was rewritten: %#v", got)
	}
	marked := manifestReadValue(cty.ObjectVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("smoke-crd")}).Mark("sensitive"), cty.NullVal(cty.Map(cty.String)))
	if got := mirrorManifestMarker(marked, block); !got.RawEquals(marked) {
		t.Errorf("a marked labels value was rewritten: %#v", got)
	}
	cm := configMapTestConfig(cty.NullVal(cty.Map(cty.String)))
	if got := mirrorManifestMarker(cm, configMapTypeSchema().Block); !got.RawEquals(cm) {
		t.Errorf("a metadata-block type was rewritten")
	}
}
