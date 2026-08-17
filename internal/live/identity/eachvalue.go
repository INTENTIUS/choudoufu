// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/configs"
)

// This file is #260: each.value.<attr> resolved by SELECTING one attribute
// out of the for_each element's own expression, rather than by evaluating
// the element as a value.
//
// The shape it exists for is a for_each source whose elements are objects
// with one dynamic attribute among several literal ones:
//
//	route53_records = {
//	  A    = { name = local.name, type = "A", zone_id = data.aws_route53_zone.this.id }
//	  AAAA = { name = local.name, type = "AAAA", zone_id = data.aws_route53_zone.this.id }
//	}
//
// zone_id is a data-source read, so the element does not evaluate as a
// value, so each.value carried no binding at all and `type = each.value.type`
// refused - over a string literal sitting two lines from the reference. The
// element's key set was already proven structurally (#178's key-set fix);
// this proves its ATTRIBUTES the same way, through [resolver.selectStatic],
// which is the machinery the identical shape reached through a local has
// used since #178.
//
// # Why the selection is expression-shaped and not value-shaped
//
// The obvious alternative - bind each.value to a partial object, or to an
// object with unknown members - is unsafe, and the reason is try().
//
// Today `try(each.value.name, "fallback")` refuses because
// [configs.StaticEvaluator]'s reference pre-scan
// (staticScopeData.StaticValidateReferences) rejects each.value before HCL
// evaluates anything, so try's error-catching never runs. Anything that
// makes each.value evaluable moves the failure into the evaluation path,
// where try DOES catch it - and two cases that are distinguishable today
// collapse into one:
//
//   - the attribute is genuinely absent from the element, the author wrote
//     try() precisely for that, and stock OpenTofu takes the fallback. This
//     is real: terraform-aws-modules/vpc's vpc-endpoints module reads
//     try(each.value.protocol, "tcp") over elements that carry only
//     description and cidr_blocks.
//   - the attribute is PRESENT and merely unresolvable here - it names a
//     data source, or a resource attribute that is not an identity
//     attribute. Dropping it from a partial object makes the reference
//     error, try returns the fallback, and the fallback is written into a
//     cloud tag as the resource's identity. A wrong marker, silently, where
//     a refusal stood before.
//
// A value cannot tell those apart: both arrive as "not there". An
// expression can, because the element's syntax is still in hand -
// [resolver.selectStatic]'s three-valued (parts, ok, applicable) return
// answers exactly this question, and [resolver.eachAttrAbsent] is the
// second half of it, proving absence only from a constructor whose every
// key this package could read.
//
// # What absence means, and when it may be claimed
//
// Absence is a property of the value the MODULE sees, not of the expression
// the caller wrote. `optional(string, "tcp")` in a variable's declared type
// turns an absent attribute into a present one before anything inside the
// module reads it - prepareFinalInputVariableValue applies the defaults and
// the conversion first, which is [varConvertedElems] here. So an element
// expression only survives a module-variable hop when that hop converts
// nothing at all (an undeclared variable, `type = any`, or a local, which
// has no declared type); every other hop drops the expression and leaves
// this file with nothing to say. All four corpus sites under a try() are
// `type = any`.

// eachAttrPresence is what this package can prove about one attribute of a
// for_each element whose value it could not evaluate.
type eachAttrPresence int

const (
	// eachAttrUndecided is the default and the safe one: nothing here can
	// say whether the element has this attribute, and no diagnostic explains
	// why. try() must NOT fall back over it, because falling back would
	// substitute a literal for a value the configuration really does supply.
	eachAttrUndecided eachAttrPresence = iota

	// eachAttrUnresolved means the attribute IS there and the selection
	// refused it - a data-source read, a non-identity attribute of a
	// sibling, a sensitive value - leaving its own diagnostic behind, which
	// says more about the cause than anything this file could write. try()
	// must not fall back over it either: the attribute exists, so the
	// language never reaches the fallback.
	eachAttrUnresolved

	// eachAttrAbsent means the element provably does not have the attribute:
	// its expression is an object constructor (or a merge of them) whose
	// every key this package read, and none of them is this one. Referencing
	// it raises "This object does not have an attribute named ..." in stock
	// OpenTofu, so try() moves past it and a bare reference refuses.
	eachAttrAbsent

	// eachAttrPresent means the attribute is there and its own expression
	// resolved into identity parts.
	eachAttrPresent
)

// eachValueSelect resolves each.value.<attr>... against the element
// expression bound for this instance, without evaluating the element.
//
// It records no diagnostic of its own, but it does let
// [resolver.selectStatic]'s stand, reporting eachAttrUnresolved so a caller
// knows one is there. Rolling that back is the PROBE's job, not this
// function's: [resolver.fallbackArmVerdict] may be about to select a
// different try() argument entirely, so it marks and rewinds around the
// call, exactly as [resolver.soleElementFromValue]'s caller does.
func (r *resolver) eachValueSelect(trav hcl.Traversal, scope instScope, ident configs.StaticIdentifier) (parts []Part, presence eachAttrPresence) {
	b := scope.eachValueExpr
	if b == nil || b.expr == nil || len(trav) < 2 || !isAttrStep(trav[1], "value") {
		return nil, eachAttrUndecided
	}
	rest := make([]hcl.Traverser, 0, len(trav)-2)
	for _, step := range trav[2:] {
		rest = append(rest, step)
	}

	// The element expression belongs to the module it was WRITTEN in, which
	// is the calling module whenever the for_each source came through a
	// module variable - [resolver.namedDef]'s hop, whose own restore() ran
	// long before this instance's arguments were resolved. Re-entering it
	// puts back the locals, variables, data results and provider mapping
	// that hop had.
	savedMod, savedCfg, savedInst, savedEval := r.mod, r.curCfg, r.modInst, r.eval
	if !r.enterModuleFor(b.modInst) {
		return nil, eachAttrUndecided
	}
	defer func() { r.mod, r.curCfg, r.modInst, r.eval = savedMod, savedCfg, savedInst, savedEval }()

	got, ok, applicable := r.selectStatic(b.expr, rest, b.scope, ident, 0)
	switch {
	case applicable && ok:
		return got, eachAttrPresent
	case applicable:
		// Present, and this package cannot resolve it. Whatever the reason,
		// the attribute is NOT absent, so nothing may fall back over it.
		return nil, eachAttrUnresolved
	case r.eachAttrAbsent(b.expr, b.scope, rest, ident):
		return nil, eachAttrAbsent
	}
	return nil, eachAttrUndecided
}

// eachValuePart is [resolver.resolveTraversal]'s each.value branch for an
// expression-bound instance. Unlike [resolver.eachValueSelect] it does
// record a diagnostic when it cannot answer, because a bare reference that
// resolves to nothing is a refusal.
func (r *resolver) eachValuePart(trav hcl.Traversal, scope instScope, ident configs.StaticIdentifier) ([]Part, bool) {
	rng := trav.SourceRange()
	parts, presence := r.eachValueSelect(trav, scope, ident)
	switch presence {
	case eachAttrPresent:
		return parts, true
	case eachAttrUnresolved:
		// The selection's own diagnostic is already recorded and names the
		// actual obstacle; a second, vaguer one on top of it would only
		// bury it.
		return nil, false
	case eachAttrAbsent:
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s reads %s, but the element this instance's for_each binds each.value to does not have that attribute. "+
				"OpenTofu raises \"This object does not have an attribute named ...\" for the same reference; wrap it in try() with a fallback, or add the attribute to the for_each source.",
			ident.Subject, traversalString(trav))
		return nil, false
	}
	r.errorf(rng, "Identity not resolvable from configuration",
		"%s reads %s, and the value the for_each source binds to this instance is not knowable from configuration alone. "+
			"An identity can be composed from constants, variables, locals, path and terraform/tofu values, and other managed resources' identity attributes.",
		ident.Subject, traversalString(trav))
	return nil, false
}

// eachAttrAbsent proves that the element expression does not carry the
// attribute rest names - the one claim in this file that licenses try() to
// take a fallback, and therefore the one that has to be conservative in
// exactly one direction: a false here costs a refusal, a wrong true costs a
// fabricated marker.
//
// It proves absence only from syntax it read completely: an object
// constructor whose every key expression evaluated to a readable string, or
// a merge() of such constructors (merge's result has an attribute iff some
// argument does). Anything else - a local, a module output, a function call,
// a constructor with one key it could not read - is not absence but
// ignorance, and answers false.
//
// Deliberately NOT chased: a local or variable alias. [resolver.selectStatic]
// already chases those and would have answered applicable=true had the alias
// carried the key, but its applicable=false conflates "the alias does not
// have it" with "the chase did not understand the alias", and only the first
// is absence. Extending absence through the alias needs the chase to report
// which of those it hit; until it does, an aliased element refuses rather
// than falls back.
func (r *resolver) eachAttrAbsent(expr hcl.Expression, scope instScope, rest []hcl.Traverser, ident configs.StaticIdentifier) bool {
	if len(rest) != 1 {
		// Zero steps is a bare each.value, which is the element itself and
		// cannot be absent. More than one step asks about an attribute of an
		// attribute, and this proves nothing about depth.
		return false
	}
	name, ok := stepKeyString(rest[0])
	if !ok {
		return false
	}
	return r.objectLacksKey(expr, scope, name, ident, 0)
}

func (r *resolver) objectLacksKey(expr hcl.Expression, scope instScope, name string, ident configs.StaticIdentifier, depth int) bool {
	if depth > maxStaticDecomposeDepth {
		return false
	}
	switch e := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		return r.objectLacksKey(e.Expression, scope, name, ident, depth+1)

	case *hclsyntax.ObjectConsExpr:
		for _, item := range e.Items {
			kv, diags := r.evalPure(item.KeyExpr, scope, ident)
			if diags.HasErrors() {
				// One key this package cannot read is one attribute the
				// element might have. Not absence.
				return false
			}
			ks, err := convert.Convert(kv, cty.String)
			// IsMarked before AsString, which panics on a marked value - the
			// same guard [resolver.objectConsElems] and
			// [resolver.selectStatic] carry, for the same reason.
			if err != nil || ks.IsNull() || !ks.IsKnown() || ks.IsMarked() {
				return false
			}
			if ks.AsString() == name {
				return false
			}
		}
		return true

	case *hclsyntax.FunctionCallExpr:
		if e.Name != "merge" || e.ExpandFinal {
			// A splatted final argument is a list whose length this has not
			// read, so "no argument has it" is not provable from the call.
			return false
		}
		for _, arg := range e.Args {
			if !r.objectLacksKey(arg, scope, name, ident, depth+1) {
				return false
			}
		}
		return len(e.Args) > 0
	}
	return false
}
