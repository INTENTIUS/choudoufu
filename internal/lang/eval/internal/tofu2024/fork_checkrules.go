// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu2024

// choudoufu fork port (#2409): remove when upstream #4390 lands. The whole
// file is the checkrules port's evaluator half: resource preconditions and
// postconditions, and check blocks. See internal/engine/forkport/checkrules.

import (
	"context"
	"maps"
	"slices"
	"sync/atomic"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/checks"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/engine/forkport/checkrules"
	"github.com/intentius/choudoufu/internal/lang/eval/internal/configgraph"
	"github.com/intentius/choudoufu/internal/lang/exprs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// forkCheckRules evaluates rules in order and reports each failure as a
// diagnostic "<what> failed", as the default runtime's evalCheckRule does,
// with the severity checkrules.Report gives for the phase. checkBlock is
// true for check block assertions. It returns the dependency marks of the
// condition results, for the caller to carry onto what the rules guard,
// and whether any rule failed, reported or not. Each result is recorded
// (C3, #2410) as the rule of ruleType at its index of obj.
func forkCheckRules(ctx context.Context, rules []*configgraph.CheckRule, what string, checkBlock bool, obj addrs.Checkable, ruleType addrs.CheckRuleType) (_ cty.ValueMarks, _ tfdiags.Diagnostics, failed bool) {
	severity, report := checkrules.Report(ctx, checkBlock)
	var diags tfdiags.Diagnostics
	ruleMarks := make(cty.ValueMarks)
	for i, rule := range rules {
		status, moreMarks, moreDiags := rule.Check(ctx)
		maps.Copy(ruleMarks, moreMarks)
		diags = diags.Append(moreDiags)
		if checkrules.PhaseOf(ctx) == checkrules.Validate {
			// The default runtime's validate also evaluates each error
			// message, with unknown values, to report what is invalid in it.
			_, moreDiags := rule.ErrorMessageValuer.Value(ctx)
			diags = diags.Append(moreDiags)
		}
		var msg string
		switch status {
		case checks.StatusUnknown:
			// A condition unknown because what it reads failed to evaluate
			// is already reported by that failure.
			if _, evalError := moreMarks[exprs.EvalError]; checkBlock && !evalError && checkrules.WarnUnknown(ctx) {
				diags = diags.Append(&hcl.Diagnostic{
					Severity: hcl.DiagWarning,
					Summary:  what + " known after apply",
					Detail:   "The condition could not be evaluated at this time, a result will be known when this plan is applied.",
					Subject:  rule.ConditionRange().ToHCL().Ptr(),
				})
			}
		case checks.StatusFail:
			failed = true
			if !report {
				break
			}
			var moreDiags tfdiags.Diagnostics
			msg, moreDiags = rule.ErrorMessage(ctx)
			diags = diags.Append(moreDiags)
			if msg == "" {
				msg = "This check failed, but has an invalid error message as described in the other accompanying messages."
			}
			diags = diags.Append(&hcl.Diagnostic{
				Severity: severity,
				Summary:  what + " failed",
				Detail:   msg,
				Subject:  rule.ConditionRange().ToHCL().Ptr(),
			})
		}
		checkrules.Record(ctx, obj, ruleType, i, status, msg)
	}
	configgraph.RemoveNonDependencyMarks(ruleMarks)
	return ruleMarks, diags, failed
}

// forkGuardPreconditions wraps a resource instance's configuration valuer so
// that its preconditions are checked first, as the default runtime does
// before planning or applying the instance. A failure that is an error
// leaves the configuration unevaluated, and blocked then reports true, so
// that the instance's result is not asked of the planning or apply engine:
// the default runtime does not call the provider for it either.
func forkGuardPreconditions(config *configs.Resource, localScope exprs.Scope, addr addrs.AbsResourceInstance, inner exprs.Valuer) (_ exprs.Valuer, blocked func() bool) {
	if !checkrules.Enabled() || len(config.Preconditions) == 0 {
		return inner, func() bool { return false }
	}
	g := &forkPreconditionGuard{
		rules: slices.Collect(compileCheckRules(config.Preconditions, localScope)),
		addr:  addr,
		inner: inner,
	}
	return g, g.failed.Load
}

type forkPreconditionGuard struct {
	rules  []*configgraph.CheckRule
	addr   addrs.AbsResourceInstance
	inner  exprs.Valuer
	failed atomic.Bool
}

var _ exprs.Valuer = (*forkPreconditionGuard)(nil)

func (g *forkPreconditionGuard) StaticCheckTraversal(traversal hcl.Traversal) tfdiags.Diagnostics {
	return g.inner.StaticCheckTraversal(traversal)
}

func (g *forkPreconditionGuard) ValueSourceRange() *tfdiags.SourceRange {
	return g.inner.ValueSourceRange()
}

func (g *forkPreconditionGuard) Value(ctx context.Context) (cty.Value, tfdiags.Diagnostics) {
	ruleMarks, diags, failed := forkCheckRules(ctx, g.rules, "Resource precondition", false, g.addr, addrs.ResourcePrecondition)
	if diags.HasErrors() {
		g.failed.Store(true)
		return exprs.AsEvalError(cty.DynamicVal), diags
	}
	if failed && checkrules.PhaseOf(ctx) == checkrules.Validate {
		// Validation reports no condition failure, but the precondition
		// still guards the configuration, which the default runtime's
		// validate sees only with unknown repetition values.
		return cty.DynamicVal.WithMarks(ruleMarks), diags
	}
	v, moreDiags := g.inner.Value(ctx)
	diags = diags.Append(moreDiags)
	// What the preconditions refer to is a dependency of the instance, as
	// in the default runtime.
	if v != cty.NilVal && len(ruleMarks) != 0 {
		v = v.WithMarks(ruleMarks)
	}
	return v, diags
}

// forkPostconditions returns the check of a resource instance's
// postconditions against the result the planning or apply engine gave for
// it, with self bound to that result, or nil if there is nothing to check.
func forkPostconditions(config *configs.Resource, localScope exprs.Scope, addr addrs.AbsResourceInstance) func(context.Context, cty.Value) tfdiags.Diagnostics {
	if !checkrules.Enabled() || len(config.Postconditions) == 0 {
		return nil
	}
	var selfIdent, selfName string
	if addr.Resource.Key == nil {
		selfIdent, selfName = addr.Resource.Resource.Type, addr.Resource.Resource.Name
	}
	return func(ctx context.Context, result cty.Value) tfdiags.Diagnostics {
		if result == cty.NilVal || exprs.IsEvalError(result) {
			return nil // the failure that made it is already reported
		}
		scope := selfScope(localScope, configgraph.WithoutResourceInstanceDependency(result, addr), selfIdent, selfName)
		rules := slices.Collect(compileCheckRules(config.Postconditions, scope))
		_, diags, _ := forkCheckRules(ctx, rules, "Resource postcondition", false, addr, addrs.ResourcePostcondition)
		return diags
	}
}

// forkCheckBlock is one check block in one module instance. Its assertions
// are checked whenever the evaluator walks the module instance (at the end
// of a plan and of an apply), and a failure is a warning.
type forkCheckBlock struct {
	addr  addrs.AbsCheck
	rules []*configgraph.CheckRule
}

func forkCompileCheckBlocks(checkConfigs map[string]*configs.Check, scope exprs.Scope, moduleInstanceAddr addrs.ModuleInstance) []*forkCheckBlock {
	if !checkrules.Enabled() {
		return nil
	}
	var ret []*forkCheckBlock
	for _, name := range slices.Sorted(maps.Keys(checkConfigs)) {
		c := checkConfigs[name]
		ret = append(ret, &forkCheckBlock{
			addr:  c.Addr().Absolute(moduleInstanceAddr),
			rules: slices.Collect(compileCheckRules(c.Asserts, scope)),
		})
	}
	return ret
}

// CheckAll is called by the module instance's CheckAll walk.
func (c *forkCheckBlock) CheckAll(ctx context.Context) tfdiags.Diagnostics {
	_, diags, _ := forkCheckRules(ctx, c.rules, "Check block assertion", true, c.addr, addrs.CheckAssertion)
	return diags
}
