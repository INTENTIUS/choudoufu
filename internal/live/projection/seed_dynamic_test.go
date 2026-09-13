// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// TestWithSeededAttrsFillsADynamicAttribute is GitHub issue #1079's first
// estate: kubernetes_manifest's `manifest` is DynamicPseudoType in the
// schema, the import leaves it null, and the configuration's concrete
// object is the seed. An exact type match refused it and the prior carried
// a null manifest forever.
//
// Proving it red: restore the exact Equals check in withSeededAttrs.
func TestWithSeededAttrsFillsADynamicAttribute(t *testing.T) {
	imported := cty.ObjectVal(map[string]cty.Value{
		"manifest": cty.NullVal(cty.DynamicPseudoType),
		"object":   cty.ObjectVal(map[string]cty.Value{"kind": cty.StringVal("CronTab")}),
		"wait_for": cty.NullVal(cty.List(cty.String)),
	})
	manifest := cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("stable.example.com/v1"),
		"kind":       cty.StringVal("CronTab"),
		"metadata":   cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("my-crontab")}),
	})
	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"manifest": {Type: cty.DynamicPseudoType, Required: true},
		"object":   {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
		"wait_for": {Type: cty.List(cty.String), Optional: true},
	}}
	// The provider imports the manifest as a typed null object, never as a
	// dynamic null; the schema's declared type is what admits the seed.
	imported = cty.ObjectVal(map[string]cty.Value{
		"manifest": cty.NullVal(cty.Object(map[string]cty.Type{"kind": cty.String})),
		"object":   imported.GetAttr("object"),
		"wait_for": imported.GetAttr("wait_for"),
	})
	seeded, ok := withSeededAttrs(imported, map[string]cty.Value{
		"manifest": manifest,
		// A concretely typed attribute still needs the exact type: a set
		// offered where the object holds a list is refused, as before.
		"wait_for": cty.SetVal([]cty.Value{cty.StringVal("x")}),
	}, block)
	if !ok {
		t.Fatal("the dynamic manifest was not seeded")
	}
	if got := seeded.GetAttr("manifest"); !got.RawEquals(manifest) {
		t.Errorf("manifest after seeding = %#v, want the configuration's object", got)
	}
	if got := seeded.GetAttr("wait_for"); !got.IsNull() {
		t.Errorf("wait_for was seeded from a value of the wrong type: %#v", got)
	}
	if got := seeded.GetAttr("object"); !got.RawEquals(imported.GetAttr("object")) {
		t.Errorf("object moved: %#v", got)
	}
}
