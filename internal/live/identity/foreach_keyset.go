// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/lang"
)

// staticForEachKeyNames reports the instance keys a for_each expression
// produces when the expression's KEY-DETERMINING sub-expressions are
// statically evaluable, even though the expression AS A WHOLE is not.
//
// A for_each needs two different things from one expression, and only one
// of them becomes an address. The key set decides which instances exist,
// and each key becomes part of an address, which becomes a tofu-address
// marker: it has to be known before anything is read from the cloud. The
// paired value is only ever read back through each.value, from inside an
// instance that already exists. Evaluating the whole expression as a unit
// conflates the two, so a map whose keys are literal but whose values
// mention a data source, a module output or another resource refuses on
// account of a value no instance key ever contains.
//
// That conflation is what this function undoes. It is the module-call
// counterpart of the keyOnly expansion [resolver.forEachExpansion] already
// builds for a resource's own for_each, and it hands back nothing else:
// callers that need a specific instance's each.value still go through
// [ChildModuleRepetitionData], which re-derives the value by evaluating
// the whole expression and refuses, exactly as before, when it cannot. A
// key set proven here therefore never licenses a value that was not.
//
// The shapes it proves, and why each is exact rather than approximate:
//
//   - an object constructor: its keys are its item key expressions, one
//     instance per item, no filtering possible. A key repeated within one
//     constructor refuses rather than folding two items into one instance -
//     that collapse is the shape that made two count.index instances share
//     one live marker (#178), and a non-injective key set is the same
//     defect by another route.
//   - a conditional whose condition is itself static: exactly one branch is
//     taken, so that branch's key set is the whole key set.
//   - merge(): the union of its arguments' key sets, which is merge's own
//     semantics. A key supplied by two arguments is one key in the result
//     and one instance here, so the union stays injective.
//   - a bare var.X or local.X reference, chased to its own defining
//     expression and recursed into exactly as if it had been written there
//     directly - a local's own "locals" block entry, in the same module; a
//     module variable's value at the CALLING module's own argument
//     expression, crossing exactly one module-call boundary (issue #308's
//     Gap B). See [chaseVarOrLocal].
//   - a for-comprehension building an object, `{ for k, v in SRC : key =>
//     val if cond }`: its key clause and "if" filter are evaluated once per
//     entry of SRC's own key set, reading only the attributes those two
//     clauses actually select off the value variable - never the entry's
//     value as a whole (issue #308's Gap A). See [collectForExprKeys].
//
// Anything else - a call other than merge, an index, a splat - is not
// proven and returns false, leaving the caller's own diagnostic to stand.
//
// ok is false whenever the key set is not proven. There is no path here
// that returns a partial or approximate key set: a caller must never
// substitute a guess for a false ok.
//
// cfg is the *configs.Config node the expression is written in, needed -
// beside cfg.Module itself, everything below read from before this - so
// that a bare var.X/local.X source collection can be chased to its own
// defining expression, across a module-call boundary for var.X (issue
// #308's Gap B; see [chaseVarOrLocal]).
func staticForEachKeyNames(ctx context.Context, cfg *configs.Config, subject string, expr hcl.Expression) ([]string, bool) {
	if cfg == nil || cfg.Module == nil || cfg.Module.StaticEvaluator == nil || expr == nil {
		return nil, false
	}
	names, ok := collectStaticForEachKeys(ctx, cfg, subject, expr)
	if !ok || len(names) == 0 {
		// An empty proven key set is indistinguishable from an
		// unproven one to every caller, and the callers all already
		// treat "no instances" as their own separate case, so there
		// is nothing to gain by reporting it here.
		return nil, false
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out, true
}

// collectStaticForEachKeys walks the shapes staticForEachKeyNames proves,
// returning the key names in the order the expression produces them. The
// slice it returns may repeat a name only where the shape it came from
// makes repetition mean one key (merge across arguments); a repetition
// that would silently collapse two instances into one is refused at the
// node that produced it.
func collectStaticForEachKeys(ctx context.Context, cfg *configs.Config, subject string, expr hcl.Expression) ([]string, bool) {
	var mod *configs.Module
	if cfg != nil {
		mod = cfg.Module
	}

	switch e := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		return collectStaticForEachKeys(ctx, cfg, subject, e.Expression)

	case *hclsyntax.ObjectConsExpr:
		names := make([]string, 0, len(e.Items))
		seen := make(map[string]bool, len(e.Items))
		for _, item := range e.Items {
			name, ok := staticKeyString(ctx, mod, subject, item.KeyExpr)
			if !ok {
				return nil, false
			}
			if seen[name] {
				// Two items keyed the same would expand to one
				// instance and one marker for two declarations.
				return nil, false
			}
			seen[name] = true
			names = append(names, name)
		}
		return names, true

	case *hclsyntax.ConditionalExpr:
		val, ok := staticSubValue(ctx, mod, subject, e.Condition)
		if !ok {
			return nil, false
		}
		b, err := convert.Convert(val, cty.Bool)
		if err != nil || b.IsNull() || !b.IsKnown() || b.IsMarked() {
			return nil, false
		}
		if b.True() {
			return collectStaticForEachKeys(ctx, cfg, subject, e.TrueResult)
		}
		return collectStaticForEachKeys(ctx, cfg, subject, e.FalseResult)

	case *hclsyntax.FunctionCallExpr:
		// merge is the one call whose result keys are decided entirely
		// by its arguments' keys, so the union of the argument key sets
		// is the result key set with no evaluation of any value.
		// ExpandFinal (merge(x...)) is excluded: the argument list is
		// then a single value this function would have to evaluate
		// whole, which is the very thing it exists to avoid.
		if e.Name != "merge" || e.ExpandFinal || len(e.Args) == 0 {
			return nil, false
		}
		var names []string
		for _, arg := range e.Args {
			sub, ok := collectStaticForEachKeys(ctx, cfg, subject, arg)
			if !ok {
				return nil, false
			}
			names = append(names, sub...)
		}
		return names, true

	case *hclsyntax.ForExpr:
		// The for-comprehension case: issue #308's Gap A. See
		// [collectForExprKeys].
		return collectForExprKeys(ctx, cfg, subject, e)

	case *hclsyntax.ScopeTraversalExpr:
		// A bare var.X or local.X reference: issue #308's Gap B. Chased to
		// its own defining expression and recursed into; falls through to
		// the whole-value evaluation below - unchanged from before this
		// case existed - whenever the chase itself declines (root
		// variable, a variable left to its default, or a call this cannot
		// safely attribute to one instance; see [chaseVarOrLocal]).
		if trav, diags := hcl.AbsTraversalForExpr(e); !diags.HasErrors() && len(trav) == 2 {
			if root := trav.RootName(); root == "local" || root == "var" {
				if nameStep, ok := trav[1].(hcl.TraverseAttr); ok {
					if defCfg, defExpr, _, ok := chaseVarOrLocal(cfg, root, nameStep.Name); ok {
						return collectStaticForEachKeys(ctx, defCfg, subject, defExpr)
					}
				}
			}
		}
	}

	// Anything else is proven only if it evaluates whole under the static
	// scope, which is the ordinary case the caller already handled - it
	// reaches here as one ARGUMENT of a shape above, where a fully static
	// operand sits beside a partly dynamic one (merge(local.base, {k =
	// dynamic})), and refusing it would make the composite unprovable for
	// a reason that has nothing to do with the dynamic part. It is also
	// where a var.X/local.X reference lands when the chase above declined.
	val, ok := staticSubValue(ctx, mod, subject, expr)
	if !ok {
		return nil, false
	}
	return collectionKeyNames(val)
}

// chaseVarOrLocal resolves a bare "local.name" or "var.name" reference to
// its own defining expression, unevaluated, together with the
// *configs.Config it has to be read in - the module a local's own
// definition lives in is the same one the reference was written in; the
// module a variable's value is written in is the CALLING module, one
// module-call boundary up (issue #308's Gap B).
//
// decl is the "variable" block the returned expression is the argument
// FOR, non-nil only for the "var" case - the same value
// [resolver.namedDef] hands back for the same reason: a value read out of
// the returned expression has not yet had the variable's own declared-type
// conversion and optional-attribute defaults applied
// (prepareFinalInputVariableValue, internal/tofu/eval_variable.go), and a
// caller reading one attribute out of an entry the expression describes
// needs decl to answer for an attribute the entry's own literal omits
// entirely (typedvar.go's #251 answers the same question for a value that
// evaluates whole; [resolveEntryAttr] is this function's caller's
// per-attribute counterpart, needed because a for-comprehension's source
// entry here often does not evaluate whole at all - that is the shape
// issue #308 is about).
//
// ok is false whenever there is nothing here to chase, safely:
//
//   - an undeclared local.
//   - a variable at the root module: a root variable's value comes from the
//     CLI or tfvars, never from a module call argument, so there is no
//     boundary to cross - the same reading [resolver.namedDef] gives
//     len(r.modInst) == 0.
//   - a variable left to its own declared default: the default is already
//     a plain configuration-authored expression sitting in THIS module's
//     own "variable" block, and the ordinary whole-value evaluation this
//     function's callers fall back to already reads it correctly - nothing
//     to chase.
//   - a variable supplied by a module call that itself carries a count or
//     a for_each. [resolver.namedDef] can chase this too, but only because
//     it is always answering for one SPECIFIC instance
//     (r.modInst.CallInstance()), threading that instance's own repetition
//     data through [ChildModuleRepetitionData] before reading the call's
//     argument expression. A for_each keyset PROOF - unlike a single
//     instance's own resolution - has no specific instance to ask for: a
//     repeated call may pass a different argument value to each of its own
//     instances, and answering with "the call's argument expression" as
//     though it meant one fixed thing would silently pick one instance's
//     answer for every instance this proof is trying to establish. Declining
//     here leaves the ordinary "Dynamic value in static context" diagnostic
//     the caller's own whole-value evaluation already raises for this shape.
func chaseVarOrLocal(cfg *configs.Config, root, name string) (defCfg *configs.Config, defExpr hcl.Expression, decl *configs.Variable, ok bool) {
	if cfg == nil || cfg.Module == nil {
		return nil, nil, nil, false
	}
	switch root {
	case "local":
		local, ok := cfg.Module.Locals[name]
		if !ok || local == nil {
			return nil, nil, nil, false
		}
		return cfg, local.Expr, nil, true

	case "var":
		if cfg.Parent == nil || cfg.Parent.Module == nil || len(cfg.Path) == 0 {
			return nil, nil, nil, false
		}
		callName := cfg.Path[len(cfg.Path)-1]
		mc, ok := cfg.Parent.Module.ModuleCalls[callName]
		if !ok || mc == nil || mc.Config == nil {
			return nil, nil, nil, false
		}
		if mc.Count != nil || mc.ForEach != nil {
			return nil, nil, nil, false
		}
		attrs, diags := mc.Config.JustAttributes()
		if diags.HasErrors() {
			return nil, nil, nil, false
		}
		attr, ok := attrs[name]
		if !ok {
			// Left to the variable's own declared default: nothing to
			// chase, see the doc above.
			return nil, nil, nil, false
		}
		return cfg.Parent, attr.Expr, cfg.Module.Variables[name], true
	}
	return nil, nil, nil, false
}

// forEachSourceEntry is one for_each source element as
// [collectForEachEntries] found it: its own value expression, UNEVALUATED,
// together with the *configs.Config it was written in and - only when the
// chase to reach it crossed exactly one module variable's declaration -
// that variable's own declaration, which supplies an optional attribute's
// default value for an attribute the entry's own literal never sets at
// all. See [chaseVarOrLocal]'s doc on decl for why that hop is what makes
// the declaration relevant here and nowhere else in this file.
type forEachSourceEntry struct {
	expr hcl.Expression
	cfg  *configs.Config
	decl *configs.Variable
}

// collectForEachEntries proves a collection's key set while keeping each
// entry's own value EXPRESSION rather than evaluating it - the value-side
// counterpart of [collectStaticForEachKeys], needed only by
// [collectForExprKeys], which has to read a single attribute out of an
// entry whose value as a WHOLE may never prove (issue #308's shape: one
// entry's `image` attribute reaches a data source, while the filter this
// function's caller has to evaluate reads only `create`, sitting right
// beside it in the same object constructor).
//
// The shapes it recurses through mirror [collectStaticForEachKeys] exactly
// - an object constructor, a statically-decided conditional, merge(), and
// the same var.X/local.X chase (see [chaseVarOrLocal]) - with one
// narrowing: a nested for-comprehension is not chased as a SOURCE here. A
// for_each source that is itself a comprehension is not a shape issue #308
// or anything in the corpus needs, and chasing it would mean re-deriving
// [resolver.forExprElems]'s own value/expression bookkeeping a second time
// for no proven need; a source this does not recognize simply is not
// proven, exactly like [collectStaticForEachKeys]'s own "anything else".
func collectForEachEntries(ctx context.Context, cfg *configs.Config, subject string, expr hcl.Expression) (map[string]forEachSourceEntry, bool) {
	if cfg == nil || cfg.Module == nil || cfg.Module.StaticEvaluator == nil || expr == nil {
		return nil, false
	}

	switch e := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		return collectForEachEntries(ctx, cfg, subject, e.Expression)

	case *hclsyntax.ObjectConsExpr:
		out := make(map[string]forEachSourceEntry, len(e.Items))
		for _, item := range e.Items {
			name, ok := staticKeyString(ctx, cfg.Module, subject, item.KeyExpr)
			if !ok {
				return nil, false
			}
			if _, dup := out[name]; dup {
				// Two items keyed the same: the same non-injective shape
				// [collectStaticForEachKeys]'s ObjectConsExpr case refuses,
				// for the same reason.
				return nil, false
			}
			out[name] = forEachSourceEntry{expr: item.ValueExpr, cfg: cfg}
		}
		return out, true

	case *hclsyntax.ConditionalExpr:
		val, ok := staticSubValue(ctx, cfg.Module, subject, e.Condition)
		if !ok {
			return nil, false
		}
		b, err := convert.Convert(val, cty.Bool)
		if err != nil || b.IsNull() || !b.IsKnown() || b.IsMarked() {
			return nil, false
		}
		if b.True() {
			return collectForEachEntries(ctx, cfg, subject, e.TrueResult)
		}
		return collectForEachEntries(ctx, cfg, subject, e.FalseResult)

	case *hclsyntax.FunctionCallExpr:
		if e.Name != "merge" || e.ExpandFinal || len(e.Args) == 0 {
			return nil, false
		}
		out := map[string]forEachSourceEntry{}
		for _, arg := range e.Args {
			sub, ok := collectForEachEntries(ctx, cfg, subject, arg)
			if !ok {
				return nil, false
			}
			for k, v := range sub {
				// merge()'s own precedence: a key supplied by two arguments
				// takes the later argument's value. e.Args is walked in
				// order, so this always overwrites with the later one
				// whatever order Go happens to range sub in.
				out[k] = v
			}
		}
		return out, true

	case *hclsyntax.ScopeTraversalExpr:
		if trav, diags := hcl.AbsTraversalForExpr(e); !diags.HasErrors() && len(trav) == 2 {
			if root := trav.RootName(); root == "local" || root == "var" {
				if nameStep, ok := trav[1].(hcl.TraverseAttr); ok {
					if defCfg, defExpr, decl, ok := chaseVarOrLocal(cfg, root, nameStep.Name); ok {
						entries, ok := collectForEachEntries(ctx, defCfg, subject, defExpr)
						if !ok {
							return nil, false
						}
						if decl != nil {
							for k, v := range entries {
								v.decl = decl
								entries[k] = v
							}
						}
						return entries, true
					}
				}
			}
		}
	}

	return nil, false
}

// referencedAttrs collects every single-level attribute name a for-
// comprehension clause selects off a loop variable - `v.create` contributes
// "create" - across every clause expression given. ok is false the moment
// any traversal rooted at root is anything OTHER than exactly that shape: a
// bare `v`, `v[0]`, `v.a.b`, or root used as an argument to a function -
// none of which this file can safely read a fragment of without evaluating
// more of the entry than the caller may.
func referencedAttrs(exprs []hcl.Expression, root string) (map[string]bool, bool) {
	attrs := map[string]bool{}
	if root == "" {
		return attrs, true
	}
	for _, expr := range exprs {
		if expr == nil {
			continue
		}
		for _, trav := range expr.Variables() {
			if trav.RootName() != root {
				continue
			}
			if len(trav) != 2 {
				return nil, false
			}
			attrStep, ok := trav[1].(hcl.TraverseAttr)
			if !ok {
				return nil, false
			}
			attrs[attrStep.Name] = true
		}
	}
	return attrs, true
}

// elementDefaults reads the per-element [typeexpr.Defaults] out of a
// collection-typed variable's own declared-type defaults tree: a map,
// list or set's element type is always stored at Children[""]
// (typeexpr.Defaults's own doc), which is where container_definitions'
// declared `map(object({create = optional(bool, true), ...}))` keeps the
// object-level defaults every entry shares. A variable declared directly
// as an object type (no collection wrapper) answers with its own node,
// which is not the corpus shape this file was written for but is the
// correct reading if it ever occurs: there is no collection hop to make.
func elementDefaults(d *typeexpr.Defaults) *typeexpr.Defaults {
	if d == nil {
		return nil
	}
	switch {
	case d.Type.IsMapType(), d.Type.IsListType(), d.Type.IsSetType():
		return d.Children[""]
	case d.Type.IsObjectType():
		return d
	}
	return nil
}

// resolveEntryAttr answers what one for_each source entry's own value has
// at attrName, without evaluating any OTHER attribute of it - the read
// issue #308's filter needs (`v.create`, sitting beside an unrelated
// `image` attribute that reaches a data source and must stay untouched).
//
// Three sources are tried, in order, and the first that answers wins:
//
//  1. The entry's whole value, if it happens to prove anyway
//     ([staticSubValue]) - the ordinary case for an entry with no
//     unprovable attribute at all (issue #308's own second entry, keyed by
//     local.container_name, is exactly this: every attribute is a plain
//     literal or a local).
//  2. The entry's own object-constructor syntax, read structurally for
//     just the one item named attrName - the case an unprovable SIBLING
//     attribute forces (issue #308's `fluent-bit` entry: `image` fails to
//     evaluate, but `create` sits in the same literal and reads on its
//     own).
//  3. The declared variable's own optional-attribute default for attrName,
//     when the entry's literal does not mention it at all - what
//     `create = optional(bool, true)` supplies for an entry that never
//     writes `create`, the same default prepareFinalInputVariableValue
//     would apply before anything inside the module saw it (typedvar.go's
//     #251 answers this for a value that proves whole; this is its
//     per-attribute counterpart, needed because an entry that does NOT
//     prove whole here still has to answer for an attribute it omits).
//
// ok is false when none of the three answers - the attribute is written
// with an expression this pass cannot evaluate, and it has no declared
// default to fall back on, so it genuinely cannot be determined without
// reading the cloud.
func resolveEntryAttr(ctx context.Context, entry forEachSourceEntry, subject, attrName string) (cty.Value, bool) {
	entryExpr := entry.expr
	for {
		if paren, ok := entryExpr.(*hclsyntax.ParenthesesExpr); ok {
			entryExpr = paren.Expression
			continue
		}
		break
	}

	if val, ok := staticSubValue(ctx, entry.cfg.Module, subject, entryExpr); ok {
		if (val.Type().IsObjectType() && val.Type().HasAttribute(attrName)) || val.Type().IsMapType() {
			if val.ContainsMarked() {
				return cty.NilVal, false
			}
			attrVal := val.GetAttr(attrName)
			if attrVal.IsMarked() || attrVal.IsNull() || !attrVal.IsKnown() {
				return cty.NilVal, false
			}
			return attrVal, true
		}
	}

	if obj, ok := entryExpr.(*hclsyntax.ObjectConsExpr); ok {
		for _, item := range obj.Items {
			name, ok := staticKeyString(ctx, entry.cfg.Module, subject, item.KeyExpr)
			if !ok || name != attrName {
				continue
			}
			return staticSubValue(ctx, entry.cfg.Module, subject, item.ValueExpr)
		}
	}

	if entry.decl != nil && entry.decl.TypeDefaults != nil {
		if elemDefaults := elementDefaults(entry.decl.TypeDefaults); elemDefaults != nil {
			if def, ok := elemDefaults.DefaultValues[attrName]; ok {
				if def.ContainsMarked() || def.IsNull() || !def.IsKnown() {
					return cty.NilVal, false
				}
				return def, true
			}
		}
	}

	return cty.NilVal, false
}

// evalForExprClause evaluates one for-comprehension clause - its key
// expression or its "if" filter - against ONE entry's bound loop
// variables, mirroring the split [resolver.evalPure] applies for a
// resource's own for_each: the comprehension's own k/v names are supplied
// directly, in a child [hcl.EvalContext], while every other reference
// (local.*, var.*, path.*, terraform.*, tofu.*) is resolved the ordinary
// way through the module's own static evaluator. It is
// [collectForExprKeys]'s one point of contact with cty/HCL evaluation, and
// the one place a PARTIAL - not whole-entry - value ever gets bound to a
// for-comprehension's value variable.
//
// impureCallsIn is checked explicitly, the same guard
// [resolver.evalStatic] applies before its own equivalent low-level call:
// the low-level EvalContext this function builds is not routed through
// [configs.StaticEvaluator.Pure]'s own uuid()/timestamp()/bcrypt()
// neutralization, so nothing else here would catch one.
func evalForExprClause(ctx context.Context, mod *configs.Module, subject string, expr hcl.Expression, vars map[string]cty.Value) (cty.Value, bool) {
	if mod == nil || mod.StaticEvaluator == nil || expr == nil {
		return cty.NilVal, false
	}
	if len(impureCallsIn(expr)) > 0 {
		return cty.NilVal, false
	}

	var travs []hcl.Traversal
	for _, trav := range expr.Variables() {
		if _, bound := vars[trav.RootName()]; bound {
			continue
		}
		switch trav.RootName() {
		case "var", "local", "path", "terraform", "tofu":
			// Evaluable in a static scope.
		default:
			return cty.NilVal, false
		}
		travs = append(travs, trav)
	}
	refs, refDiags := lang.References(addrs.ParseRef, travs)
	if refDiags.HasErrors() {
		return cty.NilVal, false
	}

	ident := configs.StaticIdentifier{Module: addrs.RootModule, Subject: subject, DeclRange: expr.Range()}
	hclCtx, ctxDiags := mod.StaticEvaluator.Pure().EvalContext(ctx, ident, refs)
	if ctxDiags.HasErrors() {
		return cty.NilVal, false
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}
	if len(vars) > 0 {
		child := hclCtx.NewChild()
		child.Variables = vars
		hclCtx = child
	}

	val, valDiags := expr.Value(hclCtx)
	if valDiags.HasErrors() {
		return cty.NilVal, false
	}
	return val, true
}

// collectForExprKeys proves a for-comprehension's key set - `{ for k, v in
// SRC : keyExpr => valExpr if cond }` - by evaluating the key clause and
// the "if" filter once per entry of SRC's own key set, reading only the
// attributes those two clauses actually select off the value variable via
// [referencedAttrs] and [resolveEntryAttr] - never the entry's value as a
// whole. See [collectStaticForEachKeys]'s own doc for why a for-
// comprehension needed a dedicated function rather than folding into the
// generic recursion above it: proving this needs somewhere to bind the
// loop variables per entry, which is exactly what this function builds.
//
// fe.ValExpr (the comprehension's own OUTPUT value clause) is deliberately
// never read here: the key set this proves does not depend on it, and a
// value clause that reaches something unprovable - the ordinary case, since
// the value is exactly where a managed resource's attribute or a live data
// source is allowed to sit - must not refuse the key set on its account.
// [ChildModuleRepetitionData] answers each.value separately and keeps
// refusing it exactly as before.
//
// A key-less for-expression (`for v in x : f(v)`) produces a tuple, never
// an object, so for_each cannot take it directly and this refuses outright
// rather than trying to prove something for_each would reject anyway.
// Grouping mode (`k => v...`) is also declined: a for_each source has no
// use for a value shaped as a tuple of matches, and nothing in the corpus
// this fix was written for needs it.
func collectForExprKeys(ctx context.Context, cfg *configs.Config, subject string, fe *hclsyntax.ForExpr) ([]string, bool) {
	if fe.KeyExpr == nil || fe.Group {
		return nil, false
	}

	entries, ok := collectForEachEntries(ctx, cfg, subject, fe.CollExpr)
	if !ok {
		return nil, false
	}

	clauses := []hcl.Expression{fe.KeyExpr}
	if fe.CondExpr != nil {
		clauses = append(clauses, fe.CondExpr)
	}
	attrNames, ok := referencedAttrs(clauses, fe.ValVar)
	if !ok {
		return nil, false
	}

	entryKeys := make([]string, 0, len(entries))
	for k := range entries {
		entryKeys = append(entryKeys, k)
	}
	sort.Strings(entryKeys)

	var mod *configs.Module
	if cfg != nil {
		mod = cfg.Module
	}

	seen := map[string]bool{}
	var names []string
	for _, srcKey := range entryKeys {
		entry := entries[srcKey]

		vars := map[string]cty.Value{}
		if fe.KeyVar != "" {
			vars[fe.KeyVar] = cty.StringVal(srcKey)
		}
		if fe.ValVar != "" {
			attrs := make(map[string]cty.Value, len(attrNames))
			for name := range attrNames {
				val, ok := resolveEntryAttr(ctx, entry, subject, name)
				if !ok {
					return nil, false
				}
				attrs[name] = val
			}
			vars[fe.ValVar] = cty.ObjectVal(attrs)
		}

		if fe.CondExpr != nil {
			condVal, ok := evalForExprClause(ctx, mod, subject, fe.CondExpr, vars)
			if !ok {
				return nil, false
			}
			include, err := convert.Convert(condVal, cty.Bool)
			if err != nil || include.IsNull() || !include.IsKnown() || include.IsMarked() {
				return nil, false
			}
			if !include.True() {
				continue
			}
		}

		keyVal, ok := evalForExprClause(ctx, mod, subject, fe.KeyExpr, vars)
		if !ok {
			return nil, false
		}
		ks, err := convert.Convert(keyVal, cty.String)
		if err != nil || ks.IsNull() || !ks.IsKnown() || ks.IsMarked() {
			return nil, false
		}
		name := ks.AsString()
		if seen[name] {
			// Two entries producing the same key would collapse into one
			// instance - the same non-injective shape every other case in
			// this file refuses rather than silently folding.
			return nil, false
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, true
}

// forEachKeysKnown reports whether an already-evaluated for_each value
// determines its own instance KEYS, which is the question stock OpenTofu asks
// and is strictly weaker than "is this value wholly known".
//
// Stock's rule lives in [evalchecks.performValueChecks] and its set-specific
// companion [evalchecks.performSetValueChecks]:
//
//   - For a map or an object, only the value ITSELF has to be known
//     (`!resultVal.IsKnown()`). An element may be unknown, because a map's
//     element values never become part of an address - stock's own refusal
//     text for the case it does reject says so outright: "it's better to
//     define the map keys statically in your configuration and place
//     apply-time results only in the map values".
//   - For a set, every element has to be known, because a set's elements ARE
//     its keys. performSetValueChecks collapses a set that is not wholly
//     known to an unknown value for exactly that reason.
//
// This package asked `IsWhollyKnown` of all three, so it refused a map whose
// keys were literal and whose values held an apply-time attribute - a
// configuration stock plans without complaint. That is the shape at
// `modules/acm-certificate/main.tf:30` in the corpus (issue #187): the keys
// are the certificate's domain names, which the AWS provider fills in during
// PlanResourceChange, and only the DNS record name, type and value underneath
// them are unknown until apply.
//
// The narrowing is what makes this safe against #183's unset-variable cohort:
// a required root variable with no value evaluates to an unknown at the TOP
// level, so `IsKnown` is false and the refusal fires exactly as before. No
// provenance travels with the value; stock's own rule already separates
// "I cannot say which instances exist" from "I cannot say what is inside
// them", and only the first is an address problem.
func forEachKeysKnown(val cty.Value) bool {
	if val == cty.NilVal {
		return false
	}
	ty := val.Type()
	if ty.IsMapType() || ty.IsObjectType() {
		return val.IsKnown()
	}
	return val.IsWhollyKnown()
}

// collectionKeyNames reads the key set out of an already-evaluated value,
// under the same reading a module call's for_each gets everywhere else in
// this package: a map or object is keyed by its own keys, a set of strings
// by its elements. Any other type is not a for_each collection at all, and
// the caller's own invalid-for_each diagnostic covers it.
func collectionKeyNames(val cty.Value) ([]string, bool) {
	// ContainsMarked rather than IsMarked, because a mark on an ELEMENT
	// hoists to the whole value only for a set - a marked string inside a
	// list, map, object or tuple leaves the outer value unmarked, and the
	// reads below would then panic on it. That asymmetry is cty's, is
	// asserted in internal/live/marksafe's TestOnlySetsHoistElementMarks,
	// and is what made six sites in this package crash the run.
	//
	// Refusing rather than unmarking: these names become for_each keys,
	// which become part of the address, which becomes the tofu-address
	// marker written to a cloud tag in plaintext. A sensitive value must
	// not travel that path.
	if val.ContainsMarked() {
		return nil, false
	}
	ty := val.Type()
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		names := make([]string, 0, val.LengthInt())
		for it := val.ElementIterator(); it.Next(); {
			k, _ := it.Element()
			if k.Type() != cty.String || k.IsNull() || !k.IsKnown() {
				return nil, false
			}
			names = append(names, k.AsString())
		}
		return names, true
	case ty.IsSetType():
		if ty.ElementType() != cty.String {
			return nil, false
		}
		names := make([]string, 0, val.LengthInt())
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			// IsMarked on the element as well as ContainsMarked on the
			// collection above. The outer guard already makes this
			// unreachable, but marksafe proves each receiver where it is
			// read rather than reasoning across an iterator - the key half
			// of it.Element() carries a proof and the value half does not,
			// which is exactly the distinction the eighth panic site turned
			// on. A redundant guard is cheaper than an argument.
			if v.IsMarked() || v.IsNull() || !v.IsKnown() {
				return nil, false
			}
			names = append(names, v.AsString())
		}
		return names, true
	}
	return nil, false
}

// staticKeyString evaluates one object-constructor key expression and
// returns it as the string an instance key has to be. A naked identifier
// key is a literal name rather than a reference, which is
// [hclsyntax.ObjectConsKeyExpr]'s own rule and not something this function
// re-decides.
func staticKeyString(ctx context.Context, mod *configs.Module, subject string, expr hcl.Expression) (string, bool) {
	val, ok := staticSubValue(ctx, mod, subject, expr)
	if !ok {
		return "", false
	}
	s, err := convert.Convert(val, cty.String)
	if err != nil || s.IsNull() || !s.IsKnown() || s.IsMarked() {
		return "", false
	}
	return s.AsString(), true
}

// staticSubValue evaluates one sub-expression under the same evaluable
// scope every other static read in this package uses - var, local, path,
// terraform and tofu, and nothing else - and reports false rather than a
// diagnostic, because the caller already holds a diagnostic describing the
// whole expression and a second one about a fragment of it would read as a
// second problem.
func staticSubValue(ctx context.Context, mod *configs.Module, subject string, expr hcl.Expression) (cty.Value, bool) {
	for _, trav := range expr.Variables() {
		switch trav.RootName() {
		case "var", "local", "path", "terraform", "tofu":
			// Evaluable in a static scope.
		default:
			return cty.NilVal, false
		}
	}
	ident := configs.StaticIdentifier{Module: addrs.RootModule, Subject: subject, DeclRange: expr.Range()}
	val, diags := mod.StaticEvaluator.Pure().Evaluate(ctx, expr, ident)
	if diags.HasErrors() {
		return cty.NilVal, false
	}
	if val == cty.NilVal || !val.IsWhollyKnown() || val.IsNull() || val.IsMarked() {
		return cty.NilVal, false
	}
	return val, true
}
