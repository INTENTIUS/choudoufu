// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package dataread

// Issue #193's fix class (c), behind [Options.ProjectManagedArguments] and
// OFF by default: a managed resource attribute reference answered from the
// resource block's own configuration argument - offline, from configuration
// alone, with no live read and no state.
//
// The rule is narrow and structural. aws_mq_broker.x.subnet_ids is refused
// today as a managed reference, but subnet_ids is an argument the broker's
// own block SETS; the value is in the configuration, and reading it there is
// the same epistemic step this fork already takes when it synthesizes a
// resource's identity from its own arguments, taken through one more
// reference. aws_vpc.this[0].id is a different thing entirely: nothing in
// the configuration sets it, so there is nothing to project and it keeps
// refusing exactly as before. Which of the two a reference is, is decided by
// whether the block's body carries an attribute of that name - never by a
// type name, and never by a guess.
//
// WHAT IS NOT BUILT, and why the option must stay off until it is: this is
// CLASSIFICATION only. [Analyze] never needs a real value - its lookup
// answers coverage with cty.DynamicVal - so this returns an object keyed by
// the projectable argument names with unknown values. [Read] still has no
// projector, so a live-plan whose data source needs a projected value hits
// read.go's IsWhollyKnown guard and refuses. Turning the option on before
// the read side lands would make live-check report "no configuration edit is
// needed" for a configuration live-plan then refuses - the false-reassurance
// shape, which is worse than the refusal it replaces.
//
// Two further limits, both deliberate and both measurable rather than
// guessed at: a managed block's nested blocks (the broker's `user { ... }`)
// are not projected, only its top-level attributes; and expansion is not
// modelled, so a reference through a count or for_each index falls back to
// refusing (see lookupCoversTraversal in internal/configs/static_scope.go).

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/lang"
)

// managedProjection returns the statically-derivable arguments of one managed
// resource block, as an object value, or false when the module declares no
// such block or nothing in it evaluates.
func (an *analyzer) managedProjection(module addrs.Module, res addrs.Resource, lookup func(addrs.Module) configs.StaticDataLookup) (cty.Value, bool) {
	if !an.projectManaged || res.Mode != addrs.ManagedResourceMode {
		return cty.NilVal, false
	}
	key := "managed:" + sourceKey(module, res)
	if v, ok := an.managedCache[key]; ok {
		return v.val, v.ok
	}
	if an.visiting[key] {
		// A cycle between managed blocks: nothing to project.
		return cty.NilVal, false
	}
	node := an.cfg.Descendent(module)
	if node == nil || node.Module == nil {
		return cty.NilVal, false
	}
	rc := node.Module.ManagedResources[res.String()]
	if rc == nil {
		return cty.NilVal, false
	}
	body, bodyOK := rc.Config.(*hclsyntax.Body)
	if !bodyOK {
		return cty.NilVal, false
	}

	an.visiting[key] = true
	defer delete(an.visiting, key)

	attrs := map[string]cty.Value{}
	for name, attr := range body.Attributes {
		if metaArguments[name] {
			continue
		}
		ne := namedExpr{label: name, expr: attr.Expr}
		if an.projectionEvaluates(module, rc, ne, lookup) {
			attrs[name] = cty.DynamicVal
		}
	}
	if len(attrs) == 0 {
		an.managedCache[key] = managedProj{cty.NilVal, false}
		return cty.NilVal, false
	}
	val := cty.ObjectVal(attrs)
	an.managedCache[key] = managedProj{val, true}
	return val, true
}

type managedProj struct {
	val cty.Value
	ok  bool
}

// projectionEvaluates reports whether one argument expression of a managed
// block evaluates statically in its own module, through the same live
// evaluator the data-source path uses.
func (an *analyzer) projectionEvaluates(module addrs.Module, rc *configs.Resource, ne namedExpr, lookup func(addrs.Module) configs.StaticDataLookup) (ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			ok = false
		}
	}()
	eval := liveModuleEvaluator(an.ctx, an.cfg, module, lookup)
	if eval == nil {
		return false
	}
	ident := configs.StaticIdentifier{
		Module:    module,
		Subject:   rc.Addr().String() + "'s " + ne.label,
		DeclRange: ne.expr.Range(),
	}
	refs, bad := staticRefs(ne.expr)
	if bad {
		return false
	}
	hclCtx, ctxDiags := eval.EvalContextWithParent(an.ctx, nil, ident, refs)
	if ctxDiags.HasErrors() {
		return false
	}
	if hclCtx == nil {
		return true
	}
	_, valDiags := ne.expr.Value(hclCtx)
	return !valDiags.HasErrors()
}

// lookupFactory is the data-source coverage closure factory lifted out of
// [analyzer.evalRecorded] so the projection can reuse it, with the
// managed-mode branch added (#193). With the option off the managed branch
// answers false for everything and this is byte-for-byte the closure
// evalRecorded built inline before.
func (an *analyzer) lookupFactory(record func(addrs.Module, addrs.Resource)) func(addrs.Module) configs.StaticDataLookup {
	return func(m addrs.Module) configs.StaticDataLookup {
		return func(addr addrs.Resource) (cty.Value, bool) {
			depNode := an.cfg.Descendent(m)
			if depNode == nil || depNode.Module == nil {
				return cty.NilVal, false
			}
			dep := depNode.Module.DataResources[addr.String()]
			if dep == nil {
				if addr.Mode == addrs.ManagedResourceMode {
					return an.managedProjection(m, addr, an.lookupFactory(record))
				}
				return cty.NilVal, false
			}
			record(m, addr)
			return cty.DynamicVal, true
		}
	}
}

// staticRefs parses an expression's variables into references.
func staticRefs(expr hcl.Expression) ([]*addrs.Reference, bool) {
	refs, diags := lang.References(addrs.ParseRef, expr.Variables())
	return refs, diags.HasErrors()
}
