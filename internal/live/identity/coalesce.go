// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
)

// This file is one rule about WHICH ARGUMENT of coalesce() the language
// selects, not a rule about what coalesce() computes. It is the third of
// the same move: [resolver.resolveConditional] decides `cond ? A : B` by
// evaluating cond, [resolver.resolveFallbackChain] decides `try(A, B, ...)`
// by asking which arguments provably error, and this one decides
// `coalesce(A, B, ...)` by asking which arguments are provably empty. All
// three then hand the selected sub-expression straight back to
// [resolver.resolveExpr], so every existing rule - parentPart's
// registered-IdentityAttrs check, the record-backed branch, the
// provider-schema Computed boundary in [resolver.siblingLiteralExpr] -
// applies to it exactly as it would had the author written that argument on
// its own. Nothing here decides what a value IS; it decides which
// expression the identity is built from.
//
// fallback.go's own closing note said coalesce() "selects on null rather
// than on error and is a different question." It is, and this is that
// question's answer.
//
// # The semantics being modelled
//
// [funcs.CoalesceFunc] walks its arguments in order and returns the first
// that is neither null nor - when the call's unified return type is string,
// which every identity component's is - the empty string. An UNKNOWN
// argument stops the walk and makes the whole call unknown, which is what
// stock OpenTofu shows at plan time for an argument reaching an
// unapplied resource.
//
// Plan time is not the question this package asks. It asks what the value
// will BE once applied, because that is what a marker has to name, so the
// argument stock leaves unknown is precisely the one whose value this
// package may already know symbolically - as a [Formula] over a parent it
// will read, or over a record-backed parent the estate's record store
// hydrates before any formula renders.
//
// # The rule, stated symmetrically
//
// An argument is SKIPPED only when its apply-time value is provably null or
// the empty string, and SELECTED only when its apply-time value is provably
// a non-null, non-empty string. Anything else is undecidable, and an
// undecidable argument declines the whole call rather than falling through
// to the next one - a fall-through past an argument that turns out to be
// set would build the identity from the wrong expression entirely.
//
// There are exactly two proofs, and each covers one half of what an
// argument can be:
//
//   - Static evaluation. An argument this package can evaluate outright is
//     decided by its own value: a known null or "" is skipped, any other
//     known unmarked value is selected. An evaluation that does not settle
//     to a known unmarked value decides nothing.
//   - Symbolic resolution carrying a non-empty literal. An argument that
//     resolves to [Part]s is a string this package will render, and it is
//     provably non-empty exactly when one of those parts is a non-empty
//     literal - `"${random_pet.this.id}-lambda-simple"` and
//     `"/aws/lambda/${var.function_name}"` both are, whatever their parents
//     turn out to hold. A formula that is nothing but parent references is
//     NOT proven: a parent attribute that came back empty would make
//     coalesce() skip the argument at apply while this package had already
//     built the marker from it, which is a wrong marker rather than a
//     missing one.
//
// The asymmetry between the two proofs is the point rather than an
// oversight. Skipping needs a value and only static evaluation produces
// one; selecting needs non-emptiness and a formula can prove that without
// producing a value at all.

// resolveSelection is the second door onto the two selection rules, for an
// expression [resolver.isSymbolic] did not send to the symbolic switch at
// all.
//
// isSymbolic reads the ROOTS of an expression's traversals, and `var` and
// `local` are never among the symbolic ones - correctly, because the
// ordinary answer for both is to evaluate them. So a conditional or a
// coalesce() whose managed resource is reached through a module argument or
// a local value looks entirely static, fails to evaluate as a whole, and
// then had nothing left to try: the structural decomposition that would
// have found the reference lives in a switch it never reaches.
//
// [resolver.namedLeaf] runs immediately before this and is what makes the
// gap visible rather than theoretical - it chases exactly those two roots,
// and it recognizes a template but not a selection, so
// `"${var.function_name}-logs"` decomposed and
// `coalesce(var.role_name, var.function_name, "*")` did not.
//
// try() is deliberately not offered here even though the symbolic switch
// offers it. Its selector is "does this argument raise an error", and
// [resolver.fallbackArmVerdict] decides a non-symbolic argument by
// evaluating it - which, on this path, has already failed for every
// argument by construction. It could only ever return undecidable.
//
// applicable false leaves the caller's own diagnostic standing, unchanged.
func (r *resolver) resolveSelection(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (parts []Part, ok bool, applicable bool) {
	switch e := expr.(type) {
	case *hclsyntax.ConditionalExpr:
		// Taken over whole, the way [resolver.namedLeaf] takes over a
		// template: the shape is recognized, so whatever
		// resolveConditional decides - including its own refusal for a
		// condition that will not evaluate - is the answer that belongs to
		// this expression.
		got, gotOK := r.resolveConditional(e, scope, ident)
		return got, gotOK, true
	case *hclsyntax.FunctionCallExpr:
		return r.resolveCoalesceCall(e, scope, ident)
	}
	return nil, false, false
}

// coalesceArm is what this package can prove about one coalesce() argument.
type coalesceArm int

const (
	// coalesceUndecidable is the default and the safe one: nothing here can
	// say whether the language would select this argument, so the whole
	// call declines.
	coalesceUndecidable coalesceArm = iota

	// coalesceSkipped means the argument's apply-time value is provably
	// null or the empty string, so coalesce() moves past it.
	coalesceSkipped

	// coalesceSelected means the argument's apply-time value is provably a
	// non-null, non-empty string, so coalesce() stops here and this
	// argument's value is the call's.
	coalesceSelected
)

// resolveCoalesceCall recognizes coalesce(...) and resolves it to whichever
// of its arguments the language would select.
//
// applicable is false whenever the shape is not this at all, or whenever
// any argument up to and including the selected one cannot be decided: the
// caller's own diagnostic stands unreplaced, the same contract
// [resolver.resolveFallbackChain] and [resolver.resolveArityCollapse] have.
// When applicable is true, ok reports whether resolution succeeded and a
// diagnostic is already recorded in its place when it did not.
//
// Every declining path restores r.diags and r.pendingSiblingApply to what
// they were on entry, so a probe that failed leaves nothing behind for the
// caller's own refusal to be confused with.
func (r *resolver) resolveCoalesceCall(call *hclsyntax.FunctionCallExpr, scope instScope, ident configs.StaticIdentifier) (parts []Part, ok bool, applicable bool) {
	if call.Name != "coalesce" || len(call.Args) == 0 {
		return nil, false, false
	}

	mark := len(r.diags)
	sibMark := len(r.pendingSiblingApply)
	decline := func() ([]Part, bool, bool) {
		r.diags = r.diags[:mark]
		r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
		return nil, false, false
	}

	for _, arm := range call.Args {
		// The value probe first, because it is the only one that can prove
		// an argument SKIPPED. It leaves no diagnostic behind either way: a
		// failure here means "no value", not "this configuration is wrong",
		// which is the same rollback [resolver.fallbackArmVerdict] and
		// [resolver.soleElementFromValue] use for the same reason.
		valMark := len(r.diags)
		val, evalOK := r.evalStatic(arm, scope, ident)
		r.diags = r.diags[:valMark]
		if evalOK {
			switch coalesceValueArm(val) {
			case coalesceSkipped:
				continue
			case coalesceSelected:
				// An ordinary static value, handed back whole so that
				// [resolver.stringValueIn]'s own rules - the sensitive-value
				// refusal above all - apply to it unchanged.
				got, gotOK := r.resolveExpr(arm, scope, ident)
				return got, gotOK, true
			default:
				return decline()
			}
		}

		// No value. The argument may still be a string this package can
		// render symbolically, which is the whole shape this file exists
		// for; it is selected only if that rendering is provably non-empty.
		got, gotOK := r.resolveExpr(arm, scope, ident)
		if !gotOK || !partsProvablyNonEmpty(got) {
			return decline()
		}
		return got, true, true
	}

	// Every argument is provably skipped, which means coalesce() itself
	// raises "no non-null, non-empty-string arguments" and this
	// configuration does not plan under stock OpenTofu either. Nothing is
	// claimed about it here.
	return decline()
}

// coalesceValueArm decides one argument from a value [resolver.evalStatic]
// produced for it.
//
// Marked and unknown both decide nothing rather than deciding "skip": a
// sensitive value's contents are not readable here, and an unknown one is
// exactly the argument whose apply-time value this probe cannot see. Null
// is skipped in every return type; the empty string only where the value
// really is a string, which is what coalesce()'s own retType == cty.String
// guard tests and what keeps a numeric 0 selected rather than skipped.
func coalesceValueArm(val cty.Value) coalesceArm {
	// ContainsMarked before anything else, and before IsKnown: a mark on an
	// element hoists to the containing value only for a set, so a marked
	// string inside a list leaves the outer value unmarked and a later
	// AsString would panic. The asymmetry is cty's and is asserted in
	// internal/live/marksafe's TestOnlySetsHoistElementMarks.
	if val == cty.NilVal || val.ContainsMarked() || !val.IsKnown() {
		return coalesceUndecidable
	}
	if val.IsNull() {
		return coalesceSkipped
	}
	if val.Type().Equals(cty.String) && val.AsString() == "" {
		return coalesceSkipped
	}
	return coalesceSelected
}

// partsProvablyNonEmpty reports whether a resolved argument's rendering
// cannot be the empty string, whatever its parents turn out to hold.
//
// A [Part] is either a literal or a promise to read one attribute of one
// parent, and the rendering is their concatenation, so a single non-empty
// literal settles it. A formula made only of parent references does not:
// see this file's own note on why that is a wrong marker rather than a
// missing one.
func partsProvablyNonEmpty(parts []Part) bool {
	for _, p := range parts {
		if p.Parent == nil && p.Literal != "" {
			return true
		}
	}
	return false
}
