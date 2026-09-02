// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the read-and-rewrite half of a tags-only object write: given
// a resource's schema and a live object, tell whether it carries a tags
// argument this package can act on, replace that argument, and tell whether
// a plan changed anything besides tags.
//
// Every function here mirrors one in
// [github.com/intentius/choudoufu/internal/live/mv]'s rewrite.go, which
// proved this exact pattern safe for a tags-only write through the provider
// protocol. They are not imported from there because mv's versions are
// unexported methods and functions private to its own *mover type - there is
// no seam to call through without exporting half of that package's internals
// for one caller. Duplicated deliberately, kept small, and covered by this
// package's own tests rather than assumed identical forever.

// taggable reports whether a resource type carries the tag map the marker
// spec describes. It is [markers.Taggable] and nothing else, for the reason
// internal/live/untag's taggable spells out: this is the predicate that
// decides whether a marker may be written onto a real object, and it had
// three independent implementations that fell a clause behind the one
// stamping uses.
func taggable(block *configschema.Block) bool { return markers.Taggable(block) }

// tagsFromObject reads the marker tags off a resource object read from the
// live system. "tags" is read after "tags_all" so that an explicitly set tag
// wins over the same key arriving through the provider's default_tags merge -
// the same precedence discovery reads listed objects with. The second
// return distinguishes "no tags attribute at all" from "tagged with
// nothing".
func tagsFromObject(schema providers.Schema, obj cty.Value) (map[string]string, bool) {
	if schema.Block == nil || !taggable(schema.Block) {
		return nil, false
	}
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

// assertedTagAttr is the one argument a tags-only write claims as set in the
// configuration it synthesizes. It is the same attribute [taggable] and
// [withTags] act on, named once here so [configValue]'s rule can be stated as
// "everything the provider may compute is null except the argument being
// written".
//
// Not merely defensive: [markers.TagSurface] admits a tags map that is
// Optional AND Computed - only a Computed-only one is refused - so a provider
// release that marks tags optional+computed would otherwise have
// [computedForProvider] null out the very tag map the write exists to set,
// turning every stamp on that type into a silent no-op. No type in
// hashicorp/aws 6.59.0 declares tags that way today; the exemption is what
// keeps that from becoming a discovery made in production.
const assertedTagAttr = "tags"

// configClaim is how much of the live object a synthetic configuration
// claims the operator wrote down. There is no way to know: the resource
// being written has no configuration, which is the whole reason this file
// invents one, and the two honest readings of "what would the HCL have said"
// disagree about the attributes a provider may fill in for itself.
// [syntheticConfigs] offers both, least first, and lets the provider decide.
type configClaim int

const (
	// claimTagsOnly nulls every Computed attribute, not only the
	// Computed-only ones. An optional+computed argument the operator never
	// wrote is null in real HCL and the provider computes it; carrying its
	// read-back value across asserts the opposite, that the operator wrote
	// down whatever the cloud happens to hold. Providers read the config
	// back (SDKv2's GetRawConfig, the framework's Config) and some gate on
	// it: a CustomizeDiff that refuses an argument as "not supported with"
	// some other setting reads known-and-non-null as "explicitly set" and
	// refuses a tag write that never touched the argument. GitHub issue
	// #373, on a NAT gateway whose secondary_private_ip_address_count came
	// back populated once the emulator's read grew accurate enough to
	// return it, on an estate whose HCL has never mentioned the argument
	// and which stock plans and applies without complaint.
	claimTagsOnly configClaim = iota

	// claimEverythingSettable nulls only the attributes a configuration
	// cannot set at all, and carries every settable one across. It is what
	// this file did before #373, and it is still right for the other half
	// of the problem: an optional+computed attribute a provider INJECTS
	// rather than reads - hashicorp/aws 6's per-resource `region`, on every
	// regional type - goes unknown in a plugin-framework plan when the
	// config omits it, and the provider's own force-new-if-region-changes
	// check reads unknown-against-a-known-state as a change. Measured: a
	// claimTagsOnly config makes aws_batch_job_queue plan
	// `.region` as requiring replacement on corpus-overture-tiles, an
	// estate whose HCL sets no region and which stock replans empty.
	claimEverythingSettable
)

// syntheticConfigs is the configurations a tags-only write may offer the
// provider for one object, in the order to try them: the one claiming least
// first.
//
// Neither claim is right for every provider, and no property of the schema
// separates the two cases above - `region` and
// secondary_private_ip_address_count are both optional+computed scalars, and
// the only thing that distinguishes them is what the provider does with the
// config it reads back. So this asks rather than guesses. The caller plans
// each in turn and takes the first whose plan is a clean tags-only change;
// the guards that decide "clean" - no replacement, nothing changed outside
// the tags - are the same ones that already stood between a plan and an
// apply, so a configuration can only be accepted here on the same terms as
// before. When the two agree, which is every type with no optional+computed
// attribute at all, there is one.
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
// object's value across.
//
// Under [claimEverythingSettable] that is "a configuration could not have set
// this": Computed and neither Optional nor Required. Under [claimTagsOnly] it
// widens to "the provider is free to fill this in for itself", which is every
// Computed attribute.
//
// One exception, and it is about objchange rather than about providers: an
// optional+computed attribute with a NestedType is left to
// [configNestedObject] instead of nulled wholesale, because
// objchange.optionalValueNotComputable treats a null config for such an
// attribute whose prior holds any non-computed value as a deliberate removal
// and proposes null. That moves the plan, and a moved plan is refused rather
// than applied - so nulling the container could only ever cost this path the
// write. Recursing nulls the computed leaves inside it and leaves the
// container asserted, which is what a configuration that wrote the block
// says.
func computedForProvider(attr *configschema.Attribute, claim configClaim) bool {
	switch {
	case !attr.Computed:
		return false
	case !attr.Optional && !attr.Required:
		// Provider-only. Nulled whatever its shape, nested types included,
		// under either claim, exactly as this function's predecessor did.
		return true
	case claim != claimTagsOnly:
		return false
	default:
		return attr.NestedType == nil
	}
}

// configValue is the live object as a configuration making the given claim
// would express it. Types are never altered - only values are nulled - so the
// result still conforms to the schema's implied type.
//
// [assertedTagAttr] is the one argument a tags-only write is always claiming
// as set, whatever the claim; everything else follows
// [computedForProvider].
//
// Nulling a Computed attribute cannot move the plan on its own:
// [objchange.ProposedNew] answers the prior value for a Computed attribute
// whose config is null, so the proposed object - and therefore the planned
// object, and therefore what is written - is the same under either claim.
// What changes is the raw config the provider reads back, and what the
// provider then decides to do about it.
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
// object rather than a block. There is no tag map inside a nested object -
// the marker surface is a top-level attribute - so it has no exemption.
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
// untouched.
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

// ---------------------------------------------------------------------------
// Attribute-level comparison, for both drift reporting and the
// tags-only-change assertion
// ---------------------------------------------------------------------------

// changedAttrs names the top-level arguments whose value differs between
// prior and next, skipping any name in skip, each rendered
// "name (prior -> next)".
func changedAttrs(block *configschema.Block, prior, next cty.Value, skip map[string]bool) []string {
	if prior == cty.NilVal || prior.IsNull() || next == cty.NilVal || next.IsNull() {
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
		if skip[name] {
			continue
		}
		if !prior.Type().HasAttribute(name) || !next.Type().HasAttribute(name) {
			continue
		}
		p := prior.GetAttr(name)
		n := next.GetAttr(name)
		if !n.IsWhollyKnown() {
			// Unknown is "I will fill this in", which is what a computed
			// attribute derived from tags looks like at plan time; not a
			// difference this pass can act on.
			continue
		}
		if !equivalent(p, n) {
			out = append(out, fmt.Sprintf("%s (%s -> %s)", name, shortValue(p), shortValue(n)))
		}
	}
	return out
}

// driftedAttrs is [changedAttrs] for verification: every attribute but
// tags_all (the provider's own computed rollup of tags plus default_tags,
// which moves whenever tags does and is not a separate signal) is worth
// reporting as drift, tags included - a genuine mismatch there is real
// information about what an operator is about to make authoritative.
func driftedAttrs(block *configschema.Block, prior, live cty.Value) []string {
	return changedAttrs(block, prior, live, map[string]bool{"tags_all": true})
}

// changedOutsideTags is [changedAttrs] for the stamp write's own assertion:
// both tags and tags_all are expected to move, since this run is the one
// changing them, so only a change somewhere else means the write proposes
// more than a tags-only rewrite.
func changedOutsideTags(block *configschema.Block, prior, planned cty.Value) []string {
	return changedAttrs(block, prior, planned, map[string]bool{"tags": true, "tags_all": true})
}

// equivalent reports whether two values say the same thing about the cloud.
//
// It is RawEquals plus one allowance for a real thing legacy-SDK providers
// do: an object read back from the cloud carries null for an argument nobody
// set, while a plan for that object carries the schema's zero default
// instead. Treating that as a change would make every legacy-SDK resource
// look drifted for no observable reason. See
// [github.com/intentius/choudoufu/internal/live/mv]'s rewrite.go, which
// documents the same allowance for the same reason, in more detail.
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
	if a.IsNull() || b.IsNull() {
		// One side is null and the other is not the zero form of its type,
		// so they say different things. Return here rather than falling
		// through: the collection arms below call LengthInt, ElementIterator
		// and GetAttr, every one of which panics on a null value, and a
		// panic reaches the operator as an OpenTofu crash report. Found by
		// live/e2e/corpus-autoscaling-complete, whose second live-import
		// against an already-stamped estate crashed in the "ratify" pass on
		// a null map(string) read back opposite a populated one.
		return false
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
