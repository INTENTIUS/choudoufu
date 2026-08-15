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
// defining expression, unevaluated, and whether reading it requires the
// resolver to switch modules first - a module variable's value is the
// module CALL's argument expression, which lives in and is evaluated
// against the CALLING module, not the module that declares the variable.
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
func (r *resolver) namedDef(root, name string) (hcl.Expression, func(), bool) {
	noop := func() {}

	switch root {
	case "local":
		local, ok := r.mod.Locals[name]
		if !ok {
			return nil, noop, false
		}
		return local.Expr, noop, true

	case "var":
		if len(r.modInst) == 0 {
			return nil, noop, false
		}
		parentInst, call := r.modInst.Call()
		savedMod, savedInst, savedEval := r.mod, r.modInst, r.eval
		if !r.enterModuleFor(parentInst) {
			return nil, noop, false
		}
		restore := func() { r.mod, r.modInst, r.eval = savedMod, savedInst, savedEval }

		mc, ok := r.mod.ModuleCalls[call.Name]
		if !ok || mc.Config == nil {
			restore()
			return nil, noop, false
		}
		if mc.Count != nil || mc.ForEach != nil {
			// The module call's own argument expression is evaluated in
			// the scope of the call's OWN repetition (each.key/each.value
			// or count.index of the module block, not of whatever resource
			// asked for var.name) - the exact scope [resolver.walkModule]
			// threads for a resource's own for_each, but there is no path
			// from here to it: [resolver.instScope] belongs to one
			// resource instance, and a module call's for_each is a
			// property of the CALL, evaluated before any instance inside
			// it exists (see [ChildModuleKeys]'s doc, and the 59c note on
			// evalPure below). Evaluating the argument expression with the
			// wrong scope, or none, does not fail loudly the way the
			// caller's own attempt just did - it can misreport an
			// undefined "each"/"count" as though the configuration never
			// mentioned it. Declining here leaves the ordinary
			// "Dynamic value in static context" diagnostic in place, the
			// same answer this shape has always gotten.
			restore()
			return nil, noop, false
		}
		attrs, diags := mc.Config.JustAttributes()
		if diags.HasErrors() {
			restore()
			return nil, noop, false
		}
		attr, ok := attrs[name]
		if !ok {
			restore()
			return nil, noop, false
		}
		return attr.Expr, restore, true
	}
	return nil, noop, false
}

// ---- the key-set fix ---------------------------------------------------

// staticForEachKeys is #178's key-set fix: the key set of an object
// constructor is knowable whatever its values are, and the key set is all
// a for_each expansion needs to enumerate instances. It is tried only after
// [resolver.evalStatic] has already failed to evaluate a for_each
// expression as a whole (see [resolver.forEachExpansion]), and it succeeds
// only when expr is, directly or through local/var aliasing or merge() of
// several, an object constructor - never touching a single value
// expression, which is the point: a resource reference in one of them must
// not refuse the whole block the way evaluating the object as one value
// does.
//
// It deliberately does not chase a selector before reaching the object
// (for_each = local.foo.bar is not supported): the corpus shape this fix
// exists for is always a bare local or module variable ranged over
// directly.
func (r *resolver) staticForEachKeys(expr hcl.Expression, ident configs.StaticIdentifier, depth int) ([]string, bool) {
	if depth > maxStaticDecomposeDepth {
		return nil, false
	}
	if paren, ok := expr.(*hclsyntax.ParenthesesExpr); ok {
		return r.staticForEachKeys(paren.Expression, ident, depth+1)
	}

	if trav, diags := hcl.AbsTraversalForExpr(expr); !diags.HasErrors() && len(trav) == 2 {
		if root := trav.RootName(); root == "local" || root == "var" {
			if nameStep, ok := trav[1].(hcl.TraverseAttr); ok {
				defExpr, restore, defOk := r.namedDef(root, nameStep.Name)
				if defOk {
					defer restore()
					return r.staticForEachKeys(defExpr, ident, depth+1)
				}
			}
		}
	}

	if obj, ok := expr.(*hclsyntax.ObjectConsExpr); ok {
		return r.objectConsKeys(obj, ident)
	}

	if call, ok := expr.(*hclsyntax.FunctionCallExpr); ok && call.Name == "merge" {
		seen := map[string]bool{}
		var keys []string
		for _, arg := range call.Args {
			got, ok := r.staticForEachKeys(arg, ident, depth+1)
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
		return keys, true
	}

	return nil, false
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
		if err != nil || ks.IsNull() || !ks.IsKnown() {
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
	if root != "local" && root != "var" {
		return nil, false, false
	}
	nameStep, ok := trav[1].(hcl.TraverseAttr)
	if !ok {
		return nil, false, false
	}
	return r.resolveNamed(root, nameStep.Name, trav[2:], scope, ident)
}

// resolveNamed resolves "local.name<rest>" or "var.name<rest>": it looks up
// what the local or the variable is defined as via [resolver.namedDef], then
// selects into that definition the way rest says to, via
// [resolver.selectStatic].
func (r *resolver) resolveNamed(root, name string, rest []hcl.Traverser, scope instScope, ident configs.StaticIdentifier) ([]Part, bool, bool) {
	defExpr, restore, ok := r.namedDef(root, name)
	if !ok {
		return nil, false, false
	}
	defer restore()
	return r.selectStatic(defExpr, rest, scope, ident, 0)
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
		if root := trav.RootName(); root == "local" || root == "var" {
			if nameStep, ok := trav[1].(hcl.TraverseAttr); ok {
				combined := make([]hcl.Traverser, 0, len(trav)-2+len(rest))
				combined = append(combined, trav[2:]...)
				combined = append(combined, rest...)
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
			if err != nil || ks.IsNull() || !ks.IsKnown() || ks.AsString() != key {
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
		if s.Key.Type() == cty.String && s.Key.IsKnown() && !s.Key.IsNull() {
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
