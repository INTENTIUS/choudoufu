// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the data-plane half of GitHub issue #2205's marker
// discovery. Azure Resource Graph indexes ARM resources; a Key Vault key,
// secret or certificate is a data-plane object inside a vault and is not in
// it. Their tags are real (the three azurerm types take the ARM tags map),
// so the stamp writes the marker, but finding the object again needs the
// vault's own listing.
//
// The listing is behind [DataPlaneLister] so everything here is tested with
// fakes. No implementation that talks to Azure is wired: a real listing
// against a vault is not run, needs the maintainer.

// SourceDataPlane marks a [TypeScan] the data-plane leg made.
const SourceDataPlane EnumerationSource = "DATA_PLANE"

// DataPlaneKind names a Key Vault collection and the azurerm type whose
// objects it holds.
type DataPlaneKind string

const (
	DataPlaneKeys         DataPlaneKind = "keys"
	DataPlaneSecrets      DataPlaneKind = "secrets"
	DataPlaneCertificates DataPlaneKind = "certificates"
)

// TFType is the azurerm type of the collection's objects.
func (k DataPlaneKind) TFType() string {
	switch k {
	case DataPlaneKeys:
		return "azurerm_key_vault_key"
	case DataPlaneSecrets:
		return "azurerm_key_vault_secret"
	case DataPlaneCertificates:
		return "azurerm_key_vault_certificate"
	}
	return ""
}

// DataPlaneScope is one collection to list: the vault's base URL (built
// from the vault name and the provider block's environment suffix,
// identity.CloudKeyVaultDNSSuffix) and the collection.
type DataPlaneScope struct {
	BaseURL string
	Kind    DataPlaneKind
}

// DataPlaneItem is one listed object. ID is the item's versioned id, the
// string azurerm imports the object by.
type DataPlaneItem struct {
	ID   string
	Name string
	Tags map[string]string
}

// DataPlaneLister lists one collection, every page read, or returns an
// error and no items.
type DataPlaneLister interface {
	List(ctx context.Context, scope DataPlaneScope) ([]DataPlaneItem, error)
}

// DataPlaneSweep is the leg listing [DataPlaneScope]s through a
// [DataPlaneLister] and filing every object carrying this estate's marker as
// Resource Graph hits are filed.
type DataPlaneSweep struct {
	Lister DataPlaneLister
	Scopes []DataPlaneScope
}

// Leg is [substrate.SweepDataPlane].
func (DataPlaneSweep) Leg() substrate.Sweep { return substrate.SweepDataPlane }

// Sweep runs the leg. A collection that cannot be listed is a
// [SweepGapListFailed] gap and is left out of SweepCovered, so an empty
// answer is never read as "nothing there".
func (g DataPlaneSweep) Sweep(ctx context.Context, in *SweepInput) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	req, res, decl := in.Request, in.Result, in.decl
	covered := map[string]bool{}
	failed := map[string]bool{}
	for _, scope := range g.Scopes {
		tfType := scope.Kind.TFType()
		if tfType == "" || g.Lister == nil {
			continue
		}
		items, err := g.Lister.List(ctx, scope)
		if err != nil {
			failed[tfType] = true
			res.SweepGaps = append(res.SweepGaps, SweepGap{
				TypeName: tfType,
				Reason:   SweepGapListFailed,
				Detail:   fmt.Sprintf("%s of %s was not listed: %s", scope.Kind, scope.BaseURL, err),
			})
			continue
		}
		var mine int
		for _, it := range items {
			tags := markers.CanonicalARMMarkers(it.Tags)
			if tags[TagEstate] != req.Estate {
				continue
			}
			mine++
			typeName := tfType
			raw, _ := GatherAddress(tags)
			if addr, ok := UnescapeAddress(EscapeAddress(raw)); ok && ValidMarkerAddress(EscapeAddress(raw)) {
				typeName = addr.Resource.Resource.Type
			}
			diags = diags.Append(fileCandidate(ctx, req, decl, typeName, taggedCandidate{
				importID:     it.ID,
				identityAttr: "id",
				tags:         tags,
				objectType:   tfType,
			}, res, " (via the "+strings.TrimRight(scope.BaseURL, "/")+" data plane)", true))
		}
		res.Scans = append(res.Scans, TypeScan{
			TypeName:  tfType,
			Filtering: FilterClientSide,
			Scope:     ScopeEstate,
			Sweep:     true,
			Source:    SourceDataPlane,
			Listed:    mine,
		})
		covered[tfType] = true
	}
	for t := range failed {
		delete(covered, t)
	}
	for _, t := range []string{"azurerm_key_vault_certificate", "azurerm_key_vault_key", "azurerm_key_vault_secret"} {
		if covered[t] {
			res.SweepCovered = append(res.SweepCovered, t)
		}
	}
	return diags
}
