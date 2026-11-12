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
	"fmt"
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
// The check returns the value the instance's dependents see: the result,
// or, when a postcondition failed while planning, an unknown value marked
// as an evaluation error (#2418). The default runtime's plan walk stops at a
// failed condition, so nothing that refers to the instance reports errors
// of its own; in a refresh-only plan, where the failure is a warning, it
// never evaluates a managed resource's configuration at all.
func forkPostconditions(config *configs.Resource, localScope exprs.Scope, addr addrs.AbsResourceInstance) func(context.Context, cty.Value) (cty.Value, tfdiags.Diagnostics) {
	if !checkrules.Enabled() || len(config.Postconditions) == 0 {
		return nil
	}
	var selfIdent, selfName string
	if addr.Resource.Key == nil {
		selfIdent, selfName = addr.Resource.Resource.Type, addr.Resource.Resource.Name
	}
	return func(ctx context.Context, result cty.Value) (cty.Value, tfdiags.Diagnostics) {
		if result == cty.NilVal || exprs.IsEvalError(result) {
			return result, nil // the failure that made it is already reported
		}
		scope := selfScope(localScope, configgraph.WithoutResourceInstanceDependency(result, addr), selfIdent, selfName)
		rules := slices.Collect(compileCheckRules(config.Postconditions, scope))
		_, diags, failed := forkCheckRules(ctx, rules, "Resource postcondition", false, addr, addrs.ResourcePostcondition)
		if phase := checkrules.PhaseOf(ctx); failed && (phase == checkrules.Plan || phase == checkrules.RefreshOnly) {
			_, resultMarks := result.Unmark()
			return exprs.AsEvalError(cty.UnknownVal(result.Type()).WithMarks(resultMarks)), diags
		}
		return result, diags
	}
}

// forkCheckBlock is one check block in one module instance. Its assertions
// are checked whenever the evaluator walks the module instance (at the end
// of a plan and of an apply), and a failure is a warning.
type forkCheckBlock struct {
	addr  addrs.AbsCheck
	rules []*configgraph.CheckRule

	// data and scope are #2418's: the check block's scoped data resource,
	// if it has one, and the scope that finds it.
	data  *addrs.Resource
	scope *moduleInstanceScope
}

// forkCompileCheckBlocks compiles the check blocks of a module instance, and
// returns with them the scoped data resources each declares (#2418). Each
// block's assertions are evaluated in a scope of their own, the only one
// that can refer to the block's scoped data resource.
func forkCompileCheckBlocks(checkConfigs map[string]*configs.Check, scope *moduleInstanceScope, moduleInstanceAddr addrs.ModuleInstance) ([]*forkCheckBlock, map[addrs.Resource]addrs.Check) {
	if !checkrules.Enabled() {
		return nil, nil
	}
	var ret []*forkCheckBlock
	scoped := make(map[addrs.Resource]addrs.Check)
	for _, name := range slices.Sorted(maps.Keys(checkConfigs)) {
		c := checkConfigs[name]
		checkAddr := c.Addr()
		checkScope := *scope
		checkScope.forkCheck = &checkAddr
		block := &forkCheckBlock{
			addr:  checkAddr.Absolute(moduleInstanceAddr),
			rules: slices.Collect(compileCheckRules(c.Asserts, &checkScope)),
			scope: &checkScope,
		}
		if c.DataResource != nil {
			addr := c.DataResource.Addr()
			block.data = &addr
			scoped[addr] = checkAddr
		}
		ret = append(ret, block)
	}
	return ret, scoped
}

// CheckAll is called by the module instance's CheckAll walk.
//
// As in the default runtime, a check block's scoped data resource is read
// before its assertions, and a read that failed is the check's result
// (#2418): its rule of type CheckDataResource fails with the read's error,
// and the assertions are not evaluated. Otherwise that rule passes.
func (c *forkCheckBlock) CheckAll(ctx context.Context) tfdiags.Diagnostics {
	if c.data != nil {
		if msg, failed := c.awaitData(ctx); failed {
			checkrules.Record(ctx, c.addr, addrs.CheckDataResource, 0, checks.StatusFail, msg)
			return nil
		}
		checkrules.Record(ctx, c.addr, addrs.CheckDataResource, 0, checks.StatusPass, "")
	}
	ruleMarks, diags, _ := forkCheckRules(ctx, c.rules, "Check block assertion", true, c.addr, addrs.CheckAssertion)
	// What the assertions read is checked again during the apply.
	checkrules.RecordCheckDependencies(ctx, slices.Collect(configgraph.ResourceInstanceAddrs(
		configgraph.ContributingResourceInstances(cty.True.WithMarks(ruleMarks)),
	))...)
	return diags
}

// awaitData waits for every instance of the check block's scoped data
// resource to be read, or its read deferred, and returns the failure of the
// first whose read failed.
func (c *forkCheckBlock) awaitData(ctx context.Context) (string, bool) {
	r, ok := c.scope.inst.resourceNodes[*c.data]
	if !ok {
		return "", false
	}
	insts := r.Instances(ctx)
	for _, key := range slices.SortedFunc(maps.Keys(insts), func(a, b addrs.InstanceKey) int {
		switch {
		case addrs.InstanceKeyLess(a, b):
			return -1
		case addrs.InstanceKeyLess(b, a):
			return 1
		}
		return 0
	}) {
		inst := insts[key]
		// Its diagnostics are the resource's own CheckAll's to report.
		_, _ = inst.Value(ctx)
		if msg, failed := checkrules.CheckDataFailure(ctx, inst.Addr); failed {
			return msg, true
		}
	}
	return "", false
}

// forkRefuseScopedResource refuses a reference to a check block's scoped
// data resource from anywhere but that check block's assertions, as the
// default runtime's staticValidateResourceReference does (#2418). It
// returns nil for a reference it allows, and otherwise what to resolve the
// reference to: a valuer that reports the refusal from the reference's
// static check, once, and evaluates to an error placeholder.
func (m *moduleInstanceScope) forkRefuseScopedResource(addr addrs.Resource, rng tfdiags.SourceRange) exprs.Valuer {
	check, scoped := m.inst.forkScopedResources[addr]
	if !scoped || (m.forkCheck != nil && m.forkCheck.Equal(check)) {
		return nil
	}
	var diags tfdiags.Diagnostics
	diags = diags.Append(&hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  "Reference to scoped resource",
		Detail:   fmt.Sprintf("The referenced data resource %q %q is not available from this context.", addr.Type, addr.Name),
		Subject:  rng.ToHCL().Ptr(),
	})
	return forkRefusedReference{diags: diags, rng: rng}
}

type forkRefusedReference struct {
	diags tfdiags.Diagnostics
	rng   tfdiags.SourceRange
}

var _ exprs.Valuer = forkRefusedReference{}

func (r forkRefusedReference) StaticCheckTraversal(hcl.Traversal) tfdiags.Diagnostics {
	return r.diags
}

func (r forkRefusedReference) Value(context.Context) (cty.Value, tfdiags.Diagnostics) {
	return exprs.AsEvalError(cty.DynamicVal), r.diags
}

func (r forkRefusedReference) ValueSourceRange() *tfdiags.SourceRange {
	return &r.rng
}
