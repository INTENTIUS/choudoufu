// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #878, maintainer ruling on PR #889: the approval covers the
// planned VALUES too, so a change that keeps its address, its action and its
// live object and moves what it plans to write refuses like any other
// difference.
//
// # What is in the digest
//
// Both sides of every compared change: the after-values always, and the
// before-values too, so a drift in what a change is being made FROM is
// caught as well as a drift in what it is being made TO. Values are read at
// top-level attribute granularity against the provider schema the RUN has,
// with the approved plan and the fresh plan decoded through the same schema
// so the two renderings are comparable by construction.
//
// Rendering is canonical rather than byte-for-byte:
//
//   - Objects and maps are rendered with their keys sorted, so the order the
//     provider happened to hand them back is not a difference.
//   - Sets are rendered by sorting their ELEMENTS' renderings, because a set
//     has no order and two plans may walk one in either.
//   - Lists and tuples keep their order, because there the order is the
//     value.
//   - Every scalar carries a type tag, so the string "3" and the number 3
//     are not the same planned value.
//
// # What is NOT in the digest
//
//   - Unknown values ("known after apply"). An unknown renders as one
//     constant at any depth, and an attribute that is unknown on EITHER side
//     is dropped from the comparison entirely. So no refinement, length bound
//     or prefix an unknown happens to carry can produce a refusal, and an
//     attribute the provider will only settle at apply time can never make a
//     matched artifact refuse.
//   - Sensitive values in plaintext. A value marked sensitive at any depth is
//     rendered as "sensitive:sha256:<hex>" over the canonical rendering of
//     the unmarked value: stable across runs, so a moved secret still
//     refuses, and never printed.
//   - Anything from a change whose values could not be decoded against the
//     run's schema - a plan file written against a different provider version
//     is the case that matters. Those changes are still compared by address,
//     action and identity; their values are not, and [Values.Decoded] says
//     so rather than a silent empty digest saying "nothing moved".
//   - Timestamps, the state serial and lineage, the plan file's own metadata.
//     None of it describes what the apply will do.

// SchemaFor resolves the provider schema for one resource type, or nil when
// the run has none for it. Its shape is internal/command's own
// statefulMarkerGuard closure over tofu.Schemas.ResourceTypeConfig, so the
// one caller passes what it already builds.
type SchemaFor func(provider addrs.Provider, mode addrs.ResourceMode, typeName string) *providers.Schema

// unknownToken is what an unknown value renders as, at any depth and
// whatever it is refined to.
const unknownToken = "(known after apply)"

// Attr is one top-level attribute's canonical rendering.
type Attr struct {
	// Text is the canonical rendering.
	Text string

	// Unknown is true when the attribute is not wholly known. Such an
	// attribute is excluded from the comparison rather than compared as a
	// token, so an unknown can never be the reason a matched artifact
	// refuses.
	Unknown bool
}

// Values is one change's planned values, both sides, canonically rendered.
type Values struct {
	// Decoded is false when this change's values could not be read at all -
	// no schema for the type, or a decode failure against the schema the run
	// has. A change with Decoded false is compared by address, action and
	// identity only, and [CompareValues] returns nothing for it rather than
	// reporting a false agreement.
	Decoded bool

	// Before and After are the two sides, keyed by top-level attribute name.
	// Nil with the matching Null flag set when that side has no object at
	// all, which is the ordinary shape of a create's before and a destroy's
	// after.
	Before, After map[string]Attr

	// BeforeNull and AfterNull say that side had no object, which is a
	// different statement from "an object with no attributes".
	BeforeNull, AfterNull bool
}

// valuesOf renders both sides of one change.
func valuesOf(change *plans.ResourceInstanceChangeSrc, schemaFor SchemaFor) Values {
	if change == nil {
		return Values{}
	}
	var schema *providers.Schema
	if schemaFor != nil {
		schema = schemaFor(change.ProviderAddr.Provider, change.Addr.Resource.Resource.Mode, change.Addr.Resource.Resource.Type)
	}
	if schema == nil || schema.Block == nil {
		return Values{}
	}
	decoded, err := change.Decode(schema)
	if err != nil || decoded == nil {
		return Values{}
	}
	before, beforeNull := renderAttrs(decoded.Before)
	after, afterNull := renderAttrs(decoded.After)
	return Values{
		Decoded:    true,
		Before:     before,
		After:      after,
		BeforeNull: beforeNull,
		AfterNull:  afterNull,
	}
}

// renderAttrs renders an object value one attribute at a time. The second
// result is whether the object itself is null or unknown, in which case
// there are no attributes to render.
func renderAttrs(obj cty.Value) (map[string]Attr, bool) {
	if obj == cty.NilVal {
		return nil, true
	}
	// A whole-object mark is honoured before anything is looked inside, so a
	// configuration that marks a resource's entire value sensitive still
	// gets a stable digest and still leaks nothing.
	obj, objMarks := obj.Unmark()
	if !obj.IsKnown() || obj.IsNull() {
		return nil, true
	}
	ty := obj.Type()
	if !ty.IsObjectType() && !ty.IsMapType() {
		return nil, true
	}
	out := make(map[string]Attr)
	for it := obj.ElementIterator(); it.Next(); {
		k, v := it.Element()
		if k.IsNull() || !k.IsKnown() || k.Type() != cty.String {
			continue
		}
		text, unknown := renderValue(v)
		if isSensitive(objMarks) {
			text = digestOf(text)
		}
		out[k.AsString()] = Attr{Text: text, Unknown: unknown}
	}
	return out, false
}

// renderValue is the canonical rendering of one value. The second result is
// whether anything in it is unknown.
func renderValue(v cty.Value) (string, bool) {
	if v == cty.NilVal {
		return "absent", false
	}
	v, valMarks := v.Unmark()
	if isSensitive(valMarks) {
		inner, unknown := renderValue(v)
		return digestOf(inner), unknown
	}
	if !v.IsKnown() {
		// Deliberately one constant: an unknown's refinements are how much
		// the planner happens to have worked out, not part of what was
		// approved.
		return unknownToken, true
	}
	if v.IsNull() {
		return "null", false
	}

	ty := v.Type()
	switch {
	case ty == cty.String:
		return "s:" + v.AsString(), false
	case ty == cty.Number:
		return "n:" + v.AsBigFloat().Text('f', -1), false
	case ty == cty.Bool:
		if v.True() {
			return "b:true", false
		}
		return "b:false", false
	case ty.IsSetType():
		// A set has no order, so the elements' RENDERINGS are sorted. Two
		// plans that walked the same set in different orders must not read
		// as two different plans.
		parts, unknown := renderElements(v)
		sort.Strings(parts)
		return "set{" + strings.Join(parts, ",") + "}", unknown
	case ty.IsListType() || ty.IsTupleType():
		// Order is the value here, so it is kept.
		parts, unknown := renderElements(v)
		return "list[" + strings.Join(parts, ",") + "]", unknown
	case ty.IsMapType() || ty.IsObjectType():
		type kv struct{ k, v string }
		var pairs []kv
		anyUnknown := false
		for it := v.ElementIterator(); it.Next(); {
			k, elem := it.Element()
			text, unknown := renderValue(elem)
			anyUnknown = anyUnknown || unknown
			key := ""
			if k.IsKnown() && !k.IsNull() && k.Type() == cty.String {
				key = k.AsString()
			}
			pairs = append(pairs, kv{key, text})
		}
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i].k != pairs[j].k {
				return pairs[i].k < pairs[j].k
			}
			return pairs[i].v < pairs[j].v
		})
		var b strings.Builder
		b.WriteString("obj{")
		for i, p := range pairs {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, "%s=%s", p.k, p.v)
		}
		b.WriteString("}")
		return b.String(), anyUnknown
	default:
		// A type this renderer does not know how to walk is rendered by its
		// GoString, which is stable for a given value and never silently
		// equal to a different one.
		return "raw:" + v.GoString(), false
	}
}

func renderElements(v cty.Value) ([]string, bool) {
	var parts []string
	anyUnknown := false
	for it := v.ElementIterator(); it.Next(); {
		_, elem := it.Element()
		text, unknown := renderValue(elem)
		anyUnknown = anyUnknown || unknown
		parts = append(parts, text)
	}
	return parts, anyUnknown
}

func isSensitive(ms cty.ValueMarks) bool {
	_, ok := ms[marks.Sensitive]
	return ok
}

// digestOf is the stable stand-in a sensitive value gets. Full sha256, hex:
// long enough that two different secrets cannot collide into "nothing
// moved", and it never carries the value itself.
func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sensitive:sha256:" + hex.EncodeToString(sum[:])
}

// CompareValues names the attributes two renderings of the same change
// disagree about, sorted, side-prefixed ("after.tags", "before.name").
//
// It returns nothing when either side could not be decoded: a change whose
// values were never read must not be reported as agreeing OR as differing.
// An attribute unknown on either side is skipped for the reason the package
// doc gives.
func CompareValues(approved, fresh Values) []string {
	if !approved.Decoded || !fresh.Decoded {
		return nil
	}
	var out []string
	out = append(out, compareSide("before", approved.Before, fresh.Before, approved.BeforeNull, fresh.BeforeNull)...)
	out = append(out, compareSide("after", approved.After, fresh.After, approved.AfterNull, fresh.AfterNull)...)
	sort.Strings(out)
	return out
}

func compareSide(side string, approved, fresh map[string]Attr, approvedNull, freshNull bool) []string {
	if approvedNull != freshNull {
		// One plan has an object here and the other does not. That is the
		// whole side differing, and naming every attribute would bury it.
		return []string{side + " (one plan has no object here)"}
	}
	if approvedNull {
		return nil
	}
	names := make(map[string]struct{}, len(approved)+len(fresh))
	for k := range approved {
		names[k] = struct{}{}
	}
	for k := range fresh {
		names[k] = struct{}{}
	}
	var out []string
	for name := range names {
		a, inApproved := approved[name]
		f, inFresh := fresh[name]
		if !inApproved || !inFresh {
			out = append(out, side+"."+name)
			continue
		}
		if a.Unknown || f.Unknown {
			continue
		}
		if a.Text != f.Text {
			out = append(out, side+"."+name)
		}
	}
	return out
}
