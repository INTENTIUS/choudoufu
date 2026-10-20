// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// markProviderMintedIfAbsent marks each component whose arguments are all
// Optional and Computed in the type's schema as server-assigned when absent
// (GitHub issue #2119). Seven untaggable azurerm types the family admits at
// 5.10.0 name their object through such an argument and mint a GUID when it
// is left out: azurerm_management_group's create sets
// `groupName = uuid.New().String()` for an empty name, and
// azurerm_role_definition, azurerm_cosmosdb_sql_role_assignment and
// azurerm_cosmosdb_sql_role_definition, azurerm_lighthouse_assignment,
// azurerm_sentinel_alert_rule_anomaly_built_in and
// azurerm_sentinel_watchlist_item do the same. An omitted one is the
// convention AWS's row generator records as ServerAssignedIfAbsent ("If
// omitted, Terraform will assign a random, unique name"), read here from
// the schema rather than from documentation, and it takes AWS's path: the
// record rung when the estate has a record store and the type carries no
// marker, discovery otherwise. Before this the resolver refused the block
// with "Identity argument not set".
//
// A component read off the configuration only (Required, or Optional
// without Computed) is left alone: omitting it is a missing argument, not
// a request for a minted one.
func markProviderMintedIfAbsent(comps []IdentityComponent, block *configschema.Block) {
	if block == nil {
		return
	}
	for i, c := range comps {
		if len(c.Attrs) == 0 {
			continue
		}
		minted := true
		for _, name := range c.Attrs {
			a, ok := block.Attributes[name]
			if !ok || a == nil || a.Required || !a.Optional || !a.Computed {
				minted = false
				break
			}
		}
		comps[i].ServerAssignedIfAbsent = minted
	}
}
