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
// # What it deliberately does not do
//
// Only an object or tuple CONSTRUCTOR is rebuilt. A call the caller wrote
// around one - merge(), concat(), a conditional, a for-comprehension - is
// left to fail exactly as before, because rebuilding it would mean deciding
// what the function does to an unknown argument, and that decision belongs
// to the function rather than here.
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
// two places that need a key SET and have already exhausted every other
// route: [resolver.countExpansion] and [resolver.forEachExpansion], each
// after its own strict evaluation and after the structural key-set chase
// have both failed. Last, never first.
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
		rebuilt, ok := rebuildConstructor(r.ctx, eval, attr.Expr, ident)
		if !ok {
			return val, diags
		}
		return rebuilt, nil
	}
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

// rebuildConstructor rebuilds an object or tuple constructor element by
// element, substituting an unknown for every element the static scope
// refuses, and reports false for any expression that is not one of those two
// constructors.
//
// It returns false rather than an unknown when it cannot rebuild, so that a
// caller keeps the diagnostic it already had. "I could not evaluate this" and
// "this evaluates to something not yet known" are different claims, and only
// the second one licenses a consumer to carry on.
func rebuildConstructor(ctx context.Context, eval *configs.StaticEvaluator, expr hcl.Expression, ident configs.StaticIdentifier) (cty.Value, bool) {
	switch e := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		return rebuildConstructor(ctx, eval, e.Expression, ident)

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
			attrs[name] = elementOrUnknown(ctx, eval, item.ValueExpr, ident)
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
			elems = append(elems, elementOrUnknown(ctx, eval, sub, ident))
		}
		return cty.TupleVal(elems), true
	}
	return cty.NilVal, false
}

// elementOrUnknown evaluates one element of a constructor: strictly first,
// then as a nested constructor, and as an unknown when neither works.
//
// The unknown is cty.DynamicVal - unknown of an unknown type - because the
// element's type is exactly what the refusing reference would have told us.
// A caller's type constraint converts it to whatever the child declared
// ([staticScopeData.GetInputVariable] runs convert.Convert on this value),
// and every consumer that needs a real value still sees an unknown.
func elementOrUnknown(ctx context.Context, eval *configs.StaticEvaluator, expr hcl.Expression, ident configs.StaticIdentifier) cty.Value {
	if val, diags := eval.Evaluate(ctx, expr, ident); !diags.HasErrors() && val != cty.NilVal {
		return val
	}
	if val, ok := rebuildConstructor(ctx, eval, expr, ident); ok {
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
