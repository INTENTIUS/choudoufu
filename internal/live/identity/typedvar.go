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
// KEYS come out of the converted collection too, which is the correction
// this function's first version needed. Its note claimed a key set survives
// the hop untouched because "a for_each key is converted to a string on both
// sides" - true of a map or an object, and false of a SET, where cty binds no
// key separate from the element. A set's element iterator yields the element
// in BOTH halves (cty/element_iterator.go), and hclsyntax.ForExpr.Value binds
// e.KeyVar to that verbatim, so `{ for k, v in var.s : "n-${k}" => v }` over a
// variable declared set(string) renders one instance per ELEMENT and names it
// after the element. Answering with the argument tuple's indices instead
// rendered n-0/n-1 for a two-element set - the wrong-marker shape, silent, and
// the same count by coincidence rather than by construction. Reading the keys
// off the converted value is the same thing
// [resolver.evaluatedCollElements] already does where the collection
// evaluates whole, so the two halves of the chase now answer alike.
//
// What this does NOT cover, deliberately: the same raw-value question
// applies to [resolver.resolveNamed]'s path, where `var.s.a` selects one item
// out of the same argument and renders it as an identity part. That path can
// reach a symbolic Formula over a managed resource's attribute, which is not
// a cty value and has no conversion, so covering it needs a rule about what
// a declared type means for the symbolic half - a separate question from
// this one.
func varConvertedElems(decl *configs.Variable, keys []cty.Value, elems []elemBinding) ([]cty.Value, []elemBinding, bool) {
	if decl == nil {
		return keys, elems, true
	}
	ty := decl.ConstraintType
	if ty == cty.NilType || ty == cty.DynamicPseudoType {
		// Nothing to convert to. prepareFinalInputVariableValue calls
		// convert.Convert with this same type and gets the value back
		// unchanged, so passing through is parity, not a shortcut - and it
		// is the case that carries the element EXPRESSIONS across the hop
		// intact, because nothing happened to them. Every other case below
		// drops them, which is #260's third wrinkle: once a declared type
		// applies, an attribute the constructor does not write may still be
		// present inside the module (optional(string, "tcp") supplies it),
		// so the constructor is no longer evidence of what the element has.
		return keys, elems, true
	}

	raw, ok := rebuiltContainer(keys, elems)
	if !ok {
		return unreadableConversion(ty, keys, elems)
	}
	given := raw
	if decl.TypeDefaults != nil {
		// Exactly where prepareFinalInputVariableValue applies them: to the
		// given value, before the conversion, and never to a null.
		given = decl.TypeDefaults.Apply(given)
	}
	conv, err := convert.Convert(given, ty)
	if err != nil {
		return unreadableConversion(ty, keys, elems)
	}
	return convertedElems(conv, keys, elems)
}

// unreadableConversion is what this file answers when the converted
// collection is not available to read - the container could not be rebuilt,
// or the conversion failed outright.
//
// For every target but a set that costs the values and no more: a map, an
// object, a list and a tuple all keep the keys they were given through a
// conversion, so the key set the chase already holds is still the right one
// and only the values are unproven.
//
// A SET target refuses instead. Its keys are its elements, so a set this
// could not read has no key set here at all, and the keys in hand - the
// argument's own indices or attribute names - are the wrong answer rather
// than an incomplete one. Refusing costs a resolution; answering would cost a
// marker naming something that does not exist.
func unreadableConversion(ty cty.Type, keys []cty.Value, elems []elemBinding) ([]cty.Value, []elemBinding, bool) {
	if ty.IsSetType() {
		return nil, nil, false
	}
	return keys, unboundLike(elems), true
}

// unboundLike is a bindings slice of the same length holding nothing at all -
// no value and no expression. cty.NilVal is this file's one "unproven" signal
// for a value and every consumer reads it as "leave the binding out", which
// is what makes an unusable conversion cost a refusal rather than a wrong
// answer; the expression is dropped alongside it for the same reason, since
// the syntax the caller wrote is not what a declared type puts inside the
// module.
func unboundLike(elems []elemBinding) []elemBinding {
	return make([]elemBinding, len(elems))
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
// discarded again by [convertedElems], so it contributes nothing except its
// presence, which is the one thing about it the whole-value conversion needs.
func rebuiltContainer(keys []cty.Value, bindings []elemBinding) (cty.Value, bool) {
	if len(keys) != len(bindings) || len(keys) == 0 {
		return cty.NilVal, false
	}
	elems := make([]cty.Value, len(bindings))
	for i, b := range bindings {
		if b.val == cty.NilVal {
			elems[i] = cty.DynamicVal
			continue
		}
		elems[i] = b.val
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

// convertedElems reads the converted collection's elements the way cty itself
// hands them out, keys and values together and in cty's own order.
//
// The previous version matched the chase's keys against the converted ones
// with RawEquals and kept the chase's key set whatever came back. That is
// only meaningful where the target KEEPS its keys, and it hid the set case
// twice over: a set has no positions at all, and for a set(number) the
// elements ARE numbers and RawEquals the integer indices by coincidence -
// [0, 5] converted to a set is {0, 5}, so index 0 matched element 0 while the
// value chased at that position was 5. Reading the iterator instead removes
// the question: cty synthesizes a StringVal for a map key or an object
// attribute name, a NumberIntVal for a list or tuple index, and the ELEMENT
// itself for a set (cty/element_iterator.go), which is exactly what
// hclsyntax.ForExpr.Value binds a comprehension's key variable to.
//
// A value is still bound only where the chase proved one. An unproven value
// entered the conversion as cty.DynamicVal, comes out unknown, and is dropped
// by the same three tests [resolver.provenValue] applies - so the conversion
// is allowed to CHANGE a value and never to supply one out of nothing. The
// one thing it does supply is a declared type's optional-attribute default,
// which is configuration the module author wrote and which OpenTofu really
// does put inside the module.
//
// A set whose membership is not wholly known refuses rather than answering.
// Two unknown elements are RawEquals to each other and collapse into one, so
// an unreadable element costs not just its own key but the COUNT - which is
// the one thing about a key set that must never be approximated. Every other
// container keeps its keys whatever its values do.
func convertedElems(conv cty.Value, keys []cty.Value, elems []elemBinding) ([]cty.Value, []elemBinding, bool) {
	if conv == cty.NilVal {
		return keys, unboundLike(elems), true
	}
	ty := conv.Type()

	// ContainsMarked before anything else reads the value, and before
	// IsWhollyKnown in particular, which iterates and panics on a marked
	// element; a mark hoists to the containing value only for a set, which is
	// cty's asymmetry and is asserted in internal/live/marksafe's
	// TestOnlySetsHoistElementMarks - and a set is precisely the type this
	// function exists for. Nothing marked can be here - every value came
	// through [resolver.provenValue], which already refuses one, and a
	// conversion introduces no marks - but the read happens here.
	if conv.ContainsMarked() || conv.IsNull() || !conv.IsKnown() {
		return unreadableConversion(ty, keys, elems)
	}
	switch {
	case ty.IsMapType(), ty.IsObjectType(), ty.IsListType(), ty.IsTupleType(), ty.IsSetType():
	default:
		// A primitive, which has no elements to read at all - a variable
		// declared `type = string` whose argument was an object is not a
		// collection once converted, and OpenTofu would have rejected it
		// before that anyway.
		return keys, unboundLike(elems), true
	}
	if ty.IsSetType() && !conv.IsWhollyKnown() {
		return nil, nil, false
	}

	outKeys := make([]cty.Value, 0, len(keys))
	outVals := make([]elemBinding, 0, len(keys))
	for it := conv.ElementIterator(); it.Next(); {
		k, v := it.Element()
		outKeys = append(outKeys, k)
		// The same three tests [resolver.provenValue] applies to a value
		// before binding it, applied again to the converted one: a sibling's
		// unknown converts to an unknown here and must stay unbound, and a
		// conversion that produced a null produced nothing an instance's
		// each.value can be read from.
		if v.ContainsMarked() || v.IsNull() || !v.IsWhollyKnown() {
			outVals = append(outVals, elemBinding{})
			continue
		}
		// Value only: the conversion is what the module sees, and the
		// pre-conversion expression is not it.
		outVals = append(outVals, elemBinding{val: v})
	}
	return outKeys, outVals, true
}
