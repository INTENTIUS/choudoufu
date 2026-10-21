// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// GitHub issue #1120: the ARM tag surface.

func TestARMTagSurface(t *testing.T) {
	tags := &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
	for name, tc := range map[string]struct {
		attrs map[string]*configschema.Attribute
		want  bool
	}{
		"azurerm tags":          {map[string]*configschema.Attribute{"tags": tags}, true},
		"azapi tags":            {map[string]*configschema.Attribute{"tags": {Type: cty.Map(cty.String), Optional: true, Computed: true}}, true},
		"aws tags_all":          {map[string]*configschema.Attribute{"tags": tags, "tags_all": {Type: cty.Map(cty.String), Computed: true}}, false},
		"computed-only":         {map[string]*configschema.Attribute{"tags": {Type: cty.Map(cty.String), Computed: true}}, false},
		"a list (named values)": {map[string]*configschema.Attribute{"tags": {Type: cty.List(cty.String), Optional: true}}, false},
		"no tags":               {map[string]*configschema.Attribute{"name": {Type: cty.String, Required: true}}, false},
	} {
		block := &configschema.Block{Attributes: tc.attrs}
		if got := ARMTaggable(block); got != tc.want {
			t.Errorf("%s: ARMTaggable = %v, want %v", name, got, tc.want)
		}
		// Every ARM-surface block is a tags-surface block, so no type
		// stamped through TagSurface before #1120 is left unstamped.
		if ARMTaggable(block) && !Taggable(block) {
			t.Errorf("%s: ARM-taggable but not Taggable", name)
		}
	}
}

func TestARMTagsOfFoldsMarkerKeys(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{"tags": cty.MapVal(map[string]cty.Value{
		"Tofu-Estate":    cty.StringVal("prod"),
		"TOFU-ADDRESS":   cty.StringVal("azapi_resource.vnet"),
		"Tofu-Address-2": cty.StringVal("x"),
		"tofu-Slot":      cty.StringVal("3"),
		"Environment":    cty.StringVal("prod"),
	})})
	got, ok := ARMTagsOf(obj)
	if !ok {
		t.Fatal("ARMTagsOf: no tags")
	}
	want := map[string]string{
		TagEstate:          "prod",
		TagAddress:         "azapi_resource.vnet",
		ContinuationTag(2): "x",
		TagSlot:            "3",
		"Environment":      "prod",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ARMTagsOf = %v, want %v", got, want)
	}

	// Two spellings of one marker, which ARM refuses to store: the
	// lexically first wins, whatever the map order.
	two := cty.ObjectVal(map[string]cty.Value{"tags": cty.MapVal(map[string]cty.Value{
		"TOFU-ESTATE": cty.StringVal("a"),
		"tofu-estate": cty.StringVal("b"),
	})})
	if got, _ := ARMTagsOf(two); got[TagEstate] != "a" {
		t.Errorf("two spellings: tofu-estate = %q, want the lexically first spelling's %q", got[TagEstate], "a")
	}

	if _, ok := ARMTagsOf(cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("x")})); ok {
		t.Error("an object with no tags attribute read as carrying tags")
	}
	if got, ok := ARMTagsOf(cty.ObjectVal(map[string]cty.Value{"tags": cty.NullVal(cty.Map(cty.String))})); !ok || len(got) != 0 {
		t.Errorf("null tags = %v, %v; want empty, true", got, ok)
	}
	if _, ok := ARMTagsOf(cty.ObjectVal(map[string]cty.Value{"tags": cty.MapVal(map[string]cty.Value{"a": cty.StringVal("b")}).Mark("sensitive")})); ok {
		t.Error("a marked tags map read as readable")
	}
}

func TestSetARMMarkerTagKeepsOneSpelling(t *testing.T) {
	tags := map[string]string{"Tofu-Estate": "old", "TOFU-ESTATE": "older", "env": "x"}
	SetARMMarkerTag(tags, TagEstate, "new")
	if want := map[string]string{"TOFU-ESTATE": "new", "env": "x"}; !reflect.DeepEqual(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
	SetARMMarkerTag(tags, TagAddress, "a.b")
	if tags[TagAddress] != "a.b" {
		t.Errorf("a marker the map lacks is written lowercase: %v", tags)
	}
	DeleteARMMarkerTag(tags, TagEstate)
	if _, ok := tags["TOFU-ESTATE"]; ok {
		t.Errorf("DeleteARMMarkerTag left a spelling: %v", tags)
	}
}

func TestCanonicalMarkerKey(t *testing.T) {
	for in, want := range map[string]string{"Tofu-Estate": TagEstate, "tofu-address-4": ContinuationTag(4), "TOFU-SLOT": TagSlot, "tofu-address-5": "", "Environment": ""} {
		got, ok := CanonicalMarkerKey(in)
		if got != want || ok != (want != "") {
			t.Errorf("CanonicalMarkerKey(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}
