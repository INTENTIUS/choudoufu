// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// moduleOutputValues builds the closure [rebuildConstructor] consults for a
// leaf that reads a child module's output, bound to the module instance the
// constructor's own expressions are written in.
//
// The shape it answers is one leaf of a module CALL argument:
//
//	module "security_group" {
//	  ingress_with_cidr_blocks = [{
//	    from_port   = 5432
//	    cidr_blocks = module.vpc.vpc_cidr_block
//	  }]
//	}
//
// [resolver.tolerantVariables] rebuilds that list element by element because
// [configs.ModuleCall.VariablesUsing] evaluates the whole argument as one
// expression and one refusing leaf takes the literal siblings down with it.
// Before this, every leaf the strict evaluation refused became
// cty.DynamicVal, and a module output is refused by
// [configs.staticScopeData.StaticValidateReferences] unconditionally - so
// `lookup(elem, "cidr_blocks", ...)` inside the child stayed unresolvable
// even when the output's own expression is an ordinary configuration value
// with nothing live anywhere in it.
//
// The rule is the one [resolver.resolveModuleOutput] already applies at the
// other call site (a module output read directly by a resource's identity
// argument, localvalue.go): a module output is not a value that has to wait
// for the module to be evaluated, it is an expression written in the child
// module, and the child module's scope is one this resolver can enter and
// evaluate in. What differs is only WHERE the reference sits - inside a
// module-call argument here, inside a resource argument there.
//
// # Why this cannot widen what becomes a marker
//
// The load-bearing condition is the first one; the rest are defence in depth,
// and this says which is which because a reader deciding whether to keep one
// deserves to know it was checked.
//
//   - The output's expression is evaluated STRICTLY, through
//     [resolver.moduleEvaluator] - the child instance's own pure evaluator,
//     with its data-read coverage and its caller's argument closure, exactly
//     as any other expression in that module is read. No tolerant wrapper is
//     installed on it, so a leaf the child itself cannot evaluate (a managed
//     resource attribute, an unset variable) fails here just as it fails
//     everywhere else, and this returns false. That is what keeps every
//     adversarial case in
//     TestModuleOutputInsideModuleCallArgumentRefuses refused.
//   - A sensitive output is refused on the DECLARATION rather than on the
//     mark surviving the round trip, because a marker is written to a cloud
//     tag in clear. Mutation-checked: removing this one line, and only this
//     one line, resolves the sensitive fixture to a real CIDR.
//   - The result must be wholly known, non-null and unmarked, and an impure
//     output is refused before it is evaluated. Both are belt and braces:
//     [resolver.moduleEvaluator] is already pure, so uuid() comes back
//     unknown either way, and an unknown substituted here is indistinguishable
//     from the cty.DynamicVal a refused leaf already produced - so
//     [resolver.tolerantPart]'s own wholly-known gate turns it away downstream.
//     Removing either changes no fixture's verdict. They stay because this
//     function's contract is "a known value or nothing", and a caller reading
//     that line should not have to trace two other functions to trust it.
//
// A false from any of them leaves the caller with cty.DynamicVal, which is
// byte-for-byte what it had before this existed. So this function can only
// turn a refusal into a resolution, never a resolution into a different one.
func (r *resolver) moduleOutputValues(parentCfg *configs.Config, parentInst addrs.ModuleInstance) func(hcl.Traversal) (cty.Value, bool) {
	if parentCfg == nil || parentCfg.Module == nil {
		return nil
	}
	return func(trav hcl.Traversal) (cty.Value, bool) {
		return r.moduleOutputValue(parentCfg, parentInst, trav)
	}
}

// moduleOutputValue resolves one "module.<call>[<key>].<output><rest>"
// traversal to the value the child module's output expression evaluates to.
// See [resolver.moduleOutputValues] for the rule and its guards.
func (r *resolver) moduleOutputValue(parentCfg *configs.Config, parentInst addrs.ModuleInstance, trav hcl.Traversal) (cty.Value, bool) {
	if len(trav) < 3 || trav.RootName() != "module" {
		return cty.NilVal, false
	}
	nameStep, ok := trav[1].(hcl.TraverseAttr)
	if !ok {
		return cty.NilVal, false
	}
	callName := nameStep.Name
	mc, ok := parentCfg.Module.ModuleCalls[callName]
	if !ok || mc.Config == nil {
		return cty.NilVal, false
	}

	// Key handling is [resolver.resolveModuleOutput]'s, for its reasons: an
	// indexed reference supplies the instance key, an unindexed one is only
	// meaningful on a call with no repetition, and the key has to be one the
	// call actually expands to - otherwise module.foo[7] on a three-instance
	// call would enter the child module anyway and answer for an instance
	// that will never exist.
	rest := trav[2:]
	repeated := mc.Count != nil || mc.ForEach != nil
	key := addrs.InstanceKey(addrs.NoKey)
	if idx, isIndex := rest[0].(hcl.TraverseIndex); isIndex {
		if !repeated {
			return cty.NilVal, false
		}
		k, ok := indexKeyValue(idx.Key)
		if !ok {
			return cty.NilVal, false
		}
		key = k
		rest = rest[1:]
	} else if repeated {
		return cty.NilVal, false
	}
	if repeated {
		if _, ok := ChildModuleRepetitionData(r.ctx, parentCfg, childSubject(callName), mc.Count, mc.ForEach, key); !ok {
			return cty.NilVal, false
		}
	}

	if len(rest) == 0 {
		// "module.foo" with no output named: an object of every output
		// rather than one value, which is not a leaf this can answer.
		return cty.NilVal, false
	}
	outStep, isAttr := rest[0].(hcl.TraverseAttr)
	if !isAttr {
		return cty.NilVal, false
	}
	rest = rest[1:]

	childInst := parentInst.Child(callName, key)
	childCfg, ok := ConfigForModule(r.rootCfg, childInst)
	if !ok || childCfg.Module == nil {
		return cty.NilVal, false
	}
	out, ok := childCfg.Module.Outputs[outStep.Name]
	if !ok || out.Expr == nil || out.Sensitive {
		return cty.NilVal, false
	}

	eval := r.moduleEvaluator(childInst)
	if eval == nil {
		return cty.NilVal, false
	}
	if len(impureCallsIn(out.Expr)) > 0 {
		// The same refusal [resolver.evalStatic] raises, repeated here
		// because this path never goes through it: a value that differs
		// every run cannot answer "which live object does this block own".
		return cty.NilVal, false
	}

	ident := configs.StaticIdentifier{
		Module:    childInst.Module(),
		Subject:   "output." + outStep.Name,
		DeclRange: out.DeclRange,
	}
	val, diags := eval.Evaluate(r.ctx, out.Expr, ident)
	if diags.HasErrors() || val == cty.NilVal {
		return cty.NilVal, false
	}
	if len(rest) > 0 {
		selected, selDiags := hcl.Traversal(rest).TraverseRel(val)
		if selDiags.HasErrors() {
			return cty.NilVal, false
		}
		val = selected
	}
	if val == cty.NilVal || val.IsNull() || !val.IsWhollyKnown() || val.ContainsMarked() {
		return cty.NilVal, false
	}
	return val, true
}
