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

// configValue is the live object as a configuration would express it: every
// attribute the provider alone can set is nulled, and everything else is
// carried across as it stands. This is what makes the plan below a
// tags-only change rather than an assertion that the operator wrote down
// every computed attribute the provider filled in.
func configValue(block *configschema.Block, val cty.Value) cty.Value {
	if block == nil || val == cty.NilVal || val.IsNull() || !val.IsKnown() {
		return val
	}

	vals := make(map[string]cty.Value, len(block.Attributes)+len(block.BlockTypes))
	for name, attr := range block.Attributes {
		v := val.GetAttr(name)
		switch {
		case attr.Computed && !attr.Optional && !attr.Required:
			vals[name] = cty.NullVal(v.Type())
		case attr.NestedType != nil:
			vals[name] = configNestedObject(attr.NestedType, v)
		default:
			vals[name] = v
		}
	}
	for name, nested := range block.BlockTypes {
		v := val.GetAttr(name)
		switch nested.Nesting {
		case configschema.NestingSingle, configschema.NestingGroup:
			vals[name] = configValue(&nested.Block, v)
		default:
			vals[name] = mapElements(v, func(elem cty.Value) cty.Value {
				return configValue(&nested.Block, elem)
			})
		}
	}
	return cty.ObjectVal(vals)
}

// configNestedObject is configValue for an attribute whose type is a nested
// object rather than a block.
func configNestedObject(obj *configschema.Object, val cty.Value) cty.Value {
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
			case attr.Computed && !attr.Optional && !attr.Required:
				vals[name] = cty.NullVal(av.Type())
			case attr.NestedType != nil:
				vals[name] = configNestedObject(attr.NestedType, av)
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
