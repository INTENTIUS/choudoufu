// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/hashicorp/hcl/v2"

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
// Issue #315 widens the SAME key-set-only fallback one step further: when
// the call's for_each is a `{ for k, v in SRC : k => v if ... }`
// passthrough whose SOURCE has one genuinely unprovable attribute
// (#308's own shape), every one of this call's OWN argument expressions
// that reads each.value.<attr> - not merely var.name/local.name - used to
// refuse wholesale, because EachValue stayed cty.NilVal even though most
// individual attributes were themselves plain literals sitting beside the
// one unprovable one. mc.Config's own attribute expressions are passed as
// consumers below so [ChildModuleRepetitionData] can answer each.value
// projected down to only the fields those arguments actually read
// ([referencedEachValueAttrs], [eachValueAttrs]) - never the whole entry,
// never a guess at an attribute nothing here asked for.
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
	if len(modInst) == 0 || !r.frozenClosureIsStale(modInst) {
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
		//
		// consumers - issue #315 - is every one of this call's OWN
		// argument expressions, the full set var.* lookups against the
		// closure below could reach. The rebuilt eval it produces is
		// installed once and reused for whichever variable a later
		// lookup asks for, so the each.value projection has to cover
		// every argument that might read it, not just one.
		var consumers []hcl.Expression
		if mc.ForEach != nil {
			if attrs, diags := mc.Config.JustAttributes(); !diags.HasErrors() {
				consumers = make([]hcl.Expression, 0, len(attrs))
				for _, attr := range attrs {
					consumers = append(consumers, attr.Expr)
				}
			}
		}
		rd, ok := ChildModuleRepetitionData(r.ctx, parentCfg, childSubject(callInst.Call.Name), mc.Count, mc.ForEach, callInst.Key, consumers...)
		if !ok {
			return nil
		}
		parentEval = parentEval.WithRepetitionData(rd)
	}

	return mc.VariablesUsing(r.ctx, parentEval)
}

// frozenClosureIsStale reports whether rebuilding modInst's var.* closure
// against its caller's live evaluator can answer differently from the
// load-time frozen one - which is exactly the condition for rebuilding it,
// and the reason this is a predicate rather than an unconditional rebuild.
//
// [resolver.moduleEvaluator] puts two things on a parent evaluator that
// [ModuleCall.decodeStaticVariables] could not have captured, because
// neither existed when the module tree was built, and this asks after both:
//
//   - repetition data, when a call on the path carries its own count or
//     for_each ([resolver.pathRepeats]). That is #252's axis.
//   - the pre-resolution read phase's coverage, when an ancestor module
//     instance has any ([resolver.ancestorCarriesResults]). That is #179's,
//     and #313's.
//
// The second used to be missing, and its absence is what #313 turned out to
// be. The claim [resolver.pathRepeats]'s own doc made - that a module tree
// with no repeating call "cannot need" the other axes - is false for the
// read-coverage one: a root-module data source feeding a plain, unrepeated
// module call's argument is the single commonest way an estate reaches a
// child module's count or for_each, and the frozen closure evaluates that
// argument through an evaluator that has never seen a data lookup. The
// read phase would then classify the source readable
// ([dataread.Analyze], whose own [dataread.liveModuleEvaluator] rebuilds
// this same chain with no gate at all), read it for real, hand the value in
// through [Context.DataResults] - and resolution would refuse the child's
// count anyway, because the value could not travel across the module call.
// The two halves promised different things about one configuration.
//
// Purity rides along on a rebuild, deliberately: the rebuilt parent
// evaluator is pure where the frozen one is not, so a module argument
// calling uuid() or timestamp() now yields an unknown and refuses instead
// of minting a fresh identity every run. That is the direction this
// repository's standing rule points - a wrong marker outranks a missing one
// - and [StaticEvaluator.Pure]'s own doc is the argument for it. It reaches
// only configurations that already carry read results, since a
// configuration with none takes neither branch below.
func (r *resolver) frozenClosureIsStale(modInst addrs.ModuleInstance) bool {
	return r.pathRepeats(modInst) || r.ancestorCarriesResults(modInst)
}

// ancestorCarriesResults reports whether the pre-resolution read phase
// covered anything in a module instance STRICTLY above modInst - the
// instances whose evaluators [resolver.callerVariables] would rebuild, and
// the only ones whose coverage a rebuild could carry down to modInst.
//
// It reads [resolver.dataIndex] directly rather than through
// [resolver.dataLookupFor] because the question is whether coverage EXISTS,
// not what it answers; no cty value is touched here, so there is nothing
// for a mark to ride on.
//
// modInst itself is deliberately excluded. Its own coverage is attached by
// [resolver.enterModuleAt] and [resolver.moduleEvaluator] directly, and
// answers its own expressions whether or not the closure is rebuilt;
// counting it would rebuild closures that carry nothing new.
func (r *resolver) ancestorCarriesResults(modInst addrs.ModuleInstance) bool {
	if r.dataIndex == nil {
		return false
	}
	for i := len(modInst) - 1; i >= 0; i-- {
		if len(r.dataIndex[modInst[:i].String()]) > 0 {
			return true
		}
	}
	return false
}

// pathRepeats reports whether any module call on the way down to modInst
// carries its own count or for_each. It is #252's half of
// [resolver.frozenClosureIsStale]; see that function for the other half and
// for why "no repeating call" is not on its own a reason to keep the frozen
// closure.
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
