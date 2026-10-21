// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// SummaryUntaggableUnrecorded is the warning writeBackRecordEnvelopes raises
// for an applied instance of a type with no marker surface whose identity
// [LocatedRecordFromIn] could not record, and that no earlier record holds
// (GitHub issue #2141).
//
// For such a type the record is the only thing that remembers the estate
// owns the object: there is no tag, label or ARM tag on it to sweep for.
// With the record present, removing the block plans the destroy from it
// (#2140). With none, a later removal leaves the object running outside
// every estate and nothing says so, which is the silent loss this warning
// puts on screen at the apply that causes it. A warning and not an error,
// by the maintainer's ruling on #2141 (2026-10-20): the object exists and
// stopping the apply would not record it either.
const SummaryUntaggableUnrecorded = "No record kept for a resource with no ownership marker"

// unrecordedInstance is one instance [SummaryUntaggableUnrecorded] names,
// with the sensitive identity attribute that was the reason, when that was
// the reason ([identity.SensitiveIdentityAttr]).
type unrecordedInstance struct {
	addr   string
	secret string
}

// unmarkedUnrecorded reports whether an instance of a type with this schema,
// served by providerType, has no marker surface at all, so that a record is
// its only ownership carrier. The family's own answer
// ([substrate.SurfaceOf]), so an AWS type is asked about tags, an azurerm
// or azapi type about ARM tags and a Kubernetes type about labels and
// manifest metadata, and a provider no family claims gets the compatible
// default every other seam gives it.
func unmarkedUnrecorded(providerType string, schema providers.Schema) bool {
	if schema.Block == nil {
		return false
	}
	_, carries := substrate.SurfaceOf(providerType, schema.Block)
	return !carries
}

// recordHoldsIdentity reports whether the record store already holds an
// identity for addr - one an earlier apply or live-import wrote, which
// writeBackRecordEnvelopes leaves alone when it cannot re-derive one, and
// which still finds the object when its block is removed. A read that fails
// answers false: the warning is then raised on an object that may in fact be
// recorded, which costs a line, where answering true would hide the one case
// the warning exists for.
func recordHoldsIdentity(ctx context.Context, store *RecordStore, addr addrs.AbsResourceInstance) bool {
	if store == nil {
		return false
	}
	_, _, _, found, err := store.GetIdentity(ctx, addr)
	return err == nil && found
}

// unrecordedUnmarkedDiags turns writeBackRecordEnvelopes' per-type
// collection into one [SummaryUntaggableUnrecorded] warning per type, in
// type order so the output is stable.
func unrecordedUnmarkedDiags(byType map[string][]unrecordedInstance) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, typeName := range types {
		insts := byType[typeName]
		addrList := make([]string, 0, len(insts))
		for _, inst := range insts {
			addrList = append(addrList, inst.addr)
		}
		sort.Strings(addrList)
		why := "this fork cannot hold its identity in full in a record"
		if secret := insts[0].secret; secret != "" {
			why = fmt.Sprintf("the identity a record would hold is its %q attribute, which the provider marks sensitive, and the record store does not hold secret material", secret)
		}
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryUntaggableUnrecorded, fmt.Sprintf(
			"%s carries no ownership marker, so a record is the only thing that remembers this estate owns it, and no record was written for %s: %s. "+
				"The resource was applied and exists. If its block is later removed, no plan will find it to destroy: it will be left running outside every estate, and nothing will say so. "+
				"Destroy it (tofu destroy -target) before removing its block, or remove it by hand afterwards.",
			typeName, strings.Join(addrList, ", "), why,
		)))
	}
	return diags
}
