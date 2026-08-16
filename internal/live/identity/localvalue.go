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
//     is knowable whatever the values are.
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
// ok is false whenever there is nothing to chase: an undeclared local, a
// variable at the root module (root variables come from the CLI or tfvars,
// never from another resource, so evalStatic's ordinary handling of them
// was already correct), or a variable the caller left to its declared
// default (a default is always configuration-authored, never a resource
// reference, for the same reason).
func (r *resolver) namedDef(root, name string, scope instScope) (hcl.Expression, instScope, func(), bool) {
	noop := func() {}

	switch root {
	case "local":
		local, ok := r.mod.Locals[name]
		if !ok {
			return nil, instScope{}, noop, false
		}
		return local.Expr, scope, noop, true

	case "var":
		if len(r.modInst) == 0 {
			return nil, instScope{}, noop, false
		}
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
			return nil, instScope{}, noop, false
		}
		restore := func() { r.mod, r.curCfg, r.modInst, r.eval = savedMod, savedCfg, savedInst, savedEval }

		mc, ok := r.mod.ModuleCalls[callInst.Call.Name]
		if !ok || mc.Config == nil {
			restore()
			return nil, instScope{}, noop, false
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
			rd, ok := ChildModuleRepetitionData(r.ctx, r.mod, childSubject(callInst.Call.Name), mc.Count, mc.ForEach, callInst.Key)
			if !ok {
				restore()
				return nil, instScope{}, noop, false
			}
			defScope = instScope{repetition: rd}
		}
		attrs, diags := mc.Config.JustAttributes()
		if diags.HasErrors() {
			restore()
			return nil, instScope{}, noop, false
		}
		attr, ok := attrs[name]
		if !ok {
			restore()
			return nil, instScope{}, noop, false
		}
		return attr.Expr, defScope, restore, true
	}
	return nil, instScope{}, noop, false
}

// ---- the key-set fix ---------------------------------------------------

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
//   - An object constructor directly ([resolver.objectConsKeys]).
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
//   - A for-comprehension producing an object ([resolver.forExprKeys]):
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
// declining: [resolver.staticCollKeys] answers with the key values cty
// itself binds a loop variable to - a string for a map, an object or a set
// of strings, a NUMBER for a list or a tuple - and this function, which
// serves the for_each position, narrows that to the strings a for_each may
// use as instance keys. A tuple therefore still refuses HERE (its keys are
// numbers, which for_each does not accept), while [resolver.forExprKeys]
// can range over it and get 0, 1, 2.
//
// It deliberately does not chase a selector before reaching the object
// (for_each = local.foo.bar is not supported): the corpus shape this fix
// exists for is always a bare local or module variable ranged over
// directly.
func (r *resolver) staticForEachKeys(expr hcl.Expression, ident configs.StaticIdentifier, depth int, tupleIsArgs bool) ([]string, bool) {
	keys, ok := r.staticCollKeys(expr, ident, depth, tupleIsArgs)
	if !ok {
		return nil, false
	}
	return stringKeys(keys)
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

// staticCollKeys is the shape-aware half of the key-set chase: it answers
// with the key set of whatever collection expr denotes, each key carried as
// the cty value HCL binds a for-expression's key variable to.
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
// Every shape chased is one whose key set is decided without evaluating a
// single VALUE expression, which is the whole point: a managed resource's
// attribute buried in a value must not refuse the block.
func (r *resolver) staticCollKeys(expr hcl.Expression, ident configs.StaticIdentifier, depth int, tupleIsArgs bool) ([]cty.Value, bool) {
	if depth > maxStaticDecomposeDepth {
		return nil, false
	}
	if paren, ok := expr.(*hclsyntax.ParenthesesExpr); ok {
		return r.staticCollKeys(paren.Expression, ident, depth+1, tupleIsArgs)
	}

	if trav, diags := hcl.AbsTraversalForExpr(expr); !diags.HasErrors() && len(trav) == 2 {
		if root := trav.RootName(); root == "local" || root == "var" {
			if nameStep, ok := trav[1].(hcl.TraverseAttr); ok {
				defExpr, _, restore, defOk := r.namedDef(root, nameStep.Name, instScope{})
				if defOk {
					defer restore()
					// tupleIsArgs propagates through the alias: the corpus
					// shape is merge(local.teams...), where the splatted
					// argument is a local naming the list.
					return r.staticCollKeys(defExpr, ident, depth+1, tupleIsArgs)
				}
			}
		}
	}

	if obj, ok := expr.(*hclsyntax.ObjectConsExpr); ok {
		names, ok := r.objectConsKeys(obj, ident)
		if !ok {
			return nil, false
		}
		return stringVals(names), true
	}

	if fe, ok := expr.(*hclsyntax.ForExpr); ok {
		names, ok := r.forExprKeys(fe, ident, depth)
		if !ok {
			return nil, false
		}
		// A for-comprehension with a key clause produces an OBJECT, whose
		// keys are strings whatever it ranged over.
		return stringVals(names), true
	}

	if tuple, ok := expr.(*hclsyntax.TupleConsExpr); ok {
		if !tupleIsArgs {
			// A tuple anywhere but merge()'s splat is a list, and a list's
			// keys are its integer indices: one per element, distinct by
			// construction, and knowable from the literal's own length
			// whatever its elements turn out to be.
			keys := make([]cty.Value, 0, len(tuple.Exprs))
			for i := range tuple.Exprs {
				keys = append(keys, cty.NumberIntVal(int64(i)))
			}
			return keys, true
		}
		seen := map[string]bool{}
		var keys []string
		for _, elem := range tuple.Exprs {
			// An element of the splatted list is one of merge's arguments,
			// an object in its own right - never itself a list of them, and
			// never a list at all, which is why this asks for strings.
			got, ok := r.staticForEachKeys(elem, ident, depth+1, false)
			if !ok {
				return nil, false
			}
			for _, k := range got {
				if !seen[k] {
					seen[k] = true
					keys = append(keys, k)
				}
			}
		}
		return stringVals(keys), true
	}

	if call, ok := expr.(*hclsyntax.FunctionCallExpr); ok && call.Name == "merge" {
		seen := map[string]bool{}
		var keys []string
		for i, arg := range call.Args {
			// merge(a, b...) splats only its FINAL argument, and only when
			// ExpandFinal is set: that is the one argument whose elements
			// stand in for merge's own separate arguments.
			argIsSplat := call.ExpandFinal && i == len(call.Args)-1
			// staticForEachKeys, not staticCollKeys: merge takes maps and
			// objects, so an argument whose key set is integer indices is
			// not a merge argument at all and refuses here rather than
			// contributing "0", "1" to the union.
			got, ok := r.staticForEachKeys(arg, ident, depth+1, argIsSplat)
			if !ok {
				return nil, false
			}
			for _, k := range got {
				if !seen[k] {
					seen[k] = true
					keys = append(keys, k)
				}
			}
		}
		return stringVals(keys), true
	}

	return nil, false
}

// stringVals lifts a string key set into the typed form [staticCollKeys]
// hands back. cty.StringVal constructs, so nothing here can be marked.
func stringVals(names []string) []cty.Value {
	out := make([]cty.Value, 0, len(names))
	for _, n := range names {
		out = append(out, cty.StringVal(n))
	}
	return out
}

// objectConsKeys reads every key of an object constructor, evaluating only
// the key expressions - never an item's value.
func (r *resolver) objectConsKeys(obj *hclsyntax.ObjectConsExpr, ident configs.StaticIdentifier) ([]string, bool) {
	seen := map[string]bool{}
	var keys []string
	for _, item := range obj.Items {
		kv, diags := r.evalPure(item.KeyExpr, instScope{}, ident)
		if diags.HasErrors() {
			return nil, false
		}
		ks, err := convert.Convert(kv, cty.String)
		// IsMarked: cty.Value.AsString panics on a marked value, and a key
		// built from a sensitive variable is marked. lint's and stamp's own
		// staticForEachKeys copies both test IsMarked before reading the
		// value; this one did not, so `{ "${var.secret}-a" = ... }` as a
		// for_each source crashed the run rather than refusing it.
		if err != nil || ks.IsNull() || !ks.IsKnown() || ks.IsMarked() {
			return nil, false
		}
		name := ks.AsString()
		if !seen[name] {
			seen[name] = true
			keys = append(keys, name)
		}
	}
	return keys, true
}

// forExprKeys is the for-comprehension half of #189's key-set extension: a
// for_each source reached through a local or module variable is often not
// an object constructor at all, but a for-comprehension BUILDING one -
// { for k, v in SRC : <key clause> => <value clause> } - and the key
// clause routinely needs nothing from the value side (team-members-datadog's
// all_user_with_merged_roles is exactly "for name, user in SRC : name =>
// {...}": the key clause is the bare key variable).
//
// The result's key set is knowable without ever evaluating a value clause
// whenever the key clause itself is: chase SRC's own key set (recursively,
// through [resolver.staticCollKeys] again, so a further local/merge/for
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
//     object keys and answered two, silently. [resolver.staticCollKeys]
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
func (r *resolver) forExprKeys(fe *hclsyntax.ForExpr, ident configs.StaticIdentifier, depth int) ([]string, bool) {
	if fe.KeyExpr == nil {
		return nil, false
	}

	srcKeys, srcVals, ok := r.forSourceElements(fe.CollExpr, ident, depth)
	if !ok {
		return nil, false
	}

	seen := map[string]bool{}
	var keys []string
	for i, srcKey := range srcKeys {
		vars := map[string]cty.Value{}
		if fe.KeyVar != "" {
			vars[fe.KeyVar] = srcKey
		}
		if srcVals != nil && fe.ValVar != "" {
			vars[fe.ValVar] = srcVals[i]
		}
		scope := instScope{vars: vars}

		if fe.CondExpr != nil {
			include, condOK := r.forCondIncludes(fe.CondExpr, scope, ident)
			if !condOK {
				return nil, false
			}
			if !include {
				continue
			}
		}

		kv, diags := r.evalPure(fe.KeyExpr, scope, ident)
		if diags.HasErrors() {
			return nil, false
		}
		ks, err := convert.Convert(kv, cty.String)
		// IsMarked for the same reason [resolver.objectConsKeys] tests it:
		// AsString panics on a marked value, and a key clause reading a
		// sensitive variable produces one.
		if err != nil || ks.IsNull() || !ks.IsKnown() || ks.IsMarked() {
			return nil, false
		}
		name := ks.AsString()
		if seen[name] {
			if fe.Group {
				continue
			}
			return nil, false
		}
		seen[name] = true
		keys = append(keys, name)
	}
	return keys, true
}

// forSourceElements reports what a for-comprehension's SOURCE collection
// binds its loop variables to, one entry per element in iteration order.
//
// vals is non-nil only when the source evaluated as a whole value, which is
// the only circumstance under which the value variable may be bound: the
// values are then ordinary configuration data this resolver read for
// itself, not a guess standing in for something it refused to read. When
// the source is only structurally knowable - a list literal whose elements
// mention a managed resource, merge() of comprehensions - keys are still
// exact and vals is nil.
//
// Evaluation is tried first because it yields strictly more: the same keys
// plus the values beside them.
func (r *resolver) forSourceElements(coll hclsyntax.Expression, ident configs.StaticIdentifier, depth int) (keys, vals []cty.Value, ok bool) {
	if keys, vals, ok := r.evaluatedCollElements(coll, ident); ok {
		return keys, vals, true
	}
	// tupleIsArgs is false: a for-comprehension's source is ranged over, so
	// a list here is a list, and its keys are its integer indices - never
	// the union of its elements' own object keys, which is only ever what
	// merge()'s splatted argument means.
	keys, ok = r.staticCollKeys(coll, ident, depth+1, false)
	if !ok {
		return nil, nil, false
	}
	return keys, nil, true
}

// evaluatedCollElements reads a collection that evaluates whole under the
// static scope, keys and values alike, in cty's own iteration order.
//
// The reading is cty's, not a re-derivation of it: a map or object is keyed
// by its own keys, a list or tuple by its integer indices, a set by its
// elements - which is what an element iterator hands back, and what
// hclsyntax.ForExpr.Value then binds. Anything that is not a collection at
// all cannot be ranged over and refuses.
func (r *resolver) evaluatedCollElements(expr hclsyntax.Expression, ident configs.StaticIdentifier) (keys, vals []cty.Value, ok bool) {
	// An impure call would make the collection a different collection on
	// the next run. Its LENGTH might well be stable, but nothing here can
	// show that, and [resolver.evalStatic] refuses the same shape one layer
	// up for the same reason.
	if len(impureCallsIn(expr)) > 0 {
		return nil, nil, false
	}
	val, diags := r.evalPure(expr, instScope{}, ident)
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
		vals = append(vals, v)
	}
	return keys, vals, true
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
// This is the boundary [configs.StaticValidateReferences] refuses with
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
		if _, ok := ChildModuleRepetitionData(r.ctx, r.mod, subject, mc.Count, mc.ForEach, key); !ok {
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
	defExpr, defScope, restore, ok := r.namedDef(root, name, scope)
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
