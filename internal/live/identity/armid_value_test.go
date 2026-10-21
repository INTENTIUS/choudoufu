// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1120: [ComponentsFromValue] read azapi_resource's synthesized
// identity as a plain concatenation, so the `type` value went into the id
// as written and an azapi subnet rendered as
// parent_id + "Microsoft.Network/virtualNetworks/subnets@2024-05-01" + name.
// It now applies the same joining rule the static evaluator does
// ([armTypeSegment]), and [AzapiResourceID] is that rule for strings.
func TestComponentsFromValueJoinsTheARMTypeSegment(t *testing.T) {
	const parent = "/subscriptions/0/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet"
	schemas := map[string]providers.Schema{"azapi_resource": azapiResourceSchema()}
	ti, ok := SynthesizeTypeIdentity("azapi_resource", schemas, nil)
	if !ok {
		t.Fatal("azapi_resource is not admitted by its schema")
	}
	for _, tc := range []struct{ parent, typ, name, want string }{
		{parent, "Microsoft.Network/virtualNetworks/subnets@2024-05-01", "web", parent + "/subnets/web"},
		{"/subscriptions/0/resourceGroups/rg", "Microsoft.Network/virtualNetworks@2024-05-01", "vnet", "/subscriptions/0/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet"},
		{"/subscriptions/0", "Microsoft.Resources/resourceGroups@2021-04-01", "rg", "/subscriptions/0/resourceGroups/rg"},
		{"/", "Microsoft.Management/managementGroups@2021-04-01", "mg", "/providers/Microsoft.Management/managementGroups/mg"},
	} {
		obj := cty.ObjectVal(map[string]cty.Value{
			"type":      cty.StringVal(tc.typ),
			"parent_id": cty.StringVal(tc.parent),
			"name":      cty.StringVal(tc.name),
		})
		got, _, ok := ComponentsFromValue(ti, obj)
		if !ok || got != tc.want {
			t.Errorf("ComponentsFromValue(%s) = %q, %v; want %q", tc.typ, got, ok, tc.want)
		}
		if got, ok := AzapiResourceID(tc.parent, tc.typ, tc.name); !ok || got != tc.want {
			t.Errorf("AzapiResourceID(%s) = %q, %v; want %q", tc.typ, got, ok, tc.want)
		}
	}

	// A type azapi names no object of, or one without an API version, is
	// refused rather than rendered.
	for _, typ := range []string{"Microsoft.Resources/tenants@2020-01-01", "Microsoft.Network/virtualNetworks"} {
		if got, ok := AzapiResourceID("/", typ, "x"); ok {
			t.Errorf("AzapiResourceID(%s) = %q; want a refusal", typ, got)
		}
	}
}
