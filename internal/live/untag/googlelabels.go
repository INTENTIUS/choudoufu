// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package untag

import (
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the GCP release (GitHub issue #1119): the tags release, on
// a hashicorp/google resource's top-level labels map.

// withGoogleLabelsKept returns obj with its labels map narrowed to the keys
// keep names. Only the configuration's own labels map is rewritten: keep is
// the object's whole label set less the released key, and a label another
// client set lives in effective_labels, where this write never reaches.
func withGoogleLabelsKept(block *configschema.Block, obj cty.Value, keep map[string]string) (cty.Value, error) {
	if obj.IsMarked() {
		return cty.NilVal, fmt.Errorf("the object is marked, so its labels are not rewritten")
	}
	attr := block.Attributes[markers.GoogleLabelsAttr]
	elems := map[string]cty.Value{}
	if cur := obj.GetAttr(markers.GoogleLabelsAttr); !cur.IsMarked() && !cur.IsNull() && cur.IsKnown() {
		for it := cur.ElementIterator(); it.Next(); {
			k, v := it.Element()
			if _, ok := keep[k.AsString()]; ok {
				elems[k.AsString()] = v
			}
		}
	}
	labelVal := cty.MapValEmpty(cty.String)
	if len(elems) > 0 {
		labelVal = cty.MapVal(elems)
	}
	converted, err := convert.Convert(labelVal, attr.Type)
	if err != nil {
		return cty.NilVal, err
	}
	vals := obj.AsValueMap()
	vals[markers.GoogleLabelsAttr] = converted
	return cty.ObjectVal(vals), nil
}

// notAGoogleLabelsOnlyPlan is [notATagsOnlyPlan] for the GCP labels map.
func notAGoogleLabelsOnlyPlan(block *configschema.Block, prior cty.Value, typeName, key string, resp providers.PlanResourceChangeResponse) string {
	switch {
	case resp.Diagnostics.HasErrors():
		return fmt.Sprintf("The provider failed while planning the label release: %s. Nothing was changed.", resp.Diagnostics.Err())
	case len(resp.RequiresReplace) > 0:
		return fmt.Sprintf("Releasing the %q label from this %s would require replacing it, according to the provider. An untag never destroys or replaces anything; nothing was changed.", key, typeName)
	case resp.PlannedState == cty.NilVal || resp.PlannedState.IsNull():
		return "Planning the label release produced no object at all. This is a provider bug; nothing was changed."
	}
	skip := map[string]bool{markers.GoogleLabelsAttr: true, markers.GoogleTerraformLabelsAttr: true, markers.GoogleEffectiveLabelsAttr: true}
	if extra := changedAttrs(block, prior, resp.PlannedState, skip); len(extra) > 0 {
		return fmt.Sprintf("Releasing the %q label from this %s would also change %s. An untag is a labels-only write; nothing was changed. Run a plan to see what else has drifted and resolve that first.", key, typeName, strings.Join(extra, ", "))
	}
	return ""
}
