// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/configs"
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
	diags = diags.Append(r.beforeApplyCluster(ctx))
	findings, ok, err := projection.BucketContractFindings(ctx, r.rawStore, r.recordStoreCfg, r.recordEstate)
	if !ok {
		return diags
	}
	if err != nil {
		return diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot check the record store bucket",
			fmt.Sprintf("Before applying, the record store bucket %q is checked for the settings its records depend on, and that check could not be made: %s. Nothing has been applied.", r.recordStoreCfg.Bucket, err),
		))
	}
	refused, waivedFailing := staterecord.SplitWaived(findings, r.recordStoreCfg.AllowInsecure)
	for _, f := range waivedFailing {
		// The every-run warning (bucketWaiverWarnings) is made from the
		// configuration alone and cannot know whether the waiver is hiding
		// anything. This run just read the bucket, so it can.
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning,
			fmt.Sprintf("The waived %s assertion would have refused this apply", f.Setting),
			fmt.Sprintf("Bucket %q: %s. The apply proceeds because allow_insecure names %q.", r.recordStoreCfg.Bucket, f.Found, f.Setting),
		))
	}
	return diags.Append(bucketContractDiagnostics(r.recordStoreCfg.Bucket, refused))
}

// beforeApplyCluster is the cluster contract's half of BeforeApply (GitHub
// issue #1393), on every apply for the reason the bucket's half runs on every
// apply: an apply is the only run that writes records, and six reads are
// nothing beside the writes it is about to make. It is also what catches
// drift - an estate boundary policy somebody uninstalled last week is refused
// by the next apply, before that apply's first write.
//
// All five of the store's verbs are required here. An apply writes records,
// so a plan-only identity is not what this run has.
func (r *statelessRunner) beforeApplyCluster(ctx context.Context) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	findings, ok, err := projection.ClusterContractFindings(ctx, r.rawStore, staterecord.KubernetesRecordVerbs)
	if !ok {
		return diags
	}
	namespace := projection.RecordNamespace(r.recordStoreCfg, r.recordEstate)
	if err != nil {
		return diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot check the record store cluster",
			fmt.Sprintf("Before applying, the cluster this estate keeps its records in is checked for the properties those records depend on, and that check could not be made: %s. Nothing has been applied.", err),
		))
	}
	refused, waivedFailing := staterecord.SplitWaivedCluster(findings, r.recordStoreCfg.AllowInsecure)
	for _, f := range waivedFailing {
		// The every-run warning (recordStoreWaiverWarnings) is made from the
		// configuration alone and cannot know whether the waiver is hiding
		// anything. This run just read the cluster, so it can.
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning,
			fmt.Sprintf("The waived %s assertion would have refused this apply", f.Setting),
			fmt.Sprintf("Namespace %q: %s. The apply proceeds because allow_insecure names %q.", namespace, f.Found, f.Setting),
		))
	}
	for _, f := range refused {
		summary, detail := staterecord.ClusterContractRefusal(namespace, f)
		if summary == "" {
			continue
		}
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, detail+"\n\nNothing has been applied."))
	}
	return diags
}

// bucketWaiverWarnings is one warning per waived assertion, made from the
// configuration alone so that it costs no request and lands on a plan as
// well as an apply. GitHub issue #1340: a waiver is loud on EVERY run, not
// only the one it was first set on. A waiver that goes quiet after the first
// apply is indistinguishable from a store that passes, and a flag nobody is
// reminded of is a flag nobody revisits.
//
// Both remote backends get one, with their own names and their own costs: the
// bucket's three settings (#1339) and the cluster's four (#1393).
func bucketWaiverWarnings(rs *configs.LiveRecordStore) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if rs == nil {
		return diags
	}
	for _, name := range rs.AllowInsecure {
		switch rs.Type {
		case "kubernetes":
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning,
				fmt.Sprintf("The record store cluster's %s assertion is waived", name),
				fmt.Sprintf("record_store \"kubernetes\" names %q in allow_insecure, so %s. This warning repeats on every run for as long as the waiver is configured.",
					name, staterecord.ClusterWaiverCost(staterecord.ClusterSetting(name))),
			))
		default:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning,
				fmt.Sprintf("The record store bucket's %s assertion is waived", name),
				fmt.Sprintf("record_store \"s3\" names %q in allow_insecure for bucket %q, so %s. This warning repeats on every run for as long as the waiver is configured.",
					name, rs.Bucket, staterecord.BucketWaiverCost(staterecord.BucketSetting(name))),
			))
		}
	}
	return diags
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
