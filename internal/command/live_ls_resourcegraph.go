// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"strings"

	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/azsweep"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// liveLsResourceGraphSource is [views.LiveLsItem.Source] for a resource the
// Azure listing found.
const liveLsResourceGraphSource = "resource-graph"

// liveLsResourceGraph lists the estate's Azure resources (GitHub issue
// #1120): one Resource Graph query per subscription, through the same legs
// live-plan builds ([projectionProviders.resourceGraphLeg]), so each
// subscription is queried once, with the credential of the provider block
// that names it, and the inventory reads the subscriptions the plan does.
// A subscription that cannot be queried is a warning under the sweep's own
// summary, never an error: the listing is what could be read.
//
// Each resource is an item under its ARM id. Its address is its
// tofu-address tag, read case-insensitively (markers.CanonicalARMMarkers),
// or the declared instance whose import id is its ARM id, compared
// case-insensitively, when the tag names no declared one. A controller-held
// resource is listed with its holder, as it is on the other substrates.
func (c *LiveLsCommand) liveLsResourceGraph(ctx context.Context, estate string, config *configs.Config, provs *projectionProviders, resolutions []identity.Resolution) ([]views.LiveLsItem, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	declared := map[string]string{}
	byID := map[string]string{}
	for _, r := range resolutions {
		declared[r.Addr.String()] = r.Addr.Resource.Resource.Type
		if _, ok := discovery.ARMSubscription(r.ImportID); ok && !r.Undeclared {
			byID[strings.ToLower(r.ImportID)] = r.Addr.String()
		}
	}

	var items []views.LiveLsItem
	for _, addr := range liveManagedResourceProviders(config) {
		sub, ok := substrate.ForProvider(addr.Provider.Type)
		if !ok || sub.Sweep() != substrate.SweepResourceGraph {
			continue
		}
		leg, legDiags := provs.resourceGraphLeg(ctx, sub, addr)
		diags = diags.Append(legDiags)
		g, ok := leg.(discovery.ResourceGraphSweep)
		if !ok {
			continue
		}
		for _, sr := range azsweep.Sweep(ctx, g.Coverage, g.SweptSubscriptions(resolutions), estate) {
			if sr.Err != nil {
				diags = diags.Append(discovery.ResourceGraphListingDiag(estate, sr))
				continue
			}
			for _, r := range sr.Resources {
				items = append(items, liveLsResourceGraphItem(r, declared, byID))
			}
		}
	}
	return items, diags
}

// liveLsResourceGraphItem is one listed resource as an item.
func liveLsResourceGraphItem(r azsweep.Resource, declared map[string]string, byID map[string]string) views.LiveLsItem {
	tags := markers.CanonicalARMMarkers(r.Tags)
	item := views.LiveLsItem{
		ID:     r.ID,
		Type:   r.Type,
		Slot:   tags[markers.TagSlot],
		Source: liveLsResourceGraphSource,
		Tags:   r.Tags,
	}
	if raw, corrupt := markers.GatherAddress(tags); !corrupt && raw != "" {
		if a, ok := markers.UnescapeAddress(markers.EscapeAddress(raw)); ok {
			item.Address = a.String()
			item.Type = a.Resource.Resource.Type
		}
	}
	if hold, held := substrate.ControllerHeld(substrate.HoldEvidence{ManagedBy: r.ManagedBy, ARMTags: r.Tags}); held {
		// Held is listed with its holder and never under a block by its
		// marker alone: AKS copies the cluster's marker onto the node
		// resource group it makes.
		item.HeldBy = hold.HeldBy
		item.Address = ""
		return item
	}
	if _, ok := declared[item.Address]; ok && item.Address != "" {
		item.Declared = true
		return item
	}
	if a, ok := byID[strings.ToLower(r.ID)]; ok {
		item.Address, item.Declared, item.Type = a, true, declared[a]
	}
	return item
}
