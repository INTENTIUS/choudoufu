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
// rulings/20260823-foundation-order-ruling.md's constraint list fixed in
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
// Until GitHub issue #451, this file did not try to reproduce issue #380's
// ignore_changes synthesis here, and leaned entirely on internal/live/stamp
// still running unconditionally, flag on or off, to protect an existing
// live marker from being planned away. #451 closed that gap with
// [NodeResolver.AdjustIgnoreChanges] (nodestamp_ignorechanges.go): a
// SEPARATE hook, tofu.IgnoreChangesAdjuster, that this same *NodeResolver*
// value also implements. It exists as its own interface, rather than a
// second return value here, because AdjustConfigValue's own contract - by
// ruling 2's own constraint, only (ctx, addr, evaluated config value,
// schema) - deliberately has no prior state to compare a value against and
// no way to reach configs.Resource.Managed.IgnoreChanges from the cty.Value
// it returns; widening it to carry a *configs.Resource, or worse an
// EvalContext, is exactly the graph-node coupling ruling 2 rules out.
// AdjustIgnoreChanges instead returns the two marker-tag PATHS to protect,
// which internal/tofu unions onto configs.Resource.Managed.IgnoreChanges
// itself before n.processIgnoreChanges runs - the ordinary ignore_changes
// mechanism doing the ordinary ignore_changes thing, told what to protect
// by this pass instead of by an operator's own lifecycle block. See that
// method's own doc comment for the detail, and
// TestLivePlan_markersRecordPreservesExistingMarker_NodeResolve in
// internal/command/live_plan_test.go for the by-value proof with
// internal/live/stamp gated off entirely (GitHub issue #451's own
// retirement re-attempt).
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

	if n.Estate == "" {
		// No estate name: parity with internal/live/stamp's own guard
		// (statelessStamp's estate=="" branch, internal/command/live_plan.go),
		// which already returns a nil *stamp.Result plus a single
		// "Ownership markers not stamped" warning and writes nothing -
		// both call sites run that pass unconditionally today, flag on or
		// off, so it has already said what needs saying for this run.
		// Setting tofu-estate here anyway would not degrade gracefully
		// the way an unstamped resource does: cty.StringVal("") is a
		// value, not an absence, so a later run reading it back sees a
		// tofu-estate tag that names no estate rather than no tag at
		// all - HANDOFF's "never write a wrong marker" rule, and this is
		// the same failure a stray CREATE-over-an-owned-object plan is,
		// just on the write side instead of the read side. Returning the
		// config unchanged here, silently, is what the record-selection
		// branch below already does for its own "set nothing" case.
		return config, diags
	}
	if schema.Block == nil {
		return config, diags
	}
	if _, taggable := markers.TagSurface(schema.Block); !taggable {
		return config, diags
	}

	if n.recordSelected(addr, schema) {
		// strict { markers "record" }: this instance's identity lives in
		// the estate's record store, not in a live tag. See this file's
		// own doc comment for why setting nothing here is deliberate and
		// what still protects an existing marker - as of GitHub issue #451,
		// [NodeResolver.AdjustIgnoreChanges] below, not merely a comment
		// pointing at internal/live/stamp's #380 fix.
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

	// GitHub issue #451: before overwriting whatever this instance's
	// configuration already declared for the two ownership-marker keys,
	// check whether it declared one at all and, if it did, whether it
	// agrees with what this run would write. See [markerConflictDiag]'s
	// own doc comment for why this is a fatal refusal rather than a
	// silent overwrite, and internal/live/stamp's verifyValue (stamp.go)
	// for the sibling pass this ports - the message text is matched
	// deliberately, so an operator sees the same sentence whichever path
	// found the conflict.
	diags = diags.Append(markerConflictDiag(addr, elems, markers.TagEstate, n.Estate))
	diags = diags.Append(markerConflictDiag(addr, elems, markers.TagAddress, address))
	if diags.HasErrors() {
		return tagsVal.WithMarks(tagsMarks), diags
	}

	elems[markers.TagEstate] = cty.StringVal(n.Estate)
	elems[markers.TagAddress] = cty.StringVal(address)
	if slot, ok := n.Slots[address]; ok {
		elems[markers.TagSlot] = cty.StringVal(slot)
	}

	return cty.MapVal(elems).WithMarks(tagsMarks), diags
}

// SummaryMarkerConflict names the fatal diagnostic [markerConflictDiag]
// raises. It is [refusalscan]'s registered form of the same summary text
// internal/live/stamp.SummaryMarkerConflict carries (stamp/summaries.go) -
// a separate package-level constant rather than an import of the stamp
// package, because this package must not depend on the one GitHub issue
// #452 retires. The two strings are kept identical by hand; a test in
// nodestamp_test.go pins that they do not drift apart.
const SummaryMarkerConflict = "Ownership marker conflict"

// markerConflictDiag checks one marker key already present in elems - the
// tags map this instance's OWN configuration declared, before this pass's
// entries are added - against the value this run would write, and reports
// a fatal conflict when the two disagree.
//
// Every instance [NodeResolver.AdjustConfigValue] is called for already
// names one concrete resource instance (ruling 3's whole point - see this
// file's own doc comment on "why the module-instance problem this
// package's sibling has does not exist here"), so there is no per-instance
// template case to consider the way internal/live/stamp's verify/
// verifyValue pair has to for a count- or for_each-expanded HCL body: this
// is exactly that pair's non-per-instance branch, with the same two
// messages, word for word, because an operator reading a conflict from
// this path must not be able to tell it apart from one internal/live/stamp
// raised for the same resource on a different run. Absent, null, unknown,
// marked or non-string existing values are not conflicts - there is
// nothing to disagree with yet, or nothing this pass can read to compare
// (internal/live/marksafe's ProofUnmarked discipline: a marked value must
// never reach AsString, so a marked entry is treated the same as an
// unreadable one rather than unmarked and inspected) - so the pass
// proceeds to write its own value exactly as it did before this check
// existed.
func markerConflictDiag(addr addrs.AbsResourceInstance, elems map[string]cty.Value, key, want string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	existing, ok := elems[key]
	if !ok || existing.IsNull() || !existing.IsKnown() || existing.IsMarked() || existing.Type() != cty.String {
		return diags
	}
	got := existing.AsString()
	if got == want {
		return diags
	}

	switch key {
	case markers.TagEstate:
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryMarkerConflict, fmt.Sprintf(
			"%s declares %s = %q and this run is stamping the estate %q. A plan never overwrites a marker naming another estate: name %s in the live block (or with -estate, if this configuration has no live block) if that is the estate this run is for, or correct the tag.",
			addr, markers.TagEstate, got, want, got)))
	case markers.TagAddress:
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryMarkerConflict, fmt.Sprintf(
			"%s declares %s = %q, but its address in this configuration is %q. A marker naming another address is a rename: run `choudoufu live-mv %s %s`, or fix the tag. See live/MARKERS.md, \"The rename rule\".",
			addr, markers.TagAddress, got, want, got, want)))
	}
	return diags
}

// recordSelected reports whether addr is covered by strict { markers
// "record" } AND its type is one [identity.SelectedLocatedType] can
// actually honour that selection for - the same two-part test
// AdjustConfigValue's own doc comment describes, factored out so
// [NodeResolver.AdjustIgnoreChanges] (nodestamp_ignorechanges.go) reaches
// the identical verdict for the identical instance rather than
// re-deriving it and risking the two ever disagreeing about which
// instance the selection covers.
func (n *NodeResolver) recordSelected(addr addrs.AbsResourceInstance, schema providers.Schema) bool {
	resourceType := addr.Resource.Resource.Type
	return n.Selection.Selects(addr.ConfigResource()) &&
		identity.SelectedLocatedType(resourceType, map[string]providers.Schema{resourceType: schema})
}
