// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// callerVariables builds the var.* answer a module INSTANCE should get:
// its caller's argument expressions, evaluated in the scope that caller's
// own module call actually has - including the call's own each.key,
// each.value or count.index.
//
// The module tree's built-in answer cannot carry that. [Module.ModuleCalls]
// freezes one variables closure per call at load time
// ([ModuleCall.decodeStaticVariables]), against the parent's evaluator as
// it existed before any instance of anything had been resolved, so it
// answers every var.* with no repetition data at all - forever, for every
// instance of a for_each'd call alike.
//
// [resolver.namedDef] already works around that, but only for references
// [resolver.resolveExpr] decomposes down to a bare traversal: it
// recognises the reference itself, climbs to the caller and evaluates the
// argument expression with the call's repetition data threaded in by hand.
// A string template IS decomposed that way, so it keeps working; a
// FunctionCallExpr is not. Put the reference inside one and namedDef is
// never consulted, evaluation goes straight to [configs.StaticEvaluator],
// and var.X comes back through the frozen closure:
//
//	Unable to use each.value in static context, which is required by
//	module.user:var.name
//
// which is #252's shape A: 21 of the 53 sites its `other` bucket holds,
// across 5 corpus configurations. 18 of the 21 clear here. The 3 that
// remain are .corpus/iam/examples/iam-role's `policies =
// each.value.policies`, whose call's for_each map has a
// data.aws_caller_identity value in it - so the key-set widening in
// [ChildModuleRepetitionData] proves each.key and leaves each.value at
// cty.NilVal, and the reference refuses because the value genuinely is not
// known, not because of anything this file does.
//
// So rebuild the closure per instance instead, through
// [configs.ModuleCall.VariablesUsing] against a parent evaluator that has
// the repetition data - the same seam and the same construction
// internal/live/dataread's liveModuleEvaluator already uses one axis over,
// for the data-lookup case (#212).
//
// Nil for the root module, which has no caller, and nil whenever the
// call's own repetition data cannot be PROVEN:
// [ChildModuleRepetitionData] re-derives it from the call's own
// count/for_each expression and reports false rather than trusting the
// instance key it was handed, so a call this cannot account for keeps the
// frozen closure and refuses exactly where it always did.
func (r *resolver) callerVariables(modInst addrs.ModuleInstance) configs.StaticModuleVariables {
	if len(modInst) == 0 || !r.pathRepeats(modInst) {
		return nil
	}
	parentInst, callInst := modInst.CallInstance()
	parentCfg, ok := ConfigForModule(r.rootCfg, parentInst)
	if !ok || parentCfg.Module == nil {
		return nil
	}
	mc, ok := parentCfg.Module.ModuleCalls[callInst.Call.Name]
	if !ok || mc.Config == nil {
		return nil
	}

	parentEval := r.moduleEvaluator(parentInst)
	if parentEval == nil {
		return nil
	}

	if mc.Count != nil || mc.ForEach != nil {
		// callInst.Key is the key THIS instance was built with (see
		// [resolver.walkModule]'s modInst.Child(name, key)), so two
		// instances of one call never reach each other's data - and
		// ChildModuleRepetitionData verifies it against the call's own
		// expression rather than taking it on trust.
		rd, ok := ChildModuleRepetitionData(r.ctx, parentCfg.Module, childSubject(callInst.Call.Name), mc.Count, mc.ForEach, callInst.Key)
		if !ok {
			return nil
		}
		parentEval = parentEval.WithRepetitionData(rd)
	}

	return mc.VariablesUsing(r.ctx, parentEval)
}

// pathRepeats reports whether any module call on the way down to modInst
// carries its own count or for_each.
//
// It is what confines this whole mechanism to the shape #252 is about.
// When no call on the path repeats, the module tree's frozen closure
// already answers every var.* exactly as a rebuilt one would - there is no
// repetition data to be missing - so rebuilding could only change the
// answer along axes that are not this issue's (the rebuilt parent
// evaluator is pure and carries the parent instance's data-read coverage,
// where the frozen one is neither). Declining here keeps those axes
// untouched for the module trees that cannot need them, and skips the work
// as well: the great majority of module instances in the corpus sit under
// no repeating call at all.
func (r *resolver) pathRepeats(modInst addrs.ModuleInstance) bool {
	cur := r.rootCfg
	for _, step := range modInst {
		if cur == nil || cur.Module == nil {
			return false
		}
		if mc, ok := cur.Module.ModuleCalls[step.Name]; ok && (mc.Count != nil || mc.ForEach != nil) {
			return true
		}
		cur = cur.Children[step.Name]
	}
	return false
}

// moduleEvaluator is the evaluator one module instance's own expressions
// are read through, built the way [resolver.enterModuleAt] builds
// [resolver.eval] - purity, then that instance's data-read coverage, then
// its caller's variables - and is what callerVariables hands to the level
// below it. The two recur into each other, terminating at the root, so a
// module three calls deep sees each ancestor call's own repetition rather
// than only its immediate parent's.
//
// It reads nothing from the resolver's current position (r.mod, r.eval and
// friends), which is what makes it safe to call from inside enterModuleAt
// while those are mid-assignment.
func (r *resolver) moduleEvaluator(modInst addrs.ModuleInstance) *configs.StaticEvaluator {
	cfg, ok := ConfigForModule(r.rootCfg, modInst)
	if !ok || cfg.Module == nil || cfg.Module.StaticEvaluator == nil {
		return nil
	}
	// Pure for the same reason enterModuleAt is: an identity is a claim
	// about which cloud object a block owns, and an argument expression
	// that answers differently every call cannot make one. See impure.go.
	eval := cfg.Module.StaticEvaluator.Pure()
	if lookup := r.dataLookupFor(modInst); lookup != nil {
		eval = eval.WithDataResults(lookup)
	}
	if vars := r.callerVariables(modInst); vars != nil {
		eval = eval.WithVariables(vars)
	}
	return eval
}
