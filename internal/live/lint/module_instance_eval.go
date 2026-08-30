// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"fmt"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is lint's copy of the one thing internal/live/identity already
// knows and lint did not: a module INSTANCE's var.* answers depend on which
// instance it is, and [configs.Module]'s built-in closure cannot say so.
//
// [ModuleCall.decodeStaticVariables] freezes one variables closure per CALL
// at load time, against the parent's evaluator as it existed before any
// instance of anything was resolved - so it answers every var.* with no
// repetition data at all, forever, for every instance of a for_each'd call
// alike. An argument written `prefix = "${local.p}-${each.key}"` therefore
// does not evaluate through it: static_scope.go refuses each.key in a
// static context, GetInputVariable yields an unknown, and anything reading
// var.prefix inside the module sees a value it cannot compute.
//
// [identity.resolver.callerVariables] rebuilds that closure per instance and
// has since issue #252. Lint did not, and lint runs FIRST
// (internal/command/live_plan.go), so the count.index domain check refused a
// configuration the layer below it computes correctly - GitHub issue #580,
// which is #252's shape reached one rule earlier. The two layers disagreed
// about one configuration and the conservative one won by ordering.
//
// What is deliberately NOT copied is the data-read axis
// ([resolver.dataLookupFor], [resolver.ancestorCarriesResults]): lint runs
// before any data source has been read, has no results to install, and a
// module argument built from one is refused here exactly as it always was.
// Copying the repetition axis alone is therefore strictly narrower than
// identity's own rebuild, never wider, which is the direction a pass that
// runs first has to fail in.

// lintModuleInstanceMax bounds how many module instances
// [moduleInstanceEvaluators] will build evaluators for, and
// lintModuleRenderMax bounds the total number of index renderings the
// count.index domain check will then do across all of them.
//
// Both exist for cost, not for correctness, and neither is a refusal:
// exceeding either drops back to the module's own frozen closure, which is
// exactly the evaluator this rule used before this file existed. So the
// worst case of a bound being too small is today's answer, never a new
// one - see [countIndexDomainFor].
const (
	lintModuleInstanceMax = 64
	lintModuleRenderMax   = 1024
)

// moduleInstanceEvaluators returns one static evaluator per INSTANCE of the
// module cfg describes, each carrying the repetition data of every expanded
// module call on the path from the root down to it, so that a var.* read
// inside the module answers with what that instance's caller actually
// passed.
//
// ok is false whenever the instances cannot be enumerated or one of their
// evaluators cannot be built - an unenumerable count or for_each on a call
// in the path, a module the tree does not have, a call whose repetition data
// [identity.ChildModuleRepetitionData] declines to confirm, or more
// instances than [lintModuleInstanceMax]. A false ok is not a refusal and
// must not be read as one; it means "no better evaluator than the frozen
// one is available here", and every caller falls back to the frozen one.
//
// The root module has exactly one instance and no caller, so it reports the
// single root evaluator. A module reached through nothing but static calls
// likewise has one instance whose frozen closure is already correct, and
// this reports false for it ([pathRepeats]) rather than rebuilding a closure
// that could only answer the same - which keeps every configuration without
// an expanded module call on byte-for-byte the path it was on before.
func moduleInstanceEvaluators(ctx context.Context, cfg *configs.Config) ([]*configs.StaticEvaluator, bool) {
	if cfg == nil || cfg.Module == nil || cfg.Root == nil {
		return nil, false
	}
	root := cfg.Root
	path := cfg.Path
	if !pathRepeats(root, path) {
		return nil, false
	}

	insts, ok := moduleInstancesOf(ctx, root, path)
	if !ok {
		return nil, false
	}

	evals := make([]*configs.StaticEvaluator, 0, len(insts))
	for _, inst := range insts {
		eval := moduleInstanceEvaluator(ctx, root, inst)
		if eval == nil {
			return nil, false
		}
		evals = append(evals, eval)
	}
	if len(evals) == 0 {
		// A module call that expands to nothing - count = 0, or an empty
		// for_each - has no instance for any rule to be about. Reporting
		// false hands the caller the frozen closure, which is what it had
		// before; the block's own zero-instance case is
		// [blockHasNoInstances]'s to answer, not this file's.
		return nil, false
	}
	return evals, true
}

// pathRepeats reports whether any module call on the way down to path
// carries its own count or for_each. It is the same question
// [identity.resolver.pathRepeats] asks, and the same answer: with no
// expanded call anywhere on the path there is no repetition data a rebuild
// could install that the frozen closure does not already have, so there is
// nothing to rebuild.
func pathRepeats(root *configs.Config, path addrs.Module) bool {
	cur := root
	for _, name := range path {
		if cur == nil || cur.Module == nil {
			return false
		}
		if mc, ok := cur.Module.ModuleCalls[name]; ok && mc != nil && (mc.Count != nil || mc.ForEach != nil) {
			return true
		}
		cur = cur.Children[name]
	}
	return false
}

// moduleInstancesOf enumerates every instance of the module at path, by
// taking the cross product of each call's own instance keys down the chain.
//
// The keys come from [identity.ChildCallKeys], the one place the count /
// for_each / static three-way dispatch is written, so this walk cannot
// acquire the missing-limb defect that function's own doc records in three
// others. A call whose keys it cannot enumerate reports false here, and a
// cross product wider than [lintModuleInstanceMax] does too.
func moduleInstancesOf(ctx context.Context, root *configs.Config, path addrs.Module) ([]addrs.ModuleInstance, bool) {
	insts := []addrs.ModuleInstance{addrs.RootModuleInstance}
	parentCfg := root
	for _, name := range path {
		if parentCfg == nil || parentCfg.Module == nil {
			return nil, false
		}
		keys, diag := identity.ChildCallKeys(ctx, parentCfg, name)
		if diag != nil {
			return nil, false
		}
		if len(insts)*len(keys) > lintModuleInstanceMax {
			return nil, false
		}
		next := make([]addrs.ModuleInstance, 0, len(insts)*len(keys))
		for _, inst := range insts {
			for _, key := range keys {
				next = append(next, inst.Child(name, key))
			}
		}
		insts = next
		parentCfg = parentCfg.Children[name]
	}
	return insts, true
}

// moduleInstanceEvaluator builds the evaluator one module instance's own
// expressions are read through: the module's own static evaluator, made
// pure, with its caller's variables reinstalled for THIS instance. It and
// [callerVariablesFor] recur into each other and terminate at the root, so
// a module three expanded calls deep sees every ancestor call's repetition
// rather than only its immediate parent's - the mutual recursion
// [identity.resolver.moduleEvaluator] and [identity.resolver.callerVariables]
// already have, which issue #580's fix sketch names as a requirement.
//
// Purity is deliberate and matches identity's own choice: a module argument
// calling uuid() or timestamp() yields an unknown here and refuses, rather
// than minting a fresh value that would become a different marker on every
// run. See [configs.StaticEvaluator.Pure].
func moduleInstanceEvaluator(ctx context.Context, root *configs.Config, modInst addrs.ModuleInstance) *configs.StaticEvaluator {
	cfg, ok := identity.ConfigForModule(root, modInst)
	if !ok || cfg == nil || cfg.Module == nil || cfg.Module.StaticEvaluator == nil {
		return nil
	}
	eval := cfg.Module.StaticEvaluator.Pure()
	if vars := callerVariablesFor(ctx, root, modInst); vars != nil {
		eval = eval.WithVariables(vars)
	}
	return eval
}

// callerVariablesFor rebuilds one module instance's var.* closure against
// its caller's evaluator carrying that call's own repetition data, through
// [configs.ModuleCall.VariablesUsing] - the same seam and the same
// construction [identity.resolver.callerVariables] uses.
//
// Nil for the root module, which has no caller, and nil whenever the call's
// repetition data cannot be PROVEN: [identity.ChildModuleRepetitionData]
// re-derives it from the call's own count/for_each expression rather than
// trusting the instance key it is handed, and a call it cannot account for
// keeps the frozen closure and refuses exactly where it always did.
func callerVariablesFor(ctx context.Context, root *configs.Config, modInst addrs.ModuleInstance) configs.StaticModuleVariables {
	if len(modInst) == 0 {
		return nil
	}
	parentInst, callInst := modInst.CallInstance()
	parentCfg, ok := identity.ConfigForModule(root, parentInst)
	if !ok || parentCfg == nil || parentCfg.Module == nil {
		return nil
	}
	mc, ok := parentCfg.Module.ModuleCalls[callInst.Call.Name]
	if !ok || mc == nil || mc.Config == nil {
		return nil
	}

	parentEval := moduleInstanceEvaluator(ctx, root, parentInst)
	if parentEval == nil {
		return nil
	}

	if mc.Count != nil || mc.ForEach != nil {
		// consumers is issue #315's projection: every one of this call's own
		// argument expressions, so that a for_each whose whole value cannot
		// be proven can still answer each.value for the attribute names
		// those arguments actually read. The rebuilt evaluator is reused for
		// whichever variable a later lookup asks for, so the projection has
		// to cover every argument, not just one.
		var consumers []hcl.Expression
		if mc.ForEach != nil {
			if attrs, diags := mc.Config.JustAttributes(); !diags.HasErrors() {
				consumers = make([]hcl.Expression, 0, len(attrs))
				for _, attr := range attrs {
					consumers = append(consumers, attr.Expr)
				}
			}
		}
		subject := fmt.Sprintf("module %q", callInst.Call.Name)
		rd, ok := identity.ChildModuleRepetitionData(ctx, parentCfg, subject, mc.Count, mc.ForEach, callInst.Key, consumers...)
		if !ok {
			return nil
		}
		parentEval = parentEval.WithRepetitionData(rd)
	}

	return mc.VariablesUsing(ctx, parentEval)
}
