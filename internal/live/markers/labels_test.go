// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// kubernetesMetadataBlock is the shape hashicorp/kubernetes gives every
// resource with object metadata: a metadata list block, exactly one item,
// with optional labels and annotations maps and a name that is optional
// and computed. Read from the provider's own schema at 3.2.1
// (kubernetes_config_map) rather than invented.
func kubernetesMetadataBlock() *configschema.Block {
	return &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"data": {Type: cty.Map(cty.String), Optional: true},
			"id":   {Type: cty.String, Computed: true},
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
						"uid":         {Type: cty.String, Computed: true},
					},
				},
			},
		},
	}
}

func TestLabelSurface(t *testing.T) {
	if attr, ok := LabelSurface(kubernetesMetadataBlock()); !ok || attr == nil || !attr.Type.IsMapType() {
		t.Fatalf("the kubernetes metadata shape is not a label surface: %v, %v", attr, ok)
	}

	// A taggable AWS type is never a label surface, whatever else it carries.
	aws := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}}
	if _, ok := LabelSurface(aws); ok {
		t.Error("an AWS tags map was reported as a label surface")
	}

	for name, mutate := range map[string]func(*configschema.Block){
		"no metadata block":  func(b *configschema.Block) { delete(b.BlockTypes, "metadata") },
		"metadata is a set":  func(b *configschema.Block) { b.BlockTypes["metadata"].Nesting = configschema.NestingSet },
		"metadata unbounded": func(b *configschema.Block) { b.BlockTypes["metadata"].MaxItems = 0 },
		"labels computed only": func(b *configschema.Block) {
			b.BlockTypes["metadata"].Block.Attributes["labels"] = &configschema.Attribute{Type: cty.Map(cty.String), Computed: true}
		},
		"labels not a map": func(b *configschema.Block) {
			b.BlockTypes["metadata"].Block.Attributes["labels"] = &configschema.Attribute{Type: cty.List(cty.String), Optional: true}
		},
		"no labels attribute": func(b *configschema.Block) { delete(b.BlockTypes["metadata"].Block.Attributes, "labels") },
	} {
		t.Run(name, func(t *testing.T) {
			b := kubernetesMetadataBlock()
			mutate(b)
			if _, ok := LabelSurface(b); ok {
				t.Errorf("%s: reported as a label surface", name)
			}
		})
	}
	if _, ok := LabelSurface(nil); ok {
		t.Error("nil block reported as a label surface")
	}
}

func TestValidLabelValue(t *testing.T) {
	for value, want := range map[string]bool{
		"smoke-k8s":             true,
		"a":                     true,
		"prod-networking.v2_x":  true,
		strings.Repeat("a", 63): true,
		"":                      false,
		strings.Repeat("a", 64): false,
		"ends-with-hyphen-":     false, // a legal estate name, an illegal label value
		"-starts-with-hyphen":   false,
		"has space":             false,
		"module.app:key":        false, // the instance-key colon #1016 measured
		"aws_vpc.main[0]":       false,
	} {
		if got := ValidLabelValue(value); got != want {
			t.Errorf("ValidLabelValue(%q) = %v, want %v", value, got, want)
		}
	}
	// Every legal estate name that is short enough and does not end in a
	// hyphen is a legal label value; the two grammars agree there.
	if !ValidEstateName("smoke-k8s") || !ValidLabelValue("smoke-k8s") {
		t.Error("smoke-k8s must be both a legal estate name and a legal label value")
	}
}

func TestLabelSurfacePath(t *testing.T) {
	p := LabelSurfacePath(TagEstate)
	if len(p) != 4 {
		t.Fatalf("path has %d steps, want 4: %#v", len(p), p)
	}
	if s, ok := p[0].(cty.GetAttrStep); !ok || s.Name != "metadata" {
		t.Errorf("step 0 = %#v, want metadata", p[0])
	}
	if s, ok := p[1].(cty.IndexStep); !ok || !s.Key.RawEquals(cty.NumberIntVal(0)) {
		t.Errorf("step 1 = %#v, want [0]", p[1])
	}
	if s, ok := p[2].(cty.GetAttrStep); !ok || s.Name != "labels" {
		t.Errorf("step 2 = %#v, want labels", p[2])
	}
	if s, ok := p[3].(cty.IndexStep); !ok || !s.Key.RawEquals(cty.StringVal(TagEstate)) {
		t.Errorf("step 3 = %#v, want [%q]", p[3], TagEstate)
	}
}

func TestLabelsOf(t *testing.T) {
	obj := func(labels cty.Value) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("default/x"),
			"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"name":   cty.StringVal("x"),
				"labels": labels,
			})}),
		})
	}
	got, ok := LabelsOf(obj(cty.MapVal(map[string]cty.Value{TagEstate: cty.StringVal("e"), "app": cty.StringVal("web")})))
	if !ok || got[TagEstate] != "e" || got["app"] != "web" || len(got) != 2 {
		t.Errorf("LabelsOf = %v, %v", got, ok)
	}
	got, ok = LabelsOf(obj(cty.NullVal(cty.Map(cty.String))))
	if !ok || len(got) != 0 {
		t.Errorf("null labels: LabelsOf = %v, %v, want an empty map and true", got, ok)
	}
	if _, ok := LabelsOf(cty.ObjectVal(map[string]cty.Value{"tags": cty.NullVal(cty.Map(cty.String))})); ok {
		t.Error("an object with no metadata was reported as having labels")
	}
	if _, ok := LabelsOf(cty.NullVal(cty.DynamicPseudoType)); ok {
		t.Error("a null object was reported as having labels")
	}
}
