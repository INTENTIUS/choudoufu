// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"strings"
)

// A removed azurerm block's record (GitHub issue #2119, #1120 unit 14).
// An untaggable azurerm object carries no marker for the Resource Graph
// sweep to find, so once its block is deleted the estate's record is the
// only thing that still names it, as it is for AWS's untaggable types
// (internal/live/discovery's record-orphan leg). The record holds what the
// projection could read off the applied object: the bare ARM id for a type
// the provider serves no identity schema for, or the identity schema's
// attributes (name, resource_group_name, subscription_id and the parents'
// names, for azurerm_subnet) for one it does. The second shape has no
// string in it, and the leg imports by string, so this rebuilds the ARM id
// from those attributes with the same template admission uses.

// azurermRecordSubscriptionAttr is the identity-schema attribute azurerm
// names the subscription segment with (azurerm_subnet's identity schema at
// 5.10.0 is name, resource_group_name, subscription_id,
// virtual_network_name), which a template component spells as the cloud
// value [azurermCloudSubscription].
const azurermRecordSubscriptionAttr = "subscription_id"

// azurermRecordRow is typeName's row when its id template can be rebuilt
// from a record: one with components, in any class (an identity-schema row
// carries the template its parser formats too), that passes the shape
// guard, and that admission has not withheld.
func azurermRecordRow(typeName string) (azurermIDType, bool) {
	tab, err := loadAzurermIDs()
	if err != nil {
		return azurermIDType{}, false
	}
	row, ok := tab.rows[typeName]
	if !ok || row.Template == "" || len(row.Components) == 0 {
		return azurermIDType{}, false
	}
	if _, withheld := tab.withheld[typeName]; withheld {
		return azurermIDType{}, false
	}
	if azurermRowShapeProblem(row) != "" {
		return azurermIDType{}, false
	}
	return row, true
}

// AzurermRecordable reports whether a record of typeName can be turned back
// into its ARM id: the type has an id template [AzurermRecordImportID]
// reads. It says nothing about whether any one record holds every value.
func AzurermRecordable(typeName string) bool {
	_, ok := azurermRecordRow(typeName)
	return ok
}

// AzurermRecordImportID rebuilds the ARM id of the azurerm object a record
// names from the values it holds, keyed by argument or identity attribute.
// Every segment has to be present: a hole is refused, never filled, because
// an id with a hole in it names a different object or none (HANDOFF's
// safety rule).
func AzurermRecordImportID(typeName string, values map[string]string) (string, bool) {
	row, ok := azurermRecordRow(typeName)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, c := range row.Components {
		switch {
		case c.Cloud == azurermCloudSubscription:
			v := values[azurermRecordSubscriptionAttr]
			if v == "" {
				return "", false
			}
			b.WriteString(c.Literal)
			b.WriteString(v)
		case len(c.Attrs) > 0:
			v := ""
			for _, a := range c.Attrs {
				if values[a] != "" {
					v = values[a]
					break
				}
			}
			if v == "" {
				return "", false
			}
			b.WriteString(c.Literal)
			b.WriteString(v)
		case c.Cloud != "":
			// A cloud value this rule has no record attribute for.
			return "", false
		default:
			b.WriteString(c.Literal)
		}
	}
	return b.String(), true
}
