// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import "testing"

// GitHub issue #2119: a removed azurerm block's record turned back into its
// ARM id, by the template admission reads.
func TestAzurermRecordImportID(t *testing.T) {
	sub := "00000000-0000-0000-0000-000000000001"
	subnet := map[string]string{
		"name": "snet", "resource_group_name": "rg", "virtual_network_name": "vnet", "subscription_id": sub,
	}
	got, ok := AzurermRecordImportID("azurerm_subnet", subnet)
	want := "/subscriptions/" + sub + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/snet"
	if !ok || got != want {
		t.Errorf("azurerm_subnet: %q %v, want %q", got, ok, want)
	}

	// A hole is refused, never filled.
	delete(subnet, "virtual_network_name")
	if got, ok := AzurermRecordImportID("azurerm_subnet", subnet); ok {
		t.Errorf("azurerm_subnet with no virtual_network_name composed %q", got)
	}

	// A template admission withholds and a type with no template
	// (azurerm_role_assignment's parser has its own grammar) are not this
	// rule's.
	withheld := AzurermWithheld()
	if len(withheld) == 0 {
		t.Fatal("no withheld azurerm type to check against")
	}
	for _, typ := range []string{withheld[0][0], "azurerm_role_assignment", "aws_iam_role_policy"} {
		if AzurermRecordable(typ) {
			t.Errorf("%s is recordable; it has no template admission would build", typ)
		}
	}
	if !AzurermRecordable("azurerm_subnet") {
		t.Error("azurerm_subnet is not recordable")
	}
}
