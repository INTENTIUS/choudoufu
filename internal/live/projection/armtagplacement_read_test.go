// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// TestARMTagPlacementOnAReadObject: adopt and mv hand ARMTagPlacement the
// object as the provider read it, where azapi fills the top-level tags from
// the response body whatever the configuration did (GitHub issue #2207). A
// populated top-level tags therefore settles nothing there, and a body that
// cannot be read (unknown or marked as a whole) is declined; a readable
// body decides as it always did.
func TestARMTagPlacementOnAReadObject(t *testing.T) {
	requireTaggable(t, "Microsoft.Network/virtualNetworks", "2023-09-01", true)
	const typ = "Microsoft.Network/virtualNetworks@2023-09-01"
	populated := cty.MapVal(map[string]cty.Value{"env": cty.StringVal("prod")})
	addr := armAddr(t, "azapi_resource.vnet")
	block := azapiSchema().Block

	for name, tc := range map[string]struct {
		body             cty.Value
		wantOK, wantBody bool
	}{
		"unknown body, tags populated": {cty.DynamicVal, false, false},
		"marked body, tags populated":  {cty.ObjectVal(map[string]cty.Value{"location": cty.StringVal("x")}).Mark("sensitive"), false, false},
		"body without tags":            {cty.ObjectVal(map[string]cty.Value{"location": cty.StringVal("x")}), true, false},
		"body with tags": {cty.ObjectVal(map[string]cty.Value{
			"tags": cty.ObjectVal(map[string]cty.Value{"env": cty.StringVal("prod")}),
		}), true, true},
	} {
		t.Run(name, func(t *testing.T) {
			inBody, ok, why := ARMTagPlacement(addr, block, azapiConfig(typ, tc.body, populated))
			if ok != tc.wantOK || inBody != tc.wantBody {
				t.Errorf("inBody=%v ok=%v (%s), want inBody=%v ok=%v", inBody, ok, why, tc.wantBody, tc.wantOK)
			}
			if !ok && why == "" {
				t.Error("declined with no reason")
			}
		})
	}
}
