// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/gocty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// This file is #178's local-values fix and its key-set companion: two
// general rules about a value reached through a local's own definition, or
// through a module call's input expression, rather than about any
// particular resource type.
//
// Both raise from the same place a direct resource reference already
// avoids: [staticScopeData.GetLocalValue] and [staticScopeData.GetInputVariable]
// (internal/configs/static_scope.go) evaluate a local's or a variable's
// WHOLE defining expression the moment anything asks for it, and that
// evaluation validates every reference inside it - including a managed
// resource's attribute nobody asked for, buried in a branch the caller
// never selects. [resolver.evalStatic] then fails with "Dynamic value in
// static context", even when the one thing the identity argument actually
// wanted was a plain string sitting right next to the resource reference,
// or was the resource reference itself in a shape [resolver.resolveTraversal]
// already knows how to resolve.
//
// The fix is to ask the local's or the variable's own expression the
// narrower question the caller actually has, without evaluating it:
//
//   - staticForEachKeys asks only "what are the keys" of an object
//     constructor (directly, through var/local aliasing, or through
//     merge() of several) - the key set a for_each expansion needs, which
//     is knowable whatever the values are. It offers each key's own value
//     alongside, but only where it evaluated that one expression itself;
//     the key set never depends on a value, which is the whole point.
//   - namedLeaf and selectStatic ask "what is the one value this specific
//     selector names" - a plain literal, evaluated on its own; a managed
//     resource's attribute, resolved through the exact same
//     [resolver.resolveTraversal] / parentPart machinery a direct
//     reference already uses, so it is bound by the same identity-attribute
//     restriction and produces the same Formula-shaped answer for a
//     parent whose own identity is not yet concrete.
//
// Both are tried only after the whole-expression evaluation has already
// failed (see [resolver.resolveExpr] and [resolver.forEachExpansion]), and
// only ever supersede that failure's diagnostic when they reach a
// definite answer of their own - success or a specific refusal. Any shape
// neither function recognizes (a for-expression, a splat, a function other
// than merge, an index that cannot be read as a literal) is left alone:
// the original diagnostic stands, unchanged from today.

// maxStaticDecomposeDepth bounds how many locals, module variables, or
// merge() arguments this file will chase through before giving up. It
// exists to turn a pathological or accidentally self-referential chain into
// a clean "not applicable" rather than a stack overflow; ordinary
// configurations are two or three levels deep at most.
const maxStaticDecomposeDepth = 16

// namedDef looks up what "local.name" or "var.name" refers to: the raw
// defining expression, unevaluated; the [instScope] that expression must be
// evaluated in (never necessarily the caller's own scope - see below); and
// whether reading it requires the resolver to switch modules first - a
// module variable's value is the module CALL's argument expression, which
// lives in and is evaluated against the CALLING module, not the module that
// declares the variable.
//
// scope is the caller's own evaluation scope - the each.key/each.value/
// count.index of whatever resource instance (or, recursively, whatever
// earlier module-call argument) is asking. For "local", the returned scope
// is scope itself, unchanged: a local's own definition lives in the SAME
// module and instance as the reference to it, so it sees exactly the same
// repetition data (#213). For "var", the returned scope has nothing to do
// with the caller's own repetition at all - it is a module CALL's argument
// expression, evaluated in the CALLING module, against the CALL's OWN
// count/for_each (or no repetition, for a plain call) - so scope is read
// only to decide whether to switch modules, never propagated into the
// result.
//
// The returned restore function must be called exactly once by the caller,
// whatever else it does with the expression in between - typically via
// defer, immediately after a successful call. It is a no-op when no switch
// was needed.
//
// decl is the "variable" block the returned expression is the argument FOR,
// and is nil for a local (a local has no declared type) and nil for a "var"
// whose declaration this module does not hold. It is what makes the returned
// expression's value usable: OpenTofu never uses a module call's argument
// value as written, it converts that value to the variable's declared type
// first, in prepareFinalInputVariableValue (internal/tofu/eval_variable.go).
// Anything reading a value out of the returned expression therefore owes that
// conversion, which is what [varConvertedElems] applies. Carrying the
// declaration rather than only the type keeps the whole of that function's
// input here - the optional-attribute defaults it applies BEFORE converting
// are as load-bearing as the constraint itself.
//
// ok is false whenever there is nothing to chase: an undeclared local, a
// variable at the root module (root variables come from the CLI or tfvars,
// never from another resource, so evalStatic's ordinary handling of them
// was already correct), or a variable the caller left to its declared
// default (a default is always configuration-authored, never a resource
// reference, for the same reason).
func (r *resolver) namedDef(root, name string, scope instScope) (hcl.Expression, instScope, *configs.Variable, func(), bool) {
	noop := func() {}

	switch root {
	case "local":
		local, ok := r.mod.Locals[name]
		if !ok {
			return nil, instScope{}, nil, noop, false
		}
		return local.Expr, scope, nil, noop, true

	case "var":
		if len(r.modInst) == 0 {
			return nil, instScope{}, nil, noop, false
		}
		// Read before enterModuleFor switches r.mod: the "variable" block is
		// declared in THIS module, the one being resolved, while the argument
		// expression the switch goes to fetch lives in its caller.
		decl := r.mod.Variables[name]
		parentInst, callInst := r.modInst.CallInstance()
		// curCfg belongs in this set, not only mod/modInst/eval:
		// [resolver.enterModuleAt] sets all four together, so a restore
		// that puts back three leaves the fourth pointing at the module
		// this hop climbed INTO. The sibling restore in
		// [resolver.resolveModuleOutput] already put back all four; this
		// one did
		// not, and the consequence was silent, because curCfg has exactly
		// one reader: [resolver.resourceCloudScope], which runs at the end
		// of resolveOne and asks providerscope which provider
		// configuration the resource uses. Any resource whose identity
		// reads a var inside a module therefore had its provider resolved
		// against the module's PARENT, discarding the call's
		// `providers = { ... }` mapping - so two calls of one module,
		// remapped to two different aliased providers, produced identical
		// cloud scopes and [resolver.checkCollisions] reported them as one
		// identity. Found in terraform-aws-modules/terraform-aws-lambda's
		// examples/multiple-regions, which exists to exercise exactly that
		// shape.
		savedMod, savedCfg, savedInst, savedEval := r.mod, r.curCfg, r.modInst, r.eval
		if !r.enterModuleFor(parentInst) {
			return nil, instScope{}, nil, noop, false
		}
		restore := func() { r.mod, r.curCfg, r.modInst, r.eval = savedMod, savedCfg, savedInst, savedEval }

		mc, ok := r.mod.ModuleCalls[callInst.Call.Name]
		if !ok || mc.Config == nil {
			restore()
			return nil, instScope{}, nil, noop, false
		}
		defScope := instScope{}
		if mc.Count != nil || mc.ForEach != nil {
			// The module call's own argument expression is evaluated in
			// the scope of the call's OWN repetition (each.key/each.value
			// or count.index of the module block, not of whatever resource
			// asked for var.name) - the exact scope [resolver.walkModule]
			// threads for a resource's own for_each, reached here through
			// [ChildModuleRepetitionData], which re-derives it from the
			// call's own count/for_each expression exactly as
			// [ChildModuleCountKeys]/[ChildModuleKeys] do, rather than
			// trust callInst.Key blindly (see [ChildModuleKeys]'s doc, and
			// the 59c note on evalPure below).
			//
			// callInst.Key - unlike [addrs.ModuleInstance.Call]'s plain
			// ModuleCall, which discards it - is the key THIS instance of
			// r.modInst was built with (see
			// [addrs.ModuleInstance.CallInstance] and [resolver.walkModule]'s
			// modInst.Child(name, key)), so two different instances of the
			// same for_each'd call always resolve two different rd values
			// here, never each other's.
			//
			// The result is carried in the RETURNED scope, not spliced into
			// r.eval directly: [resolver.evalPure] always rebuilds its
			// working evaluator as r.eval.WithRepetitionData(scope.repetition)
			// from whatever scope the caller is threading at the point of
			// actual evaluation, so a repetition value set on r.eval here
			// would simply be overwritten the moment selectStatic's own
			// resolveExpr call reaches evalStatic - the caller's scope is
			// the only seam that survives that far.
			//
			// A false ok means the key could not be confirmed as this
			// call's own - not only "not implemented" but "not safe to
			// guess" - and declining here leaves the ordinary "Dynamic
			// value in static context" diagnostic in place, the same
			// answer this shape has always gotten.
			rd, ok := ChildModuleRepetitionData(r.ctx, r.curCfg, childSubject(callInst.Call.Name), mc.Count, mc.ForEach, callInst.Key)
			if !ok {
				restore()
				return nil, instScope{}, nil, noop, false
			}
			defScope = instScope{repetition: rd}
		}
		attrs, diags := mc.Config.JustAttributes()
		if diags.HasErrors() {
			restore()
			return nil, instScope{}, nil, noop, false
		}
		attr, ok := attrs[name]
		if !ok {
			restore()
			return nil, instScope{}, nil, noop, false
		}
		return attr.Expr, defScope, decl, restore, true
	}
	return nil, instScope{}, nil, noop, false
}

// ---- the key-set fix ---------------------------------------------------

// elemBinding is everything the key-set chase learned about ONE key's
// element of a for_each source, and it carries two independent answers that
// must not be conflated:
//
//   - val is the element's value if - and only if - this resolver evaluated
//     that one expression whole ([resolver.provenValue]). cty.NilVal means
//     "not proven", never "null", and every consumer reads it that way.
//   - expr is the element's own value EXPRESSION, kept whether or not val
//     proved, so that a later each.value.<attr> can select one attribute out
//     of it structurally instead of asking for the element as a value. This
//     is #260: one dynamic attribute inside an element made val NilVal and
//     therefore refused every literal sibling beside it, because a value is
//     all-or-nothing while a selection is not.
//
// scope is the scope expr must be evaluated in - a for-comprehension's own
// loop variables, or a module call's own repetition - and modInst is the
// module instance it was WRITTEN in, which is very often not the module the
// resource being resolved lives in: [resolver.namedDef]'s "var" hop reads a
// module call's argument in the CALLER, under a restore() that has long
// since run by the time an instance's arguments are resolved. Re-entering
// that module instance before touching expr is what makes the deferred
// selection see the same locals, variables and provider mapping the
// immediate one did.
type elemBinding struct {
	val     cty.Value
	expr    hcl.Expression
	scope   instScope
	modInst addrs.ModuleInstance

	// declTy is the type the element has INSIDE the module, when the
	// expression survived a hop through a declared type that is not the
	// identity function on it ([preservedExpr]'s object case). cty.NilType -
	// which is every binding built anywhere else - means the expression is
	// the element as the module sees it and nothing constrains what may be
	// selected out of it.
	//
	// A non-nil declTy makes the caller's constructor evidence of LESS than
	// it usually is, in both directions, and [resolver.eachValueSelect] is
	// where both are enforced: optional() can supply an attribute the
	// constructor never wrote, so absence may only be read off this type;
	// and a non-string attribute type converts what the caller wrote, so
	// only a string-typed attribute may be selected at all.
	declTy cty.Type
}

// binding is the ordinary way to build one: prove the value if it proves,
// and keep the expression either way, pinned to where it was written.
func (r *resolver) binding(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) elemBinding {
	return elemBinding{
		val:     r.provenValue(expr, scope, ident),
		expr:    expr,
		scope:   scope,
		modInst: r.modInst,
	}
}

// staticForEachKeys is #178's key-set fix, extended by #189's follow-up: the
// key set of an object constructor - or of a for-comprehension that builds
// one, or of a list of such things merged together - is knowable whatever
// its VALUES are, and the key set is all a for_each expansion needs to
// enumerate instances. It is tried only after [resolver.evalStatic] has
// already failed to evaluate a for_each expression as a whole (see
// [resolver.forEachExpansion]), and it succeeds only when expr is, through
// any combination of the shapes below, ultimately built from object
// constructors - never touching a single value expression, which is the
// point: a resource reference in one of them must not refuse the whole
// block the way evaluating the object as one value does.
//
// The shapes chased, at any depth and in any combination:
//
//   - local/var aliasing, through [resolver.namedDef].
//   - An object constructor directly ([resolver.objectConsElems]).
//   - merge() of several arguments, each chased the same way and unioned -
//     including merge(list...): the splatted final argument is itself
//     chased, and a list literal reached that way is handled by unioning
//     ITS OWN elements the same way merge's separate arguments always were
//     (see the TupleConsExpr case below), so merge(a, b) and
//     merge([a, b]...) reach the same answer through the same code.
//   - A tuple/list constructor, unioning every element's own key set - but
//     ONLY when it stands where merge()'s splatted final argument stands,
//     which is the sole position where a list's elements ARE the separate
//     objects being unioned. See tupleIsArgs.
//   - A for-comprehension producing an object ([resolver.forExprElems]):
//     chases the SOURCE collection's own key set - a map's keys, a list's
//     integer indices - then evaluates the comprehension's KEY clause once
//     per source element with the loop's key variable bound, and its value
//     variable bound only where the source collection evaluated whole. A
//     key clause that needs a value side nothing here read fails to
//     evaluate (an unbound reference) and refuses cleanly, rather than
//     answer with something nothing here actually knows.
//
// tupleIsArgs is the audit fix for a wrong-key-set defect the TupleConsExpr
// case shipped with: outside merge(list...)'s splat, a tuple is a LIST, and
// a list's keys are its integer indices, NOT the union of its elements' own
// object keys. Reading `[{host=...,port=...}, {...}, {...}]` as the key set
// {"host","port"} answered a THREE-element list with TWO instances under
// two invented keys - and did it silently, with no diagnostic, because
// [resolver.staticForEachKeys] only runs where evaluating the expression
// whole has already failed. It reached that answer two ways: a top-level
// `for_each = <tuple>` (which OpenTofu rejects outright - for_each takes a
// map or a set of strings) and, far more damagingly, a for-comprehension
// ranging over a list, where `{ for i, h in local.hosts : "item-${i}" => h }`
// really produces "item-0"/"item-1"/"item-2".
//
// #239 recovers the second of those, which is an ordinary idiom rather than
// an invalid configuration, by making the chase SHAPE-AWARE instead of
// declining: [resolver.staticCollElems] answers with the key values cty
// itself binds a loop variable to - a string for a map, an object or a set
// of strings, a NUMBER for a list or a tuple - and this function, which
// serves the for_each position, narrows that to the strings a for_each may
// use as instance keys. A tuple therefore still refuses HERE (its keys are
// numbers, which for_each does not accept), while [resolver.forExprElems]
// can range over it and get 0, 1, 2.
//
// It deliberately does not chase a selector before reaching the object
// (for_each = local.foo.bar is not supported): the corpus shape this fix
// exists for is always a bare local or module variable ranged over
// directly.
//
// The second result is the value each key is bound to, parallel to the
// first and cty.NilVal wherever this resolver did not evaluate one for
// itself. See [resolver.staticCollElems] for what "did not evaluate one"
// means and why an unproven value is left unbound rather than guessed.
func (r *resolver) staticForEachKeys(expr hcl.Expression, ident configs.StaticIdentifier, depth int, tupleIsArgs bool, scope instScope) ([]string, []elemBinding, bool) {
	keys, elems, ok := r.staticCollElems(expr, ident, depth, tupleIsArgs, scope)
	if !ok {
		return nil, nil, false
	}
	names, ok := stringKeys(keys)
	if !ok {
		return nil, nil, false
	}
	return names, elems, true
}

// stringKeys narrows a collection's key set to the strings a for_each
// expansion - or a merge() argument, whose keys are an object's - can use.
// A non-string key is exactly a list or tuple's integer index, and refusing
// it is what keeps `for_each = <tuple>` refused: OpenTofu rejects that
// outright ("the for_each argument must be a map, or set of strings"), so
// answering it with "0", "1" would invent instance keys nothing produces.
func stringKeys(keys []cty.Value) ([]string, bool) {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		// IsMarked before AsString, which panics rather than errors on a
		// marked value. Every producer feeding this either constructs the
		// key with cty or takes the KEY half of an element iterator, so
		// none can be marked today - but that is a property of five call
		// sites rather than of this loop, and the loop is where the read
		// happens.
		if k.Type() != cty.String || k.IsNull() || !k.IsKnown() || k.IsMarked() {
			return nil, false
		}
		out = append(out, k.AsString())
	}
	return out, true
}

// staticCollElems is the shape-aware half of the key-set chase: it answers
// with the key set of whatever collection expr denotes, each key carried as
// the cty value HCL binds a for-expression's key variable to, plus - where
// this resolver evaluated one for itself - the value bound beside it.
//
// The typing is not decoration. cty's own element iterators synthesize a
// StringVal for a map key or an object attribute name and a NumberIntVal
// for a list or tuple index (cty/element_iterator.go), and
// hclsyntax.ForExpr.Value binds e.KeyVar to that value verbatim - so
// `"item-${i}"` over a three-element list renders item-0, item-1, item-2,
// and over a map of the same size renders the map's own keys. Returning
// []string here is what made those two indistinguishable and produced the
// #239 defect; the caller that needs strings asks for them explicitly
// through [stringKeys].
//
// A SET is the third case and the one with no key of its own: cty's iterator
// hands back the element in BOTH halves, so a variable declared set(...)
// answers with its elements and not with the indices of the argument tuple
// the call wrote. Only the declared-type conversion can produce one here, so
// that is where it is read - see [varConvertedElems].
//
// No key here is ever decided by evaluating a VALUE expression, which is
// the whole point of the chase: a managed resource's attribute buried in a
// value must not refuse the block. The values returned alongside are a
// SEPARATE, strictly optional answer, and the two must not be confused:
//
//   - vals is parallel to keys and holds cty.NilVal wherever the value
//     expression for that key did not evaluate on its own in the static
//     scope - because it reaches a managed resource, a data source or a
//     module output, because it is marked, or because it calls an impure
//     function. An unproven value stays unproven; nothing here substitutes
//     a guess, and a false ok is never traded for a partial answer.
//   - a value is bound only where this resolver ACTUALLY EVALUATED that
//     one expression. A collection whose shape is knowable while its
//     contents are not - a tuple whose length is its literal's length, a
//     merge() of comprehensions reaching a sibling - yields exact keys and
//     no values at all, which is the boundary [resolver.forSourceElements]
//     draws for a for-comprehension's source and the one
//     [expansion.keyOnly] draws for each.value.
func (r *resolver) staticCollElems(expr hcl.Expression, ident configs.StaticIdentifier, depth int, tupleIsArgs bool, scope instScope) (keys []cty.Value, elems []elemBinding, ok bool) {
	if depth > maxStaticDecomposeDepth {
		return nil, nil, false
	}
	if paren, ok := expr.(*hclsyntax.ParenthesesExpr); ok {
		return r.staticCollElems(paren.Expression, ident, depth+1, tupleIsArgs, scope)
	}

	if trav, diags := hcl.AbsTraversalForExpr(expr); !diags.HasErrors() && len(trav) == 2 {
		if root := trav.RootName(); root == "local" || root == "var" {
			if nameStep, ok := trav[1].(hcl.TraverseAttr); ok {
				defExpr, defScope, decl, restore, defOk := r.namedDef(root, nameStep.Name, scope)
				if defOk {
					defer restore()
					// tupleIsArgs propagates through the alias: the corpus
					// shape is merge(local.teams...), where the splatted
					// argument is a local naming the list.
					keys, elems, ok := r.staticCollElems(defExpr, ident, depth+1, tupleIsArgs, defScope)
					if !ok {
						return nil, nil, false
					}
					// #251: what the chase just read is the module CALL's
					// argument, and no value inside a module is ever the one
					// the call wrote - OpenTofu converts to the variable's
					// declared type first. decl is nil for a local, where
					// there is no declared type and nothing to apply; for a
					// variable it is applied here, at the hop, so a chain of
					// hops converts once per hop exactly as OpenTofu does.
					// The KEYS come back from the conversion too: a map or an
					// object keeps the ones it was given, but a SET's keys ARE
					// its elements, so the argument's own indices are not the
					// key set the module sees.
					return varConvertedElems(decl, keys, elems)
				}
			}
		}
	}

	// A collection reached through an enclosing for-comprehension's own
	// value variable - the binding [instScope.exprVars] carries and nothing
	// on this side used to be able to see. See [resolver.elemVarSource].
	if leaf, leafScope, leafMod, ok := r.elemVarSource(expr, scope, ident); ok {
		savedMod, savedCfg, savedInst, savedEval := r.mod, r.curCfg, r.modInst, r.eval
		if !r.enterModuleFor(leafMod) {
			return nil, nil, false
		}
		defer func() { r.mod, r.curCfg, r.modInst, r.eval = savedMod, savedCfg, savedInst, savedEval }()
		return r.staticCollElems(leaf, ident, depth+1, tupleIsArgs, leafScope)
	}

	if obj, ok := expr.(*hclsyntax.ObjectConsExpr); ok {
		names, itemElems, ok := r.objectConsElems(obj, ident, scope)
		if !ok {
			return nil, nil, false
		}
		return stringVals(names), itemElems, true
	}

	if fe, ok := expr.(*hclsyntax.ForExpr); ok {
		names, forElems, ok := r.forExprElems(fe, ident, depth, scope)
		if !ok {
			return nil, nil, false
		}
		// A for-comprehension with a key clause produces an OBJECT, whose
		// keys are strings whatever it ranged over.
		return stringVals(names), forElems, true
	}

	if tuple, ok := expr.(*hclsyntax.TupleConsExpr); ok {
		if !tupleIsArgs {
			// A tuple anywhere but merge()'s splat is a list, and a list's
			// keys are its integer indices: one per element, distinct by
			// construction, and knowable from the literal's own length
			// whatever its elements turn out to be. Each element's own
			// expression is what a for-comprehension over this list binds
			// its value variable to, so it is offered where it evaluates.
			keys := make([]cty.Value, 0, len(tuple.Exprs))
			elems := make([]elemBinding, 0, len(tuple.Exprs))
			for i, elem := range tuple.Exprs {
				keys = append(keys, cty.NumberIntVal(int64(i)))
				elems = append(elems, r.binding(elem, scope, ident))
			}
			return keys, elems, true
		}
		u := newKeyUnion()
		for _, elem := range tuple.Exprs {
			// An element of the splatted list is one of merge's arguments,
			// an object in its own right - never itself a list of them, and
			// never a list at all, which is why this asks for strings.
			got, gotElems, ok := r.staticForEachKeys(elem, ident, depth+1, false, scope)
			if !ok {
				return nil, nil, false
			}
			u.add(got, gotElems)
		}
		names, uElems := u.result()
		return stringVals(names), uElems, true
	}

	if call, ok := expr.(*hclsyntax.FunctionCallExpr); ok && call.Name == "merge" {
		u := newKeyUnion()
		for i, arg := range call.Args {
			// merge(a, b...) splats only its FINAL argument, and only when
			// ExpandFinal is set: that is the one argument whose elements
			// stand in for merge's own separate arguments.
			argIsSplat := call.ExpandFinal && i == len(call.Args)-1
			// staticForEachKeys, not staticCollElems: merge takes maps and
			// objects, so an argument whose key set is integer indices is
			// not a merge argument at all and refuses here rather than
			// contributing "0", "1" to the union.
			got, gotElems, ok := r.staticForEachKeys(arg, ident, depth+1, argIsSplat, scope)
			if !ok {
				return nil, nil, false
			}
			u.add(got, gotElems)
		}
		names, uElems := u.result()
		return stringVals(names), uElems, true
	}

	// values(X): #397's own chase, one hop further out than merge -
	// terraform-aws-modules/terraform-aws-alb's own local.additional_certs
	// (main.tf:456-473, corpus-alb-complete) is exactly
	// `merge(values({for listener_key, listener_values in var.listeners :
	// listener_key => {inner map, one entry per additional cert} if
	// ...})...)`: flatten a map of maps into one flat map, common enough
	// that OpenTofu's own function reference uses it as the worked example
	// for `values`. Without this case the outer merge()'s splatted
	// argument is a FunctionCallExpr this switch does not recognise, and
	// the WHOLE chase declines - not only for this for_each's key set (the
	// prior worker's own dead end), but for [resolver.elementExprBindings]
	// too, which is what silently left [expansion.eachValueDeferred] with
	// nothing to select each.value.certificate_arn out of and forced
	// every instance back onto [expansion.managedFrom]'s one shared,
	// expansion-wide answer - see [resolver.forEachExpansion]'s own
	// managedFrom comment and gauntlet issue #397.
	//
	// Only a MAP or an OBJECT source is accepted: a list has no keys to
	// sort by, and real `values()` itself errors on one, so
	// [stringKeys] failing here is exactly the safe decline
	// [resolver.stringKeys]'s own callers already rely on elsewhere.
	// cty's own element iterators - what `values()` itself iterates
	// with - visit a map or object in sorted-key order (documented on
	// [cty.Value.ElementIterator]), so the sort below is not a guess at
	// the builtin's behaviour; it is what the builtin already does.
	if call, ok := expr.(*hclsyntax.FunctionCallExpr); ok && call.Name == "values" && len(call.Args) == 1 && !call.ExpandFinal {
		srcKeys, srcElems, srcOK := r.staticCollElems(call.Args[0], ident, depth+1, false, scope)
		if !srcOK {
			return nil, nil, false
		}
		names, ok := stringKeys(srcKeys)
		if !ok {
			return nil, nil, false
		}
		order := make([]int, len(names))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return names[order[a]] < names[order[b]] })
		if tupleIsArgs {
			// merge(values(X)...): values(X)'s own elements stand in for
			// merge's separate arguments, the identical splat semantics
			// the TupleConsExpr and "merge" cases above already give a
			// literal list or a merge() call - each element here is
			// itself an object to union in, not a value to bind to an
			// integer index. Every element's own expression has to be in
			// hand to decompose it one hop further; one that is not (an
			// object built from a value this chase never read, such as
			// [forExprElems]'s own Group branch) declines the whole
			// level rather than silently dropping it from the union.
			u := newKeyUnion()
			for _, i := range order {
				if i >= len(srcElems) || srcElems[i].expr == nil {
					return nil, nil, false
				}
				got, gotElems, ok := r.staticForEachKeys(srcElems[i].expr, ident, depth+1, false, srcElems[i].scope)
				if !ok {
					return nil, nil, false
				}
				u.add(got, gotElems)
			}
			names, uElems := u.result()
			return stringVals(names), uElems, true
		}
		// Not a splat: values(X) is an ordinary LIST in its own right,
		// whose keys are its integer indices, one per source value in
		// the same sorted-key order cty's iterator visits them in.
		keys := make([]cty.Value, 0, len(order))
		elems := make([]elemBinding, 0, len(order))
		for pos, i := range order {
			keys = append(keys, cty.NumberIntVal(int64(pos)))
			if i < len(srcElems) {
				elems = append(elems, srcElems[i])
			} else {
				elems = append(elems, elemBinding{})
			}
		}
		return keys, elems, true
	}

	return nil, nil, false
}

// elemVarSource answers "what collection does this expression denote, when
// the only thing standing between it and a literal is a for-comprehension's
// own value variable that never proved a value".
//
// It is the collection-side counterpart of [resolver.exprVarPart], which
// already answers the same question for an identity ARGUMENT: a name in
// [instScope.exprVars] is bound to the SOURCE element's own expression, and a
// reference to it is answered by selecting into that expression rather than
// by evaluating a value. Until #397 nothing asked the question on the
// collection side, because the chase only ever walked chains of separate
// local/var names, where an enclosing loop variable cannot occur.
//
// terraform-aws-modules/terraform-aws-alb's own local.additional_certs is
// where it does occur (main.tf:456-473): the outer comprehension's per-listener
// value clause is ITSELF a comprehension, and what it ranges over is
//
//	lookup(listener_values, "additional_certificate_arns", [])
//
// - the OUTER comprehension's value variable, which never proves a value
// because one listener's list holds a module output and another's holds an
// unapplied resource attribute.
//
// Three spellings, all of them ones this file already treats as one family
// on the each.value side ([resolver.eachValueSelector],
// [resolver.lookupOrTryDefaultOverVar]):
//
//	v            v.attr            v["attr"]
//	lookup(v, "attr", default)
//	try(v.attr, default)
//
// The answer is an EXPRESSION plus where to read it, never a value:
// [resolver.selectStaticExpr] hands back the leaf a present key was written
// with, in the element's own scope and module instance - most often the
// caller across a module-call boundary, which is why the module is entered
// before the selection and the caller is told which one to enter for the
// recursion. Where the key is provably ABSENT ([resolver.objectLacksKey],
// the same absence proof the filter clause already relies on), the answer is
// the lookup()/try() DEFAULT instead, read in the current module, because
// that is where it was written.
//
// A key that is neither provably present nor provably absent declines, and
// the caller is left with exactly the refusal it already had.
func (r *resolver) elemVarSource(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (hcl.Expression, instScope, addrs.ModuleInstance, bool) {
	if len(scope.exprVars) == 0 {
		return nil, instScope{}, nil, false
	}

	// v, v.attr, v["attr"] - the whole expression is the selection.
	if trav, diags := hcl.AbsTraversalForExpr(expr); !diags.HasErrors() && len(trav) > 0 {
		b, bound := scope.exprVars[trav.RootName()]
		if !bound {
			return nil, instScope{}, nil, false
		}
		leaf, ok := r.selectFromElemExpr(b, trav[1:], ident)
		if !ok {
			return nil, instScope{}, nil, false
		}
		return leaf, b.scope, b.modInst, true
	}

	root, steps, key, defaultExpr, ok := r.elemVarLookupOrTry(expr, ident)
	if !ok {
		return nil, instScope{}, nil, false
	}
	b, bound := scope.exprVars[root]
	if !bound || b == nil || b.expr == nil {
		return nil, instScope{}, nil, false
	}
	if leaf, ok := r.selectFromElemExpr(b, steps, ident); ok {
		return leaf, b.scope, b.modInst, true
	}
	if !r.elemLacksKey(b, key, ident) {
		return nil, instScope{}, nil, false
	}
	// The default is written HERE, beside the lookup(), not inside the
	// element - so it is read in the caller's own scope and module, which is
	// the one already current.
	return defaultExpr, scope, r.modInst, true
}

// elemVarLookupOrTry reads lookup(v, "key", default) and try(v.key, default)
// into the four pieces [resolver.elemVarSource] needs: the value variable's
// name, the traversal steps to apply to its element, the attribute name to
// prove absent if those steps do not land, and the default to fall back to.
//
// It is [resolver.lookupOrTryDefaultOverVar] with the variable's name read
// OUT of the expression rather than checked against one supplied by the
// caller - the filter clause knows whose value variable it is looking at,
// and a collection source does not.
func (r *resolver) elemVarLookupOrTry(expr hcl.Expression, ident configs.StaticIdentifier) (root string, steps []hcl.Traverser, key string, defaultExpr hclsyntax.Expression, ok bool) {
	call, isCall := expr.(*hclsyntax.FunctionCallExpr)
	if !isCall || call.ExpandFinal {
		return "", nil, "", nil, false
	}
	switch call.Name {
	case "lookup":
		if len(call.Args) != 3 {
			return "", nil, "", nil, false
		}
		trav, diags := hcl.AbsTraversalForExpr(call.Args[0])
		if diags.HasErrors() || len(trav) != 1 {
			return "", nil, "", nil, false
		}
		kv, kdiags := r.evalPure(call.Args[1], instScope{}, ident)
		if kdiags.HasErrors() {
			return "", nil, "", nil, false
		}
		ks, err := convert.Convert(kv, cty.String)
		// IsMarked before AsString, which panics rather than errors on a
		// marked value - the same guard every key read in this file carries.
		if err != nil || ks.IsNull() || !ks.IsKnown() || ks.IsMarked() {
			return "", nil, "", nil, false
		}
		name := ks.AsString()
		return trav.RootName(), []hcl.Traverser{hcl.TraverseAttr{Name: name}}, name, call.Args[2], true

	case "try":
		if len(call.Args) != 2 {
			return "", nil, "", nil, false
		}
		trav, diags := hcl.AbsTraversalForExpr(call.Args[0])
		if diags.HasErrors() || len(trav) != 2 {
			return "", nil, "", nil, false
		}
		attr, isAttr := trav[1].(hcl.TraverseAttr)
		if !isAttr {
			return "", nil, "", nil, false
		}
		return trav.RootName(), trav[1:], attr.Name, call.Args[1], true
	}
	return "", nil, "", nil, false
}

// selectFromElemExpr is [resolver.selectStaticExpr] performed inside the
// module the element's expression was WRITTEN in, which is the same
// re-entry [resolver.exprVarPart] and [resolver.eachValueDeferredParts]
// already do before touching an element binding, and for the same reason:
// a module call named inside that expression has to be looked up among its
// own module's children.
func (r *resolver) selectFromElemExpr(b *elemBinding, rest []hcl.Traverser, ident configs.StaticIdentifier) (hcl.Expression, bool) {
	if b == nil || b.expr == nil {
		return nil, false
	}
	savedMod, savedCfg, savedInst, savedEval := r.mod, r.curCfg, r.modInst, r.eval
	if !r.enterModuleFor(b.modInst) {
		return nil, false
	}
	defer func() { r.mod, r.curCfg, r.modInst, r.eval = savedMod, savedCfg, savedInst, savedEval }()
	return r.selectStaticExpr(b.expr, rest, b.scope, ident, 0)
}

// elemLacksKey is [resolver.objectLacksKey] performed in the element's own
// module, for the same reason [resolver.selectFromElemExpr] enters it.
func (r *resolver) elemLacksKey(b *elemBinding, name string, ident configs.StaticIdentifier) bool {
	if b == nil || b.expr == nil {
		return false
	}
	savedMod, savedCfg, savedInst, savedEval := r.mod, r.curCfg, r.modInst, r.eval
	if !r.enterModuleFor(b.modInst) {
		return false
	}
	defer func() { r.mod, r.curCfg, r.modInst, r.eval = savedMod, savedCfg, savedInst, savedEval }()
	return r.objectLacksKey(b.expr, b.scope, name, ident, 0)
}

// elementExprBindings is #354's collector: the element EXPRESSIONS of a
// for_each source whose VALUE the tolerant retry has already answered.
//
// It runs beside [resolver.forEachExpansion]'s tolerant branch, never instead
// of it, and it changes no key and no value. What the retry produces is the
// binding - a value rebuilt from the caller's own literal skeleton with an
// unknown where a leaf could not be read - and what this adds is the syntax
// that unknown came from, so [resolver.eachValueDeferredParts] has somewhere
// to look when an identity argument reads exactly that attribute.
//
// It has its own guard-conditional unwrapping, which [resolver.staticCollElems]
// deliberately does not: `local.create && var.attachments != null ?
// var.attachments : {}` is how nearly every terraform-aws-modules block gates
// an optional sub-resource, and the chase declines at that node. Unwrapping it
// inside staticCollElems itself was measured and rejected - it makes the
// STRUCTURAL chase succeed, which pre-empts the tolerant retry, and the
// structural chase's own value for an element it cannot read is an unknown of
// the whole element rather than an object with one unknown attribute in it.
// That is a worse binding: `corpus-autoscaling-complete`'s
// aws_autoscaling_policy.this["request-count-per-target"] resolves from
// `try(coalesce(each.value.name, each.key), "")` today because each.value.name
// is a readable NULL, and it stops resolving the moment the whole element goes
// unknown. Collecting only expressions here cannot do that: no value this
// function produces is ever bound.
//
// Diagnostics the chase leaves behind are rolled back. It is a probe, and the
// caller has its own answer either way.
func (r *resolver) elementExprBindings(expr hcl.Expression, ident configs.StaticIdentifier) map[string]elemBinding {
	mark, sibMark := len(r.diags), len(r.pendingSiblingApply)
	defer func() {
		r.diags = r.diags[:mark]
		r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
	}()

	for depth := 0; depth <= maxStaticDecomposeDepth; depth++ {
		switch e := expr.(type) {
		case *hclsyntax.ParenthesesExpr:
			expr = e.Expression
			continue
		case *hclsyntax.ConditionalExpr:
			branch, ok := r.chosenBranch(e, ident)
			if !ok {
				return nil
			}
			expr = branch
			continue
		}
		break
	}

	keys, elems, ok := r.staticForEachKeys(expr, ident, 0, false, instScope{})
	if !ok {
		return nil
	}
	out := map[string]elemBinding{}
	for i, name := range keys {
		if i >= len(elems) || elems[i].expr == nil {
			continue
		}
		out[name] = elems[i]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// chosenBranch decides which arm of `cond ? A : B` the language takes, for
// the one position that needs it before any value exists: the for_each source
// [resolver.elementExprBindings] is walking for its element expressions.
//
// The condition is evaluated strictly first and only then through
// [resolver.tolerantRetry], which is the same order and the same wrapper
// [resolver.forEachExpansion] already applies one layer out. The tolerant
// evaluation matters because the guard usually READS the very argument whose
// leaf is unresolvable - `var.attachments != null` over an argument holding
// one module-output leaf - and the answer it gives is decided entirely by the
// caller's own literal skeleton: [rebuildConstructor] substitutes an unknown
// for the refused leaf and nothing else, so a condition that depends on that
// leaf comes back UNKNOWN and is refused below, while one that depends only
// on the structure the caller wrote comes back known and is correct.
//
// Known, non-null, unmarked and convertible to bool are all required. A
// condition this cannot decide leaves the caller with no expressions at all,
// which is what it had before.
func (r *resolver) chosenBranch(cond *hclsyntax.ConditionalExpr, ident configs.StaticIdentifier) (hcl.Expression, bool) {
	val, diags := r.evalPure(cond.Condition, instScope{}, ident)
	if diags.HasErrors() || val == cty.NilVal {
		retried, ok := r.tolerantRetry(cond.Condition, instScope{}, ident)
		if !ok {
			return nil, false
		}
		val = retried
	}
	b, err := convert.Convert(val, cty.Bool)
	// IsMarked before True, which panics rather than errors on a marked
	// value - the same three lines [resolver.forCondIncludes] carries, for
	// the same reason: this decides which instance addresses exist.
	if err != nil || b.IsNull() || !b.IsKnown() || b.IsMarked() {
		return nil, false
	}
	if b.True() {
		return cond.TrueResult, true
	}
	return cond.FalseResult, true
}

// keyUnion accumulates the key sets of merge()'s arguments - or of the
// elements of the list merge() splats - in argument order.
//
// Key ORDER is first-seen, which is what the union has always produced and
// what the caller sorts anyway. Key VALUE is last-seen, which is merge()'s
// own precedence rule: a key supplied by two arguments takes the later
// argument's value. Getting that backwards would bind each.value to a value
// the configuration overrode, so it is the one thing in this type that is
// load-bearing rather than incidental.
type keyUnion struct {
	order []string
	elems map[string]elemBinding
}

func newKeyUnion() *keyUnion {
	return &keyUnion{elems: map[string]elemBinding{}}
}

func (u *keyUnion) add(names []string, elems []elemBinding) {
	for i, n := range names {
		if _, seen := u.elems[n]; !seen {
			u.order = append(u.order, n)
		}
		var b elemBinding
		if i < len(elems) {
			b = elems[i]
		}
		u.elems[n] = b
	}
}

func (u *keyUnion) result() ([]string, []elemBinding) {
	elems := make([]elemBinding, 0, len(u.order))
	for _, n := range u.order {
		elems = append(elems, u.elems[n])
	}
	return u.order, elems
}

// stringVals lifts a string key set into the typed form [resolver.staticCollElems]
// hands back. cty.StringVal constructs, so nothing here can be marked.
func stringVals(names []string) []cty.Value {
	out := make([]cty.Value, 0, len(names))
	for _, n := range names {
		out = append(out, cty.StringVal(n))
	}
	return out
}

// provenValue evaluates one value expression on its own and answers with
// what it evaluated to, or cty.NilVal when it did not evaluate - which is
// the ordinary case this whole file exists for, because a value is exactly
// where a managed resource's attribute is allowed to sit.
//
// cty.NilVal is the only "unproven" signal, and every caller treats it as
// "leave the binding out" rather than "refuse": a value nothing here can
// read must not decide whether a KEY exists.
//
// Three things are refused rather than returned:
//
//   - an impure call, for the reason [resolver.evalStatic] refuses one on an
//     identity argument outright: uuid() or timestamp() would make each.value
//     a different value on the next run, and an identity derived from it
//     names a live object nothing can compute again.
//   - a marked value, because each.value reaches identity arguments and an
//     identity becomes a tofu-address marker written to a cloud tag in
//     plaintext. ContainsMarked rather than IsMarked, and BEFORE
//     IsWhollyKnown, which itself iterates and panics on a marked element -
//     a mark hoists to the containing value only for a set, which is cty's
//     asymmetry and is asserted in internal/live/marksafe's
//     TestOnlySetsHoistElementMarks.
//   - an unknown or null value, which is not something an instance's
//     each.value can be read from.
func (r *resolver) provenValue(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) cty.Value {
	if expr == nil {
		return cty.NilVal
	}
	if len(impureCallsIn(expr)) > 0 {
		return cty.NilVal
	}
	val, diags := r.evalPure(expr, scope, ident)
	if diags.HasErrors() || val == cty.NilVal {
		return cty.NilVal
	}
	if val.ContainsMarked() || val.IsNull() || !val.IsWhollyKnown() {
		return cty.NilVal
	}
	return val
}

// objectConsElems reads every key of an object constructor, evaluating only
// the key expressions to decide the key set - never an item's value. Each
// item's value expression is then offered separately through
// [resolver.provenValue], which is allowed to fail: the key set does not
// depend on it.
//
// A key written twice in one constructor keeps the first item's key, which
// is what this function has always done, and binds NO value: HCL itself
// errors on a duplicate object key, so there is no "correct" answer to copy,
// and folding two items into one binding is the shape that made two
// count.index instances share one live marker in #178.
func (r *resolver) objectConsElems(obj *hclsyntax.ObjectConsExpr, ident configs.StaticIdentifier, scope instScope) ([]string, []elemBinding, bool) {
	seen := map[string]int{}
	var keys []string
	var elems []elemBinding
	for _, item := range obj.Items {
		kv, diags := r.evalPure(item.KeyExpr, scope, ident)
		if diags.HasErrors() {
			return nil, nil, false
		}
		ks, err := convert.Convert(kv, cty.String)
		// IsMarked: cty.Value.AsString panics on a marked value, and a key
		// built from a sensitive variable is marked. lint's and stamp's own
		// staticForEachKeys copies both test IsMarked before reading the
		// value; this one did not, so `{ "${var.secret}-a" = ... }` as a
		// for_each source crashed the run rather than refusing it.
		if err != nil || ks.IsNull() || !ks.IsKnown() || ks.IsMarked() {
			return nil, nil, false
		}
		name := ks.AsString()
		if at, dup := seen[name]; dup {
			// The expression goes with the value: two items writing one key
			// have two different value expressions, and selecting an
			// attribute out of "the first one" would be this package
			// choosing which of them the language means. It answers with
			// neither.
			elems[at] = elemBinding{}
			continue
		}
		seen[name] = len(keys)
		keys = append(keys, name)
		elems = append(elems, r.binding(item.ValueExpr, scope, ident))
	}
	return keys, elems, true
}

// forExprElems is the for-comprehension half of #189's key-set extension: a
// for_each source reached through a local or module variable is often not
// an object constructor at all, but a for-comprehension BUILDING one -
// { for k, v in SRC : <key clause> => <value clause> } - and the key
// clause routinely needs nothing from the value side (team-members-datadog's
// all_user_with_merged_roles is exactly "for name, user in SRC : name =>
// {...}": the key clause is the bare key variable).
//
// The result's key set is knowable without ever evaluating a value clause
// whenever the key clause itself is: chase SRC's own key set (recursively,
// through [resolver.staticCollElems] again, so a further local/merge/for
// chain underneath composes exactly the way it already does elsewhere in
// this file), then evaluate the key clause once per source element.
// [resolver.evalPure] resolves an unbound reference as an ordinary
// evaluation failure (see its own "for-comprehension's own loop variable"
// handling), so a key clause reading something not bound here fails and the
// whole for-comprehension is refused, not answered with a guess.
//
// #239 widened it from "a map source, key variable only" to the rule that
// covers a map and a LIST alike: the key set is provable whenever the
// SOURCE's own key set is provable and every key clause evaluates from
// what is bound. Two things follow from that, and both come from
// hclsyntax.ForExpr.Value itself rather than from anything invented here:
//
//   - The key variable's cty TYPE decides what a key renders as. Over a
//     three-element list HCL binds it to 0, 1, 2, so `"item-${i}"` is
//     item-0/1/2 - three instances, which is what OpenTofu creates. The
//     pre-#239 chase read the same list as the union of its elements'
//     object keys and answered two, silently. [resolver.staticCollElems]
//     now carries the type, so this binds what HCL binds.
//   - When the source collection evaluates whole in the static scope, the
//     VALUE variable is bound too, exactly as HCL binds it, and a key
//     clause reading the value side (`h.name`) is provable rather than
//     refused. That is not a relaxation of the safety rule: it applies
//     only where the source is ordinary configuration data with no
//     resource, data source or module output anywhere inside it. Where the
//     source is not evaluable - the corpus shape this fix was written for,
//     merge() of comprehensions whose values reach a managed resource -
//     only the key variable is bound, and a key clause that needs the
//     value side fails to evaluate and refuses, as before.
//
// It refuses outright, before evaluating anything, when the comprehension
// has no key clause at all (a tuple-producing for, `for v in x : f(v)`,
// which is not what an object-typed for_each needs in the first place).
//
// An "if" clause no longer refuses unconditionally, but it does decide
// membership, so it is evaluated per element under exactly the bindings
// above and must produce a known, unmarked bool for EVERY element. One
// element it cannot decide means the key set is not proven and the whole
// comprehension refuses - a filter this cannot read (`if user.active` over
// a source whose values are not knowable) is still the case it declines.
//
// A key produced twice refuses rather than folding two elements into one
// instance. HCL itself errors on that ("Duplicate object key: two
// different items produced the key..."), so a configuration reaching it is
// already invalid, and the fold is the exact shape that made two
// count.index instances share one live marker in #178. Grouping mode
// (`k => v...`) is the one place a repeated key legitimately means one
// entry, and is deduplicated instead.
// The value clause is evaluated the same way and under the same bindings,
// but only ever as an OFFER: it decides nothing about the key set, and a
// value clause that does not evaluate - which is the ordinary case, since a
// value is exactly where a managed resource's attribute is allowed to sit -
// leaves that key's value unbound instead of refusing the comprehension.
// Grouping mode (`k => v...`) collects a tuple of every matching element's
// value rather than one value, so it offers nothing.
func (r *resolver) forExprElems(fe *hclsyntax.ForExpr, ident configs.StaticIdentifier, depth int, outer instScope) ([]string, []elemBinding, bool) {
	if fe.KeyExpr == nil {
		return nil, nil, false
	}

	srcKeys, srcElems, ok := r.forSourceElements(fe.CollExpr, ident, depth, outer)
	if !ok {
		return nil, nil, false
	}

	seen := map[string]bool{}
	var keys []string
	var elems []elemBinding
	for i, srcKey := range srcKeys {
		vars := map[string]cty.Value{}
		if fe.KeyVar != "" {
			vars[fe.KeyVar] = srcKey
		}
		// srcVals may be nil (nothing proven at all) or hold cty.NilVal for
		// this one element (a source whose other elements proved but this
		// one did not). Both mean the same thing here: leave the value
		// variable unbound, so a key clause that reads it fails to evaluate
		// and the comprehension refuses, rather than binding a stand-in.
		if fe.ValVar != "" && i < len(srcElems) && srcElems[i].val != cty.NilVal {
			vars[fe.ValVar] = srcElems[i].val
		}
		scope := outer.withVars(vars)

		if fe.CondExpr != nil {
			var srcElem elemBinding
			if i < len(srcElems) {
				srcElem = srcElems[i]
			}
			include, condOK := r.forCondIncludesTolerant(fe.CondExpr, fe.ValVar, srcElem, scope, ident)
			if !condOK {
				return nil, nil, false
			}
			if !include {
				continue
			}
		}

		kv, diags := r.evalPure(fe.KeyExpr, scope, ident)
		if diags.HasErrors() {
			return nil, nil, false
		}
		ks, err := convert.Convert(kv, cty.String)
		// IsMarked for the same reason [resolver.objectConsElems] tests it:
		// AsString panics on a marked value, and a key clause reading a
		// sensitive variable produces one.
		if err != nil || ks.IsNull() || !ks.IsKnown() || ks.IsMarked() {
			return nil, nil, false
		}
		name := ks.AsString()
		if seen[name] {
			if fe.Group {
				continue
			}
			return nil, nil, false
		}
		seen[name] = true
		keys = append(keys, name)
		switch {
		case fe.Group:
			elems = append(elems, elemBinding{})
		case isBareVar(fe.ValExpr, fe.ValVar) && i < len(srcElems) && srcElems[i].expr != nil:
			// `{ for k, v in SRC : k => v }` - the value clause IS the loop
			// variable, so this comprehension's element and the SOURCE's
			// element are the same expression, and it is the source's own
			// binding that can be selected into. Binding fe.ValExpr instead
			// would hand a later selection the identifier `v`, which means
			// nothing outside the ForExpr node that scopes it.
			//
			// The proven value still comes from evaluating the value clause
			// under this scope, so the two halves never disagree: where the
			// source's value proved, `v` is bound and evaluates to it.
			b := srcElems[i]
			b.val = r.provenValue(fe.ValExpr, scope, ident)
			elems = append(elems, b)
		default:
			// The value clause is neither Group nor a bare loop variable -
			// merge(v, {lambda_function_name = split(":", v.target_id)[6]})
			// is the corpus shape (terraform-aws-modules/alb's own
			// local.lambda_target_groups). If the SOURCE element's own
			// expression is known (srcElems[i].expr != nil), bind the loop
			// variable's name to it in scope.exprVars before building this
			// element's own binding, so a later reference to the loop
			// variable - however many function calls and index operations
			// sit between it and the identity argument, not only a bare
			// each.value.<attr> selection - has something to select into
			// ([resolver.exprVarPart]) instead of meaning nothing outside
			// this ForExpr node. See [instScope.exprVars]'s own doc comment.
			bindScope := scope
			exprBound := fe.ValVar != "" && i < len(srcElems) && srcElems[i].expr != nil
			if exprBound {
				bindScope = scope.withExprVar(fe.ValVar, &srcElems[i])
			}
			b := r.binding(fe.ValExpr, bindScope, ident)
			if loopVarUnbound(fe.ValExpr, scope, fe.KeyVar, fe.ValVar) && !exprBound {
				// The value clause reads a loop variable this scope does not
				// bind, and nothing above gave it a structural substitute
				// either, so the expression means nothing outside the
				// ForExpr node that scopes it. Carrying it anyway let a
				// later selection reach a bare `v` and hand it to
				// [addrs.ParseRef], which answered "Invalid reference" - a
				// diagnostic about a name the author never wrote, replacing
				// one that named the real obstacle. Measured: one site in
				// .corpus/iam/examples/iam-role-for-service-accounts.
				//
				// The value half is unaffected either way:
				// [resolver.provenValue] has already failed on the same
				// unbound name and left cty.NilVal, which is the answer
				// this shape had before the expression was carried at all.
				b.expr = nil
			}
			elems = append(elems, b)
		}
	}
	return keys, elems, true
}

// loopVarUnbound reports whether expr reads one of a for-comprehension's own
// loop variables that scope does not bind. The key variable is always bound
// and the value variable only where the source element's value proved, so
// this is exactly the "the source is shape-only" case.
func loopVarUnbound(expr hclsyntax.Expression, scope instScope, names ...string) bool {
	if expr == nil {
		return false
	}
	for _, trav := range expr.Variables() {
		root := trav.RootName()
		for _, n := range names {
			if n == "" || root != n {
				continue
			}
			if _, bound := scope.vars[n]; !bound {
				return true
			}
		}
	}
	return false
}

// isBareVar reports whether expr is exactly the identifier name and nothing
// more - a for-comprehension's own value variable standing alone as the
// value clause, which is the `k => v` idiom.
func isBareVar(expr hclsyntax.Expression, name string) bool {
	if name == "" || expr == nil {
		return false
	}
	trav, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(trav.Traversal) != 1 {
		return false
	}
	root, ok := trav.Traversal[0].(hcl.TraverseRoot)
	return ok && root.Name == name
}

// forSourceElements reports what a for-comprehension's SOURCE collection
// binds its loop variables to, one entry per element in iteration order.
//
// A vals entry is set only where this resolver evaluated that element's own
// value for itself, which is the only circumstance under which the value
// variable may be bound: the value is then ordinary configuration data it
// read, not a guess standing in for something it refused to read. That
// holds two ways - the whole source evaluated (every entry set), or the
// source's shape was chased and some element expressions evaluated on their
// own (those entries set, the rest cty.NilVal). Where nothing about an
// element's contents is knowable - it mentions a managed resource, a data
// source or a module output - the key is still exact and the value stays
// cty.NilVal.
//
// Evaluation is tried first because it yields strictly more: the same keys
// plus the values beside them.
func (r *resolver) forSourceElements(coll hclsyntax.Expression, ident configs.StaticIdentifier, depth int, scope instScope) (keys []cty.Value, elems []elemBinding, ok bool) {
	if keys, elems, ok := r.evaluatedCollElements(coll, ident, scope); ok {
		return keys, elems, true
	}
	// tupleIsArgs is false: a for-comprehension's source is ranged over, so
	// a list here is a list, and its keys are its integer indices - never
	// the union of its elements' own object keys, which is only ever what
	// merge()'s splatted argument means.
	//
	// The values this returns are per-element and may be cty.NilVal: a
	// source whose shape is knowable while one element's contents are not
	// still binds the value variable for the elements it DID evaluate. Each
	// binding is one expression this resolver evaluated itself, never an
	// inference from a neighbouring element.
	keys, elems, ok = r.staticCollElems(coll, ident, depth+1, false, scope)
	if !ok {
		return nil, nil, false
	}
	return keys, elems, true
}

// evaluatedCollElements reads a collection that evaluates whole under the
// static scope, keys and values alike, in cty's own iteration order.
//
// The reading is cty's, not a re-derivation of it: a map or object is keyed
// by its own keys, a list or tuple by its integer indices, a set by its
// elements - which is what an element iterator hands back, and what
// hclsyntax.ForExpr.Value then binds. Anything that is not a collection at
// all cannot be ranged over and refuses.
func (r *resolver) evaluatedCollElements(expr hclsyntax.Expression, ident configs.StaticIdentifier, scope instScope) (keys []cty.Value, elems []elemBinding, ok bool) {
	// An impure call would make the collection a different collection on
	// the next run. Its LENGTH might well be stable, but nothing here can
	// show that, and [resolver.evalStatic] refuses the same shape one layer
	// up for the same reason.
	if len(impureCallsIn(expr)) > 0 {
		return nil, nil, false
	}
	val, diags := r.evalPure(expr, scope, ident)
	if diags.HasErrors() || val == cty.NilVal {
		return nil, nil, false
	}
	// ContainsMarked, and BEFORE any other read: a mark on an element
	// hoists to the containing value only for a set, so a marked string
	// inside a list, map, object or tuple leaves the outer value unmarked
	// and IsWhollyKnown's own iteration would then panic on it. The
	// asymmetry is cty's and is asserted in internal/live/marksafe's
	// TestOnlySetsHoistElementMarks. Refusing rather than unmarking,
	// because these keys become part of an address and an address becomes a
	// cloud tag written in plaintext.
	if val.ContainsMarked() || val.IsNull() || !val.IsWhollyKnown() {
		return nil, nil, false
	}
	ty := val.Type()
	switch {
	case ty.IsMapType(), ty.IsObjectType(), ty.IsListType(), ty.IsTupleType(), ty.IsSetType():
	default:
		return nil, nil, false
	}
	for it := val.ElementIterator(); it.Next(); {
		k, v := it.Element()
		keys = append(keys, k)
		// No expression: the whole collection evaluated, so every element
		// already has a value and nothing is left for a structural selection
		// to recover. Offering the syntax back here would also be wrong -
		// the value read out of the evaluated collection is the one the
		// language settled on, conversions included.
		elems = append(elems, elemBinding{val: v})
	}
	return keys, elems, true
}

// forCondIncludes evaluates a comprehension's "if" clause for one element.
// The second result is whether the clause could be decided at all; a false
// there means the key set is not provable, never that the element is out.
func (r *resolver) forCondIncludes(cond hclsyntax.Expression, scope instScope, ident configs.StaticIdentifier) (include, ok bool) {
	cv, diags := r.evalPure(cond, scope, ident)
	if diags.HasErrors() {
		return false, false
	}
	b, err := convert.Convert(cv, cty.Bool)
	// IsMarked before True, which panics rather than errors on a marked
	// value - the same three lines the key read below carries, for the same
	// reason. A filter decided by a sensitive value decides which addresses
	// exist, so it refuses rather than being unmarked.
	if err != nil || b.IsNull() || !b.IsKnown() || b.IsMarked() {
		return false, false
	}
	return b.True(), true
}

// forCondIncludesTolerant decides a for-comprehension's "if" clause for one
// element when [resolver.forCondIncludes] cannot, and is the only entry
// [resolver.forExprElems] uses.
//
// Two routes, in order, and the order is what keeps every answer this
// package already gives byte-identical:
//
//   - [resolver.forCondIncludesShaped], the recognized value-free SHAPES -
//     lookup(v, "k", d) / try(v.k, d) as the whole condition, composed
//     through !, && and || by three-valued logic. Unchanged.
//   - [resolver.forCondFromRebuiltElem], which decides any OTHER condition
//     whose answer follows from the STRUCTURE of the element the caller
//     wrote, whatever functions and operators sit between the two.
//
// #397 is what forced the second: terraform-aws-modules/terraform-aws-alb's
// own local.additional_certs filters with
//
//	if length(lookup(listener_values, "additional_certificate_arns", [])) > 0
//
// which is a recognized value-free predicate wearing a length() and a
// comparison. Enumerating that spelling - and then length() == 0, and >= 2,
// and the coalesce()/compact() variants the same corpus writes elsewhere -
// is a list that never closes and derives nothing. The rule underneath it
// does derive: the LENGTH of a collection whose SHAPE the caller wrote out
// is knowable even when none of its contents are, and so is everything else
// that reads only structure.
func (r *resolver) forCondIncludesTolerant(cond hclsyntax.Expression, valVar string, elem elemBinding, scope instScope, ident configs.StaticIdentifier) (include, ok bool) {
	if include, ok := r.forCondIncludesShaped(cond, valVar, elem, scope, ident); ok {
		return include, true
	}
	return r.forCondFromRebuiltElem(cond, valVar, elem, scope, ident)
}

// forCondFromRebuiltElem decides a filter clause by binding the
// comprehension's value variable to the element's own literal SKELETON -
// exactly what the caller wrote, with an unknown standing in for each leaf
// the static scope refused - and then evaluating the condition normally.
//
// This is [resolver.chosenBranch]'s argument applied one clause over, and the
// safety case is the same one word for word: [rebuildConstructor] substitutes
// an unknown for a refused leaf and NOTHING else, so a condition that depends
// on such a leaf evaluates to an unknown and is refused by
// [resolver.forCondIncludes]'s own IsKnown guard, while a condition that
// depends only on the structure the author wrote evaluates to that structure
// and is correct. Nothing here can answer TRUE on a filter whose real answer
// is FALSE, which is the only direction that could invent an instance key.
//
// Worked through on #397's own clause,
// `length(lookup(v, "additional_certificate_arns", [])) > 0`:
//
//   - a listener whose element WRITES that key rebuilds to an object whose
//     attribute is a tuple of N unknowns. A tuple's length is part of its
//     TYPE, so length() is known - N - and N > 0 decides. What the
//     certificate ARNs are never enters it.
//   - a listener that omits the key rebuilds to an object without it, so
//     lookup() takes its [] default, length is 0, and the element is
//     excluded - which is what keeps a key set from being invented out of a
//     default.
//   - a listener whose LIST ITSELF is a refused leaf (an unknown, not a
//     tuple of unknowns) has an unknown length, and the whole clause
//     refuses.
//
// The guards are the ones every value-to-address path in this package
// carries. An impure call in either the condition or the element is refused
// before anything is evaluated ([resolver.evalStatic]'s own reason: a filter
// that decides differently on the next run decides which live objects this
// estate claims). A rebuilt value carrying a mark anywhere is refused rather
// than unmarked, because this decides instance keys and an instance key
// becomes a cloud tag written in plaintext.
func (r *resolver) forCondFromRebuiltElem(cond hclsyntax.Expression, valVar string, elem elemBinding, scope instScope, ident configs.StaticIdentifier) (include, ok bool) {
	if valVar == "" || elem.expr == nil {
		return false, false
	}
	if len(impureCallsIn(cond)) > 0 {
		return false, false
	}
	rebuilt, rebuiltOK := r.rebuiltElemValue(elem, ident)
	if !rebuiltOK {
		return false, false
	}
	return r.forCondIncludes(cond, scope.withVars(map[string]cty.Value{valVar: rebuilt}), ident)
}

// rebuiltElemValue is [rebuildConstructor] reached from an element binding:
// enter the module the element's expression was WRITTEN in - most often the
// caller across a module-call boundary, the same re-entry
// [resolver.eachValueDeferredParts] and [resolver.forCondIncludesShaped]
// already perform for the identical reason - rebuild the constructor there,
// and come back.
//
// It is a probe. Every diagnostic the rebuild's own strict attempts leave
// behind is rolled back, exactly as [resolver.elementExprBindings] rolls back
// its chase, because the caller has its own answer either way.
//
// False is returned for anything that is not an object or tuple constructor
// at all, which is [rebuildConstructor]'s own contract: "I could not rebuild
// this" is a different claim from "this rebuilds to something not yet known",
// and only the second licenses a caller to carry on.
func (r *resolver) rebuiltElemValue(elem elemBinding, ident configs.StaticIdentifier) (cty.Value, bool) {
	if elem.expr == nil || r.eval == nil {
		return cty.NilVal, false
	}
	if len(impureCallsIn(elem.expr)) > 0 {
		return cty.NilVal, false
	}
	mark, sibMark := len(r.diags), len(r.pendingSiblingApply)
	defer func() {
		r.diags = r.diags[:mark]
		r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
	}()

	savedMod, savedCfg, savedInst, savedEval := r.mod, r.curCfg, r.modInst, r.eval
	if !r.enterModuleFor(elem.modInst) {
		return cty.NilVal, false
	}
	defer func() { r.mod, r.curCfg, r.modInst, r.eval = savedMod, savedCfg, savedInst, savedEval }()

	rb := argRebuild{
		moduleOutput: r.moduleOutputValues(r.curCfg, r.modInst),
		localExpr:    localExprs(r.curCfg),
	}
	val, ok := rebuildConstructor(r.ctx, r.eval, elem.expr, ident, rb)
	if !ok || val == cty.NilVal || val.IsNull() {
		return cty.NilVal, false
	}
	// ContainsMarked, not IsMarked: a mark on an element hoists to the
	// containing value only for a SET, so a marked string inside the object
	// this just rebuilt leaves the object itself unmarked and a later read
	// would panic on it. The asymmetry is cty's and is asserted in
	// internal/live/marksafe's TestOnlySetsHoistElementMarks.
	if val.ContainsMarked() {
		return cty.NilVal, false
	}
	return val, true
}

// forCondIncludesShaped is [resolver.forCondIncludes] widened for the
// idiom nearly every terraform-aws-modules block uses to gate an optional
// sub-resource: `<provable> && lookup(v, "flag", default)`, or the try()
// spelling `try(v.flag, default)`, where v is the comprehension's OWN value
// variable and the SOURCE element v is bound to did not prove as a whole
// value - it carries a resource or module-output reference elsewhere, so
// evaluating v at all fails and [resolver.forCondIncludes] can never even
// ask whether "flag" is set. corpus-alb-complete's own
// `aws_lb_target_group_attachment.this` is exactly this shape: `for_each = {
// for k, v in var.target_groups : k => v if local.create &&
// lookup(v, "create_attachment", true) }`, where var.target_groups's
// ex-instance element carries `target_id = aws_instance.this.id`.
//
// The fix does not need v's value. lookup()'s and try()'s own fallback only
// matters when the key is ABSENT, and [resolver.objectLacksKey] already
// proves absence from an object constructor's own KEYS - which are static
// even when its VALUES are not - for exactly this purpose on the
// each.value.<attr> side (eachvalue.go). Reused here for a for-expression's
// filter clause instead of a resource argument's selection: if the source
// element's own literal provably lacks "flag", lookup()/try() take their
// default, and the default decides the filter without touching v at all.
//
// Boolean connectives compose by ordinary three-valued (Kleene) logic,
// which is what lets `local.create && lookup(v, "create_attachment", true)`
// decide as soon as local.create alone is known, and remain correct - never
// merely permissive - when it is local.create instead that cannot be
// proved: AND is false whenever either side decides false, whatever the
// other side is, and OR is true whenever either side decides true. Neither
// half is anything to do with what for_each ranges over, so the same
// widening reaches every for-expression filter shaped this way, over any
// resource type.
//
// elem is the comprehension's value-variable binding for THIS instance
// (srcElems[i] in [resolver.forExprElems]): its own .expr/.scope/.modInst
// are what [resolver.objectLacksKey] and the default's own evaluation need,
// re-entering elem.modInst exactly as [resolver.eachValueDeferredParts]
// does for the identical reason - the element's literal was WRITTEN in the
// module that supplied it, most often the caller across a module-call
// boundary, not the module the for_each itself lives in.
func (r *resolver) forCondIncludesShaped(cond hclsyntax.Expression, valVar string, elem elemBinding, scope instScope, ident configs.StaticIdentifier) (include, ok bool) {
	if include, ok := r.forCondIncludes(cond, scope, ident); ok {
		return include, true
	}
	switch e := cond.(type) {
	case *hclsyntax.ParenthesesExpr:
		return r.forCondIncludesShaped(e.Expression, valVar, elem, scope, ident)

	case *hclsyntax.UnaryOpExpr:
		if e.Op != hclsyntax.OpLogicalNot {
			return false, false
		}
		inc, ok := r.forCondIncludesShaped(e.Val, valVar, elem, scope, ident)
		if !ok {
			return false, false
		}
		return !inc, true

	case *hclsyntax.BinaryOpExpr:
		switch e.Op {
		case hclsyntax.OpLogicalAnd:
			lInc, lOK := r.forCondIncludesShaped(e.LHS, valVar, elem, scope, ident)
			if lOK && !lInc {
				return false, true
			}
			rInc, rOK := r.forCondIncludesShaped(e.RHS, valVar, elem, scope, ident)
			if rOK && !rInc {
				return false, true
			}
			if lOK && rOK {
				return lInc && rInc, true
			}
			return false, false

		case hclsyntax.OpLogicalOr:
			lInc, lOK := r.forCondIncludesShaped(e.LHS, valVar, elem, scope, ident)
			if lOK && lInc {
				return true, true
			}
			rInc, rOK := r.forCondIncludesShaped(e.RHS, valVar, elem, scope, ident)
			if rOK && rInc {
				return true, true
			}
			if lOK && rOK {
				return lInc || rInc, true
			}
			return false, false
		}
		return false, false
	}

	if valVar == "" || elem.expr == nil {
		return false, false
	}
	name, defaultExpr, ok := r.lookupOrTryDefaultOverVar(cond, valVar, ident)
	if !ok {
		return false, false
	}

	savedMod, savedCfg, savedInst, savedEval := r.mod, r.curCfg, r.modInst, r.eval
	if !r.enterModuleFor(elem.modInst) {
		return false, false
	}
	defer func() { r.mod, r.curCfg, r.modInst, r.eval = savedMod, savedCfg, savedInst, savedEval }()

	// The key may be PRESENT rather than absent - try()/lookup()'s default
	// only ever matters when it is not, and [resolver.objectLacksKey] alone
	// can only prove the absent half. [resolver.selectStaticExpr] is the
	// same structural walk (an object constructor, or merge() of them) that
	// already answers absence; asked for the key itself, it hands back the
	// LEAF EXPRESSION a present key was written with, which is ordinary
	// configuration data - most often a plain literal, like
	// terraform-aws-modules/alb's own `attach_lambda_permission = true` -
	// wherever it evaluates on its own. This is the same asymmetry #260
	// exists for on the each.value side: a key nested inside a value this
	// package cannot prove whole (v itself, here, because a SIBLING key
	// such as target_id names a module output) is not the same question as
	// whether THIS key's own value reads.
	if leaf, leafOK := r.selectStaticExpr(elem.expr, []hcl.Traverser{hcl.TraverseAttr{Name: name}}, elem.scope, ident, 0); leafOK {
		lv, diags := r.evalPure(leaf, elem.scope, ident)
		if diags.HasErrors() {
			return false, false
		}
		lb, err := convert.Convert(lv, cty.Bool)
		if err != nil || lb.IsNull() || !lb.IsKnown() || lb.IsMarked() {
			return false, false
		}
		return lb.True(), true
	}

	if !r.objectLacksKey(elem.expr, elem.scope, name, ident, 0) {
		return false, false
	}
	dv, diags := r.evalPure(defaultExpr, elem.scope, ident)
	if diags.HasErrors() {
		return false, false
	}
	b, err := convert.Convert(dv, cty.Bool)
	if err != nil || b.IsNull() || !b.IsKnown() || b.IsMarked() {
		return false, false
	}
	return b.True(), true
}

// lookupOrTryDefaultOverVar recognizes the two spellings of "does the
// for-comprehension's value variable have this key, and if not use this
// default" that [resolver.eachValueSelector] already reads for the
// each.value side: lookup(<valVar>, "key", default) and
// try(<valVar>.key, default). Anything else answers false, so a caller
// falls back to whatever it had before.
func (r *resolver) lookupOrTryDefaultOverVar(expr hclsyntax.Expression, valVar string, ident configs.StaticIdentifier) (key string, defaultExpr hclsyntax.Expression, ok bool) {
	call, isCall := expr.(*hclsyntax.FunctionCallExpr)
	if !isCall {
		return "", nil, false
	}
	switch call.Name {
	case "lookup":
		if len(call.Args) != 3 || call.ExpandFinal {
			return "", nil, false
		}
		trav, diags := hcl.AbsTraversalForExpr(call.Args[0])
		if diags.HasErrors() || len(trav) != 1 || trav.RootName() != valVar {
			return "", nil, false
		}
		kv, kdiags := r.evalPure(call.Args[1], instScope{}, ident)
		if kdiags.HasErrors() {
			return "", nil, false
		}
		// IsMarked before AsString, which panics rather than errors on a
		// marked value - the same guard [resolver.eachValueSelector] carries
		// for its own key argument.
		ks, err := convert.Convert(kv, cty.String)
		if err != nil || ks.IsNull() || !ks.IsKnown() || ks.IsMarked() {
			return "", nil, false
		}
		return ks.AsString(), call.Args[2], true

	case "try":
		if len(call.Args) != 2 || call.ExpandFinal {
			return "", nil, false
		}
		trav, diags := hcl.AbsTraversalForExpr(call.Args[0])
		if diags.HasErrors() || len(trav) != 2 || trav.RootName() != valVar {
			return "", nil, false
		}
		attr, isAttr := trav[1].(hcl.TraverseAttr)
		if !isAttr {
			return "", nil, false
		}
		return attr.Name, call.Args[1], true
	}
	return "", nil, false
}

// ---- the local-values fix ----------------------------------------------

// namedLeaf is [resolver.resolveExpr]'s entry into #178's local-values fix.
// It is called only after evalStatic has already failed to evaluate expr as
// a whole, and it asks whether expr is a template, parentheses, or a plain
// traversal rooted at "local" or "var" - the same shapes resolveExpr's own
// symbolic path already recurses through for a direct resource reference -
// and if so resolves it leaf by leaf via [resolver.resolveNamed].
//
// applicable is false for any other shape, which tells the caller to keep
// the diagnostic evalStatic already recorded rather than replace it with
// nothing. When applicable is true, ok reports whether resolution actually
// succeeded, and a diagnostic explaining a false ok - "Not an identity
// attribute", "Unresolvable identity", or evalStatic's own message for a
// leaf that turned out to be an ordinary static value gone wrong - has
// already been recorded in its place.
func (r *resolver) namedLeaf(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (parts []Part, ok bool, applicable bool) {
	switch e := expr.(type) {
	case *hclsyntax.TemplateExpr:
		var out []Part
		for _, sub := range e.Parts {
			got, subOK := r.resolveExpr(sub, scope, ident)
			if !subOK {
				return nil, false, true
			}
			out = append(out, got...)
		}
		return out, true, true
	case *hclsyntax.TemplateWrapExpr:
		return r.namedLeaf(e.Wrapped, scope, ident)
	case *hclsyntax.ParenthesesExpr:
		return r.namedLeaf(e.Expression, scope, ident)
	}

	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() || len(trav) < 2 {
		return nil, false, false
	}
	root := trav.RootName()
	if root != "local" && root != "var" && root != "module" {
		return nil, false, false
	}
	nameStep, ok := trav[1].(hcl.TraverseAttr)
	if !ok {
		return nil, false, false
	}
	if root == "module" {
		return r.resolveModuleOutput(nameStep.Name, trav[2:], ident)
	}
	return r.resolveNamed(root, nameStep.Name, trav[2:], scope, ident)
}

// resolveModuleOutput resolves a reference into a child module's output:
// "module.<call>.<output>", or "module.<call>[<key>].<output>", with any
// further steps selected out of whatever the output is defined as.
//
// A module output is not a value that has to wait for the module to be
// evaluated. It is an expression written in the child module, and the child
// module's scope is one this resolver can already enter and evaluate in:
// [resolver.namedDef]'s "var" case walks the very same call edge in the
// opposite direction, from a child module's variable back up to the
// argument the call sets it from. Reading the output's own expression here
// is therefore not a new claim about anything - a literal is the literal
// the child wrote, and a resource reference inside the output resolves
// through parentPart under exactly the identity-attribute restriction a
// direct reference to that same resource would face. The only thing that
// changes is that a module boundary no longer stops the walk.
//
// This is the boundary [configs.staticScopeData.StaticValidateReferences] refuses with
// "Module output not supported in static context" (internal/configs's
// static_scope.go), and that refusal is correct for what it guards: a
// static context there is one that must be resolved before the module tree
// is even built - a module source, a backend, an encryption block - where
// there is no child module to enter yet. An identity argument is not that
// context. The whole configuration is loaded by the time this runs, so the
// question "what is this output defined as" has an answer, and refusing it
// unresolved refuses configurations stock OpenTofu evaluates without
// complaint.
//
// The three results are [resolver.namedLeaf]'s: the parts, whether they
// resolved, and whether this shape is one this function handles at all. A
// shape it does not handle returns applicable=false, which leaves the
// refusal internal/configs already raised standing - never a quieter
// message in place of a real one.
func (r *resolver) resolveModuleOutput(callName string, rest []hcl.Traverser, ident configs.StaticIdentifier) ([]Part, bool, bool) {
	mc, ok := r.mod.ModuleCalls[callName]
	if !ok || mc.Config == nil {
		return nil, false, false
	}

	// An indexed reference supplies the instance key; an unindexed one is
	// only meaningful when the call has no repetition at all. Both
	// mismatched pairings are left to the existing refusal rather than
	// guessed at: "module.foo.bar" on a call with count or for_each names
	// a tuple or an object holding every instance's value, which is not a
	// single identity part, and "module.foo[0].bar" on a call with neither
	// names an instance that does not exist.
	//
	// The whole-module half of that is belt and braces: the expansion
	// check below rejects it too, because addrs.NoKey is never among a
	// repeated call's keys. It is written out anyway because it refuses
	// for the reason a reader would give, one step earlier, and because it
	// does not depend on NoKey's relationship to the key set staying what
	// it is. Only the indexed-on-an-unrepeated-call half and the
	// expansion check itself are independently load-bearing; see
	// TestModuleOutputRefusesWhatItCannotName, whose fixtures isolate each
	// with a literal-valued output so that nothing downstream of the
	// module hop can mask a missing guard.
	repeated := mc.Count != nil || mc.ForEach != nil
	key := addrs.InstanceKey(addrs.NoKey)
	if len(rest) > 0 {
		if idx, isIndex := rest[0].(hcl.TraverseIndex); isIndex {
			if !repeated {
				return nil, false, false
			}
			k, ok := indexKeyValue(idx.Key)
			if !ok {
				return nil, false, false
			}
			key = k
			rest = rest[1:]
		} else if repeated {
			return nil, false, false
		}
	} else if repeated {
		return nil, false, false
	}

	// The key has to be one the call actually expands to. Without this a
	// reference to module.foo[7] on a three-instance call would enter the
	// child module anyway - [addrs.ModuleInstance.Child] builds any step
	// asked of it, and ConfigForModule resolves by name - and resolve an
	// identity for an instance that will never exist.
	if repeated {
		subject := "module." + callName
		if _, ok := ChildModuleRepetitionData(r.ctx, r.curCfg, subject, mc.Count, mc.ForEach, key); !ok {
			return nil, false, false
		}
	}

	if len(rest) == 0 {
		// "module.foo" with no output named: a whole-module reference,
		// which is an object of every output rather than one value.
		return nil, false, false
	}
	outStep, isAttr := rest[0].(hcl.TraverseAttr)
	if !isAttr {
		return nil, false, false
	}
	rest = rest[1:]

	savedMod, savedCfg, savedInst, savedEval := r.mod, r.curCfg, r.modInst, r.eval
	if !r.enterModuleFor(r.modInst.Child(callName, key)) {
		return nil, false, false
	}
	defer func() { r.mod, r.curCfg, r.modInst, r.eval = savedMod, savedCfg, savedInst, savedEval }()

	out, ok := r.mod.Outputs[outStep.Name]
	if !ok || out.Expr == nil {
		return nil, false, false
	}

	// A fresh scope, not the caller's: the output's expression is written
	// in the child module, where the referring resource's own count.index,
	// each.key and for-comprehension variables do not exist. Carrying the
	// caller's scope across the boundary would let a name bound out here
	// silently satisfy a reference in there.
	return r.selectStatic(out.Expr, rest, instScope{}, ident, 0)
}

// resolveNamed resolves "local.name<rest>" or "var.name<rest>": it looks up
// what the local or the variable is defined as via [resolver.namedDef], then
// selects into that definition the way rest says to, via
// [resolver.selectStatic] - using the scope [resolver.namedDef] itself
// says the definition must be evaluated in, which for "var" is the module
// CALL's own repetition, not scope (the caller's).
func (r *resolver) resolveNamed(root, name string, rest []hcl.Traverser, scope instScope, ident configs.StaticIdentifier) ([]Part, bool, bool) {
	// The declaration is deliberately dropped here and read only by
	// [resolver.staticCollElems]'s hop. This path renders a value into an
	// identity PART, not into a bound each.value, and a part may be a
	// symbolic Formula over a resource attribute that no cty conversion can
	// be applied to at all - so #251's conversion does not compose with it
	// without first deciding what the declared type means for the symbolic
	// half. That is tracked separately; see [varConvertedElems]'s closing
	// note for the shape it leaves open.
	defExpr, defScope, _, restore, ok := r.namedDef(root, name, scope)
	if !ok {
		return nil, false, false
	}
	defer restore()
	return r.selectStatic(defExpr, rest, defScope, ident, 0)
}

// selectStatic applies the traversal steps in rest against expr's own
// static shape - an object or tuple constructor, or merge() of them -
// without evaluating expr as a whole. expr is what [resolver.namedDef]
// found a local or a module call argument to be defined as; rest is
// whatever steps came after the name in the original traversal
// (local.foo.bar[2] has rest [.bar, [2]] once .foo itself is consumed).
//
// Once rest is empty, whatever expression selection has reached is the leaf
// usage, resolved the ordinary way through [resolver.resolveExpr]: a plain
// literal is evaluated on its own, a plain resource reference through
// [resolver.resolveTraversal] (so it is bound by the same identity-attribute
// restriction parentPart already enforces for a direct reference), and a
// further local or var reference chased again through this same machinery.
func (r *resolver) selectStatic(expr hcl.Expression, rest []hcl.Traverser, scope instScope, ident configs.StaticIdentifier, depth int) ([]Part, bool, bool) {
	if depth > maxStaticDecomposeDepth {
		return nil, false, false
	}
	if paren, ok := expr.(*hclsyntax.ParenthesesExpr); ok {
		return r.selectStatic(paren.Expression, rest, scope, ident, depth+1)
	}

	// A local aliasing another local, or a module call argument that is
	// itself a plain local/var reference: chase it before trying to match a
	// container shape, so a chain of plain references composes the way a
	// chain of direct resource references already does.
	if trav, diags := hcl.AbsTraversalForExpr(expr); !diags.HasErrors() && len(trav) >= 2 {
		if root := trav.RootName(); root == "local" || root == "var" || root == "module" {
			if nameStep, ok := trav[1].(hcl.TraverseAttr); ok {
				combined := make([]hcl.Traverser, 0, len(trav)-2+len(rest))
				combined = append(combined, trav[2:]...)
				combined = append(combined, rest...)
				// A module output reached through a local, a module
				// argument, or another module's output: the steps this
				// reference already carries are prepended to the ones
				// still owed, exactly as for a local aliasing a local, so
				// "local.name = module.vpc.id" selected with ".foo"
				// arrives as module.vpc.id.foo rather than losing either
				// half. See [resolver.resolveModuleOutput].
				if root == "module" {
					return r.resolveModuleOutput(nameStep.Name, combined, ident)
				}
				return r.resolveNamed(root, nameStep.Name, combined, scope, ident)
			}
		} else if len(rest) > 0 && r.isSymbolic(expr, scope) {
			// The chase landed on a MANAGED RESOURCE reference with steps
			// still owed - `aws_lb_target_group.this` selected with
			// `["ex_asg"].arn`. Until this branch existed the switch below
			// had no case for a traversal and answered applicable=false, so
			// the caller kept a refusal about the whole chain rather than the
			// verdict this reference deserves on its own.
			//
			// The shape is what a module output that publishes a whole
			// resource produces: `output "target_groups" { value =
			// aws_lb_target_group.this }` in terraform-aws-modules/alb, read
			// by a caller as module.alb.target_groups["ex_asg"].arn.
			// [resolver.resolveModuleOutput] enters the child module and
			// hands the output's expression here with the caller's remaining
			// steps still in rest; every one of those steps is an index or an
			// attribute of the resource itself, which is exactly what a
			// DIRECT reference spells and what [resolver.resolveTraversal]
			// already reads.
			//
			// So the steps are re-joined onto the reference and resolved as
			// the one traversal the configuration would have written if the
			// module boundary were not in the way. No new claim is made: the
			// combined traversal goes through addrs.ParseRef, the
			// identity-attribute restriction and [resolver.parentPart]
			// unchanged, and a step those refuse still refuses. Gated on
			// [resolver.isSymbolic] so that only a managed reference takes
			// it - path.*, terraform.*, data.* and a for-comprehension's own
			// loop variable are evaluable or have their own diagnostics, and
			// each.* is handled above this function by eachvalue.go.
			combined := make(hcl.Traversal, 0, len(trav)+len(rest))
			combined = append(combined, trav...)
			combined = append(combined, rest...)
			parts, ok := r.resolveTraversal(combined, scope, ident)
			return parts, ok, true
		}
	}

	if len(rest) == 0 {
		parts, ok := r.resolveExpr(expr, scope, ident)
		return parts, ok, true
	}

	step := rest[0]
	switch e := expr.(type) {
	case *hclsyntax.ObjectConsExpr:
		key, ok := stepKeyString(step)
		if !ok {
			return nil, false, false
		}
		for _, item := range e.Items {
			kv, diags := r.evalPure(item.KeyExpr, scope, ident)
			if diags.HasErrors() {
				continue
			}
			ks, err := convert.Convert(kv, cty.String)
			// IsMarked before AsString, which panics on a marked value: an
			// object key built from a sensitive variable is marked, and a
			// key this cannot read is a key it cannot match.
			if err != nil || ks.IsNull() || !ks.IsKnown() || ks.IsMarked() || ks.AsString() != key {
				continue
			}
			return r.selectStatic(item.ValueExpr, rest[1:], scope, ident, depth+1)
		}
		return nil, false, false

	case *hclsyntax.TupleConsExpr:
		idx, ok := stepIndexInt(step)
		if !ok || idx < 0 || idx >= len(e.Exprs) {
			return nil, false, false
		}
		return r.selectStatic(e.Exprs[idx], rest[1:], scope, ident, depth+1)

	case *hclsyntax.FunctionCallExpr:
		if e.Name != "merge" {
			return nil, false, false
		}
		// Last argument wins on a duplicate key, matching merge()'s own
		// precedence; stopping at the first (from the end) argument that
		// has the key at all means an earlier, losing argument's branch is
		// never even visited, so it can never leave a stray diagnostic
		// behind for a key it does not end up supplying.
		for i := len(e.Args) - 1; i >= 0; i-- {
			if parts, ok, has := r.selectStatic(e.Args[i], rest, scope, ident, depth+1); has {
				return parts, ok, true
			}
		}
		return nil, false, false
	}

	return nil, false, false
}

// selectStaticExpr is [resolver.selectStatic]'s expression-shaped half: it
// walks the same container shapes and returns the LEAF EXPRESSION the
// traversal steps land on, instead of resolving that leaf into identity parts.
//
// It exists for GitHub issue #346's first half. A [Component.SoleElement]
// argument is narrowed to its one element BEFORE anything resolves it
// ([resolver.soleElementExpr]), and that narrowing is syntactic - hcl.ExprList
// over a list construct. On the each.value route the argument's expression is
// `each.value.cidr_blocks`, not a list construct, so nothing was ever narrowed
// and a one-element list reached [resolver.stringValueIn] whole, refusing as
// "Non-string identity argument: string required, but have tuple" - over a
// list the configuration wrote out with exactly one member in it. The element
// EXPRESSION is the thing that has the list construct in it, so the narrowing
// has to happen there, which needs the selection to stop one step short of
// resolving.
//
// Deliberately narrower than selectStatic in one way: a leaf that is itself a
// local, var or module-output reference is NOT chased. selectStatic chases
// those by handing them to [resolver.resolveNamed] / [resolver.resolveModuleOutput],
// both of which resolve rather than select, and there is no expression to give
// back at the end of that chase. A caller that gets false here falls back to
// the route it had before, which resolves the reference the ordinary way.
func (r *resolver) selectStaticExpr(expr hcl.Expression, rest []hcl.Traverser, scope instScope, ident configs.StaticIdentifier, depth int) (hcl.Expression, bool) {
	if depth > maxStaticDecomposeDepth {
		return nil, false
	}
	if paren, ok := expr.(*hclsyntax.ParenthesesExpr); ok {
		return r.selectStaticExpr(paren.Expression, rest, scope, ident, depth+1)
	}
	if len(rest) == 0 {
		return expr, true
	}

	step := rest[0]
	switch e := expr.(type) {
	case *hclsyntax.ObjectConsExpr:
		key, ok := stepKeyString(step)
		if !ok {
			return nil, false
		}
		for _, item := range e.Items {
			kv, diags := r.evalPure(item.KeyExpr, scope, ident)
			if diags.HasErrors() {
				continue
			}
			ks, err := convert.Convert(kv, cty.String)
			// IsMarked before AsString, which panics on a marked value: the
			// same guard [resolver.selectStatic] carries, for its reasons.
			if err != nil || ks.IsNull() || !ks.IsKnown() || ks.IsMarked() || ks.AsString() != key {
				continue
			}
			return r.selectStaticExpr(item.ValueExpr, rest[1:], scope, ident, depth+1)
		}
		return nil, false

	case *hclsyntax.TupleConsExpr:
		idx, ok := stepIndexInt(step)
		if !ok || idx < 0 || idx >= len(e.Exprs) {
			return nil, false
		}
		return r.selectStaticExpr(e.Exprs[idx], rest[1:], scope, ident, depth+1)

	case *hclsyntax.FunctionCallExpr:
		if e.Name != "merge" {
			return nil, false
		}
		// Last argument wins on a duplicate key, matching merge()'s own
		// precedence - [resolver.selectStatic]'s rule, for its reasons.
		for i := len(e.Args) - 1; i >= 0; i-- {
			if leaf, ok := r.selectStaticExpr(e.Args[i], rest, scope, ident, depth+1); ok {
				return leaf, true
			}
		}
		return nil, false
	}

	return nil, false
}

func stepKeyString(step hcl.Traverser) (string, bool) {
	switch s := step.(type) {
	case hcl.TraverseAttr:
		return s.Name, true
	case hcl.TraverseIndex:
		// IsMarked before AsString, which panics on a marked value. A
		// traversal step's key is parsed from a source literal today, so
		// this cannot fire - hcl only builds a TraverseIndex for a constant
		// index - but the test costs nothing and the alternative is a
		// crash if that ever stops being true.
		if s.Key.Type() == cty.String && s.Key.IsKnown() && !s.Key.IsNull() && !s.Key.IsMarked() {
			return s.Key.AsString(), true
		}
	}
	return "", false
}

func stepIndexInt(step hcl.Traverser) (int, bool) {
	idx, ok := step.(hcl.TraverseIndex)
	if !ok || idx.Key.Type() != cty.Number || !idx.Key.IsKnown() || idx.Key.IsNull() {
		return 0, false
	}
	var n int
	if err := gocty.FromCtyValue(idx.Key, &n); err != nil {
		return 0, false
	}
	return n, true
}
