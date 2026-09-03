// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// rewrite is the cloud write: one instance's tags-only change, driven through
// the provider's own plan/apply pair.
//
// The prior state is the object the projection read a moment ago. The desired
// object is that same object with two tag entries set - tofu-address to the
// new escaped address, and tofu-estate to this estate, which is already its
// value in every ordinary case and is restored here rather than assumed,
// because an object read back from a provider that does not serve tags would
// otherwise lose it. Everything else on the object is carried across
// untouched.
func (m *mover) rewrite(ctx context.Context, prior *states.ResourceInstanceObject) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	// Marks are a projection's addition, not the provider's: the schema's
	// sensitivity is applied to the value on the way into the projection, and
	// a marked value cannot be sent back over the wire.
	priorVal, _ := prior.Value.UnmarkDeep()

	tags, taggable := tagsFromObject(m.schema, priorVal)
	if !taggable {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Resource type with no tags",
			fmt.Sprintf("A %s has no settable tags argument in the provider's schema, so it carries no ownership marker and there is nothing to rewrite.", m.res.TypeName),
		))
	}

	chunks := discovery.SplitAddress(m.res.NewMarker)
	desiredTags := make(map[string]string, len(tags)+1+len(chunks))
	for k, v := range tags {
		desiredTags[k] = v
	}
	desiredTags[discovery.TagEstate] = m.req.Estate
	for i, chunk := range chunks {
		desiredTags[discovery.AddressTagKey(i)] = chunk
	}
	// A rename onto a shorter address needs fewer continuation tags than the
	// old one did. Unlike stamp (which only ever adds a marker it did not
	// already find), a rename has an explicit before and after, so it is
	// also the one place that cleans up: a continuation tag this new address
	// does not reach is deleted rather than left behind stale, where
	// discovery.GatherAddress would otherwise concatenate it onto the new,
	// shorter tofu-address and read back something this resource never
	// declared.
	for i := len(chunks); i < discovery.MaxContinuations; i++ {
		delete(desiredTags, discovery.AddressTagKey(i))
	}

	desired, err := withTags(m.schema.Block, priorVal, desiredTags)
	if err != nil {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot build the rewritten object",
			fmt.Sprintf("The tags of the live %s could not be replaced: %s.", m.res.TypeName, err),
		))
	}

	// The renamed resource has no configuration here, so one is synthesized.
	// Providers read it back (SDKv2's GetRawConfig, the framework's Config),
	// and there is more than one honest answer to what it should say about
	// the arguments a provider fills in for itself: handing over the live
	// object with every computed attribute filled in tells the provider
	// something a configuration never says, and omitting them tells a
	// provider that injects one that it has changed. [syntheticConfigs]
	// offers both, least claim first; each is planned in turn and the first
	// plan that is a clean tags-only change wins. [mover.checkPlan] is what
	// "clean" means, unchanged, so trying a second configuration widens what
	// can be rewritten without widening what may be.
	var (
		configVal cty.Value
		planResp  providers.PlanResourceChangeResponse
		refused   tfdiags.Diagnostics
	)
	for _, candidate := range syntheticConfigs(m.schema.Block, desired) {
		resp := m.provider.PlanResourceChange(ctx, providers.PlanResourceChangeRequest{
			TypeName:         m.res.TypeName,
			PriorState:       priorVal,
			ProposedNewState: objchange.ProposedNew(m.schema.Block, priorVal, candidate),
			Config:           candidate,
			PriorPrivate:     prior.Private,
			// A null of the dynamic pseudo-type rather than the zero cty.Value,
			// for the same reason the projection's read passes one: the plugin
			// client marshals ProviderMeta whenever the provider declares a
			// provider_meta schema, and a value with no type at all panics the
			// conformance check. A rename has no provider_meta block to
			// evaluate, so null is also the correct answer.
			ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
			PriorIdentity: prior.Identity,
		})
		if resp.Diagnostics.HasErrors() {
			// Kept, not returned: if no candidate produces a clean plan, the
			// LAST refusal is the one reported, so the message an operator
			// reads is the one for the configuration that asserts most - the
			// only one this path used to send at all.
			refused = resp.Diagnostics.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Cannot plan the marker rewrite",
				fmt.Sprintf("The provider failed while planning the tag change on the %s at %s. Nothing was written.", m.res.TypeName, m.res.LiveID),
			))
			continue
		}
		if checkDiags := m.checkPlan(priorVal, resp); checkDiags.HasErrors() {
			refused = resp.Diagnostics.Append(checkDiags)
			continue
		}
		configVal, planResp, refused = candidate, resp, nil
		break
	}
	if refused.HasErrors() {
		return diags.Append(refused)
	}
	diags = diags.Append(planResp.Diagnostics)

	applyResp := m.provider.ApplyResourceChange(ctx, providers.ApplyResourceChangeRequest{
		TypeName:        m.res.TypeName,
		PriorState:      priorVal,
		PlannedState:    planResp.PlannedState,
		Config:          configVal,
		PlannedPrivate:  planResp.PlannedPrivate,
		ProviderMeta:    cty.NullVal(cty.DynamicPseudoType),
		PlannedIdentity: planResp.PlannedIdentity,
	})
	if applyResp.Diagnostics.HasErrors() {
		return diags.Append(applyResp.Diagnostics.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Failed marker rewrite",
			fmt.Sprintf(
				"The provider failed while writing the tag change to the %s at %s. The write may have partly landed: read the resource's tofu-address tag before deciding what to do next - if it already names %s, the rename is done.",
				m.res.TypeName, m.res.LiveID, m.res.New),
		)))
	}
	diags = diags.Append(applyResp.Diagnostics)

	m.res.Written = true
	log.Printf("[TRACE] stateless/mv: rewrote tofu-address on %s %s: %q -> %q",
		m.res.TypeName, m.res.LiveID, m.res.OldMarker, m.res.NewMarker)

	return diags.Append(m.verify(applyResp.NewState))
}

// checkPlan refuses the two plans a marker rewrite must never apply.
//
// A plan that requires replacement would destroy the resource this operation
// exists to keep, and a plan that moves anything but the tags is the provider
// proposing more than the rename asked for. Both stop before the apply, with
// the offending attributes named, rather than being carried out and reported.
func (m *mover) checkPlan(priorVal cty.Value, resp providers.PlanResourceChangeResponse) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if len(resp.RequiresReplace) > 0 {
		paths := make([]string, 0, len(resp.RequiresReplace))
		for _, p := range resp.RequiresReplace {
			paths = append(paths, tfdiags.FormatCtyPath(p))
		}
		sort.Strings(paths)
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Unexpected replacement in marker rewrite",
			fmt.Sprintf(
				"Changing the ownership marker on the %s at %s would require replacing it, according to the provider (%s). A rename never destroys anything, so nothing was written. This is a provider bug or a resource type whose tags are not modifiable in place.",
				m.res.TypeName, m.res.LiveID, strings.Join(paths, ", ")),
		))
	}

	planned := resp.PlannedState
	if planned == cty.NilVal || planned.IsNull() {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No planned object for the marker rewrite",
			fmt.Sprintf("Planning the tag change on the %s at %s produced no object at all. This is a bug in the provider; nothing was written.", m.res.TypeName, m.res.LiveID),
		))
	}
	if errs := planned.Type().TestConformance(m.schema.Block.ImpliedType()); len(errs) > 0 {
		for _, err := range errs {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Provider produced an invalid plan",
				fmt.Sprintf("Planning the tag change on the %s at %s produced a value that does not conform to the provider's own schema: %s. This is a bug in the provider; nothing was written.",
					m.res.TypeName, m.res.LiveID, tfdiags.FormatError(err)),
			))
		}
		return diags
	}

	if extra := changedOutsideTags(m.schema.Block, priorVal, planned); len(extra) > 0 {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Unexpected changes in the marker rewrite",
			fmt.Sprintf(
				"Rewriting the ownership marker on the %s at %s would also change %s. A rename is a tags-only write, so nothing was written. Run live-plan to see what else this resource has drifted into and resolve that first.",
				m.res.TypeName, m.res.LiveID, strings.Join(extra, ", ")),
		))
	}
	return diags
}

// verify reads the marker back off the object the provider returned from the
// apply.
//
// A mismatch is a warning rather than an error because the write itself
// succeeded and the read is the untrustworthy half: some APIs do not serve
// tags back on the read a provider performs after an apply (the aws_iam_role
// gap the e2e notes record as #5), which would turn a perfectly good rename
// into a failure. The warning says exactly what was and was not observed so
// that an operator can check with the cloud's own API.
func (m *mover) verify(newState cty.Value) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if newState == cty.NilVal || newState.IsNull() {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Unreadable marker after the rewrite",
			fmt.Sprintf("The provider returned no object from the apply on the %s at %s, so this run cannot confirm the tofu-address tag now reads %q. The write itself reported no error.", m.res.TypeName, m.res.LiveID, m.res.NewMarker),
		))
	}

	tags, taggable := tagsFromObject(m.schema, newState)
	raw, corrupt := discovery.GatherAddress(tags)
	// A cross-estate move is verified on both tags: the address alone
	// would read as verified on a move that kept its address and changed
	// nothing.
	estateOK := m.req.FromEstate == "" || tags[discovery.TagEstate] == m.req.Estate
	if got := discovery.EscapeAddress(raw); taggable && !corrupt && got == m.res.NewMarker && estateOK {
		m.res.Verified = true
		return diags
	}

	return diags.Append(tfdiags.Sourceless(
		tfdiags.Warning,
		"Unreadable marker after the rewrite",
		fmt.Sprintf(
			"The tag write on the %s at %s reported no error, but the object the provider returned afterwards carries %s = %q rather than %q. Some providers do not serve tags back on a read; check the resource's tags with the cloud's own API before rerunning.",
			m.res.TypeName, m.res.LiveID, discovery.TagAddress, raw, m.res.NewMarker),
	))
}

// ---------------------------------------------------------------------------
// Reading and rebuilding tags
// ---------------------------------------------------------------------------

// tagsFromObject reads the marker tags off a resource object.
//
// "tags" is read after "tags_all" so that an explicitly set tag wins over the
// same key arriving through the provider's default_tags merge, which is the
// same precedence discovery reads listed objects with.
func tagsFromObject(schema providers.Schema, obj cty.Value) (map[string]string, bool) {
	if schema.Block == nil || !settableTags(schema.Block) {
		return nil, false
	}
	return tagsFromListed(obj)
}

// tagsFromListed reads a tags map off any resource object, whether it came
// from a read or from a list result. The second return distinguishes "no tags
// attribute at all" from "tagged with nothing".
func tagsFromListed(obj cty.Value) (map[string]string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() {
		return nil, false
	}
	ty := obj.Type()
	if !ty.IsObjectType() {
		return nil, false
	}

	var found bool
	tags := make(map[string]string)
	for _, name := range []string{"tags_all", "tags"} {
		if !ty.HasAttribute(name) {
			continue
		}
		v, _ := obj.GetAttr(name).Unmark()
		if v.IsNull() || !v.IsKnown() {
			found = true
			continue
		}
		if !v.CanIterateElements() {
			continue
		}
		found = true
		for it := v.ElementIterator(); it.Next(); {
			k, val := it.Element()
			if k.Type() != cty.String || k.IsNull() || val.IsNull() || !val.IsKnown() || val.Type() != cty.String {
				continue
			}
			tags[k.AsString()] = val.AsString()
		}
	}
	if !found {
		return nil, false
	}
	return tags, true
}

// settableTags reports whether a resource type carries the tag map the marker
// spec describes. It is [markers.Taggable] and nothing else, for the reason
// internal/live/untag's taggable spells out: live-mv REWRITES a marker on a
// live object, so admitting a type stamping refuses would move an address
// into a tag map the provider owns the key space of.
func settableTags(block *configschema.Block) bool { return markers.Taggable(block) }

// withTags returns the object with its tags attribute replaced.
func withTags(block *configschema.Block, obj cty.Value, tags map[string]string) (cty.Value, error) {
	attr := block.Attributes["tags"]

	var tagVal cty.Value
	if len(tags) == 0 {
		tagVal = cty.MapValEmpty(cty.String)
	} else {
		vals := make(map[string]cty.Value, len(tags))
		for k, v := range tags {
			vals[k] = cty.StringVal(v)
		}
		tagVal = cty.MapVal(vals)
	}
	converted, err := convert.Convert(tagVal, attr.Type)
	if err != nil {
		return cty.NilVal, err
	}

	vals := make(map[string]cty.Value, len(block.Attributes)+len(block.BlockTypes))
	for name := range block.Attributes {
		vals[name] = obj.GetAttr(name)
	}
	for name := range block.BlockTypes {
		vals[name] = obj.GetAttr(name)
	}
	vals["tags"] = converted
	return cty.ObjectVal(vals), nil
}

// ---------------------------------------------------------------------------
// Deriving a configuration value from a live object
// ---------------------------------------------------------------------------

// assertedTagAttr is the one argument a tags-only write claims as set in the
// configuration it synthesizes, exempt from [computedForProvider] so a
// provider that ever declares tags optional+computed does not turn every
// rename into a silent no-op. See internal/live/liveimport's tags.go, which
// carries the full reasoning for both this and [computedForProvider].
const assertedTagAttr = "tags"

// configClaim is how much of the live object a synthetic configuration
// claims the operator wrote down. The two readings of "what would the HCL
// have said" disagree about the attributes a provider may fill in for
// itself, and no property of the schema separates the cases; internal/live/
// liveimport's tags.go carries the full reasoning and the measurements.
type configClaim int

const (
	// claimTagsOnly nulls every Computed attribute, not only the
	// Computed-only ones - an optional+computed argument the operator never
	// wrote is null in real HCL, and a provider that gates a CustomizeDiff
	// on finding one known and non-null in the raw config refuses a tag
	// write that never touched it (GitHub issue #373).
	claimTagsOnly configClaim = iota

	// claimEverythingSettable nulls only what a configuration cannot set at
	// all, which is what this file did before #373 - still right where a
	// provider INJECTS an optional+computed attribute rather than reading
	// it, and reads its absence from the config as a change.
	claimEverythingSettable
)

// syntheticConfigs is the configurations a tags-only write may offer the
// provider for one object, least claim first. The caller plans each in turn
// and takes the first whose plan is a clean tags-only change, judged by the
// same guards that already stood between a plan and an apply. When the two
// agree - every type with no optional+computed attribute - there is one.
func syntheticConfigs(block *configschema.Block, val cty.Value) []cty.Value {
	least := configValue(block, val, claimTagsOnly)
	most := configValue(block, val, claimEverythingSettable)
	if least.RawEquals(most) {
		return []cty.Value{least}
	}
	return []cty.Value{least, most}
}

// computedForProvider reports whether a synthetic configuration making the
// given claim must leave an attribute null rather than carry the live
// object's value across. An optional+computed attribute with a NestedType
// recurses instead of being nulled under either claim, because objchange
// reads a null config for one of those as a removal and would move the plan.
// internal/live/liveimport's copy carries the reasoning.
func computedForProvider(attr *configschema.Attribute, claim configClaim) bool {
	switch {
	case !attr.Computed:
		return false
	case !attr.Optional && !attr.Required:
		return true
	case claim != claimTagsOnly:
		return false
	default:
		return attr.NestedType == nil
	}
}

// configValue is the live object as the configuration for a tags-only write
// would express it: the tag map this run is asserting, plus every argument
// only a configuration can supply, and nothing else.
//
// This is what makes the plan below a tags-only change rather than an
// assertion that the operator wrote down every value the provider is free to
// fill in for itself. Types are never altered - only values are nulled - so
// the result still conforms to the schema's implied type.
func configValue(block *configschema.Block, val cty.Value, claim configClaim) cty.Value {
	if block == nil || val == cty.NilVal || val.IsNull() || !val.IsKnown() {
		return val
	}

	vals := make(map[string]cty.Value, len(block.Attributes)+len(block.BlockTypes))
	for name, attr := range block.Attributes {
		v := val.GetAttr(name)
		switch {
		case name == assertedTagAttr:
			vals[name] = v
		case computedForProvider(attr, claim):
			vals[name] = cty.NullVal(v.Type())
		case attr.NestedType != nil:
			vals[name] = configNestedObject(attr.NestedType, v, claim)
		default:
			vals[name] = v
		}
	}
	for name, nested := range block.BlockTypes {
		v := val.GetAttr(name)
		switch nested.Nesting {
		case configschema.NestingSingle, configschema.NestingGroup:
			vals[name] = configValue(&nested.Block, v, claim)
		default:
			vals[name] = mapElements(v, func(elem cty.Value) cty.Value {
				return configValue(&nested.Block, elem, claim)
			})
		}
	}
	return cty.ObjectVal(vals)
}

// configNestedObject is configValue for an attribute whose type is a nested
// object rather than a block.
func configNestedObject(obj *configschema.Object, val cty.Value, claim configClaim) cty.Value {
	if val == cty.NilVal || val.IsNull() || !val.IsKnown() {
		return val
	}

	one := func(v cty.Value) cty.Value {
		if v.IsNull() || !v.IsKnown() {
			return v
		}
		vals := make(map[string]cty.Value, len(obj.Attributes))
		for name, attr := range obj.Attributes {
			av := v.GetAttr(name)
			switch {
			case computedForProvider(attr, claim):
				vals[name] = cty.NullVal(av.Type())
			case attr.NestedType != nil:
				vals[name] = configNestedObject(attr.NestedType, av, claim)
			default:
				vals[name] = av
			}
		}
		return cty.ObjectVal(vals)
	}

	if obj.Nesting == configschema.NestingSingle || obj.Nesting == configschema.NestingGroup {
		return one(val)
	}
	return mapElements(val, one)
}

// mapElements rebuilds a collection with every element passed through f,
// preserving the collection kind. An empty or unknown collection comes back
// untouched, since there is nothing to map and rebuilding one risks changing
// its type.
func mapElements(val cty.Value, f func(cty.Value) cty.Value) cty.Value {
	if val == cty.NilVal || val.IsNull() || !val.IsKnown() || val.LengthInt() == 0 {
		return val
	}

	ty := val.Type()
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		elems := make(map[string]cty.Value)
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			elems[k.AsString()] = f(v)
		}
		if ty.IsObjectType() {
			return cty.ObjectVal(elems)
		}
		return cty.MapVal(elems)
	default:
		var elems []cty.Value
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			elems = append(elems, f(v))
		}
		switch {
		case ty.IsSetType():
			return cty.SetVal(elems)
		case ty.IsTupleType():
			return cty.TupleVal(elems)
		default:
			return cty.ListVal(elems)
		}
	}
}

// changedOutsideTags names the top-level arguments a planned change moves
// other than the tags, each rendered as "name (prior -> planned)".
//
// tags_all is excluded alongside tags because it is the provider's computed
// merge of tags with its default_tags, so it moves whenever tags does and is
// not a separate change. An attribute the provider left unknown is excluded
// too: unknown is "I will fill this in", which is what a computed attribute
// derived from tags looks like at plan time. So is a difference that is only
// a legacy-SDK provider filling in a zero value where the object it read
// carried a null - see [equivalent].
func changedOutsideTags(block *configschema.Block, prior, planned cty.Value) []string {
	if prior == cty.NilVal || prior.IsNull() || planned == cty.NilVal || planned.IsNull() {
		return nil
	}

	names := make([]string, 0, len(block.Attributes)+len(block.BlockTypes))
	for name := range block.Attributes {
		names = append(names, name)
	}
	for name := range block.BlockTypes {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []string
	for _, name := range names {
		if name == "tags" || name == "tags_all" {
			continue
		}
		if !prior.Type().HasAttribute(name) || !planned.Type().HasAttribute(name) {
			continue
		}
		p := prior.GetAttr(name)
		n := planned.GetAttr(name)
		if !n.IsWhollyKnown() {
			continue
		}
		if !equivalent(p, n) {
			out = append(out, fmt.Sprintf("%s (%s -> %s)", name, shortValue(p), shortValue(n)))
		}
	}
	return out
}

// equivalent reports whether two values say the same thing about the cloud.
//
// It is RawEquals plus one allowance, and the allowance exists because of a
// real thing providers built on the legacy SDK do: an object read back from
// the cloud carries null for an argument nobody set, and the plan for that
// same object carries the schema's zero default instead - false,
// "", 0, an empty block list. aws_security_group's revoke_rules_on_delete and
// aws_s3_bucket's replication_configuration both do exactly this, and neither
// is a change to anything in the cloud; it is the imprecise mapping between
// the SDK's type system and OpenTofu's that PlanResourceChangeResponse names
// LegacyTypeSystem. Treating it as a change would make renaming most AWS
// resources impossible for a reason that has nothing to do with renaming.
//
// Anything else - one real value replaced by another - is still a difference,
// which is the whole point of the check that calls this.
//
// The allowance has to reach all the way down, because that is where the
// providers put it: aws_s3_bucket's replication_configuration comes back as a
// one-element block list whose "role" is null on the read and "" in the plan.
// Set elements are the one place it stops, since a set has no positions to
// pair its elements up by and guessing which element became which is exactly
// the kind of inference this whole mode refuses to make.
func equivalent(a, b cty.Value) bool {
	if a == cty.NilVal || b == cty.NilVal {
		return a == b
	}
	if a.RawEquals(b) {
		return true
	}
	if a.IsNull() && zeroish(b) {
		return true
	}
	if b.IsNull() && zeroish(a) {
		return true
	}
	if !a.IsKnown() || !b.IsKnown() || !a.Type().Equals(b.Type()) {
		return false
	}

	ty := a.Type()
	switch {
	case ty.IsObjectType():
		for name := range ty.AttributeTypes() {
			if !equivalent(a.GetAttr(name), b.GetAttr(name)) {
				return false
			}
		}
		return true
	case ty.IsListType(), ty.IsTupleType():
		if a.LengthInt() != b.LengthInt() {
			return false
		}
		for i := 0; i < a.LengthInt(); i++ {
			idx := cty.NumberIntVal(int64(i))
			if !equivalent(a.Index(idx), b.Index(idx)) {
				return false
			}
		}
		return true
	case ty.IsMapType():
		if a.LengthInt() != b.LengthInt() {
			return false
		}
		for it := a.ElementIterator(); it.Next(); {
			k, av := it.Element()
			if b.HasIndex(k).False() || !equivalent(av, b.Index(k)) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// zeroish reports whether a value is the empty or default form of its type.
func zeroish(v cty.Value) bool {
	if v.IsNull() {
		return true
	}
	if !v.IsKnown() {
		return false
	}
	ty := v.Type()
	switch {
	case ty == cty.Bool:
		return v.False()
	case ty == cty.String:
		return v.AsString() == ""
	case ty == cty.Number:
		return v.RawEquals(cty.Zero)
	case ty.IsCollectionType(), ty.IsTupleType():
		return v.LengthInt() == 0
	}
	return false
}

// shortValue renders a value for an error message, without letting a big
// nested block push the sentence off the screen.
func shortValue(v cty.Value) string {
	switch {
	case v == cty.NilVal:
		return "(absent)"
	case v.IsNull():
		return "null"
	case !v.IsKnown():
		return "(known after apply)"
	}
	s := v.GoString()
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}
