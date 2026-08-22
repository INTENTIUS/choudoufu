// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// tolerantVariables wraps the var.* closure a module instance's own
// expressions read through, so that a module ARGUMENT the static scope
// cannot evaluate whole still answers with the parts of itself that it can.
//
// The shape it is about is the widest single class in the module-example
// corpus. A caller writes a composite argument whose SKELETON is entirely
// literal and one of whose leaves is not:
//
//	module "ecs" {
//	  capacity_providers = {
//	    ASG = {
//	      auto_scaling_group_provider = {
//	        auto_scaling_group_arn = module.autoscaling.autoscaling_group_arn
//	        managed_draining       = "ENABLED"
//	      }
//	    }
//	  }
//	}
//
// [configs.ModuleCall.VariablesUsing] evaluates that argument as ONE
// expression, so the single unresolvable leaf makes the whole of
// var.capacity_providers unavailable inside the child. Everything the child
// then derives from it is refused with it - including
//
//	for_each = var.create && var.capacity_providers != null ? var.capacity_providers : {}
//
// whose instance keys are decided by the map's KEYS, which are literal and
// were never in doubt. One dynamic leaf, arbitrarily deep, refuses a key set
// the configuration states outright. Measured over the 74 terraform-aws-modules
// examples at c9df9ef116, that poisoning is the root of 327 of the 695
// `Unable to compute static value` sites, across 23 of the 50 examples the
// refusal fires in.
//
// # Why an unknown is the right substitute, and not a guess
//
// Stock OpenTofu plans every one of these configurations. Its plan-time
// evaluator has no strict/loose distinction to make: a reference it cannot
// answer yet becomes an UNKNOWN value and evaluation continues, so an object
// with one apply-time leaf is a known object with an unknown attribute.
// Refusing the whole argument is this fork's own behaviour, not stock's, and
// bringing the two together is what this does.
//
// The substitution is unknown rather than absent, and that is what makes it
// safe. Every path in this package that turns a value into an identity
// demands a KNOWN value - [staticSubValue] requires IsWhollyKnown,
// [collectionKeyNames] rejects an unknown key, [resolver] refuses an unknown
// identity argument - so a value that flowed through a substituted leaf
// cannot become a marker. What can newly succeed is a derivation that reads
// only the structure the caller wrote: a map's key set, a tuple's length, a
// literal sibling attribute. That is exactly the split
// [forEachKeysKnown] draws one layer in, where stock asks IsKnown of a map
// and IsWhollyKnown of a set because a map's values never enter an address.
//
// # Composition, and why one module call is not the unit
//
// The substitution has to survive being passed on. A module that receives a
// partial argument routinely builds something out of it and hands THAT to a
// module of its own - terraform-aws-modules/security-group's preset
// submodules are the shape, and `corpus-security-group-complete` the estate
// that found it:
//
//	# the estate
//	module "consul" {
//	  ingress_referenced_security_group_id = { app = aws_security_group.app.id }
//	}
//
//	# modules/consul, one call further down
//	locals {
//	  combined = { for pair in setproduct(keys(var.preset_ingress_rules),
//	                                      keys(var.ingress_referenced_security_group_id)) : ... }
//	}
//	module "security_group" {
//	  ingress_rules = merge(local.combined, var.ingress_rules)
//	}
//
// The instance keys of the `aws_vpc_security_group_ingress_rule` that
// module eventually declares are a setproduct of two key sets both written
// down - eleven preset names in the module's own default and `app` in the
// estate - and only the VALUE under `app` is unknowable. Two things had to
// change for that to be reachable.
//
// The evaluator this rebuild reads leaves through is now itself wrapped,
// recursively, for the parent module instance. Without that the
// substitution stops at the first module call: `local.combined` refuses
// because the consul module's own frozen closure still refuses
// `var.ingress_referenced_security_group_id`, and everything derived from
// it refuses with it, one call short of the resource whose expansion was
// the question. Recursion terminates at the root module, which has no
// caller and therefore nothing to be tolerant about.
//
// And an argument the caller did not write as a bare constructor is now
// EVALUATED through that wrapped evaluator before the rebuild is attempted.
// `merge(local.combined, var.ingress_rules)` is not an object constructor,
// so there is nothing for [rebuildConstructor] to rewrite; what there is,
// is a function that already knows what it does to an unknown argument.
// Letting merge() answer is not the thing the paragraph below rules out -
// rebuilding a call would mean this package deciding what merge() means,
// and it never does. It runs the function on the value the substitution
// produced and takes the function's own answer, exactly as stock's
// plan-time evaluator does with an unknown, and only when the evaluation
// raises no diagnostic at all. A refused reference anywhere in it still
// falls through to the rebuild, and then to the caller's own refusal.
//
// # What it deliberately does not do
//
// A call is never RECONSTRUCTED. Only an object or tuple constructor is
// rebuilt element by element, because rebuilding merge() or concat() would
// mean deciding what the function does to an unknown argument, and that
// decision belongs to the function. Evaluating the call, above, is how the
// function gets to make it.
//
// A refusing KEY refuses the whole object. A key that cannot be evaluated is
// an instance address that cannot be named, and substituting an unknown for
// one would either collapse two instances into a single marker or invent a
// name for neither - the #178 defect by another route. Values are the only
// thing substituted, keys never.
//
// A duplicate key refuses too, for the same reason [collectStaticForEachKeys]
// refuses one: two items keyed alike would silently become one.
//
// # Where the constructor is allowed to be (issue #375)
//
// The rebuild is about the VALUE the caller described, not about where the
// caller's punctuation put it, so two more spellings of the same argument
// reach it. A bare `local.cfg` is chased to the expression written next to
// the local and that is rebuilt ([rebuildLocal]); a bare
// `module.net.configuration` is answered by [resolver.moduleOutputValue],
// which the leaf case has consulted since #313 and which the whole argument
// could not reach because there was no constructor for it to be a leaf of.
// Both were refusals where the identical value written out at the call
// resolved, and testdata/module-arg-hoisted pins the equivalence by rendering
// the same identity from both spellings.
//
// What still refuses is the case the paragraph above rules out for its own
// reasons: `merge(a, b)` written as the argument, where a leaf inside a or b
// refuses. Rebuilding THAT means rebuilding a call, and the boundary is
// deliberate rather than an oversight - see the same fixture's "merged"
// module. `local.cfg.subnet` and `local.cfg["subnet"]` are out too: a step
// into a rebuilt value is a different question from a rebuilt value, and
// [resolver.selectStatic] is the path that already reads a step into a local
// for the symbolic, one-argument-at-a-time case.
//
// # Order, and why this is never the resolver's ordinary evaluator
//
// strict is called first and its answer used whenever it has one, so this
// changes nothing about any argument that already evaluated. That alone is
// not enough, and the first version of this change - which installed the
// wrapper on [resolver.eval] itself, for every expression the resolver reads
// - was wrong for a reason worth recording, because it looked correct and
// two tests caught it.
//
// An unknown is not the best answer this package has. When a leaf names a
// managed resource, [resolver.staticForEachKeys] can carry the ELEMENT
// EXPRESSION forward instead of a value, and the resolver then resolves that
// resource's own identity to a concrete string (#260). Substituting an
// unknown makes the whole-value evaluation SUCCEED, so that richer path is
// never reached and a resolution that used to be concrete becomes a refusal.
// testdata/shapeb-tryref is exactly that: `users = { alice = { name =
// aws_iam_role.r.name } }` resolves to "the-role" today, and the poisoned
// version of this change turned it into nothing.
//
// So the wrapper is reached only through [resolver.tolerantRetry], from the
// four places that have already exhausted every other route:
// [resolver.countExpansion] and [resolver.forEachExpansion], each after its
// own strict evaluation and after the structural key-set chase have both
// failed; [resolver.tolerantPart] for one identity ARGUMENT's value after
// both [resolver.evalStatic] and [resolver.namedLeaf] have; and
// [resolver.soleElementFromValue] for a collection-typed argument after its
// own strict evaluation has. Last, never first, at each of the four.
//
// Order is also why an unset required root variable is untouched. Under
// internal/live/check that arrives as cty.UnknownVal (load.go), not as an
// error, so the strict evaluation succeeds and the retry never runs; the
// #183 cohort refuses on the unknown exactly as it did. See
// TestTolerantArgsDoNotResolveAnUnsetVariable.
func (r *resolver) tolerantVariables(modInst addrs.ModuleInstance, strict configs.StaticModuleVariables) configs.StaticModuleVariables {
	if len(modInst) == 0 {
		return strict
	}
	parentInst, callInst := modInst.CallInstance()
	parentCfg, ok := ConfigForModule(r.rootCfg, parentInst)
	if !ok || parentCfg.Module == nil {
		return strict
	}
	mc, ok := parentCfg.Module.ModuleCalls[callInst.Call.Name]
	if !ok || mc.Config == nil {
		return strict
	}
	if strict == nil {
		// The module tree's own frozen closure, which is what the child's
		// evaluator was built with (config_build.go passes call.Variables
		// into NewStaticModuleCall). Calling it here rather than reaching
		// for a rebuilt one keeps the strict answer bit-identical to the
		// one this module instance would have got without this wrapper.
		strict = mc.Variables
	}
	if strict == nil {
		return nil
	}
	attrs, _ := mc.Config.JustAttributes()
	if len(attrs) == 0 {
		return strict
	}

	// The evaluator the rebuild reads leaves through. It is the parent's,
	// pure, with the call's own repetition data when the call repeats and
	// that data can be proven - the same construction
	// [resolver.callerVariables] uses, minus the data-read coverage, so a
	// data source the read phase happened to cover cannot answer here and
	// change which references resolve. Built lazily: the great majority of
	// arguments evaluate strictly and never need it.
	//
	// Its own var.* closure is this same wrapper, one module up, so a
	// substitution the grandparent made is visible to the leaf being read
	// here. Nil at the root, where WithVariables is a no-op and the
	// recursion ends.
	var fallback *configs.StaticEvaluator
	fallbackReady := false
	evalFor := func() *configs.StaticEvaluator {
		if fallbackReady {
			return fallback
		}
		fallbackReady = true
		if parentCfg.Module.StaticEvaluator == nil {
			return nil
		}
		eval := parentCfg.Module.StaticEvaluator.Pure()
		if mc.Count != nil || mc.ForEach != nil {
			rd, ok := ChildModuleRepetitionData(r.ctx, parentCfg, childSubject(callInst.Call.Name), mc.Count, mc.ForEach, callInst.Key)
			if !ok {
				return nil
			}
			eval = eval.WithRepetitionData(rd)
		}
		// nil for the root module, and WithVariables ignores a nil, so
		// the root's evaluator is left exactly as it is.
		eval = eval.WithVariables(r.tolerantVariables(parentInst, nil))
		fallback = eval
		return fallback
	}

	childMod := modInst.Module()
	return func(variable *configs.Variable) (cty.Value, hcl.Diagnostics) {
		val, diags := strict(variable)
		if !diags.HasErrors() {
			return val, diags
		}
		attr, ok := attrs[variable.Name]
		if !ok {
			return val, diags
		}
		eval := evalFor()
		if eval == nil {
			return val, diags
		}
		ident := configs.StaticIdentifier{
			Module:    childMod,
			Subject:   "var." + variable.Name,
			DeclRange: attr.Range,
		}
		if composed, ok := composedArgument(r.ctx, eval, attr.Expr, ident); ok {
			return composed, nil
		}
		rb := argRebuild{
			moduleOutput: r.moduleOutputValues(parentCfg, parentInst),
			localExpr:    localExprs(parentCfg),
		}
		// The whole argument as a child module's output, before the
		// rebuild: `base_configuration = module.network.configuration` is
		// the same reference [elementOrUnknown] already answers when it
		// sits one level in, as a leaf of a constructor the caller wrote
		// out. There is no constructor to rebuild when the caller writes
		// the reference on its own, so without this the identical
		// reference resolves inside `{ cfg = module.network.configuration
		// }` and refuses as `module.network.configuration` - a difference
		// in the caller's punctuation, not in what is knowable.
		// [resolver.moduleOutputValue]'s guards are the ones that make it
		// safe there and they are unchanged here: the output's own
		// expression is evaluated strictly in the child module, a
		// sensitive output is refused on its declaration, an impure one
		// before it is evaluated, and the answer has to come back wholly
		// known, non-null and unmarked. A false leaves the caller with
		// exactly the diagnostic it already had.
		if rb.moduleOutput != nil {
			if trav, travDiags := hcl.AbsTraversalForExpr(attr.Expr); !travDiags.HasErrors() {
				if out, ok := rb.moduleOutput(trav); ok {
					return out, nil
				}
			}
		}
		rebuilt, ok := rebuildConstructor(r.ctx, eval, attr.Expr, ident, rb)
		if !ok {
			return val, diags
		}
		return rebuilt, nil
	}
}

// localExprs answers a local value's own defining expression in cfg's module,
// and is what lets [rebuildConstructor] rebuild a constructor the caller
// hoisted into a local instead of writing out at the module call.
//
// The two spellings
//
//	module "host" { base_configuration = { enabled = true, subnet = aws_subnet.s.id } }
//
// and
//
//	locals    { cfg = { enabled = true, subnet = aws_subnet.s.id } }
//	module "host" { base_configuration = local.cfg }
//
// state the same fact about the same value, and stock plans both. The first
// resolves here because [rebuildConstructor] sees an
// [hclsyntax.ObjectConsExpr] and rebuilds it leaf by leaf; the second used to
// refuse outright, because the module-call argument is a bare traversal and
// the constructor is one syntactic step away, inside the local. Nothing about
// the local makes its contents less knowable - a local has no scope of its
// own, no repetition, and no value beyond the expression written next to it.
//
// Returns nil when there is no module to read, in which case
// [rebuildConstructor] declines the chase and behaves exactly as it did
// before this existed.
func localExprs(cfg *configs.Config) func(string) (hcl.Expression, bool) {
	if cfg == nil || cfg.Module == nil {
		return nil
	}
	return func(name string) (hcl.Expression, bool) {
		local, ok := cfg.Module.Locals[name]
		if !ok || local.Expr == nil {
			return nil, false
		}
		return local.Expr, true
	}
}

// composedArgument evaluates a whole module-call argument through an
// evaluator whose own var.* closure is already tolerant, and is what lets a
// substitution cross more than one module call.
//
// It exists because the second, third and fourth calls down a real module
// composition are rarely a bare constructor. A module that receives a map
// passes on `merge(local.combined, var.extra)`; one that receives a list
// passes on `concat(...)` or a for-comprehension over it. There is no
// constructor there for [rebuildConstructor] to rewrite element by element,
// and rewriting the CALL is exactly what this package refuses to do -
// so instead the call is run, on a value whose unknown leaves the wrapper
// one module up already substituted, and whatever the function makes of an
// unknown is the answer. That is the same thing stock OpenTofu's plan-time
// evaluator does with an apply-time value, and it keeps the decision inside
// the function where it belongs.
//
// Evaluate, not [configs.StaticEvaluator.EvaluateStructural]: Structural's
// contract is to look PAST a refused reference when the value it renders is
// wholly known anyway, and the value here is by construction not wholly
// known - its whole point is the unknown leaf. What is demanded instead is
// that no diagnostic was raised at all, which is the stronger claim: every
// reference this expression reaches was inside static scope and answered, so
// nothing was skipped, ignored or guessed. An expression naming a managed
// resource directly still raises, still falls through to the rebuild, and
// still ends at the caller's own refusal if that declines too.
//
// The value it returns therefore contains exactly one kind of invention: the
// cty.DynamicVal an ancestor's [elementOrUnknown] put in place of a leaf the
// static scope refused. Everything else is the caller's own configuration,
// run through the caller's own functions. Every gate that turns a value into
// a marker still demands a known one, so a substituted leaf that reaches one
// is refused there exactly as it was before.
func composedArgument(ctx context.Context, eval *configs.StaticEvaluator, expr hcl.Expression, ident configs.StaticIdentifier) (cty.Value, bool) {
	val, diags := eval.Evaluate(ctx, expr, ident)
	if diags.HasErrors() || val == cty.NilVal {
		return cty.NilVal, false
	}
	return val, true
}

// tolerantRetry re-evaluates one expression with [resolver.tolerantVariables]
// installed, and is the only way that wrapper is ever reached.
//
// It reports false rather than recording a diagnostic, because it runs only
// after a caller has already recorded one: its answer is either "here is a
// value the strict pass could not produce" or "nothing changes". The caller
// discards its own diagnostic on a true, which is the one place the refusal
// this closes actually disappears.
//
// The impure-function check is repeated rather than inherited. [resolver.evalStatic]
// raises it before evaluating, and this goes through [resolver.evalPure]
// instead so that the retry's own failures stay off r.diags - so without it,
// an expression refused for calling uuid() would come back through this door
// with a fabricated value. See impure.go.
func (r *resolver) tolerantRetry(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (cty.Value, bool) {
	if r.eval == nil || len(r.modInst) == 0 {
		return cty.NilVal, false
	}
	if len(impureCallsIn(expr)) > 0 {
		return cty.NilVal, false
	}
	vars := r.tolerantVariables(r.modInst, r.callerVariables(r.modInst))
	if vars == nil {
		return cty.NilVal, false
	}
	saved := r.eval
	r.eval = r.eval.WithVariables(vars)
	defer func() { r.eval = saved }()

	val, diags := r.evalPure(expr, scope, ident)
	if diags.HasErrors() || val == cty.NilVal {
		return cty.NilVal, false
	}
	return val, true
}

// tolerantPart is [resolver.resolveExpr]'s last attempt at ONE identity
// argument's value, and the third and only other caller of
// [resolver.tolerantRetry].
//
// The two key-set callers prove which instances exist. This proves what one
// instance's argument says, which is the other half of the same fact and
// fails for the identical reason: a caller writes a composite module
// argument whose skeleton is literal and one of whose leaves is not, and
// [configs.ModuleCall.VariablesUsing] evaluates it as ONE expression, so the
// child's whole var.X is unavailable. terraform-aws-modules/security-group's
// own ingress_with_cidr_blocks is the shape - the caller writes
//
//	ingress_with_cidr_blocks = [{
//	  from_port   = 5432
//	  to_port     = 5432
//	  protocol    = "tcp"
//	  cidr_blocks = module.vpc.vpc_cidr_block
//	}]
//
// and the module builds aws_security_group_rule's identity out of
// `lookup(var.ingress_with_cidr_blocks[count.index], "from_port", ...)` and
// three siblings like it. One leaf reads a module output; the other three
// are integers and a string written in the caller's own file. Before this,
// all four refused together.
//
// # What keeps a substituted leaf out of a marker
//
// The retry's value is accepted only when it comes back WHOLLY KNOWN,
// non-null and carrying no mark anywhere. That is not a courtesy check, it
// is the whole safety argument: [rebuildConstructor] substitutes
// cty.DynamicVal for exactly the leaves the static scope refuses and keeps
// every other element's strict value untouched, so an expression that reads
// a substituted leaf evaluates to an unknown and is turned away here, while
// one that reads only leaves the caller wrote out evaluates to those leaves
// and nothing else. `lookup(elem, "cidr_blocks", ...)` stays refused on the
// very same rebuilt value that answers `lookup(elem, "from_port", ...)` with
// 5432. Nothing is guessed for the refusing leaf and no sibling's value ever
// stands in for it.
//
// ContainsMarked, not IsMarked: a mark on a leaf leaves the container
// unmarked (see internal/live/marksafe's TestOnlySetsHoistElementMarks), and
// a sensitive leaf reached through a rebuilt constructor must not become an
// identity any more than a sensitive whole value may.
//
// # Why this cannot repeat the regression the wrapper's own doc records
//
// It runs at a point resolveExpr today always returns false from: after the
// strict [resolver.evalStatic] and after [resolver.namedLeaf] reported the
// shape is not one it handles. It is inside resolveExpr's NON-symbolic
// branch, which an expression naming a managed resource never enters
// ([resolver.isSymbolic]), so the element-expression chase that
// testdata/shapeb-tryref pins is upstream of here and untouched: nothing
// this function does can make an earlier evaluation succeed, because every
// earlier evaluation has already run and failed by the time it is called.
// It adds a resolution where there was a refusal, or it changes nothing.
//
// On failure it restores r.diags itself, so the refusal evalStatic already
// recorded is the one the caller keeps - never a vaguer second one stapled
// on top of it.
func (r *resolver) tolerantPart(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier, diagMark, sibMark int) ([]Part, bool) {
	retried, ok := r.tolerantRetry(expr, scope, ident)
	if !ok || retried.ContainsMarked() || retried.IsNull() || !retried.IsWhollyKnown() {
		return nil, false
	}
	probe, probeSib := len(r.diags), len(r.pendingSiblingApply)
	s, ok := r.stringValueIn(retried, expr, scope, ident)
	if !ok {
		// Both, not only the diagnostics: a [siblingApplyRefusal] carries
		// the INDEX of the diagnostic it may later withdraw, so leaving one
		// behind while truncating r.diags out from under it would point it
		// at whatever diagnostic lands there next. Unreachable today - the
		// only append is stringValueIn's !IsWhollyKnown branch, which the
		// gate above has already excluded - and written out so that stays a
		// property of this function rather than of that one.
		r.diags = r.diags[:probe]
		r.pendingSiblingApply = r.pendingSiblingApply[:probeSib]
		return nil, false
	}
	r.diags = r.diags[:diagMark]
	r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
	return []Part{{Literal: s}}, true
}

// rebuildConstructor rebuilds an object or tuple constructor element by
// element, substituting an unknown for every element the static scope
// refuses, and reports false for any expression that is not one of those two
// constructors.
//
// It returns false rather than an unknown when it cannot rebuild, so that a
// caller keeps the diagnostic it already had. "I could not evaluate this" and
// "this evaluates to something not yet known" are different claims, and only
// the second one licenses a consumer to carry on.
//
// rb carries the two out-of-band answers a refused leaf can have - a child
// module's output value and a local's own defining expression - and the depth
// the local chase has already spent. See [argRebuild].
func rebuildConstructor(ctx context.Context, eval *configs.StaticEvaluator, expr hcl.Expression, ident configs.StaticIdentifier, rb argRebuild) (cty.Value, bool) {
	switch e := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		return rebuildConstructor(ctx, eval, e.Expression, ident, rb)

	case *hclsyntax.ScopeTraversalExpr:
		return rebuildLocal(ctx, eval, e.Traversal, ident, rb)

	case *hclsyntax.ObjectConsExpr:
		attrs := make(map[string]cty.Value, len(e.Items))
		for _, item := range e.Items {
			name, ok := constructorKey(ctx, eval, item.KeyExpr, ident)
			if !ok {
				return cty.NilVal, false
			}
			if _, dup := attrs[name]; dup {
				return cty.NilVal, false
			}
			attrs[name] = elementOrUnknown(ctx, eval, item.ValueExpr, ident, rb)
		}
		if len(attrs) == 0 {
			return cty.EmptyObjectVal, true
		}
		return cty.ObjectVal(attrs), true

	case *hclsyntax.TupleConsExpr:
		if len(e.Exprs) == 0 {
			return cty.EmptyTupleVal, true
		}
		elems := make([]cty.Value, 0, len(e.Exprs))
		for _, sub := range e.Exprs {
			elems = append(elems, elementOrUnknown(ctx, eval, sub, ident, rb))
		}
		return cty.TupleVal(elems), true
	}
	return cty.NilVal, false
}

// argRebuild is what [rebuildConstructor] consults for a leaf, and for the
// argument as a whole, when the static scope refuses it.
//
// moduleOutput, when non-nil, is the one thing that can answer a refused leaf
// with a real value rather than an unknown: a reference into a child module's
// output whose own expression the child's own evaluator settles
// ([resolver.moduleOutputValues]).
//
// localExpr, when non-nil, answers a bare `local.<name>` with the expression
// written next to it, so a constructor the caller hoisted into a local is
// rebuilt exactly as one written out at the module call is ([localExprs]).
//
// depth counts the local chases already taken, because a local naming another
// local is the one recursion here that syntax does not bound. A zero value -
// as in this file's own unit tests - restores exactly the behaviour that
// existed before either field, where every refused leaf became cty.DynamicVal
// and a bare traversal was not a constructor at all.
type argRebuild struct {
	moduleOutput func(hcl.Traversal) (cty.Value, bool)
	localExpr    func(name string) (hcl.Expression, bool)
	depth        int
}

// rebuildLocal chases a bare `local.<name>` to the expression it is defined
// as and rebuilds THAT, so that hoisting a constructor into a local does not
// decide whether its literal siblings are knowable. See [localExprs] for the
// rule and the two spellings it equates.
//
// Only the bare two-step form is chased. `local.cfg.subnet` and
// `local.cfg["subnet"]` carry steps that would have to be applied to the
// rebuilt value, and a step into a substituted leaf is a different question
// from a substituted leaf itself; they keep refusing, and
// [resolver.selectStatic] is the path that already reads a step into a local
// for the symbolic, one-argument-at-a-time case.
//
// A `var.<name>` traversal is deliberately NOT chased: the evaluator this
// runs on already answers var.* through [resolver.tolerantVariables] one
// module up, so the caller's own argument has been substituted before this
// function is ever reached, and chasing it here would substitute it a second
// time under a different set of guards.
//
// The chase cannot invent a value. It ends at [rebuildConstructor], which
// either finds a constructor - and then rebuilds it under the same rules,
// with the same evaluator, in the same module - or reports false and leaves
// the caller with the diagnostic it already had. A self-referential local
// terminates on [configs.staticScopeData.scope]'s own circular-reference
// check the moment the strict evaluation of its leaf re-enters it; depth
// bounds the chain of distinct locals that HCL's own syntax does not.
func rebuildLocal(ctx context.Context, eval *configs.StaticEvaluator, trav hcl.Traversal, ident configs.StaticIdentifier, rb argRebuild) (cty.Value, bool) {
	if rb.localExpr == nil || rb.depth >= maxStaticDecomposeDepth {
		return cty.NilVal, false
	}
	if len(trav) != 2 || trav.RootName() != "local" {
		return cty.NilVal, false
	}
	nameStep, ok := trav[1].(hcl.TraverseAttr)
	if !ok {
		return cty.NilVal, false
	}
	def, ok := rb.localExpr(nameStep.Name)
	if !ok {
		return cty.NilVal, false
	}
	rb.depth++
	return rebuildConstructor(ctx, eval, def, ident, rb)
}

// elementOrUnknown evaluates one element of a constructor: strictly first,
// then as a child module's output, then as a nested constructor, and as an
// unknown when none of the three works.
//
// The unknown is cty.DynamicVal - unknown of an unknown type - because the
// element's type is exactly what the refusing reference would have told us.
// A caller's type constraint converts it to whatever the child declared
// ([staticScopeData.GetInputVariable] runs convert.Convert on this value),
// and every consumer that needs a real value still sees an unknown.
//
// The module-output attempt runs only after the strict evaluation has already
// failed, so it can never change an element that had a value; and it answers
// only with a wholly-known, non-null, unmarked one
// ([resolver.moduleOutputValue]), so declining leaves cty.DynamicVal - the
// same substitution a refused leaf has always had.
func elementOrUnknown(ctx context.Context, eval *configs.StaticEvaluator, expr hcl.Expression, ident configs.StaticIdentifier, rb argRebuild) cty.Value {
	if val, diags := eval.Evaluate(ctx, expr, ident); !diags.HasErrors() && val != cty.NilVal {
		return val
	}
	if rb.moduleOutput != nil {
		if trav, diags := hcl.AbsTraversalForExpr(expr); !diags.HasErrors() {
			if val, ok := rb.moduleOutput(trav); ok {
				return val
			}
		}
	}
	if val, ok := rebuildConstructor(ctx, eval, expr, ident, rb); ok {
		return val
	}
	return cty.DynamicVal
}

// constructorKey evaluates one object-constructor key to the attribute name
// it has to be, refusing anything it cannot pin down.
//
// This is [staticKeyString]'s rule applied through a caller-supplied
// evaluator rather than through the module's own: same demand for a known,
// unmarked, non-null string, for the same reason. A naked identifier key is a
// literal name, which is [hclsyntax.ObjectConsKeyExpr]'s rule and is honoured
// by evaluating the key expression as written.
func constructorKey(ctx context.Context, eval *configs.StaticEvaluator, expr hcl.Expression, ident configs.StaticIdentifier) (string, bool) {
	val, diags := eval.Evaluate(ctx, expr, ident)
	if diags.HasErrors() || val == cty.NilVal {
		return "", false
	}
	s, err := convert.Convert(val, cty.String)
	if err != nil || s.IsNull() || !s.IsKnown() || s.IsMarked() {
		return "", false
	}
	return s.AsString(), true
}
