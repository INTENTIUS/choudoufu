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
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the GCP carrier (GitHub issue #1119): the tags-only write of
// [approveOne], onto a hashicorp/google resource's top-level labels map
// (substrate.WriteGoogleLabelsPlan). The judge is the tags write's, with
// the three label maps in place of tags and tags_all, so a plan that would
// replace the object or change anything else is refused in the same words.
// What differs: the address is one encoded label with no continuations
// ([markers.GoogleAddressValue]), and the estate has to be a legal label
// value.

// googleLabelsFromObject reads the marker labels off an object of a
// google-labels surface type. The second return is false for any other
// type.
func googleLabelsFromObject(block *configschema.Block, obj cty.Value) (map[string]string, bool) {
	if _, ok := markers.GoogleLabelSurface(block); !ok {
		return nil, false
	}
	obj, _ = obj.Unmark()
	return markers.GoogleLabelsOf(obj)
}

// withGoogleLabels returns obj with its labels attribute replaced.
// terraform_labels and effective_labels are left as read: they are
// computed, and the provider's own diff recomputes both from labels.
func withGoogleLabels(block *configschema.Block, obj cty.Value, labels map[string]string) (cty.Value, error) {
	if obj.IsMarked() {
		return cty.NilVal, fmt.Errorf("the object is marked, so its labels are not rewritten")
	}
	attr := block.Attributes[markers.GoogleLabelsAttr]
	labelVal := cty.MapValEmpty(cty.String)
	if len(labels) > 0 {
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
	vals := obj.AsValueMap()
	vals[markers.GoogleLabelsAttr] = converted
	return cty.ObjectVal(vals), nil
}

// changedOutsideGoogleLabels is [changedOutsideTags] for the three label
// maps a labels-only write moves.
func changedOutsideGoogleLabels(block *configschema.Block, prior, planned cty.Value) []string {
	return changedAttrs(block, prior, planned, map[string]bool{
		markers.GoogleLabelsAttr:          true,
		markers.GoogleTerraformLabelsAttr: true,
		markers.GoogleEffectiveLabelsAttr: true,
	})
}

// notAGoogleLabelsOnlyPlan is [notATagsOnlyPlan] for the labels write.
func notAGoogleLabelsOnlyPlan(block *configschema.Block, prior cty.Value, typeName string, resp providers.PlanResourceChangeResponse) string {
	if resp.Diagnostics.HasErrors() {
		return fmt.Sprintf("The provider failed while planning the label write: %s. Nothing was written.", resp.Diagnostics.Err())
	}
	if len(resp.RequiresReplace) > 0 {
		return replacementRefusal(typeName, resp)
	}
	if resp.PlannedState == cty.NilVal || resp.PlannedState.IsNull() {
		return "Planning the label write produced no object at all. This is a provider bug; nothing was written."
	}
	if extra := changedOutsideGoogleLabels(block, prior, resp.PlannedState); len(extra) > 0 {
		return fmt.Sprintf("Stamping this %s would also change %s. Approve is a labels-only write; nothing was written. Run live-plan to see what else has drifted and resolve that first.", typeName, strings.Join(extra, ", "))
	}
	return ""
}

// approveGoogleLabels is [approveOne] for the GCP carrier.
func approveGoogleLabels(ctx context.Context, estate string, addr addrs.AbsResourceInstance, e *eligible, slot string) StampOutcome {
	out := StampOutcome{Addr: addr, TypeName: e.typeName}

	if !markers.ValidGoogleLabelValue(estate) {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("%s cannot carry this estate's ownership marker: %s. Nothing was written.", addr, markers.NotAGoogleLabelValue(estate))
		return out
	}
	wantAddress := markers.GoogleAddressValue(addr)

	labels, ok := googleLabelsFromObject(e.schema.Block, e.applied)
	if !ok {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("%s has no settable labels argument in the provider's schema. Nothing was written.", e.typeName)
		return out
	}
	gotEstate := labels[discovery.TagEstate]
	gotAddress := labels[discovery.TagAddress]
	slotMatches := slot == "" || labels[discovery.TagSlot] == slot

	switch {
	case gotEstate == estate && gotAddress == wantAddress && slotMatches:
		out.Outcome = OutcomeAlreadyStamped
		out.Detail = "Already carries this estate's markers; nothing written."
		return out
	case gotEstate != "" && gotEstate != estate:
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("Carries tofu-estate = %q, owned by another estate. A migration never adopts another estate's resource; nothing was written.", gotEstate)
		return out
	case gotAddress != "" && gotAddress != wantAddress:
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("Already carries tofu-address = %q, which does not encode %s. Rewriting it here would be a rename, which is choudoufu live-mv's job, not a side effect of an import; nothing was written.", gotAddress, addr)
		return out
	}

	// Only the configuration's own labels map is rewritten: labels another
	// client set on the object stay where they are, in effective_labels.
	desiredLabels := map[string]string{}
	if own := e.applied.GetAttr(markers.GoogleLabelsAttr); !own.IsNull() && own.IsKnown() && !own.IsMarked() {
		for it := own.ElementIterator(); it.Next(); {
			k, v := it.Element()
			if !v.IsMarked() && !v.IsNull() && v.IsKnown() && v.Type() == cty.String {
				desiredLabels[k.AsString()] = v.AsString()
			}
		}
	}
	desiredLabels[discovery.TagEstate] = estate
	desiredLabels[discovery.TagAddress] = wantAddress
	if slot != "" {
		desiredLabels[discovery.TagSlot] = slot
	}

	desired, err := withGoogleLabels(e.schema.Block, e.applied, desiredLabels)
	if err != nil {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The labels of this %s could not be replaced: %s.", e.typeName, err)
		return out
	}

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
		if why := notAGoogleLabelsOnlyPlan(e.schema.Block, e.applied, e.typeName, resp); why != "" {
			refusal = why
			continue
		}
		configVal, planResp, refusal = candidate, resp, ""
		break
	}
	if refusal != "" {
		out.Outcome = OutcomeFailed
		out.Detail = refusal
		return out
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
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The provider failed while writing the labels: %s. The write may have partly landed; read the resource's tofu-estate label before deciding what to do next.", applyResp.Diagnostics.Err())
		return out
	}

	out.Outcome = OutcomeStamped
	out.Detail = "Wrote tofu-estate and tofu-address."
	if slot != "" {
		out.Detail = fmt.Sprintf("Wrote tofu-estate, tofu-address and tofu-slot = %q.", slot)
	}
	newLabels, newOK := googleLabelsFromObject(e.schema.Block, applyResp.NewState)
	if !newOK || newLabels[discovery.TagEstate] != estate || newLabels[discovery.TagAddress] != wantAddress ||
		(slot != "" && newLabels[discovery.TagSlot] != slot) {
		out.Detail = "The write reported no error, but the object read back afterwards does not carry the new markers. Verify with the cloud's own API before relying on this."
	}
	return out
}
