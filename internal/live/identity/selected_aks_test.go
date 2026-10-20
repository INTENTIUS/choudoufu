// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"testing"
)

// TestSelectedLocatedAKSRetaggedTypes pins that an operator can move the
// existing resources AKS retags onto the record rung. Microsoft's "Use
// Azure tags in AKS" (https://learn.microsoft.com/en-us/azure/aks/use-tags)
// assigns the cluster's tags to its route table, public IP and virtual
// network, overwriting the set those resources carried, so their
// tofu-estate/tofu-address are replaced by the cluster's. strict { markers
// "record" { types = [...] } } is the documented way to hold such a
// resource's identity in the record store instead.
//
// Until GitHub issue #2135 it was refused for all three, and for every one
// of the 196 azurerm types with an identity schema: each schema requires
// subscription_id, which is not a resource argument (azurerm takes it from
// the provider block), and [LocatedIdentityPlanFor] recorded a composite
// identity only when every required component was a top-level attribute
// of the block. The maintainer's ruling on #2135: the record reads it from
// the provider block, as the identity route does. So the plan carries it
// as a provider component ([LocatedIdentityPlan.ProviderComponents]) and
// the rest as components read off the applied object.
//
// Read against the committed 5.10.0 identity extract, so a provider bump
// that changes the premise fails here and says so.
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
		if why := SelectedLocatedRefusal(typ, schemas); why != "" {
			t.Errorf("%s: the selection is refused: %s", typ, why)
		}
		plan, recordable := LocatedIdentityPlanFor(typ, schema)
		if !recordable || !plan.Composite() {
			t.Fatalf("%s: plan %+v recordable %v, want a recordable composite", typ, plan, recordable)
		}
		if len(plan.ProviderComponents) != 1 || plan.ProviderComponents["subscription_id"] != CloudSubscriptionID {
			t.Errorf("%s: provider components %v, want subscription_id from %s", typ, plan.ProviderComponents, CloudSubscriptionID)
		}
		got := map[string]bool{}
		for _, c := range plan.Components {
			got[c] = true
		}
		if !got["name"] || !got["resource_group_name"] || got["subscription_id"] {
			t.Errorf("%s: components read off the object %v, want name and resource_group_name and not subscription_id", typ, plan.Components)
		}
	}
}

// TestSelectedLocatedEveryAzurermIdentitySchemaType widens the test above to
// the whole extract: no azurerm identity-schema type is refused the
// selection because of subscription_id any more. A type is still refused
// when its identity schema names another attribute its block does not
// declare (azurerm_dns_cname_record's dns_zone_name and record_type, whose
// arguments are zone_name and no argument at all): the applied object does
// not carry that value under that name either, so a record read off it
// would be partial, and that refusal is the record's own rule, not #2135's.
func TestSelectedLocatedEveryAzurermIdentitySchemaType(t *testing.T) {
	ex, schemas := loadAzurermIdentityExtract(t)
	selectable, otherAttr := 0, 0
	for typ, ty := range ex.Types {
		why := SelectedLocatedRefusal(typ, schemas)
		missing := ""
		for _, attr := range ty.Identity {
			if _, onBlock := ty.Arguments[attr]; !onBlock && attr != "subscription_id" {
				missing = attr
				break
			}
		}
		switch {
		case why == "":
			selectable++
			if missing != "" {
				t.Errorf("%s: selectable although its identity attribute %s is not on the block", typ, missing)
			}
		case missing != "":
			otherAttr++
		default:
			t.Errorf("%s: refused with every identity attribute but subscription_id on the block: %s", typ, why)
		}
	}
	if selectable == 0 {
		t.Fatal("no azurerm identity-schema type is selectable")
	}
	t.Logf("%d of %d azurerm identity-schema types are selectable for markers \"record\"; %d are refused for an identity attribute other than subscription_id that the block does not declare", selectable, len(ex.Types), otherAttr)
}
