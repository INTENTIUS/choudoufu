// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
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
//
// Anything else - a bare reference, a for-comprehension, any call other
// than merge - is not proven and returns false, leaving the caller's own
// diagnostic to stand. A for-comprehension in particular is excluded on
// purpose: its if clause can drop elements, so proving its key set means
// evaluating that clause against every element of a collection this
// function has no scope to bind an iterator symbol in.
//
// ok is false whenever the key set is not proven. There is no path here
// that returns a partial or approximate key set: a caller must never
// substitute a guess for a false ok.
func staticForEachKeyNames(ctx context.Context, mod *configs.Module, subject string, expr hcl.Expression) ([]string, bool) {
	if mod == nil || mod.StaticEvaluator == nil || expr == nil {
		return nil, false
	}
	names, ok := collectStaticForEachKeys(ctx, mod, subject, expr)
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
func collectStaticForEachKeys(ctx context.Context, mod *configs.Module, subject string, expr hcl.Expression) ([]string, bool) {
	switch e := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		return collectStaticForEachKeys(ctx, mod, subject, e.Expression)

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
			return collectStaticForEachKeys(ctx, mod, subject, e.TrueResult)
		}
		return collectStaticForEachKeys(ctx, mod, subject, e.FalseResult)

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
			sub, ok := collectStaticForEachKeys(ctx, mod, subject, arg)
			if !ok {
				return nil, false
			}
			names = append(names, sub...)
		}
		return names, true
	}

	// Anything else is proven only if it evaluates whole under the static
	// scope, which is the ordinary case the caller already handled - it
	// reaches here as one ARGUMENT of a shape above, where a fully static
	// operand sits beside a partly dynamic one (merge(local.base, {k =
	// dynamic})), and refusing it would make the composite unprovable for
	// a reason that has nothing to do with the dynamic part.
	val, ok := staticSubValue(ctx, mod, subject, expr)
	if !ok {
		return nil, false
	}
	return collectionKeyNames(val)
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
