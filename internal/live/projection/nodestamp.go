// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is GitHub issue #388's stamp half: [NodeResolver] additionally
// implements internal/tofu.ConfigValueAdjuster, the seam
// rfc/20260823-foundation-order-ruling.md's constraint list fixed in
// opentofu/opentofu#3016's terms - markers are set on the evaluated
// CONFIGURATION value, at the node, before PlanResourceChange ever runs,
// never on the planned state after. It is the node-path twin of
// internal/live/stamp: same three marker keys, same MARKERS.md spec, a
// different seam. See internal/live/stamp/doc.go for why config synthesis
// (this file's "before evaluation" cousin, "before parsing") was chosen
// over post-plan mutation in the first place - #3016 is that doc's reason 3
// stated as a protocol rule rather than a local design preference, and nothing
// in this file re-argues it.
//
// # Why the module-instance problem this package's sibling has does not exist here
//
// internal/live/markers.ModulePrefix exists because internal/live/stamp
// rewrites ONE shared *hclsyntax.Body for every instance of a keyed module
// call: the tofu-address value has to be a template
// (tofu.marker_module_prefix, interpolated per instance) because the text
// being rewritten is shared. AdjustConfigValue is handed addr, a concrete
// [addrs.AbsResourceInstance] - module.container_definition["fluent-bit"].
// aws_cloudwatch_log_group.this - with every count/for_each/module-instance
// key already resolved. [markers.EscapeAddress] applied directly to
// addr.String() produces exactly the same bytes stamp's template would have
// interpolated for that one instance, with no template, no
// tofu.marker_module_prefix, and no evaluator symbol involved at all. This
// is ruling 3's whole point: a value the static evaluator could only ever
// approximate with an expression is, at the node, just a value.
//
// # markers = record: setting nothing, on purpose
//
// strict { markers "record" { ... } } tells the estate to hold a selected
// resource's identity in the record store instead of a live tag
// (identity.SelectionFor, identity.SelectedLocatedType). AdjustConfigValue
// honours it the same way internal/live/stamp's SkipMarkersRecord branch
// does for the HCL path: for a selected, record-eligible instance it
// returns config completely unchanged - no tofu-estate, no tofu-address,
// nothing.
//
// It deliberately does NOT try to reproduce GitHub issue #380's
// ignore_changes synthesis here. #380's fix appends
// tags["tofu-estate"]/tags["tofu-address"] to the resource's own
// configs.Resource.Managed.IgnoreChanges - a fact about the CONFIGURATION,
// read by n.processIgnoreChanges from n.Config.Managed a few lines after
// this adjuster runs (node_resource_abstract_instance.go). Nothing in
// tofu.ConfigValueAdjuster's interface - by ruling 2's own constraint, only
// (ctx, addr, evaluated config value, schema) - reaches that field, and
// deliberately so: widening the interface to carry a *configs.Resource, or
// worse an EvalContext, is exactly the graph-node coupling ruling 2 rules
// out so the same resolver keeps working under upstream's proposed
// event-model runtime (#3414). Nor does this adjuster have prior state to
// read, so it could not reconstruct #380's preserved value even by
// inspection: it has no way to know what a live object's tofu-estate tag
// currently says.
//
// This is not a gap this unit is leaving open, though. The HCL stamp is not
// retired in this unit (see internal/live/stamp/doc.go and this package's
// own noderesolver.go doc comment: the migration flag routes identity
// resolution and now marker stamping through the node ADDITIONALLY, not
// INSTEAD, until the gauntlet holds without the static path), so
// internal/live/stamp still runs on every estate this flag reaches, and its
// #380 fix already appends the per-key ignore_changes to
// n.Config.Managed.IgnoreChanges before the graph walk ever starts -
// exactly the same n.Config.Managed.IgnoreChanges
// n.processIgnoreChanges reads, unmodified, immediately after this
// adjuster returns. A record-selected resource's existing marker therefore
// survives today for the same reason it already did before this file
// existed: #380's mechanism runs earlier in the pipeline than the node does,
// on the configuration text, and this adjuster's only obligation is to stay
// out of its way - which "set nothing" does. See
// TestLivePlan_markersRecordPreservesExistingMarker_NodeResolve in
// internal/command/live_plan_test.go for the by-value proof that the two
// mechanisms compose correctly with the flag on. The day the HCL stamp
// retires, this withholding path will need its OWN way to protect an
// existing marker with no configuration-level ignore_changes to lean on -
// flagged here so that day's unit does not have to rediscover the gap.
//
// # tofu-slot: threaded through, not left behind
//
// Unlike the marker-record ignore_changes mechanism, the slot assignment
// turned out not to need a config-level seam at all. [NodeResolver.Slots]
// is [discovery.Result.SlotTable] - a plain map from escaped instance
// address to the slot value already minted for it, exactly the lookup key
// this file already computes for tofu-address - populated at the same
// point live_mode.go/live_plan.go populate RecordStore/MarkerIndex, from
// the SAME discovery sweep stamp.Request.Slots already carries for the HCL
// path (the sweep runs pre-walk regardless of which path stamps the
// marker; see HANDOFF's "sweep-demand shrink" open edge, which is about
// the sweep's DEMAND, not its availability). A count instance with an
// assigned slot gets tags["tofu-slot"] set to it here, by a map lookup, in
// place of the HCL path's synthesized `lookup({...}, "...", "")`
// expression - the same assignment, read the same way, with no
// count.index or tofu.marker_module_prefix needed because addr is already
// the one concrete instance the lookup is for. An instance the sweep never
// assigned a slot to (nil Slots, or no entry for this address - a
// configuration with no count blocks, or a for_each'd resource, which
// never receives one) gets no tofu-slot tag, exactly as the HCL path never
// writes one for either shape.

// AdjustConfigValue implements internal/tofu.ConfigValueAdjuster.
func (n *NodeResolver) AdjustConfigValue(_ context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if schema.Block == nil {
		return config, diags
	}
	if _, taggable := markers.TagSurface(schema.Block); !taggable {
		return config, diags
	}

	resourceType := addr.Resource.Resource.Type
	if n.Selection.Selects(addr.ConfigResource()) &&
		identity.SelectedLocatedType(resourceType, map[string]providers.Schema{resourceType: schema}) {
		// strict { markers "record" }: this instance's identity lives in
		// the estate's record store, not in a live tag. See this file's
		// own doc comment for why setting nothing here is deliberate and
		// what still protects an existing marker.
		return config, diags
	}

	if config == cty.NilVal || config.IsNull() || !config.IsKnown() {
		return config, diags
	}
	if config.IsMarked() {
		// EvaluateBlock assembles this object from its individually
		// evaluated attributes and never marks the object itself - a mark
		// on an attribute value does not hoist to its container the way a
		// set's elements' marks do (see internal/live/marksafe's own doc
		// comment on that distinction) - so this is not a shape production
		// evaluation produces. It is still a proof this file's own call to
		// AsValueMap below needs, so a marked config is refused rather than
		// unmarked and forgotten: nothing here reads deep enough into any
		// attribute's value to need to.
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot set ownership markers on a marked configuration value",
			fmt.Sprintf("%s's evaluated configuration is marked as a whole, which this pass will not unmark; its ownership markers were left for the configuration's own stamp (or an operator) to write.", addr)))
		return config, diags
	}

	address := markers.EscapeAddress(addr.String())
	tagsVal := config.GetAttr(tagsArgumentName)

	newTags, tagDiags := n.stampedTags(addr, tagsVal, address)
	diags = diags.Append(tagDiags)
	if tagDiags.HasErrors() {
		return config, diags
	}

	configElems := config.AsValueMap()
	if configElems == nil {
		configElems = make(map[string]cty.Value, 1)
	}
	configElems[tagsArgumentName] = newTags
	return cty.ObjectVal(configElems), diags
}

// tagsArgumentName is the one attribute [markers.TagSurface] ever names.
const tagsArgumentName = "tags"

// stampedTags returns tagsVal with this instance's tofu-estate/tofu-address
// (and tofu-slot, when the sweep assigned one - see this file's own doc
// comment) added, preserving every entry tagsVal already carries and its
// own marks.
//
// tagsVal may be marked (a merge() call or a whole tags argument built from
// a sensitive local, however unusual that is for a tags map) without this
// function ever reading what any of its VALUES say: [cty.Value.Unmark]
// strips only the outer mark, and marksafe's own doc comment names this the
// proof its scanner accepts (ProofUnmarked) - the elements underneath keep
// whatever marks they already had, and re-applying the outer mark at the
// end (WithMarks, a no-op for an empty mark set) means the result is exactly
// as sensitive as tagsVal was, with two more plain-string entries.
func (n *NodeResolver) stampedTags(addr addrs.AbsResourceInstance, tagsVal cty.Value, address string) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	tagsVal, tagsMarks := tagsVal.Unmark()

	elems := map[string]cty.Value{}
	switch {
	case tagsVal.IsNull():
		// No tags argument in configuration at all: elems starts empty and
		// the two marker entries below are the whole map.
	case !tagsVal.IsKnown():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot set ownership markers on an unresolved tags value",
			fmt.Sprintf("%s's tags argument is not yet known, so its ownership markers could not be set at the node; the configuration's own value is used unchanged.", addr)))
		return tagsVal.WithMarks(tagsMarks), diags
	case !tagsVal.Type().IsMapType():
		// [markers.TagSurface] proves the SCHEMA attribute is a settable
		// map(string) or map(dynamic); EvaluateBlock still converts every
		// evaluated value to that declared type before this function ever
		// sees it, so a non-map value here would be a bug in that
		// conversion, not a configuration this pass can decline gracefully.
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot merge ownership markers into this tags value",
			fmt.Sprintf("%s's tags argument evaluated to a %s, not a map; the configuration's own value is used unchanged.", addr, tagsVal.Type().FriendlyName())))
		return tagsVal.WithMarks(tagsMarks), diags
	case tagsVal.Type().ElementType() != cty.String && tagsVal.Type().ElementType() != cty.DynamicPseudoType:
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot merge ownership markers into this tags value",
			fmt.Sprintf("%s's tags argument holds %s values, which this pass does not know how to merge a plain string marker into; the configuration's own value is used unchanged.", addr, tagsVal.Type().ElementType().FriendlyName())))
		return tagsVal.WithMarks(tagsMarks), diags
	case tagsVal.LengthInt() > 0:
		for it := tagsVal.ElementIterator(); it.Next(); {
			k, v := it.Element()
			if v.Type() != cty.String && v.Type() != cty.DynamicPseudoType {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot merge ownership markers into this tags value",
					fmt.Sprintf("%s's tags argument holds a non-string value at key %q; the configuration's own value is used unchanged.", addr, k.AsString())))
				return tagsVal.WithMarks(tagsMarks), diags
			}
			elems[k.AsString()] = v
		}
	}

	elems[markers.TagEstate] = cty.StringVal(n.Estate)
	elems[markers.TagAddress] = cty.StringVal(address)
	if slot, ok := n.Slots[address]; ok {
		elems[markers.TagSlot] = cty.StringVal(slot)
	}

	return cty.MapVal(elems).WithMarks(tagsMarks), diags
}
