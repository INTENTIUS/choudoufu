// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// maxTolerantModuleDepth bounds how many module calls
// [resolver.moduleOutputsLookup] will descend through while answering one
// reference.
//
// The module tree is a tree, so nothing here can loop - a call's outputs are
// evaluated in the CHILD, which can only reach its own children, never its
// caller. The bound is about cost, not termination: every output of every
// module below the reference is evaluated to answer it, so a deep
// composition would otherwise pay for the whole subtree on each refused
// reference. Ten is past every real depth measured in the corpus (sumaform,
// the deepest, is five: root, modules/server, modules/host,
// backend_modules/aws/host and the base chain beside it), and a reference
// deeper than that becomes an unknown, which is what it already was.
const maxTolerantModuleDepth = 10

// tolerantEvaluator is modInst's own evaluator with unknown-substitution
// installed: a managed-resource or data-source reference inside one of that
// module's locals or outputs becomes an unknown instead of refusing the
// whole expression, and a reference to one of its child modules is answered
// by evaluating that child's outputs the same tolerant way.
//
// It is built exactly the way [resolver.moduleEvaluator] builds the strict
// one - purity, that instance's data-read coverage, its caller's variables -
// with two additions: the caller's variables are wrapped by
// [resolver.tolerantVariables], so a partial module ARGUMENT is visible here
// too, and [configs.StaticEvaluator.WithUnknownForRefusedReferences] carries
// the substitution and the module-output lookup.
//
// Pure for [resolver.moduleEvaluator]'s reason: an identity is a claim about
// which cloud object a block owns, and an expression that answers
// differently every call cannot make one.
//
// Nil when the module has no evaluator to start from or the depth bound is
// reached, in which case every caller falls back to what it had without
// this.
func (r *resolver) tolerantEvaluator(modInst addrs.ModuleInstance, depth int) *configs.StaticEvaluator {
	if depth > maxTolerantModuleDepth {
		return nil
	}
	cfg, ok := ConfigForModule(r.rootCfg, modInst)
	if !ok || cfg.Module == nil || cfg.Module.StaticEvaluator == nil {
		return nil
	}
	eval := cfg.Module.StaticEvaluator.Pure()
	if lookup := r.dataLookupFor(modInst); lookup != nil {
		eval = eval.WithDataResults(lookup)
	}
	if vars := r.tolerantVariables(modInst, r.callerVariables(modInst)); vars != nil {
		eval = eval.WithVariables(vars)
	}
	return eval.WithUnknownForRefusedReferences(r.moduleOutputsLookup(cfg, modInst, depth))
}

// moduleOutputsLookup answers "module.<call>" inside cfg's expressions with
// an object of that call's own output VALUES, evaluated in the child module
// through [resolver.tolerantEvaluator].
//
// # Why a module output is not a value that has to wait
//
// The static scope refuses `module.foo.bar` because "whatever it names is
// produced by evaluating the module, which has not happened yet". That is
// true of the module's RESOURCES and false of its OUTPUTS: an output is an
// expression written in the child module, and the child module's scope is
// one this resolver can enter and evaluate in. [resolver.resolveModuleOutput]
// and [resolver.moduleOutputValue] already apply that rule at two other
// places - a resource argument reading an output, and a module-call argument
// that IS one. What neither reaches is an output named in the middle of a
// larger expression, which is where every real module composition puts it:
//
//	# backend_modules/aws/base, uyuni-project/sumaform
//	locals {
//	  configuration_output = merge({ region = local.region, ... },
//	                               module.network.configuration,
//	                               !local.create_network ? { ... } : {})
//	}
//
// merge() of an unknown is unknown, so stubbing that one argument takes the
// whole local's KEY SET down with it - and the key set is what
// `lookup(var.base_configuration, "route53_domain", null)` four calls
// further down needs in order to say that a `count` is zero. Evaluating the
// child's outputs instead keeps the keys the configuration states outright
// and leaves unknown only the values that genuinely are.
//
// # What is refused rather than answered
//
// A REPEATED call (count or for_each) is declined, so the reference becomes
// unknown. Answering it would mean shaping a tuple or an object keyed by
// instance and evaluating each instance's outputs, and the instance keys are
// the very thing a caller in this situation may not have; an unknown here is
// what the reference already was.
//
// A SENSITIVE output is declined, on its declaration rather than on the mark
// surviving the round trip, because a marker is written to a cloud tag in
// clear - [resolver.moduleOutputValue]'s reason, unchanged.
//
// An IMPURE output is declined before it is evaluated, for the reason
// impure.go gives: a value that differs every run cannot answer "which live
// object does this block own".
//
// An output whose own expression fails for any other reason becomes an
// unknown ATTRIBUTE of the object rather than failing the whole lookup, so
// one unanswerable output never takes its siblings down with it. That is the
// same split this whole mechanism is about, applied to outputs.
//
// Returns nil when there is no module to read, in which case the tolerant
// scope stubs every module reference with an unknown, exactly as it would
// with no lookup at all.
func (r *resolver) moduleOutputsLookup(cfg *configs.Config, modInst addrs.ModuleInstance, depth int) configs.StaticModuleOutputLookup {
	if cfg == nil || cfg.Module == nil {
		return nil
	}
	return func(call addrs.ModuleCall) (cty.Value, bool) {
		mc, ok := cfg.Module.ModuleCalls[call.Name]
		if !ok || mc.Count != nil || mc.ForEach != nil {
			return cty.NilVal, false
		}
		childInst := modInst.Child(call.Name, addrs.NoKey)
		key := childInst.String()
		if r.tolerantOutBusy[key] {
			// Already being evaluated further up this same chain. Nothing
			// in a module tree can genuinely need its own outputs to
			// compute them, and answering an unknown here is what the
			// reference already was.
			return cty.NilVal, false
		}
		if memo, ok := r.tolerantOut[key]; ok {
			return memo, memo != cty.NilVal
		}
		childCfg, ok := ConfigForModule(r.rootCfg, childInst)
		if !ok || childCfg.Module == nil {
			return cty.NilVal, false
		}
		if r.tolerantOutBusy == nil {
			r.tolerantOutBusy = map[string]bool{}
		}
		r.tolerantOutBusy[key] = true
		eval := r.tolerantEvaluator(childInst, depth+1)
		var val cty.Value
		if eval != nil {
			attrs := make(map[string]cty.Value, len(childCfg.Module.Outputs))
			for name, out := range childCfg.Module.Outputs {
				attrs[name] = r.tolerantOutputValue(childInst, out, eval)
			}
			val = cty.ObjectVal(attrs)
		}
		delete(r.tolerantOutBusy, key)
		if val != cty.NilVal {
			// Only a real answer is memoized. A nil one means the depth
			// bound cut this descent off, and the same call asked again
			// from a shallower frame has more budget to spend on it.
			if r.tolerantOut == nil {
				r.tolerantOut = map[string]cty.Value{}
			}
			r.tolerantOut[key] = val
		}
		return val, val != cty.NilVal
	}
}

// tolerantOutputValue evaluates one output's own expression through eval,
// answering with cty.DynamicVal for every reason
// [resolver.moduleOutputsLookup] documents rather than failing.
func (r *resolver) tolerantOutputValue(childInst addrs.ModuleInstance, out *configs.Output, eval *configs.StaticEvaluator) cty.Value {
	if out == nil || out.Expr == nil || out.Sensitive {
		return cty.DynamicVal
	}
	if len(impureCallsIn(out.Expr)) > 0 {
		return cty.DynamicVal
	}
	ident := configs.StaticIdentifier{
		Module:    childInst.Module(),
		Subject:   "output." + out.Name,
		DeclRange: out.DeclRange,
	}
	val, diags := eval.Evaluate(r.ctx, out.Expr, ident)
	if diags.HasErrors() || val == cty.NilVal || val.ContainsMarked() {
		return cty.DynamicVal
	}
	return val
}
