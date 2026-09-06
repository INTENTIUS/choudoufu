// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staticeval

import (
	"context"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// evaluate.go holds the two recover-wrapped evaluations. [Evaluate] is the
// plain one, over [configs.StaticEvaluator.Evaluate]; [Scoped] is the one
// that binds a resource instance's own repetition data and a
// for-comprehension's own loop variables before evaluating, which
// StaticEvaluator.Evaluate cannot do because its scope has no notion of
// either.
//
// Both hand the recovered panic value back rather than rendering it: the
// three call sites word the refusal differently (lint drops it entirely,
// dataread prefixes it with the block's label, identity points at
// live/LIMITATIONS.md), and a shared message would have had to be the
// vaguest of the three.
//
// The recover is not defensive padding. The static scope's data source
// panics rather than erroring for the reference classes it does not serve,
// and the traversal pre-filter ([Allowed]) cannot see every one of them
// coming: a var.* reference inside a module reached through a for_each'd
// ancestor call (issue #59 phase 3) can resolve, several layers down inside
// [configs.StaticEvaluator]'s own variable-resolution machinery, to an
// expression that itself references the ancestor's own each.key or
// each.value. That resolution runs through cfg.Module.StaticEvaluator -
// built once by internal/configs when the module tree is loaded, entirely
// independent of any per-instance
// [configs.StaticEvaluator.WithRepetitionData] dup - so it never receives
// repetition data at all and panics ("Not Available in Static Context")
// rather than erroring. Nothing on the live path evaluates such an
// expression on purpose, but nothing stops a resource argument from
// referencing one either, and a crash here would take the whole run down
// over one component that was always going to be refused.

// Evaluate runs expr through eval and returns what it produced, with a
// panic recovered into recovered rather than allowed to kill the run.
//
// recovered is non-nil exactly when the evaluation panicked, and then val
// is [cty.NilVal] and diags is empty. Otherwise diags is whatever the
// evaluator produced, and val is [cty.NilVal] whenever those diagnostics
// carry an error - no caller has ever used a value the evaluator reported
// an error for, and returning one would invite it.
func Evaluate(ctx context.Context, eval *configs.StaticEvaluator, expr hcl.Expression, ident configs.StaticIdentifier) (val cty.Value, diags hcl.Diagnostics, recovered any) {
	defer func() {
		if rec := recover(); rec != nil {
			val, diags, recovered = cty.NilVal, nil, rec
		}
	}()

	v, hclDiags := eval.Evaluate(ctx, expr, ident)
	if hclDiags.HasErrors() {
		return cty.NilVal, hclDiags, nil
	}
	return v, hclDiags, nil
}

// EvaluateOK is [Evaluate] reduced to "did it produce a value", for a
// caller that reports nothing about why it did not. subject names the
// position being evaluated ("for_each", "count") in the identifier the
// evaluator is handed; the diagnostics that would render it are dropped, so
// it matters only in a debugger.
//
// Dropping the diagnostics is deliberate rather than lazy: an expression a
// lint pass cannot evaluate is not that pass's finding to report - identity
// resolution evaluates the same expression in a richer scope and reports
// what it finds there.
func EvaluateOK(ctx context.Context, eval *configs.StaticEvaluator, expr hcl.Expression, subject string) (cty.Value, bool) {
	ident := configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   subject,
		DeclRange: expr.Range(),
	}
	val, diags, recovered := Evaluate(ctx, eval, expr, ident)
	if recovered != nil || diags.HasErrors() {
		return cty.NilVal, false
	}
	return val, true
}

// Scoped is the evaluation with a per-instance scope: rep is the
// each.key/each.value/count.index the resource instance whose arguments are
// being evaluated actually has, and vars binds a for-comprehension's own
// loop variables, which the static evaluator has no notion of and would
// either panic on or misreport as undeclared references.
//
// It builds the [hcl.EvalContext] itself rather than calling
// [configs.StaticEvaluator.Evaluate], because that method offers no way to
// put vars into a child scope of the context it builds.
//
// Diagnostics come back rather than being recorded: one caller records them
// (an argument that will not evaluate is a resolution failure) and another
// discards them (an argument it cannot read is still an argument the
// configuration sets), and that is the caller's decision to make. recovered
// is non-nil exactly when the evaluation panicked; see this file's own
// comment for what panics and why.
func Scoped(ctx context.Context, eval *configs.StaticEvaluator, expr hcl.Expression, ident configs.StaticIdentifier, rep instances.RepetitionData, vars map[string]cty.Value) (val cty.Value, diags tfdiags.Diagnostics, recovered any) {
	defer func() {
		if rec := recover(); rec != nil {
			val, diags, recovered = cty.NilVal, nil, rec
		}
	}()

	var travs []hcl.Traversal
	for _, trav := range expr.Variables() {
		if _, bound := vars[trav.RootName()]; bound {
			// A for-comprehension's own loop variable, bound by the caller
			// - never "each" or "count", which are answered through rep and
			// [configs.StaticEvaluator.WithRepetitionData] below instead, at
			// every depth a reference is resolved at, not only this top
			// level. This is a local binding the static evaluator has no
			// notion of, and would either panic on or misreport as an
			// undeclared reference.
			continue
		}
		travs = append(travs, trav)
	}

	refs, refDiags := lang.References(addrs.ParseRef, travs)
	if refDiags.HasErrors() {
		return cty.NilVal, diags.Append(refDiags), nil
	}

	// rep is exactly the each.key/each.value/count.index this resource
	// instance's own arguments already see, built once from the same
	// expansion that decided the instance exists - never re-derived here, so
	// a local value reached through this expression sees the identical
	// values, not a recomputation of them. See
	// [configs.StaticEvaluator.WithRepetitionData].
	scoped := eval.WithRepetitionData(rep)
	hclCtx, ctxDiags := scoped.EvalContext(ctx, ident, refs)
	if ctxDiags.HasErrors() {
		return cty.NilVal, diags.Append(ctxDiags), nil
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
		return cty.NilVal, diags.Append(valDiags), nil
	}
	return val, diags, nil
}
