// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configgraph

// choudoufu fork port (#2410): remove when upstream #4390 lands. The
// checkrules port's recording of check results (C3) for what upstream's
// evaluator checks itself: output preconditions, variable validations, and
// which instances a resource has. Output precondition failures are also
// reported as the port reports resource conditions, as the default runtime
// does: "Module output value precondition failed", a warning in a
// refresh-only plan, nothing in a destroy. See
// internal/engine/forkport/checkrules.

import (
	"context"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/checks"
	"github.com/intentius/choudoufu/internal/engine/forkport/checkrules"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// forkRecordResults wraps a [CheckAllRules] result handler so that each
// rule's result is also recorded as the index'th rule of ruleType of obj.
func forkRecordResults(ctx context.Context, obj addrs.Checkable, ruleType addrs.CheckRuleType, handle func(tfdiags.SourceRange, checks.Status, string) tfdiags.Diagnostics) func(tfdiags.SourceRange, checks.Status, string) tfdiags.Diagnostics {
	index := 0
	return func(ruleDeclRange tfdiags.SourceRange, status checks.Status, errMsg string) tfdiags.Diagnostics {
		checkrules.Record(ctx, obj, ruleType, index, status, errMsg)
		index++
		return handle(ruleDeclRange, status, errMsg)
	}
}

// forkOutputPreconditions is upstream's result handler for an output
// value's preconditions with the port off, and with it on the port's,
// which records each result and reports a failure as the default runtime
// does.
func forkOutputPreconditions(ctx context.Context, o *OutputValue, upstream func(tfdiags.SourceRange, checks.Status, string) tfdiags.Diagnostics) func(tfdiags.SourceRange, checks.Status, string) tfdiags.Diagnostics {
	if !checkrules.Enabled() {
		return upstream
	}
	index := 0
	return func(_ tfdiags.SourceRange, status checks.Status, errMsg string) tfdiags.Diagnostics {
		rule := o.Preconditions[index]
		checkrules.Record(ctx, o.Addr, addrs.OutputPrecondition, index, status, errMsg)
		index++
		severity, report := checkrules.Report(ctx, false)
		if status != checks.StatusFail || !report {
			return nil
		}
		if errMsg == "" {
			errMsg = "This check failed, but has an invalid error message as described in the other accompanying messages."
		}
		var diags tfdiags.Diagnostics
		return diags.Append(&hcl.Diagnostic{
			Severity: severity,
			Summary:  addrs.OutputPrecondition.Description() + " failed",
			Detail:   errMsg,
			Subject:  rule.ConditionRange().ToHCL().Ptr(),
		})
	}
}

// forkSensitiveErrorMessage is the default runtime's warning for an error
// message derived from a sensitive value.
func forkSensitiveErrorMessage(r *CheckRule) *hcl.Diagnostic {
	return &hcl.Diagnostic{
		Severity: hcl.DiagWarning,
		Summary:  "Error message refers to sensitive values",
		Detail: `The error expression used to explain this condition refers to sensitive values, so OpenTofu will not display the resulting message.

You can correct this by removing references to sensitive values, or by carefully using the nonsensitive() function if the expression will not reveal the sensitive data.`,
		Subject: MaybeHCLSourceRange(r.ErrorMessageValuer.ValueSourceRange()),
	}
}

// forkRecordResourceInstances records which instances a resource has.
func forkRecordResourceInstances(ctx context.Context, r *Resource, insts *compiledInstances[*ResourceInstance]) {
	if insts == nil {
		return
	}
	placeholder := r.Addr.IsPlaceholder()
	objs := make([]addrs.Checkable, 0, len(insts.Instances))
	for _, inst := range insts.Instances {
		placeholder = placeholder || inst.Addr.IsPlaceholder()
		objs = append(objs, inst.Addr)
	}
	checkrules.RecordObjects(ctx, r.Addr.Config(), objs, placeholder)
}
