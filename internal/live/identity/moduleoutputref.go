// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// moduleOutputRef is one "module.<call>[<key>].<output><rest>" reference
// that has passed the guards every reader of a child module's output
// applies before entering the child: [resolver.resolveModuleOutput] for an
// identity argument, [resolver.moduleOutputValue] for a module-call
// argument, and the two key-set chases for a for_each (#2142,
// [resolver.staticCollElems] and [collectStaticForEachKeys]).
type moduleOutputRef struct {
	call string
	mc   *configs.ModuleCall
	// key is the instance the reference names: addrs.NoKey for a call with
	// no repetition, else the index the reference wrote.
	key addrs.InstanceKey
	// repeated reports whether the call has count or for_each.
	repeated bool
	name     string
	out      *configs.Output
	// rest is whatever the reference selects out of the output's value.
	rest []hcl.Traverser
}

// parseModuleOutputRef applies those guards to a reference written in cfg's
// module, callName being the step after "module" and rest everything after
// it. Every false is a decline, which leaves the caller's existing refusal
// standing:
//
//   - the call must exist with its configuration loaded;
//   - an indexed reference supplies the instance key and is only meaningful
//     on a repeated call, and an unindexed one only on a call with neither
//     count nor for_each. "module.foo.bar" on a for_each'd call names an
//     object holding every instance's value, which is not one value, and
//     "module.foo[0].bar" on an unrepeated call names an instance that does
//     not exist;
//   - the key must be one [ChildModuleRepetitionData] says the call expands
//     to. Without this, module.foo[7] on a three-instance call would enter
//     the child anyway - [addrs.ModuleInstance.Child] builds any step asked
//     of it and the child's configuration resolves by name - and answer for
//     an instance that will never exist;
//   - "module.foo" with no output named is a whole-module reference, an
//     object of every output rather than one value. The repetition check
//     above already rejects it on a repeated call, because addrs.NoKey is
//     never among a repeated call's keys; it is refused here on its own
//     terms anyway, one step earlier and for the reason a reader would give;
//   - the output must be one the child declares, with an expression.
//
// Whether a SENSITIVE output may be read is the caller's decision, because
// the callers differ today: a key set or a module-call argument refuses one
// on its declaration, and that is the caller's line, not this function's.
//
// The child's configuration is the same node whatever the instance key, so
// the output is looked up without entering any instance; a caller that
// evaluates the output still enters the instance it means.
func parseModuleOutputRef(ctx context.Context, cfg *configs.Config, callName string, rest []hcl.Traverser) (moduleOutputRef, bool) {
	if cfg == nil || cfg.Module == nil {
		return moduleOutputRef{}, false
	}
	mc, ok := cfg.Module.ModuleCalls[callName]
	if !ok || mc == nil || mc.Config == nil {
		return moduleOutputRef{}, false
	}

	ref := moduleOutputRef{call: callName, mc: mc, key: addrs.NoKey}
	ref.repeated = mc.Count != nil || mc.ForEach != nil
	if len(rest) > 0 {
		if idx, isIndex := rest[0].(hcl.TraverseIndex); isIndex {
			if !ref.repeated {
				return moduleOutputRef{}, false
			}
			k, ok := indexKeyValue(idx.Key)
			if !ok {
				return moduleOutputRef{}, false
			}
			ref.key = k
			rest = rest[1:]
		} else if ref.repeated {
			return moduleOutputRef{}, false
		}
	} else if ref.repeated {
		return moduleOutputRef{}, false
	}

	if ref.repeated {
		if _, ok := ChildModuleRepetitionData(ctx, cfg, childSubject(callName), mc.Count, mc.ForEach, ref.key); !ok {
			return moduleOutputRef{}, false
		}
	}

	if len(rest) == 0 {
		return moduleOutputRef{}, false
	}
	outStep, isAttr := rest[0].(hcl.TraverseAttr)
	if !isAttr {
		return moduleOutputRef{}, false
	}
	ref.name = outStep.Name
	ref.rest = rest[1:]

	child := cfg.Children[callName]
	if child == nil || child.Module == nil {
		return moduleOutputRef{}, false
	}
	out, ok := child.Module.Outputs[ref.name]
	if !ok || out == nil || out.Expr == nil {
		return moduleOutputRef{}, false
	}
	ref.out = out
	return ref, true
}

// moduleOutputCollElems is [resolver.staticCollElems]'s hop into a child
// module's output (#2142): `for_each = module.helper.set_authoritative`,
// where the helper module computes that set from its own variables.
//
// It is the same rule [resolver.resolveModuleOutput] applies to an identity
// argument - an output is an expression written in the child module, and the
// child's scope is one this resolver can enter - applied to the key set. The
// guards are [parseModuleOutputRef]'s; beyond them:
//
//   - a sensitive output refuses on its declaration, as
//     [resolver.moduleOutputValue] does, because a key becomes part of an
//     address and an address is written to a tofu-address marker in clear;
//   - a selector past the output (module.x.out.attr) declines, the same
//     boundary [resolver.staticForEachKeys] draws for local.foo.bar;
//   - the chase recurses into the output's own expression with a fresh scope,
//     for resolveModuleOutput's reason: the referring block's each, count and
//     for-comprehension variables do not exist in the child;
//   - where that structural chase does not prove the key set, the output's
//     expression is evaluated WHOLE, strictly, in the child instance's own
//     evaluator - which is where `local.authoritative ? toset(local.keys) :
//     []` lands, since the chase has no conditional or toset() case. Anything
//     that expression reads and the static scope cannot answer (a managed
//     resource's attribute, a data source result, a provider function, a
//     further module output) fails that evaluation, and the caller's refusal
//     stands. Nothing here substitutes an unknown or a guess for it.
//
// A value is bound to a key only where the whole evaluation produced it,
// wholly known; the structural chase binds exactly what it always binds.
func (r *resolver) moduleOutputCollElems(callName string, rest []hcl.Traverser, ident configs.StaticIdentifier, depth int, tupleIsArgs bool) ([]cty.Value, []elemBinding, bool) {
	ref, ok := parseModuleOutputRef(r.ctx, r.curCfg, callName, rest)
	if !ok || ref.out.Sensitive || len(ref.rest) > 0 {
		return nil, nil, false
	}

	savedMod, savedCfg, savedInst, savedEval := r.mod, r.curCfg, r.modInst, r.eval
	if !r.enterModuleFor(r.modInst.Child(callName, ref.key)) {
		return nil, nil, false
	}
	defer func() { r.mod, r.curCfg, r.modInst, r.eval = savedMod, savedCfg, savedInst, savedEval }()

	// The structural chase leaves diagnostics behind when it declines; the
	// caller's own refusal is the one that stands, so roll them back.
	mark := len(r.diags)
	if keys, elems, ok := r.staticCollElems(ref.out.Expr, ident, depth+1, tupleIsArgs, instScope{}); ok {
		return keys, elems, true
	}
	r.diags = r.diags[:mark]

	if len(impureCallsIn(ref.out.Expr)) > 0 {
		return nil, nil, false
	}
	outIdent := configs.StaticIdentifier{
		Module:    r.modInst.Module(),
		Subject:   "output." + ref.name,
		DeclRange: ref.out.DeclRange,
	}
	val, diags := r.evalPure(ref.out.Expr, instScope{}, outIdent)
	if diags.HasErrors() {
		return nil, nil, false
	}
	return wholeCollElems(val)
}

// wholeCollElems is the key set of a collection value that was evaluated
// whole, with each element bound as its value: a map's or an object's keys,
// a list's or a tuple's indices, a set's elements (a set has no key of its
// own, so cty's iterator hands back the element in both halves, which is
// the key a for_each over a set uses). Anything not wholly known, null,
// marked anywhere, or not a collection at all declines - an unknown here
// could hide a key, and a primitive has no key set to answer with.
func wholeCollElems(val cty.Value) ([]cty.Value, []elemBinding, bool) {
	if val == cty.NilVal || val.IsNull() || val.ContainsMarked() || !val.IsWhollyKnown() {
		return nil, nil, false
	}
	ty := val.Type()
	if !ty.IsMapType() && !ty.IsObjectType() && !ty.IsListType() && !ty.IsTupleType() && !ty.IsSetType() {
		return nil, nil, false
	}
	keys := make([]cty.Value, 0, val.LengthInt())
	elems := make([]elemBinding, 0, val.LengthInt())
	for it := val.ElementIterator(); it.Next(); {
		k, v := it.Element()
		keys = append(keys, k)
		elems = append(elems, elemBinding{val: v})
	}
	return keys, elems, true
}
