// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"context"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// splatCoverageDiagnostics reports the same [RefusedReference]-tagged
// diagnostic [staticScopeData.StaticValidateReferences] would raise for a
// legacy `resource.*.attr` splat's own Each attribute, when that attribute
// is not covered by the current data lookup - a demand
// [hcl.Expression.Variables] never surfaces at all, so ordinary reference
// gathering (internal/lang's ReferencesInExpr/ReferencesInBlock, which
// every caller of [lang.Scope.EvalExpr]/EvalBlock goes through) never asks
// StaticValidateReferences about it in the first place. See
// [splatEachTraversals] for exactly why Variables() is blind here:
// hashicorp/hcl/v2/hclsyntax's SplatExpr evaluates its Each expression
// against an AnonSymbolExpr placeholder, never against a
// *ScopeTraversalExpr, and variablesWalker.Enter only ever calls back for
// the latter.
//
// This is called directly, standalone, rather than folded into
// internal/lang's shared reference-gathering pipeline - the first attempt
// did exactly that (feeding the synthesized Source+Each traversal into
// ReferencesInExpr's own returned list) and it broke every ALREADY-WORKING
// splat over a count-expanded resource across the corpus:
// addrs.ParseRef's parseResourceRef falls through to a bare
// addrs.ResourceInstance{Key: NoKey} for a traversal with a non-index step
// after the resource name (exactly what a synthesized Source+".attr"
// traversal is, with no index step of its own), and internal/lang/eval.go's
// evalContext feeds EVERY reference in its refs argument to
// varBuilder.putValueBySubject for real value materialization, not only to
// StaticValidateReferences for classification. A NoKey reference to a
// count-expanded resource is exactly the shape a genuinely missing index
// produces, so real evaluation raised "Missing resource instance key" for
// every splat this touched, whether or not its Each attribute was actually
// covered - a regression discovered by running the full corpus-eks-basic
// estate, not by any unit test, because no existing fixture used a splat
// over a resource with any OTHER already-covered attribute read elsewhere
// in the same configuration.
//
// [staticScopeData.StaticValidateReferences] has no such side effect - it
// only ever returns diagnostics - so calling it here, directly, with a
// reference list built ONLY from splat Each traversals, changes nothing
// about how any existing reference is materialized into an
// *hcl.EvalContext. It is purely additive: diagnostics this call raises
// supplement whatever ordinary evaluation already found; it is never
// consulted for the value itself.
func (s staticScopeData) splatCoverageDiagnostics(ctx context.Context, expr hcl.Expression) tfdiags.Diagnostics {
	traversals := splatEachTraversals(expr)
	if len(traversals) == 0 {
		return nil
	}
	refs, _ := lang.References(addrs.ParseRef, traversals)
	if len(refs) == 0 {
		return nil
	}
	// self and source are never read by this package's own
	// StaticValidateReferences implementation (both parameters are
	// blank-identifier in its signature), so nil costs nothing here.
	return s.StaticValidateReferences(ctx, refs, nil, nil)
}

// splatEachTraversals finds every legacy `resource.*.attr` splat reachable
// from root and, for each one, returns a traversal combining the splat's
// Source (the resource being splatted over) with the attribute steps its
// "Each" expression applies to one element - the demand
// [hcl.Expression.Variables] never reports on its own. See
// [staticScopeData.splatCoverageDiagnostics] for why this exists and how
// its result is used (diagnostics only, never fed into value
// materialization).
//
// hclsyntax represents `SOURCE.*.attr` as a SplatExpr whose Each is a
// RelativeTraversalExpr evaluated per-element against an AnonSymbolExpr
// placeholder, not against SOURCE itself (hashicorp/hcl/v2/hclsyntax's
// expression.go). variablesWalker (that package's variables.go) only ever
// calls back for a *ScopeTraversalExpr, and a RelativeTraversalExpr rooted
// in a placeholder is never one - so Variables() sees the reference to
// SOURCE and stops there, structurally blind to the ".attr" the splat goes
// on to read from each element.
//
// This walks the real expression tree instead of trusting Variables(), so
// it sees the SplatExpr node directly and can read Each's own traversal off
// it.
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
