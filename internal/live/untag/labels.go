// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package untag

import (
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the label half of the release (GitHub issue #1644): the
// same labels-only plan-then-apply internal/live/liveimport's labels.go
// writes a migration's label through and internal/live/mv's label.go moves
// one between estates with, run here to take the key off. The object is
// rebuilt by [markers.WithLabels], the one rewrite of metadata[0].labels
// every writer shares, and the plan is judged by [notALabelsOnlyPlan], the
// label twin of [notATagsOnlyPlan]. Reached only when releaseOne's surface
// dispatch read the label surface off the schema.

// changedOutsideLabels is [changedOutsideTags] for the label shape: every
// top-level argument but the metadata block is compared whole, and inside
// the metadata block every attribute but labels is compared, so a plan
// that would also rename the object, move it between namespaces or rewrite
// its annotations is caught while the one map this write exists to change
// is not. liveimport's and mv's copies make the same comparison.
func changedOutsideLabels(block *configschema.Block, prior, planned cty.Value) []string {
	out := changedAttrs(block, prior, planned, map[string]bool{markers.LabelSurfaceBlock: true})
	nested, ok := block.BlockTypes[markers.LabelSurfaceBlock]
	if !ok || nested == nil || prior == cty.NilVal || prior.IsNull() || planned == cty.NilVal || planned.IsNull() {
		return out
	}
	one := func(v cty.Value) (cty.Value, bool) {
		if v.IsMarked() {
			return cty.NilVal, false
		}
		if v.IsNull() || !v.IsKnown() || !v.CanIterateElements() || v.LengthInt() != 1 {
			return cty.NilVal, false
		}
		it := v.ElementIterator()
		it.Next()
		_, elem := it.Element()
		return elem, true
	}
	priorElem, pok := one(prior.GetAttr(markers.LabelSurfaceBlock))
	plannedElem, nok := one(planned.GetAttr(markers.LabelSurfaceBlock))
	if !pok || !nok {
		// A metadata block that is no longer exactly one element is itself
		// a change outside the labels, and the top-level comparison above
		// skipped it; name it rather than let it through.
		return append(out, fmt.Sprintf("%s (%s -> %s)", markers.LabelSurfaceBlock,
			shortValue(prior.GetAttr(markers.LabelSurfaceBlock)), shortValue(planned.GetAttr(markers.LabelSurfaceBlock))))
	}
	for _, c := range changedAttrs(&nested.Block, priorElem, plannedElem, map[string]bool{markers.LabelSurfaceAttr: true}) {
		out = append(out, markers.LabelSurfaceBlock+"."+c)
	}
	return out
}

// notALabelsOnlyPlan is [notATagsOnlyPlan] for the label shape: the same
// four refusals, the last judged by [changedOutsideLabels].
func notALabelsOnlyPlan(block *configschema.Block, prior cty.Value, typeName, key string, resp providers.PlanResourceChangeResponse) string {
	switch {
	case resp.Diagnostics.HasErrors():
		return fmt.Sprintf("The provider failed while planning the label release: %s. Nothing was changed.", resp.Diagnostics.Err())
	case len(resp.RequiresReplace) > 0:
		return fmt.Sprintf("Releasing the %q label from this %s would require replacing it, according to the provider. An untag never destroys or replaces anything; nothing was changed.", key, typeName)
	case resp.PlannedState == cty.NilVal || resp.PlannedState.IsNull():
		return "Planning the label release produced no object at all. This is a provider bug; nothing was changed."
	}
	if extra := changedOutsideLabels(block, prior, resp.PlannedState); len(extra) > 0 {
		return fmt.Sprintf("Releasing the %q label from this %s would also change %s. An untag is a labels-only write on a Kubernetes object; nothing was changed. Run a plan to see what else has drifted and resolve that first.", key, typeName, strings.Join(extra, ", "))
	}
	return ""
}
