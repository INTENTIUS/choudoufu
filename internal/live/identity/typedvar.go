// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/gocty"

	"github.com/intentius/choudoufu/internal/configs"
)

// This file is #251: the declared-type conversion a value owes when it is
// chased through a module variable.
//
// [resolver.namedDef]'s "var" case answers `var.x` inside a child module with
// the module CALL's argument expression, evaluated in the calling module.
// OpenTofu never uses that value as written. prepareFinalInputVariableValue
// (internal/tofu/eval_variable.go) applies the variable's optional-attribute
// defaults and then converts to its declared ConstraintType before anything
// inside the module sees anything, so `s = { a = "007" }` reaching a child
// that declares `type = map(number)` is the number 7 in there, not the string
// "007".
//
// While [resolver.staticCollElems] discarded values wholesale this cost
// nothing - a key with any value at all refused, and a refusal is never
// wrong, only unhelpful. Since the per-key each.value binding it grew, the
// raw value is rendered straight into an identity, so the divergence became a
// marker naming a queue that does not exist. The obvious version of the case
// does NOT diverge: map(string) given 443 renders "443" whichever side
// converts, because a cty number and its string form produce the same marker
// text. It takes a declared type whose conversion is not identity-on-
// re-render, a chase through a module variable, and a dynamic sibling to
// force the per-key fallback, all at once.

// varConvertedElems applies prepareFinalInputVariableValue's conversion to
// the per-key values [resolver.staticCollElems] chased through one module
// variable hop, and answers with values that agree with the ones OpenTofu
// puts inside the module.
//
// It converts the collection WHOLE rather than element by element, because
// that is what OpenTofu converts and the two are not the same question: an
// object that is missing an attribute its declared object type requires, or
// carries one the type does not have, fails as a whole while each of its
// present elements would convert perfectly well on its own. Rebuilding the
// container from the keys and values just chased, with cty.DynamicVal
// standing in for the values that did not prove, is what makes the whole
// conversion available here - and cty carries an unknown through a
// conversion as an unknown of the target type, so a sibling nothing here can
// read neither blocks the conversion nor contributes an answer to it.
//
// Three outcomes, and the middle one is the point:
//
//   - No declared type (a bare `variable "x" {}`, whose ConstraintType is
//     cty.DynamicPseudoType) converts nothing, so the values pass through
//     exactly as they arrived. So does a local, which has no declaration at
//     all and arrives here as a nil decl.
//   - A conversion that FAILS unbinds every value rather than falling back
//     to the raw one. OpenTofu rejects such a configuration outright; a
//     value it would have rejected must not become a marker, and unbinding
//     is how this file says "not proven" - the key survives, and any
//     identity that actually reads each.value for that key refuses.
//   - A conversion that succeeds binds each key to the value read back out
//     of the CONVERTED collection, so optional-attribute defaults, nested
//     conversions and the constraint itself are all applied by cty rather
//     than re-derived here.
//
// Keys are not touched. A for_each key is converted to a string on both
// sides, which is why the key set has been right through this hop all along
// (see #251's own note), and inventing a key set from a converted collection
// would be a second, separate claim.
//
// What this does NOT cover, deliberately: the same raw-value question
// applies to [resolver.resolveNamed]'s path, where `var.s.a` selects one item
// out of the same argument and renders it as an identity part. That path can
// reach a symbolic Formula over a managed resource's attribute, which is not
// a cty value and has no conversion, so covering it needs a rule about what
// a declared type means for the symbolic half - a separate question from
// this one.
func varConvertedElems(decl *configs.Variable, keys, vals []cty.Value) []cty.Value {
	if decl == nil {
		return vals
	}
	ty := decl.ConstraintType
	if ty == cty.NilType || ty == cty.DynamicPseudoType {
		// Nothing to convert to. prepareFinalInputVariableValue calls
		// convert.Convert with this same type and gets the value back
		// unchanged, so passing through is parity, not a shortcut.
		return vals
	}

	raw, ok := rebuiltContainer(keys, vals)
	if !ok {
		return unboundLike(vals)
	}
	given := raw
	if decl.TypeDefaults != nil {
		// Exactly where prepareFinalInputVariableValue applies them: to the
		// given value, before the conversion, and never to a null.
		given = decl.TypeDefaults.Apply(given)
	}
	conv, err := convert.Convert(given, ty)
	if err != nil {
		return unboundLike(vals)
	}
	return readBackElems(conv, keys, vals)
}

// unboundLike is a values slice of the same length holding nothing at all.
// cty.NilVal is this file's one "unproven" signal and every consumer reads it
// as "leave the binding out", which is what makes an unusable conversion cost
// a refusal rather than a wrong answer.
func unboundLike(vals []cty.Value) []cty.Value {
	return make([]cty.Value, len(vals))
}

// rebuiltContainer reassembles the collection the chase decomposed, so that
// the conversion can be applied to it as a whole.
//
// The shape follows the keys, because the keys are the ones cty itself binds:
// all-string keys are an object constructor's attribute names or a
// comprehension's key clause, and consecutive integer keys from zero are a
// tuple constructor's indices ([resolver.staticCollElems] produces no other
// key shape). Anything else - a gap, a repeat, a mixture - is not a container
// this can rebuild, and answering false there costs the values and no more.
//
// A value that did not prove becomes cty.DynamicVal. That is not a stand-in
// for an answer: an unknown converts to an unknown of the target type and is
// discarded again by [readBackElems], so it contributes nothing except its
// presence, which is the one thing about it the whole-value conversion needs.
func rebuiltContainer(keys, vals []cty.Value) (cty.Value, bool) {
	if len(keys) != len(vals) || len(keys) == 0 {
		return cty.NilVal, false
	}
	elems := make([]cty.Value, len(vals))
	for i, v := range vals {
		if v == cty.NilVal {
			elems[i] = cty.DynamicVal
			continue
		}
		elems[i] = v
	}

	// stringKeys carries the marked/null/unknown guard AsString needs; a key
	// this cannot read is a key it cannot rebuild a container around.
	if names, ok := stringKeys(keys); ok {
		attrs := make(map[string]cty.Value, len(names))
		for i, n := range names {
			attrs[n] = elems[i]
		}
		if len(attrs) != len(names) {
			// A repeated key: two elements would collapse into one attribute
			// and the readback could not tell which one it got. The producers
			// deduplicate already, so this is a backstop rather than a case.
			return cty.NilVal, false
		}
		return cty.ObjectVal(attrs), true
	}

	for i, k := range keys {
		// The same guard shape stringKeys uses, for the same reason:
		// gocty.FromCtyValue reads the value and a marked one must not reach
		// it. A key that is not exactly its own position is not a tuple
		// index, so the container cannot be rebuilt from it.
		if k.Type() != cty.Number || k.IsNull() || !k.IsKnown() || k.IsMarked() {
			return cty.NilVal, false
		}
		var n int
		if err := gocty.FromCtyValue(k, &n); err != nil || n != i {
			return cty.NilVal, false
		}
	}
	return cty.TupleVal(elems), true
}

// readBackElems takes each key's value out of the converted collection, in
// the caller's own key order.
//
// A value is bound only where the chase had proven one to begin with: the
// conversion is allowed to change a value, never to supply one. That keeps
// this file's rule intact - bind only what the resolver actually evaluated -
// with the conversion sitting inside it rather than beside it.
//
// A SET target is refused outright, and that is the one case here that is not
// obvious. A set has no positions: cty's conversion of a tuple to a set
// converts each element and then deduplicates and reorders, so the element
// that sits where key 0 sat is not knowable from the set. Worse, for a
// set(number) the elements ARE numbers and would RawEquals the integer keys
// by coincidence - [0, 5] converted to a set is {0, 5}, and key 0 would match
// element 0 while the value it actually chased was 5. Matching by key is only
// meaningful where the target keeps its keys, so a set answers nothing.
func readBackElems(conv cty.Value, keys, vals []cty.Value) []cty.Value {
	out := unboundLike(vals)

	// ContainsMarked before anything else reads the value, and before
	// IsWhollyKnown in particular, which iterates and panics on a marked
	// element; a mark hoists to the containing value only for a set, which is
	// cty's asymmetry and is asserted in internal/live/marksafe's
	// TestOnlySetsHoistElementMarks. Nothing marked can be here - every value
	// came through [resolver.provenValue], which already refuses one, and a
	// conversion introduces no marks - but the read happens here.
	if conv == cty.NilVal || conv.ContainsMarked() || conv.IsNull() || !conv.IsKnown() {
		return out
	}
	switch ty := conv.Type(); {
	case ty.IsMapType(), ty.IsObjectType(), ty.IsListType(), ty.IsTupleType():
		// Keyed by exactly the keys the chase produced, so a match means the
		// same element.
	default:
		// A set (see above) or a primitive, which has no elements to read at
		// all - a variable declared `type = string` whose argument was an
		// object is not a collection once converted, and OpenTofu would have
		// rejected it before that anyway.
		return out
	}

	convKeys := make([]cty.Value, 0, len(keys))
	convVals := make([]cty.Value, 0, len(keys))
	for it := conv.ElementIterator(); it.Next(); {
		k, v := it.Element()
		convKeys = append(convKeys, k)
		convVals = append(convVals, v)
	}

	for i, k := range keys {
		if i >= len(vals) || vals[i] == cty.NilVal {
			continue
		}
		for j, ck := range convKeys {
			if !ck.RawEquals(k) {
				continue
			}
			cv := convVals[j]
			// The same three tests [resolver.provenValue] applies to a value
			// before binding it, applied again to the converted one: a
			// sibling's unknown converts to an unknown here and must stay
			// unbound, and a conversion that produced a null produced nothing
			// an instance's each.value can be read from.
			if !cv.ContainsMarked() && !cv.IsNull() && cv.IsWhollyKnown() {
				out[i] = cv
			}
			break
		}
	}
	return out
}
