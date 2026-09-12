// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the Kubernetes half of the stamp (GitHub issue #1073, under
// #1016's ruling): the same read-and-rewrite tags.go performs on an AWS
// resource's tags map, performed on a Kubernetes resource's
// metadata[0].labels. Before it existed, ratify.go classed every
// hashicorp/kubernetes type as UNTAGGABLE - "has no tags argument in the
// provider's schema" - because [taggable] was the only carrier it knew,
// and a migration of a Kubernetes estate wrote no marker at all; the next
// plan then proposed every label write itself (marker repair), and the
// kubernetes lane's first estate (reference-k8s, #1067) failed its migrate
// stage on exactly that line.
//
// What differs from the tag path, and why, is [markers.LabelSurface]'s
// whole doc comment: the marker is ONE label, tofu-estate, because the
// object's own group, kind, namespace and name are the join key back to
// configuration; there is no tofu-address, no continuation label and no
// tofu-slot, so nothing here escapes or splits an address, and the slot
// pass (slot.go) never settles a label-surface member. An estate name that
// is not a legal label value is refused rather than written, the same
// refusal projection's stampedMetadata makes at apply time.

// labelSurface reports whether a resource type carries its marker as a
// Kubernetes label. It is [markers.LabelSurface] and nothing else, for
// the reason [taggable] is [markers.Taggable] and nothing else.
func labelSurface(block *configschema.Block) bool {
	_, ok := markers.LabelSurface(block)
	return ok
}

// labelsFromObject reads metadata[0].labels off a live object of a
// label-surface type. The second return distinguishes "no labels map this
// pass can read" from "labelled with nothing".
func labelsFromObject(schema providers.Schema, obj cty.Value) (map[string]string, bool) {
	if schema.Block == nil || !labelSurface(schema.Block) {
		return nil, false
	}
	return markers.LabelsOf(obj)
}

// withLabels returns the object with its metadata[0].labels replaced,
// every other attribute of the metadata block and of the object carried
// across untouched: the sibling of [withTags] for the label shape.
func withLabels(block *configschema.Block, obj cty.Value, labels map[string]string) (cty.Value, error) {
	nested, ok := block.BlockTypes[markers.LabelSurfaceBlock]
	if !ok || nested == nil {
		return cty.NilVal, fmt.Errorf("no %s block in the schema", markers.LabelSurfaceBlock)
	}
	attr, ok := nested.Block.Attributes[markers.LabelSurfaceAttr]
	if !ok || attr == nil {
		return cty.NilVal, fmt.Errorf("no %s attribute in the %s block", markers.LabelSurfaceAttr, markers.LabelSurfaceBlock)
	}
	meta := obj.GetAttr(markers.LabelSurfaceBlock)
	if meta.IsMarked() {
		// A marked metadata block is never read (internal/live/marksafe):
		// the live object came off the provider unmarked, so a mark here
		// is a bug upstream of this write, and refusing is the safe answer.
		return cty.NilVal, fmt.Errorf("the live object's %s block is marked", markers.LabelSurfaceBlock)
	}
	if meta.IsNull() || !meta.IsKnown() || !meta.CanIterateElements() || meta.LengthInt() != 1 {
		return cty.NilVal, fmt.Errorf("the live object's %s block is not exactly one element", markers.LabelSurfaceBlock)
	}
	it := meta.ElementIterator()
	it.Next()
	_, elem := it.Element()
	if elem.IsMarked() {
		return cty.NilVal, fmt.Errorf("the live object's %s element is marked", markers.LabelSurfaceBlock)
	}
	if elem.IsNull() || !elem.IsKnown() || !elem.Type().IsObjectType() {
		return cty.NilVal, fmt.Errorf("the live object's %s element is not an object", markers.LabelSurfaceBlock)
	}

	var labelVal cty.Value
	if len(labels) == 0 {
		labelVal = cty.MapValEmpty(cty.String)
	} else {
		vals := make(map[string]cty.Value, len(labels))
		for k, v := range labels {
			vals[k] = cty.StringVal(v)
		}
		labelVal = cty.MapVal(vals)
	}
	converted, err := convert.Convert(labelVal, attr.Type)
	if err != nil {
		return cty.NilVal, err
	}

	elemAttrs := elem.AsValueMap()
	if elemAttrs == nil {
		elemAttrs = map[string]cty.Value{}
	}
	elemAttrs[markers.LabelSurfaceAttr] = converted
	newMeta := cty.ListVal([]cty.Value{cty.ObjectVal(elemAttrs)})

	vals := make(map[string]cty.Value, len(block.Attributes)+len(block.BlockTypes))
	for name := range block.Attributes {
		vals[name] = obj.GetAttr(name)
	}
	for name := range block.BlockTypes {
		vals[name] = obj.GetAttr(name)
	}
	vals[markers.LabelSurfaceBlock] = newMeta
	return cty.ObjectVal(vals), nil
}

// changedOutsideLabels is [changedOutsideTags] for the label shape: every
// top-level argument but the metadata block is compared whole, and inside
// the metadata block every attribute but labels is compared, so a plan
// that would also rename the object, move it between namespaces or rewrite
// its annotations is caught while the one map this write exists to change
// is not.
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
		return out
	}
	for _, c := range changedAttrs(&nested.Block, priorElem, plannedElem, map[string]bool{markers.LabelSurfaceAttr: true}) {
		out = append(out, markers.LabelSurfaceBlock+"."+c)
	}
	return out
}

// notALabelsOnlyPlan is [notATagsOnlyPlan] for the label shape: the same
// three refusals, the third judged by [changedOutsideLabels].
func notALabelsOnlyPlan(block *configschema.Block, prior cty.Value, typeName string, resp providers.PlanResourceChangeResponse) string {
	if resp.Diagnostics.HasErrors() {
		return fmt.Sprintf("The provider failed while planning the label write: %s. Nothing was written.", resp.Diagnostics.Err())
	}
	if len(resp.RequiresReplace) > 0 {
		return replacementRefusal(typeName, resp)
	}
	if resp.PlannedState == cty.NilVal || resp.PlannedState.IsNull() {
		return "Planning the label write produced no object at all. This is a provider bug; nothing was written."
	}
	if extra := changedOutsideLabels(block, prior, resp.PlannedState); len(extra) > 0 {
		return fmt.Sprintf("Stamping this %s would also change %s. Approve is a labels-only write on a Kubernetes resource; nothing was written. Run live-plan to see what else has drifted and resolve that first.", typeName, strings.Join(extra, ", "))
	}
	return ""
}

// approveLabel is [approveOne] for a label-surface resource: read the
// labels the object already carries, refuse what must be refused, and
// write tofu-estate into metadata[0].labels through the same plan-then-
// apply the tag write uses, judged by [notALabelsOnlyPlan].
func approveLabel(ctx context.Context, estate string, addr addrs.AbsResourceInstance, e *eligible) StampOutcome {
	out := StampOutcome{Addr: addr, TypeName: e.typeName}

	if !markers.ValidLabelValue(estate) {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("%s cannot carry this estate's ownership marker: %s. Nothing was written.", e.typeName, markers.NotALabelValue(estate))
		return out
	}

	labels, ok := labelsFromObject(e.schema, e.applied)
	if !ok {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("%s has no settable %s.%s map this run can read. Nothing was written.", e.typeName, markers.LabelSurfaceBlock, markers.LabelSurfaceAttr)
		return out
	}

	switch got := labels[markers.TagEstate]; {
	case got == estate:
		out.Outcome = OutcomeAlreadyStamped
		out.Detail = "Already carries this estate's label; nothing written."
		return out
	case got != "":
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("Carries the label tofu-estate = %q, owned by another estate. A migration never adopts another estate's object; nothing was written.", got)
		return out
	}

	desiredLabels := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		desiredLabels[k] = v
	}
	desiredLabels[markers.TagEstate] = estate
	desired, err := withLabels(e.schema.Block, e.applied, desiredLabels)
	if err != nil {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The labels of this %s could not be replaced: %s.", e.typeName, err)
		return out
	}

	newState, refusal := planAndApplyMarker(ctx, e, desired, func(resp providers.PlanResourceChangeResponse) string {
		return notALabelsOnlyPlan(e.schema.Block, e.applied, e.typeName, resp)
	}, "labels")
	if refusal != "" {
		out.Outcome = OutcomeFailed
		out.Detail = refusal
		return out
	}

	out.Outcome = OutcomeStamped
	out.Detail = "Wrote the tofu-estate label. The Kubernetes marker carries no address: the object is re-bound by its namespace and name."
	if got, readOK := labelsFromObject(e.schema, newState); !readOK || got[markers.TagEstate] != estate {
		out.Detail = "The write reported no error, but the object read back afterwards does not carry the tofu-estate label. Verify with kubectl before relying on this."
	}
	return out
}

// planAndApplyMarker is the plan-then-apply both marker writes share: each
// synthetic configuration is planned in turn, the first whose plan
// notClean accepts is applied, and the object the provider returns is the
// result. The refusal returned is notClean's for the LAST candidate when
// none is clean, or the provider's own apply error, or "".
func planAndApplyMarker(ctx context.Context, e *eligible, desired cty.Value, notClean func(providers.PlanResourceChangeResponse) string, what string) (cty.Value, string) {
	var (
		configVal cty.Value
		planResp  providers.PlanResourceChangeResponse
		refusal   string
	)
	for _, candidate := range syntheticConfigs(e.schema.Block, desired) {
		resp := e.provider.PlanResourceChange(ctx, providers.PlanResourceChangeRequest{
			TypeName:         e.typeName,
			PriorState:       e.applied,
			ProposedNewState: objchange.ProposedNew(e.schema.Block, e.applied, candidate),
			Config:           candidate,
			PriorPrivate:     e.private,
			ProviderMeta:     cty.NullVal(cty.DynamicPseudoType),
			PriorIdentity:    e.identity,
		})
		if why := notClean(resp); why != "" {
			refusal = why
			continue
		}
		configVal, planResp, refusal = candidate, resp, ""
		break
	}
	if refusal != "" {
		return cty.NilVal, refusal
	}
	applyResp := e.provider.ApplyResourceChange(ctx, providers.ApplyResourceChangeRequest{
		TypeName:        e.typeName,
		PriorState:      e.applied,
		PlannedState:    planResp.PlannedState,
		Config:          configVal,
		PlannedPrivate:  planResp.PlannedPrivate,
		ProviderMeta:    cty.NullVal(cty.DynamicPseudoType),
		PlannedIdentity: planResp.PlannedIdentity,
	})
	if applyResp.Diagnostics.HasErrors() {
		return cty.NilVal, fmt.Sprintf("The provider failed while writing the %s: %s. The write may have partly landed; read the object's markers before deciding what to do next.", what, applyResp.Diagnostics.Err())
	}
	return applyResp.NewState, ""
}
