// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"

	"github.com/intentius/choudoufu/internal/lang/funcs"
)

// Issue #193's fix class (d): length() and keys() over an UNEXPANDED
// managed resource reference, the one gap managedproj.go's own doc comment
// names and leaves open ("What is deliberately not projected"). Both
// functions read an object's TYPE - its element count, its attribute names
// - not its values, so [unprojectedAttr]'s unknown value never reaches
// either one: IsWhollyKnown, the guard that stops a whole-object use, never
// gets a chance to fire. length() answers len(common)+1 instead of the real
// schema's attribute count, and keys() is worse - it hands back
// [unprojectedAttr]'s own literal name as if it were one of the block's
// arguments, an internal implementation detail reaching a real provider
// request.
//
// [configs.lookupCoversTraversal] cannot draw this refusal on its own: a
// whole-object use of an unexpanded block and
// aws_instance.nodes[each.key].subnet_id over a for_each-expanded one both
// arrive there with an empty remaining traversal and an object-typed
// value - syntactically identical shapes, so any rule stated purely there
// would refuse the legitimate for_each case too. Only [managedProjector]
// itself knows which shape it built, and the one place that fact can still
// be read back out, after the value has already been handed to whatever
// expression referenced it, is the value's own TYPE: [build]'s unexpanded
// (no count, no for_each) case is the only one that returns a plain
// cty.ObjectVal carrying [unprojectedAttr] as one of ITS OWN top-level
// attributes. A count-expansion returns a tuple - length()/keys() see a
// tuple, not an object, and tuples carry no such attribute at all. A
// for_each-expansion returns an object keyed by the block's own each.key
// strings; [unprojectedAttr] sits one level down, inside each key's own
// value, never as a top-level attribute of the outer object (unless a
// for_each key is itself the literal string "//unprojected", a
// pathological collision no real corpus configuration has - and even then
// the only cost is a spurious refusal, never a wrong value).
//
// That is a shape check, not a value check, so it holds in both of
// managedproj.go's modes: offline (materialize false) leaves every common
// attribute cty.DynamicVal but keeps every attribute NAME, [unprojectedAttr]
// included, so the coverage phase ([Analyze]) sees exactly the same shape
// the read phase ([Read]) does and the two can never disagree about
// eligibility - the same guarantee the rest of this package's two-mode
// design promises.
//
// Computing the CORRECT answer instead of refusing was considered and
// rejected: it would need the resource type's full provider schema (not
// just the block's own configured arguments) threaded into
// [managedProjector], and - more fundamentally - it would have to widen the
// projected object's own TYPE to carry every schema attribute, known or
// not, which is the one thing managedproj.go's doc says this projection
// deliberately does NOT do (a provider-assigned attribute like .arn stays
// absent so a reference to it refuses at coverage, not at IsWhollyKnown).
// Widening the shape to answer length()/keys() correctly would change what
// EVERY reference into the object sees, not just these two functions -
// far more blast radius than the bug being fixed. Refusing cleanly, in
// this package's own "is not statically evaluable" vocabulary, is what
// stays inside the shape managedproj.go already commits to.
const arityRefusal = "%[1]s() is called on a managed resource block that was never expanded (no count, no for_each), over its whole-object projection; this projection deliberately does not carry the resource's full provider schema, so %[1]s() cannot answer about it correctly and is refused rather than answered wrong. Reference one of the block's own arguments instead (its subnet_id, say, rather than the block itself), or give the block a count or for_each so %[1]s() can answer about its instances."

// arityGuardedFunctions returns the "length" and "keys" entries this
// package's evaluators install in place of the stock ones (see
// [configs.StaticEvaluator.WithFunctionOverrides] and
// [liveModuleEvaluator]), so that either function applied to an unexpanded
// managed resource's own projection refuses instead of answering about the
// projection's truncated shape. See this file's own doc comment for why
// the guard belongs here and not at [configs.lookupCoversTraversal].
func arityGuardedFunctions() map[string]function.Function {
	return map[string]function.Function{
		"length": guardArity("length", funcs.LengthFunc),
		"keys":   guardArity("keys", stdlib.KeysFunc),
	}
}

// guardArity wraps base (the stock cty/HCL function) so that its Type
// step - which [function.Function.Call] always runs before Impl, and which
// aborting with an error skips Impl for entirely - checks the sole
// argument's shape first. A value that is not the unexpanded managed
// projection's own shape passes straight through to base, unchanged in
// every respect: same Params, same VarParam, same Type, same Impl, same
// error text for anything base would itself have refused.
func guardArity(name string, base function.Function) function.Function {
	return function.New(&function.Spec{
		Params:   base.Params(),
		VarParam: base.VarParam(),
		Type: func(args []cty.Value) (cty.Type, error) {
			if len(args) > 0 && isUnexpandedProjection(args[0]) {
				return cty.NilType, fmt.Errorf(arityRefusal, name)
			}
			return base.ReturnTypeForValues(args)
		},
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			// Unreachable through function.Function.Call when Type above
			// has already errored - Call returns before Impl ever runs.
			// Kept as a direct guard anyway so this function is correct
			// even if called some other way (as some of this package's own
			// tests do, straight against the function.Function value).
			if len(args) > 0 && isUnexpandedProjection(args[0]) {
				return cty.NilVal, fmt.Errorf(arityRefusal, name)
			}
			return base.Call(args)
		},
	})
}

// isUnexpandedProjection reports whether v is [managedProjector]'s own
// unexpanded-block shape: an object type carrying [unprojectedAttr] as one
// of its own top-level attributes. See this file's doc comment for why
// that is the exact, non-ambiguous signal, and why it does not fire for
// [managedProjector]'s count- or for_each-expanded shapes.
func isUnexpandedProjection(v cty.Value) bool {
	ty := v.Type()
	return ty.IsObjectType() && ty.HasAttribute(unprojectedAttr)
}
