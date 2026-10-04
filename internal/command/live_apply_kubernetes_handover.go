// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// The apply's half of the stock hand-over (GitHub issue #1863, the second
// of PR #1828's follow-ups to #1191; the plan's half is
// internal/live/projection's fieldgranular_handover.go).
//
// A field-granular instance stock applied owns its fields under the
// provider's default field manager, "Terraform". The live plan reads it
// under that manager when the estate's own owns nothing, and plans an
// update whose only marker change is field_manager, "Terraform" to
// "choudoufu:<estate>". Applied as it stands, that update is a server-side
// apply under the estate's manager of values "Terraform" already holds,
// and server-side apply records equal values as SHARED: "Terraform" would
// keep a claim on every field, and where the plan also changes a value the
// API server refuses the write with a conflict naming "Terraform".
//
// So after approval and before the first change, ownership of exactly the
// fields the planned write names moves from "Terraform" to the estate's
// Apply entry on the live object (kubesweep.TransferFieldOwnership), the
// same managedFields rewrite the marker patch already makes for its labels
// (#1704). The provider's write then lands on fields the estate owns
// alone, which is what force would have done against "Terraform" and
// nothing else: no other manager's entry is touched, so a field another
// estate also owns stays that estate's, and the plan-time boundary has
// already named it.
//
// A hand-over that fails is a warning. The apply goes ahead and the fields
// end up shared, which is where every migrated estate stood before this
// existed, and the next plan reads them under the estate's manager.

// fieldGranularHandovers reads the planned hand-overs out of plan: every
// update of a field-granular instance whose prior field_manager is a
// stock one (not an estate's) and whose planned one is the estate's, through a provider
// configuration the sweep holds a cluster client for. Nil when there are
// none, which keeps BeforeApply from asking any cluster anything.
func fieldGranularHandovers(sweepers map[string]kubesweep.Sweeper, plan *plans.Plan, schemas *tofu.Schemas, estate string) map[string][]discovery.FieldGranularWrite {
	if estate == "" || plan == nil || plan.Changes == nil || schemas == nil || len(sweepers) == 0 {
		return nil
	}
	want := markers.FieldManagerFor(estate)
	var out map[string][]discovery.FieldGranularWrite
	for _, rc := range plan.Changes.Resources {
		if rc == nil || rc.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode || rc.Action != plans.Update {
			continue
		}
		key := providerCacheKey(rc.ProviderAddr)
		if sweepers[key] == nil {
			continue
		}
		schema, _ := schemas.ResourceTypeConfig(rc.ProviderAddr.Provider, rc.Addr.Resource.Resource.Mode, rc.Addr.Resource.Resource.Type)
		if schema == nil {
			continue
		}
		if _, ok := substrate.FieldGranularShape(schema.Block); !ok {
			continue
		}
		change, err := rc.Decode(schema)
		if err != nil {
			continue
		}
		// The prior names a non-estate manager only when the projection
		// read the instance under the stock manager its record says it
		// was migrated from (projection's fieldgranular_handover.go); a
		// never-migrated block's prior is absent and its change a create.
		from := fieldManagerOf(change.Before)
		if _, isEstate := markers.EstateOfFieldManager(from); from == "" || isEstate || fieldManagerOf(change.After) != want {
			continue
		}
		w, ok := plannedFieldGranularWrite(rc, schema)
		if !ok || len(w.Writes) == 0 {
			continue
		}
		w.HandoverFrom = from
		if out == nil {
			out = map[string][]discovery.FieldGranularWrite{}
		}
		out[key] = append(out[key], w)
	}
	return out
}

// fieldManagerOf is a field-granular value's field_manager, or "".
func fieldManagerOf(v cty.Value) string {
	v, _ = v.UnmarkDeep()
	return ctyString(v, substrate.FieldManagerAttr)
}

// runFieldGranularHandovers moves each planned hand-over's fields from
// "Terraform" to the estate's manager, in address order per cluster, and
// returns one warning per hand-over that could not be made.
func runFieldGranularHandovers(ctx context.Context, sweepers map[string]kubesweep.Sweeper, handovers map[string][]discovery.FieldGranularWrite, estate string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if len(handovers) == 0 || estate == "" {
		return diags
	}
	to := markers.FieldManagerFor(estate)
	keys := make([]string, 0, len(handovers))
	for k := range handovers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writes := append([]discovery.FieldGranularWrite(nil), handovers[key]...)
		sort.SliceStable(writes, func(i, j int) bool { return writes[i].Addr.String() < writes[j].Addr.String() })
		transferer, _ := sweepers[key].(kubesweep.FieldTransferer)
		for _, w := range writes {
			if transferer == nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, discovery.SummaryFieldHandoverFailed,
					fmt.Sprintf("%s hands the fields it writes on %s from field manager %q to %q, and this run's cluster client cannot move field ownership, so the apply writes them shared with %q.", w.Addr, w.Object, w.HandoverFrom, to, w.HandoverFrom)))
				continue
			}
			if _, err := transferer.TransferFields(ctx, w.Object, w.HandoverFrom, to, w.Writes); err != nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, discovery.SummaryFieldHandoverFailed,
					fmt.Sprintf("%s hands the fields it writes on %s from field manager %q to %q, and moving their ownership before the apply failed: %s. The apply goes ahead; the fields are written shared with %q, and where a planned value differs from the live one the API server's conflict names %q.", w.Addr, w.Object, w.HandoverFrom, to, err, w.HandoverFrom, w.HandoverFrom)))
			}
		}
	}
	return diags
}
