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

// GitHub issue #1061: the label branch of the node-path stamp. The schema
// is hashicorp/kubernetes 3.2.1's kubernetes_config_map, read off the
// provider rather than invented, and the config values are what
// EvaluateBlock produces for it: metadata as a one-element list of
// objects, labels a map(string) or null.

const configMapTestType = "kubernetes_config_map"

func configMapTypeSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"data":      {Type: cty.Map(cty.String), Optional: true},
			"id":        {Type: cty.String, Computed: true},
			"immutable": {Type: cty.Bool, Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {
				Nesting:  configschema.NestingList,
				MinItems: 1,
				MaxItems: 1,
				Block: configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"annotations": {Type: cty.Map(cty.String), Optional: true},
						"labels":      {Type: cty.Map(cty.String), Optional: true},
						"name":        {Type: cty.String, Optional: true, Computed: true},
						"namespace":   {Type: cty.String, Optional: true},
					},
				},
			},
		},
	}}
}

func configMapTestConfig(labels cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"data":      cty.MapVal(map[string]cty.Value{"greeting": cty.StringVal("hello")}),
		"id":        cty.NullVal(cty.String),
		"immutable": cty.NullVal(cty.Bool),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"annotations": cty.NullVal(cty.Map(cty.String)),
			"labels":      labels,
			"name":        cty.StringVal("app-config"),
			"namespace":   cty.StringVal("smoke-k8s"),
		})}),
	})
}

func configMapAddr(t *testing.T) addrs.AbsResourceInstance {
	t.Helper()
	return addrs.AbsResourceInstance{Resource: addrs.Resource{
		Mode: addrs.ManagedResourceMode, Type: configMapTestType, Name: "app",
	}.Instance(addrs.NoKey)}
}

func requireLabels(t *testing.T, config cty.Value) map[string]string {
	t.Helper()
	labels, ok := markers.LabelsOf(config)
	if !ok {
		t.Fatalf("no metadata.labels on %#v", config)
	}
	return labels
}

func TestNodeResolver_AdjustConfigValue_labelsUnlabelledResource(t *testing.T) {
	resolver := &NodeResolver{Estate: "smoke-k8s"}
	got, diags := resolver.AdjustConfigValue(context.Background(), configMapAddr(t), configMapTestConfig(cty.NullVal(cty.Map(cty.String))), configMapTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	labels := requireLabels(t, got)
	if len(labels) != 1 || labels[markers.TagEstate] != "smoke-k8s" {
		t.Fatalf("labels = %v, want only %s=smoke-k8s: the Kubernetes marker is the estate alone, never the address", labels, markers.TagEstate)
	}
	if _, has := labels[markers.TagAddress]; has {
		t.Errorf("a tofu-address label was written; #1016's ruling keeps the address off the object")
	}
	// The rest of the metadata block and the resource are untouched.
	meta := got.GetAttr("metadata").AsValueSlice()[0]
	if !meta.GetAttr("name").RawEquals(cty.StringVal("app-config")) || !meta.GetAttr("namespace").RawEquals(cty.StringVal("smoke-k8s")) {
		t.Errorf("metadata name/namespace were touched: %#v", meta)
	}
	if !got.GetAttr("data").RawEquals(configMapTestConfig(cty.NullVal(cty.Map(cty.String))).GetAttr("data")) {
		t.Errorf("data was touched: %#v", got.GetAttr("data"))
	}
	// The value keeps the schema's own type, so the provider sees a
	// metadata block it recognises.
	if !got.Type().Equals(configMapTestConfig(cty.NullVal(cty.Map(cty.String))).Type()) {
		t.Errorf("the adjusted value changed type:\n got %#v\nwant %#v", got.Type(), configMapTestConfig(cty.NullVal(cty.Map(cty.String))).Type())
	}
}

func TestNodeResolver_AdjustConfigValue_keepsOwnLabels(t *testing.T) {
	resolver := &NodeResolver{Estate: "smoke-k8s"}
	own := cty.MapVal(map[string]cty.Value{"app": cty.StringVal("web"), "tier": cty.StringVal("front")})
	got, diags := resolver.AdjustConfigValue(context.Background(), configMapAddr(t), configMapTestConfig(own), configMapTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	labels := requireLabels(t, got)
	want := map[string]string{"app": "web", "tier": "front", markers.TagEstate: "smoke-k8s"}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("labels[%q] = %q, want %q", k, labels[k], v)
		}
	}
}

func TestNodeResolver_AdjustConfigValue_labelConflictIsFatal(t *testing.T) {
	resolver := &NodeResolver{Estate: "smoke-k8s"}
	foreign := cty.MapVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("someone-else")})
	got, diags := resolver.AdjustConfigValue(context.Background(), configMapAddr(t), configMapTestConfig(foreign), configMapTypeSchema())
	if !diags.HasErrors() {
		t.Fatal("a label naming another estate was overwritten silently")
	}
	if !strings.Contains(diags.Err().Error(), SummaryMarkerConflict) {
		t.Errorf("diagnostic is not the marker conflict every substrate raises: %s", diags.Err())
	}
	if labels := requireLabels(t, got); labels[markers.TagEstate] != "someone-else" {
		t.Errorf("the configuration's own value was changed on a refused write: %v", labels)
	}
}

func TestNodeResolver_AdjustConfigValue_estateNotALabelValueIsFatal(t *testing.T) {
	// A legal estate name that ends in a hyphen is not a legal label
	// value; the stamp refuses rather than writing something the API
	// server rejects or a client truncates.
	resolver := &NodeResolver{Estate: "prod-"}
	if !markers.ValidEstateName("prod-") {
		t.Fatal("test premise: prod- must be a legal estate name")
	}
	_, diags := resolver.AdjustConfigValue(context.Background(), configMapAddr(t), configMapTestConfig(cty.NullVal(cty.Map(cty.String))), configMapTypeSchema())
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), SummaryMarkerNotALabel) {
		t.Fatalf("expected %q, got: %v", SummaryMarkerNotALabel, diags.Err())
	}
}

func TestNodeResolver_AdjustConfigValue_untagReleasesLabel(t *testing.T) {
	addr := configMapAddr(t)
	resolver := &NodeResolver{Estate: "smoke-k8s", PolicyUntag: map[string]string{addr.String(): markers.TagEstate}}
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, configMapTestConfig(cty.NullVal(cty.Map(cty.String))), configMapTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	if labels, _ := markers.LabelsOf(got); len(labels) != 0 {
		t.Errorf("an untagged instance was still labelled: %v", labels)
	}
}

func TestNodeResolver_AdjustConfigValue_noEstateWritesNoLabel(t *testing.T) {
	resolver := &NodeResolver{}
	in := configMapTestConfig(cty.NullVal(cty.Map(cty.String)))
	got, _ := resolver.AdjustConfigValue(context.Background(), configMapAddr(t), in, configMapTypeSchema())
	if !got.RawEquals(in) {
		t.Errorf("a run with no estate name changed the value: %#v", got)
	}
}

func TestNodeResolver_AdjustConfigValue_awsShapeUnaffectedByLabelBranch(t *testing.T) {
	// The AWS branch still writes both tags and never a label.
	addr := locatedTestAddr(t, markersRecordTestType, "main")
	resolver := &NodeResolver{Estate: "test-estate"}
	got, diags := resolver.AdjustConfigValue(context.Background(), addr, nodeStampTestConfig(cty.NullVal(cty.Map(cty.String))), markersRecordTypeSchema())
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Err())
	}
	tags := requireTags(t, got)
	if tags[markers.TagEstate] != "test-estate" || tags[markers.TagAddress] == "" {
		t.Errorf("AWS tags = %v", tags)
	}
	if _, has := markers.LabelsOf(got); has {
		t.Error("an AWS resource grew a metadata.labels")
	}
}
