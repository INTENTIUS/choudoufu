// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// BeforeApply asserts the record store bucket's contract (GitHub issue
// #1339) at the last point before an apply changes anything.
//
// # When the contract is asserted, and why there
//
// Two places, and an ordinary plan is neither. The ruling on #1339 is that
// the assertions do not run on every plan, and the reasoning that picked
// these two instead:
//
//   - Here, on every apply. An apply is the only run that writes records, so
//     it is the run an unversioned bucket can hurt, and three configuration
//     reads are nothing beside the writes it is about to make. It is also
//     what catches drift: a bucket whose versioning somebody suspended last
//     week is refused by the next apply, before that apply's first write.
//   - On an estate's first contact with its store, whatever the command
//     (projection's assertBucketOnFirstContact), because that is the one run
//     a wrong bucket costs nothing to walk away from.
//
// What this gives up, stated rather than hidden: a plan against an estate
// whose bucket has drifted proceeds without a word, and the operator learns
// at apply time, after approving. The runnable project's verify (#1341) is
// the on-demand answer for anyone who wants it sooner.
//
// A refusal here leaves nothing behind: nothing has been applied, and no
// record has been written.
func (r *statelessRunner) BeforeApply(ctx context.Context) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if r.rawStore == nil || r.recordStoreCfg == nil {
		return diags
	}
	findings, ok, err := projection.BucketContractFindings(ctx, r.rawStore, r.recordStoreCfg, r.recordEstate)
	if !ok {
		return diags
	}
	if err != nil {
		return diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot check the record store bucket",
			fmt.Sprintf("Before applying, the record store bucket %q is checked for the settings its records depend on, and that check could not be made: %s. Nothing has been applied.", r.recordStoreCfg.Bucket, err),
		))
	}
	return diags.Append(bucketContractDiagnostics(r.recordStoreCfg.Bucket, findings))
}

// bucketContractDiagnostics turns failed findings into one error diagnostic
// each, so a bucket with two things wrong says both in one run.
func bucketContractDiagnostics(bucket string, findings []staterecord.BucketFinding) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, f := range findings {
		summary, detail := staterecord.BucketContractRefusal(bucket, f)
		if summary == "" {
			continue
		}
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, detail+"\n\nNothing has been applied."))
	}
	return diags
}
