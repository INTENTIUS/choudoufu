// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lang

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/blocktoattr"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// References finds all of the references in the given set of traversals,
// returning diagnostics if any of the traversals cannot be interpreted as a
// reference.
//
// This function does not do any de-duplication of references, since references
// have source location information embedded in them and so any invalid
// references that are duplicated should have errors reported for each
// occurrence.
//
// If the returned diagnostics contains errors then the result may be
// incomplete or invalid. Otherwise, the returned slice has one reference per
// given traversal, though it is not guaranteed that the references will
// appear in the same order as the given traversals.
func References(parseRef ParseRef, traversals []hcl.Traversal) ([]*addrs.Reference, tfdiags.Diagnostics) {
	if len(traversals) == 0 {
		return nil, nil
	}

	var diags tfdiags.Diagnostics
	refs := make([]*addrs.Reference, 0, len(traversals))

	for _, traversal := range traversals {
		ref, refDiags := parseRef(traversal)
		diags = diags.Append(refDiags)
		if ref == nil {
			continue
		}
		refs = append(refs, ref)
	}

	return refs, diags
}

// ReferencesInBlock is a helper wrapper around References that first searches
// the given body for traversals, before converting those traversals to
// references.
//
// A block schema must be provided so that this function can determine where in
// the body variables are expected.
func ReferencesInBlock(parseRef ParseRef, body hcl.Body, schema *configschema.Block) ([]*addrs.Reference, tfdiags.Diagnostics) {
	if body == nil {
		return nil, nil
	}

	// We use blocktoattr.ExpandedVariables instead of hcldec.Variables or
	// dynblock.VariablesHCLDec here because when we evaluate a block we'll
	// first apply the dynamic block extension and _then_ the blocktoattr
	// transform, and so blocktoattr.ExpandedVariables takes into account
	// both of those transforms when it analyzes the body to ensure we find
	// all of the references as if they'd already moved into their final
	// locations, even though we can't expand dynamic blocks yet until we
	// already know which variables are required.
	//
	// The set of cases we want to detect here is covered by the tests for
	// the plan graph builder in the main 'tofu' package, since it's
	// in a better position to test this due to having mock providers etc
	// available.
	traversals := blocktoattr.ExpandedVariables(body, schema)
	funcs := filterProviderFunctions(blocktoattr.ExpandedFunctions(body, schema))
	traversals = append(traversals, splatEachTraversals(body)...)

	return References(parseRef, append(traversals, funcs...))
}

// ReferencesInExpr is a helper wrapper around References that first searches
// the given expression for traversals, before converting those traversals
// to references.
func ReferencesInExpr(parseRef ParseRef, expr hcl.Expression) ([]*addrs.Reference, tfdiags.Diagnostics) {
	if expr == nil {
		return nil, nil
	}
	traversals := expr.Variables()
	if fexpr, ok := expr.(hcl.ExpressionWithFunctions); ok {
		funcs := filterProviderFunctions(fexpr.Functions())
		traversals = append(traversals, funcs...)
	}
	traversals = append(traversals, splatEachTraversals(expr)...)
	return References(parseRef, traversals)
}

// ProviderFunctionsInExpr is a helper wrapper around References that searches for provider
// function traversals in an ExpressionWithFunctions, then converts the traversals into
// references
func ProviderFunctionsInExpr(parseRef ParseRef, expr hcl.Expression) ([]*addrs.Reference, tfdiags.Diagnostics) {
	if expr == nil {
		return nil, nil
	}
	if fexpr, ok := expr.(hcl.ExpressionWithFunctions); ok {
		funcs := filterProviderFunctions(fexpr.Functions())
		return References(parseRef, funcs)
	}
	return nil, nil
}

func filterProviderFunctions(funcs []hcl.Traversal) []hcl.Traversal {
	pfuncs := make([]hcl.Traversal, 0, len(funcs))
	for _, fn := range funcs {
		if len(fn) == 0 {
			continue
		}
		if root, ok := fn[0].(hcl.TraverseRoot); ok {
			if addrs.ParseFunction(root.Name).IsNamespace(addrs.FunctionNamespaceProvider) {
				pfuncs = append(pfuncs, fn)
			}
		}
	}
	return pfuncs
}

// splatEachTraversals finds every legacy `resource.*.attr` splat reachable
// from root and, for each one, returns a traversal combining the splat's
// Source (the resource being splatted over) with the attribute steps its
// "Each" expression applies to one element - the demand [hcl.Expression.
// Variables] never reports on its own.
//
// hclsyntax represents `SOURCE.*.attr` as a SplatExpr whose Each is a
// RelativeTraversalExpr evaluated per-element against an AnonSymbolExpr
// placeholder, not against SOURCE itself (hashicorp/hcl/v2/hclsyntax's
// expression.go). variablesWalker (that package's variables.go) only ever
// calls back for a *ScopeTraversalExpr, and a RelativeTraversalExpr rooted
// in a placeholder is never one - so `Variables()` sees the reference to
// SOURCE and stops there, structurally blind to the ".attr" the splat goes
// on to read from each element. A caller downstream of Variables() (this
// package's own References, and everything built on it: static reference
// classification, coverage checks, refusal diagnostics) never learns that
// "attr" was demanded at all.
//
// This walks the real expression tree instead of trusting Variables(), so
// it sees the SplatExpr node directly and can read Each's own traversal off
// it. It only ever ADDS traversals alongside whatever Variables() already
// found; nothing here removes or replaces a reference.
//
// This reaches every splat this package's own callers evaluate: an output
// or local value's expression (through ReferencesInExpr, since a module
// output's `value` and a local's own expression are both evaluated that
// way), and a resource, data source or provider configuration's own body
// (through ReferencesInBlock). A splat inside a `dynamic` block's generated
// content is not reached: [Scope.ExpandBlock] evaluates the block before
// this ever sees the expanded body, and the expanded body reaching
// ReferencesInBlock afterward is no longer a *hclsyntax.Body this function
// can walk, so [splatEachTraversals] finds nothing there and neither
// clause below fires - the same "found=false, add nothing" fallback used
// for every other shape this cannot decide.
func splatEachTraversals(x any) []hcl.Traversal {
	node, ok := x.(hclsyntax.Node)
	if !ok {
		return nil
	}
	var out []hcl.Traversal
	//nolint:errcheck // a walk error here means one splat's shape could not
	// be read; every other reference in the expression is still found by
	// the ordinary Variables()-based path, so nothing needs to abort.
	hclsyntax.Walk(node, splatWalker{out: &out})
	return out
}

// splatWalker is an [hclsyntax.Walker] that collects a combined
// Source+Each traversal for every [hclsyntax.SplatExpr] it finds. See
// [splatEachTraversals].
type splatWalker struct {
	out *[]hcl.Traversal
}

func (w splatWalker) Enter(n hclsyntax.Node) hcl.Diagnostics {
	splat, ok := n.(*hclsyntax.SplatExpr)
	if !ok {
		return nil
	}
	source, ok := splat.Source.(*hclsyntax.ScopeTraversalExpr)
	if !ok {
		// The splat's source is itself an expression (a function call, a
		// nested splat, ...) rather than a plain reference: nothing this
		// function can turn into an absolute traversal, so it contributes
		// nothing extra. Variables() already found whatever plain
		// references live inside that source expression on its own.
		return nil
	}
	each, ok := splatEachSteps(splat.Each)
	if !ok || len(each) == 0 {
		// Either an Each shape this function does not recognize (see
		// [splatEachSteps]), or a bare `resource.*` with nothing following
		// the splat - which demands nothing beyond the resource itself,
		// already covered by Variables() finding Source on its own.
		return nil
	}
	combined := make(hcl.Traversal, 0, len(source.Traversal)+len(each))
	combined = append(combined, source.Traversal...)
	combined = append(combined, each...)
	*w.out = append(*w.out, combined)
	return nil
}

func (w splatWalker) Exit(hclsyntax.Node) hcl.Diagnostics { return nil }

// splatEachSteps extracts the relative traversal steps a SplatExpr's Each
// expression applies to its per-element placeholder ([hclsyntax.
// AnonSymbolExpr]), when Each is exactly that placeholder, optionally
// wrapped in one relative traversal - the shape a legacy `resource.*.attr`
// splat parses to. Anything else (a function call over the element, an
// index into it, a nested splat) is a shape this cannot yet decide, so it
// reports found=false and the caller adds nothing rather than guess at a
// demand it cannot name precisely.
func splatEachSteps(each hclsyntax.Expression) (hcl.Traversal, bool) {
	switch e := each.(type) {
	case *hclsyntax.AnonSymbolExpr:
		// `resource.*` with nothing following the splat.
		return nil, true
	case *hclsyntax.RelativeTraversalExpr:
		if _, ok := e.Source.(*hclsyntax.AnonSymbolExpr); !ok {
			return nil, false
		}
		return e.Traversal, true
	default:
		return nil, false
	}
}
