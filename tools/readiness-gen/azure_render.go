// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
)

// siteAzure is the Azure section's site-rendered summary (GitHub issue
// #2119): each provider's tier-by-status cross tab, the fence each tier
// gets by where its types live, and the azurerm types short of in-contract
// with their reason. azapi's 3,397 rows are all in contract and stay in
// live/readiness.json rather than on the page.
type siteAzure struct {
	Statuses []string           `json:"statuses"`
	Azurerm  siteAzureProvider  `json:"azurerm"`
	Azapi    siteAzureProvider  `json:"azapi"`
	Fence    []siteAzureFence   `json:"fence"`
	Types    []siteType         `json:"types"`
	Scopes   []siteAzureScopeID `json:"scopes"`
}

type siteAzureProvider struct {
	Provider         string     `json:"provider"`
	Tiers            []siteTier `json:"tiers"`
	ColumnTotals     []int      `json:"column_totals"`
	Total            int        `json:"total"`
	VersionDependent int        `json:"version_dependent,omitempty"`
}

// siteAzureFence is one azurerm tier's count at each scope, and what the
// estate's fence (live/MARKERS.md, "Granting an Azure estate") reaches at
// each.
type siteAzureFence struct {
	Tier   string `json:"tier"`
	Counts []int  `json:"counts"`
	Fence  string `json:"fence"`
}

// siteAzureScopeID is one column of the fence table.
type siteAzureScopeID struct {
	Scope string `json:"scope"`
	Fence string `json:"fence"`
}

// azureScopeOrder is the fence table's column order: inside a group first,
// then each scope a group's role assignment never reaches.
var azureScopeOrder = []string{ARMScopeResourceGroup, ARMScopeFromArgument, ARMScopeSubscription, ARMScopeTenant, ARMScopeUnknown}

// azureScopeFence says what a resource-group role assignment reaches at
// each scope. It is the RBAC half of the fence only; the tag half is
// [azureTierFence]'s.
var azureScopeFence = map[string]string{
	ARMScopeResourceGroup: "the estate's group RBAC covers it",
	ARMScopeFromArgument:  "covered when the argument names something inside one of the estate's groups, not otherwise",
	ARMScopeSubscription:  "outside every group: the grant does not reach it",
	ARMScopeTenant:        "outside every group: the grant does not reach it",
	ARMScopeUnknown:       "no id template, so where it lives is not known",
}

// azureTierFence is the tag half of the fence for a tier: what the Azure
// Policy pair in live/azure/estate-boundary.json adds on top of the
// group's RBAC.
var azureTierFence = map[string]string{
	TierMarkerCarried:      "group RBAC, plus the Policy deny on a write lacking or changing tofu-estate and the denyAction on deleting a tagged resource",
	TierDeclarationCarried: "group RBAC only: Policy's Indexed mode never evaluates an untaggable type, and deleting the parent removes it unseen",
	TierRecordCarried:      "group RBAC only, and the type is not admitted",
	TierExcludedByDesign:   "not admitted",
}

func buildSiteAzure(a AzureSubstrate) siteAzure {
	d := siteAzure{
		Statuses: append([]string(nil), statusOrder...),
		Azurerm:  azureCrossTab(a.Azurerm),
		Azapi:    azureCrossTab(a.Azapi),
		Types:    []siteType{},
	}
	for _, r := range a.Azapi.Types {
		if r.Facts.VersionDependent {
			d.Azapi.VersionDependent++
		}
	}
	for _, s := range azureScopeOrder {
		d.Scopes = append(d.Scopes, siteAzureScopeID{Scope: s, Fence: azureScopeFence[s]})
	}
	byScope := map[crossKey]int{}
	for _, r := range a.Azurerm.Types {
		byScope[crossKey{Tier: r.Tier, Status: r.Facts.ARMScope}]++
		if r.Status != StatusInContract {
			d.Types = append(d.Types, siteType{Type: r.Type, Tier: r.Tier, Status: r.Status, Reason: azureReasonFor(r)})
		}
	}
	for _, tier := range tierOrder {
		row := siteAzureFence{Tier: tier, Fence: azureTierFence[tier]}
		for _, s := range azureScopeOrder {
			row.Counts = append(row.Counts, byScope[crossKey{Tier: tier, Status: s}])
		}
		d.Fence = append(d.Fence, row)
	}
	return d
}

func azureCrossTab(s Substrate) siteAzureProvider {
	cross := map[crossKey]int{}
	for _, r := range s.Types {
		cross[crossKey{Tier: r.Tier, Status: r.Status}]++
	}
	p := siteAzureProvider{Provider: s.Provider, ColumnTotals: make([]int, len(statusOrder))}
	for _, tier := range tierOrder {
		row := siteTier{Tier: tier}
		for i, st := range statusOrder {
			n := cross[crossKey{Tier: tier, Status: st}]
			row.Counts = append(row.Counts, n)
			row.Total += n
			p.ColumnTotals[i] += n
		}
		p.Total += row.Total
		p.Tiers = append(p.Tiers, row)
	}
	return p
}

// azureReasonFor is [reasonFor] for an azurerm row, whose reasons are about
// live/azurerm-ids.json rather than a ratification batch.
func azureReasonFor(r Row) string {
	switch {
	case r.Status == StatusInContract:
		return ""
	case r.Facts.ARMIDClass == azurermClassWithheld:
		return fmt.Sprintf("its id template is the parent's, or not an object's own, so admission withholds it rather than risk a wrong marker (internal/live/substrate's azurermRowShapeProblem). See [LIMITATIONS.md](%s#unadmitted-type).", limitationsMDURL)
	case r.Facts.SchemaRoute:
		return "no id template was read for it; the provider's identity schema admits it at run time when the schema settles the identity, as it would an unrowed AWS type."
	case r.Status == StatusNeedsEvidence:
		return fmt.Sprintf("no ARM id template could be read from azurerm's id parsers for it (live/azurerm-ids.json's reason), so it is refused as unadmitted. See [LIMITATIONS.md](%s#unadmitted-type).", limitationsMDURL)
	}
	return reasonFor(r)
}
