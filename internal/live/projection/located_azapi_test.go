// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #2119: an azapi_resource's record keeps the three arguments
// azapi built its id from beside the id itself. The id is what an import
// needs; the type is what says whether the Resource Graph sweep can find the
// object once its block is removed (a subnet takes no tags), and nothing else
// holds it then. Before this the record was the id alone.
func TestAzapiRecordKeepsItsArgumentsBesideTheID(t *testing.T) {
	schema := providers.Schema{
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":        {Type: cty.String, Computed: true},
			"type":      {Type: cty.String, Required: true},
			"name":      {Type: cty.String, Optional: true, Computed: true},
			"parent_id": {Type: cty.String, Optional: true, Computed: true},
			"body":      {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
			"tags":      {Type: cty.Map(cty.String), Optional: true, Computed: true},
		}},
		// azapi 2.13.0's identity schema: id required, type optional.
		IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Required: true},
			"type": {Type: cty.String, Optional: true},
		}},
	}
	const (
		parent = "/subscriptions/0/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet"
		id     = parent + "/subnets/web"
		typ    = "Microsoft.Network/virtualNetworks/subnets@2024-05-01"
	)
	obj := cty.ObjectVal(map[string]cty.Value{
		"id":        cty.StringVal(id),
		"type":      cty.StringVal(typ),
		"name":      cty.StringVal("web"),
		"parent_id": cty.StringVal(parent),
		"body":      cty.NullVal(cty.DynamicPseudoType),
		"tags":      cty.NullVal(cty.Map(cty.String)),
	})
	rec, ok := LocatedRecordFrom("azapi_resource", schema, obj)
	if !ok {
		t.Fatal("no record for an applied azapi_resource")
	}
	if rec.ImportID != id {
		t.Errorf("ImportID %q, want the id the provider read back %q", rec.ImportID, id)
	}
	want := map[string]string{"type": typ, "parent_id": parent, "name": "web"}
	if len(rec.Components) != len(want) {
		t.Errorf("Components %v, want %v", rec.Components, want)
	}
	for k, v := range want {
		if rec.Components[k] != v {
			t.Errorf("Components[%s] = %q, want %q", k, rec.Components[k], v)
		}
	}
}
