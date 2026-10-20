// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"strings"
	"testing"
)

// TestSelectedLocatedAKSRetaggedTypes pins that an operator cannot today
// move the existing resources AKS retags onto the record rung. Microsoft's
// "Use Azure tags in AKS" (https://learn.microsoft.com/en-us/azure/aks/use-tags)
// assigns the cluster's tags to its route table, public IP and virtual
// network, overwriting the set those resources carried, so their
// tofu-estate/tofu-address are replaced by the cluster's. strict { markers
// "record" { types = [...] } } is the documented way to hold such a
// resource's identity in the record store instead, and at azurerm 5.10.0 it
// is refused for all three (internal/live/lint's
// strict-markers-unrecordable carries this sentence).
//
// The cause is one attribute. Each type's resource identity schema is
// name + resource_group_name + subscription_id, all required for import,
// and [LocatedIdentityPlanFor] records a composite identity only when every
// required component is a top-level attribute of the resource block, so a
// later run can read it back off the applied object. subscription_id is
// not one: azurerm takes it from the provider block. The same holds for
// every one of the 196 azurerm types with an identity schema in the
// committed extract.
//
// Read against the committed 5.10.0 identity extract, so a provider bump or
// a change to the record plan that makes these selectable fails here and
// says so.
func TestSelectedLocatedAKSRetaggedTypes(t *testing.T) {
	_, schemas := loadAzurermIdentityExtract(t)
	for _, typ := range []string{"azurerm_route_table", "azurerm_public_ip", "azurerm_virtual_network"} {
		schema, ok := schemas[typ]
		if !ok {
			t.Errorf("%s: not in %s", typ, azurermIdentityExtractPath)
			continue
		}
		if _, onIdentity := schema.IdentitySchema.Attributes["subscription_id"]; !onIdentity {
			t.Errorf("%s: subscription_id is no longer an identity attribute; re-read this test's premise", typ)
		}
		if _, onBlock := schema.Block.Attributes["subscription_id"]; onBlock {
			t.Errorf("%s: subscription_id is now a resource argument; re-read this test's premise", typ)
		}
		why := SelectedLocatedRefusal(typ, schemas)
		if !strings.Contains(why, "a record cannot hold the whole identity of a "+typ) {
			t.Errorf("%s: want the unrecordable refusal, got %q", typ, why)
		}
	}
}
