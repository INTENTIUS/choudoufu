// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// modulescope.go is issue #1063's fix: [instanceRepetitionData] and
// [staticeval.ArgumentScoped] together answer a resource's OWN
// count.index/each.key/each.value, but a resource declared INSIDE a module
// names itself from that module's own input variables
// ([staticeval.Allowed]'s "var" root), and a module's own var.* is never a
// property of the resource's own block - it comes from the PARENT's
// `module` block, one call site shared by every instance of that call.
//
// [configs.StaticEvaluator.WithVariables]'s own doc names the trap exactly:
// [configs.Module.StaticEvaluator]'s var.* closure
// ([configs.ModuleCall.decodeStaticVariables]) is frozen once, at load
// time, against the parent's evaluator as it existed before any module
// instance had been resolved - so it answers every var.* with NO repetition
// data at all, forever, regardless of which instance of a for_each'd or
// count'd `module` block is actually asking. A module argument that reads
// `each.value.name` panics ("Not Available in Static Context") through that
// frozen closure for every instance alike, [staticeval.Scoped] recovers the
// panic into an ordinary evaluation failure, and the candidate ARN cannot be
// composed - exactly the shape #1063 was opened for
// (`module.team_pod["pod-a"].aws_iam_policy.pod_policy[0]`).
//
// [internal/live/identity]'s own resolver already solved this for the
// identity-bearing arguments it resolves directly (modulevars.go's
// callerVariables/moduleEvaluator), including the pre-resolution data-read
// coverage this package never has. That machinery is not reusable here
// as-is: it is a method on the resolver's own struct, carries a
// dataLookup this package's static-only subset must never grant, and (#315)
// widens a for_each's own KEY set past what its VALUES prove - a widening
// [instanceRepetitionData] itself deliberately does not apply to a
// resource's own count/for_each, and applying it only at the module layer
// would let two siblings answer the same "is this collection known" question
// two different ways. This file re-derives the identity package's own
// insight - rebuild the child's var.* closure against the parent's ACTUAL
// per-instance evaluator, via [configs.ModuleCall.VariablesUsing] and
// [configs.StaticEvaluator.WithVariables] - using exactly
// [directReadFallback]'s own narrower, non-widened primitives
// ([staticeval.Count], [staticeval.ForEachElements]), the same discipline
// [repetitionDataFor] already holds a resource's own block to.
//
// # How deep this goes, and why
//
// [moduleScope] recurses outward one module-call boundary at a time,
// terminating at the root module, which needs no rebuild at all (its own
// variables come from -var/tfvars, answered by [configs.Module.StaticEvaluator]
// with no caller-instance ambiguity to begin with). There is no depth limit
// coded in: a module nested inside a module inside a module resolves by
// applying the SAME rule outward at each boundary, because each level's own
// [configs.StaticEvaluator] is rebuilt from its OWN parent's evaluator
// before that evaluator is handed down - so a grandparent's own for_each
// binding is visible to a grandchild's variable expression exactly the way
// [identity.resolver.moduleEvaluator]'s own recursion documents it. The
// bound is not "how many levels this package supports" but "how many
// levels the surrounding module tree actually has": every level refuses
// independently and for its own stated reason the moment ITS OWN call's
// collection, or a variable value contributed by it, is not itself
// statically known - a wrong guess never propagates past the boundary that
// could not prove it.

// moduleScope returns the *configs.Module a resource declared inside modInst
// should be evaluated against - a module whose own [configs.StaticEvaluator]
// answers var.* through the ACTUAL argument values modInst's own ancestor
// `module` calls pass down, not the frozen, load-time closure every
// instance of a for_each'd or count'd call shares - or reports why it could
// not build one.
//
// modInst's own module is looked up fresh (via [identity.ConfigForModule])
// rather than trusted from a caller's earlier lookup, so this is always the
// single place that decides which [*configs.Module] a caller evaluates
// modInst's own resources against.
func moduleScope(ctx context.Context, root *configs.Config, modInst addrs.ModuleInstance) (*configs.Module, string, bool) {
	cfg, ok := identity.ConfigForModule(root, modInst)
	if !ok || cfg.Module == nil {
		return nil, fmt.Sprintf("%s's own module could not be found in the configuration", modInst), false
	}

	if len(modInst) == 0 {
		// The root module has no caller: its own var.* answers -var/tfvars
		// input, which [configs.Module.StaticEvaluator] already resolves
		// with no per-instance ambiguity, so there is nothing to rebuild.
		return cfg.Module, "", true
	}

	parentInst, callInst := modInst.CallInstance()
	parentMod, cause, ok := moduleScope(ctx, root, parentInst)
	if !ok {
		return nil, cause, false
	}

	mc, ok := parentMod.ModuleCalls[callInst.Call.Name]
	if !ok || mc.Config == nil {
		return nil, fmt.Sprintf("%s's own module call could not be found in %s's configuration", modInst, parentInst), false
	}

	// Pure for the reason [identity.resolver.moduleEvaluator] gives its own
	// identical call: an identity is a claim about which cloud object a
	// block owns, and a module argument that calls uuid() or timestamp()
	// must not mint a fresh one every run this fallback attempts.
	parentEval := parentMod.StaticEvaluator
	if parentEval == nil {
		return nil, fmt.Sprintf("%s's parent module carries no static evaluator", modInst), false
	}
	parentEval = parentEval.Pure()

	if mc.Count != nil || mc.ForEach != nil {
		rep, ok := moduleCallRepetitionData(ctx, parentMod, mc, callInst.Key)
		if !ok {
			return nil, fmt.Sprintf(
				"%s is one instance of %s's own %s, and that block's own collection is not itself statically known from configuration alone, so there is no per-instance count.index/each.key/each.value scope to evaluate the arguments %s passes down to %s with",
				callInst, callInst, collectionKindFor(mc.Count, mc.ForEach), callInst, modInst), false
		}
		parentEval = parentEval.WithRepetitionData(rep)
	}

	scoped := *cfg.Module
	scoped.StaticEvaluator = cfg.Module.StaticEvaluator.WithVariables(mc.VariablesUsing(ctx, parentEval))
	return &scoped, "", true
}

// moduleCallRepetitionData is [repetitionDataFor] run against a module
// CALL's own count/for_each block instead of a resource's - mc's own
// arguments (the `module "x" { ... }` block's attributes, which feed the
// child module's variables) are what [staticeval.Allowed]'s "each"/"count"
// roots answer for rep, exactly as a resource's own name/path arguments do
// for [instanceRepetitionData]'s rep. mod is the PARENT module mc.Count/
// mc.ForEach are written in, never the child mc calls into - the same
// caller/callee distinction [identity.ChildModuleRepetitionData]'s own doc
// draws for the identical reason.
func moduleCallRepetitionData(ctx context.Context, mod *configs.Module, mc *configs.ModuleCall, key addrs.InstanceKey) (instances.RepetitionData, bool) {
	return repetitionDataFor(ctx, mod, mc.Count, mc.ForEach, key)
}

// firstModuleVariableReferenced reports the name of the first module input
// variable rc's own argName argument refers to (a "var" traversal root),
// and whether it found one at all. It is the narrowest possible probe -
// exactly the roots [staticeval.FirstDisallowedScoped] would already admit
// at the RESOURCE's own level - deliberately not a full evaluation, because
// [moduleVariableProblem] only wants to know whether there is a module
// variable worth diagnosing before it goes looking for why that variable's
// OWN value might have failed.
func firstModuleVariableReferenced(rc *configs.Resource, argName string) (string, bool) {
	content, _, diags := rc.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: argName}},
	})
	if diags.HasErrors() {
		return "", false
	}
	attr, ok := content.Attributes[argName]
	if !ok {
		return "", false
	}
	for _, trav := range attr.Expr.Variables() {
		if trav.RootName() != "var" {
			continue
		}
		if len(trav) < 2 {
			continue
		}
		if step, ok := trav[1].(hcl.TraverseAttr); ok {
			return step.Name, true
		}
	}
	return "", false
}

// moduleVariableProblem is issue #1063's own distinguishing refusal: given
// that [composeIAMPolicyARN] is about to (or already did) fail to evaluate
// rc's own argName argument, decide whether the failure traces to the
// PARENT module call's own argument for a variable argName refers to,
// rather than to anything on rc's own body - and if so, say that plainly
// instead of letting it surface as the generic "its %s argument could not
// be evaluated" text [composeIAMPolicyARN]'s own caller already renders for
// a resource-level failure.
//
// This has to be answered BEFORE composeARN's own generic failure, not
// inferred from it: by the time [staticeval.ArgumentScoped] reports failure,
// it has already folded "the resource's own root is disallowed", "the
// module variable behind it could not be evaluated" and "the result is not
// a usable string" into one boolean, with no way for a caller downstream to
// tell those apart again. Reaching into the module chain a second time,
// independently, is the only way to keep the two refusals distinguishable
// - the same reason [instanceRepetitionData]'s own refusal is rendered
// BEFORE [composeIAMPolicyARN] is even called, rather than left for that
// function to explain.
//
// distinct is false whenever this cannot say anything more specific than
// the resource-level refusal already will - argName does not reference a
// module variable at all (root module, or no "var" reference), or that
// variable's own value evaluates just fine (so whatever fails next is
// rc's own problem, not its caller's).
func moduleVariableProblem(ctx context.Context, root *configs.Config, addr addrs.AbsResourceInstance, rc *configs.Resource, argName string) (string, bool) {
	if len(addr.Module) == 0 {
		return "", false
	}
	varName, found := firstModuleVariableReferenced(rc, argName)
	if !found {
		return "", false
	}

	parentInst, callInst := addr.Module.CallInstance()
	parentMod, cause, ok := moduleScope(ctx, root, parentInst)
	if !ok {
		return fmt.Sprintf(
			"%s's own %s argument depends on %s's own var.%s, and %s's own module call cannot be evaluated because %s",
			addr, argName, callInst, varName, callInst, cause), true
	}

	mc, ok := parentMod.ModuleCalls[callInst.Call.Name]
	if !ok || mc.Config == nil {
		return "", false
	}

	variable, ok := parentModuleVariable(root, addr.Module, varName)
	if !ok {
		// Not one of THIS module's own declared variables - a nested
		// module deeper than one level, or a naming mismatch this probe
		// cannot account for. Leave it to the generic message.
		return "", false
	}

	parentEval := parentMod.StaticEvaluator.Pure()
	if mc.Count != nil || mc.ForEach != nil {
		rep, ok := moduleCallRepetitionData(ctx, parentMod, mc, callInst.Key)
		if !ok {
			// moduleScope(addr.Module) already succeeded above, which means
			// it already proved this same repetition once; this branch is
			// unreachable in practice and exists only so a future change to
			// either function cannot silently disagree with the other.
			return "", false
		}
		parentEval = parentEval.WithRepetitionData(rep)
	}

	_, diags := mc.VariablesUsing(ctx, parentEval)(variable)
	if !diags.HasErrors() {
		return "", false
	}
	return fmt.Sprintf(
		"%s's own %s argument depends on %s's own var.%s, and %s's own %s argument (which var.%s's value comes from) could not be evaluated from configuration alone, even with the call's own per-instance scope in hand - the problem is in the module call that declares %s, not in %s's own argument",
		addr, argName, callInst, varName, callInst, varName, varName, callInst.Call.Name, addr), true
}

// parentModuleVariable looks up one declared *configs.Variable of modInst's
// own module by name, so [moduleVariableProblem] can hand
// [configs.ModuleCall.VariablesUsing]'s closure the exact object it expects
// - the closure reads variable.Name and variable.Default/Required() off it,
// never anything this probe would have to fabricate.
func parentModuleVariable(root *configs.Config, modInst addrs.ModuleInstance, name string) (*configs.Variable, bool) {
	cfg, ok := identity.ConfigForModule(root, modInst)
	if !ok || cfg.Module == nil {
		return nil, false
	}
	v, ok := cfg.Module.Variables[name]
	return v, ok
}
