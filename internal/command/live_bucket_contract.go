// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// recordWriteRun is what the contract's diagnostics call the run they are
// about. The assertion is one function wherever it runs; what changes with
// the command is the noun for the run and what a refusal can truthfully say
// this run has not done.
type recordWriteRun struct {
	// noun goes into "would have refused this <noun>".
	noun string
	// nothing closes a refusal: what has not happened.
	nothing string
}

var (
	contractRunApply  = recordWriteRun{noun: "apply", nothing: "Nothing has been applied."}
	contractRunMove   = recordWriteRun{noun: "rename", nothing: "No marker has been rewritten, and no record has been moved."}
	contractRunImport = recordWriteRun{noun: "migration", nothing: "No marker has been stamped, and no record has been written."}
)

// assertRecordStoreContract asserts the record store's contract - the
// bucket's (GitHub issue #1339), the cluster's (#1393), or none for a local
// store - over a store this run has already opened. It is the one path every
// run that writes records goes through, so that the waivers, the every-run
// warnings and the refusals cannot differ between two such commands.
//
// # Where the contract is asserted, and why there
//
// Three places, and an ordinary plan is none of them. The ruling on #1339 is
// that the assertions do not run on every plan, and the reasoning that
// picked these:
//
//   - On every apply, at the last point before it changes anything
//     ([statelessRunner.BeforeApply]). An apply writes records, so it is a
//     run a store with versioning off or no estate boundary can hurt, and a
//     handful of configuration reads are nothing beside the writes it is
//     about to make. It is also what catches drift: a bucket whose
//     versioning somebody suspended last week, or an admission policy
//     somebody uninstalled, is refused by the next apply, before that
//     apply's first write.
//   - Before `live-mv` rewrites a marker, and before `live-import -approve`
//     stamps one. Both write records too (#1448:
//     [projection.RecordStore.MoveRecord] and internal/live/liveimport's
//     stamp), which the first version of this comment denied. A read-only
//     invocation of either - `live-mv -dry-run`, or a `live-import` with no
//     -approve - writes nothing and is not asked to satisfy a contract it
//     never leans on.
//   - On an estate's first contact with its store, whatever the command
//     (projection's assertStoreOnFirstContact), because that is the one run
//     a wrong store costs nothing to walk away from.
//
// What this gives up, stated rather than hidden: a plan against an estate
// whose store has drifted proceeds without a word, and the operator learns
// at apply time, after approving. `live-bucket` (#1341) and `live-cluster`
// are the on-demand answer for anyone who wants it sooner.
//
// Every run that comes here writes records, so this asks for everything a
// writing run needs; a plan-only identity is not what such a run has.
//
// A refusal here leaves nothing behind: run.nothing says what, and no record
// has been written.
func assertRecordStoreContract(ctx context.Context, store staterecord.Store, rs *configs.LiveRecordStore, estate string, run recordWriteRun) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if store == nil || rs == nil {
		return diags
	}
	findings, checker, err := projection.ContractFindings(ctx, store, rs, estate)
	if checker == nil {
		return diags
	}
	if err != nil {
		summary, detail := checker.ContractCheckFailed(err)
		return diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, detail))
	}
	label, subject := checker.ContractSubject()
	refused, warned, waivedFailing := staterecord.SplitWaived(findings, rs.AllowInsecure)
	for _, f := range warned {
		// A concern, not a refusal: see staterecord.Warned and
		// staterecord.NotChecked. It lands here rather than at first contact
		// because it has to be said on EVERY run that writes, not only the
		// estate's first.
		summary, detail := checker.ContractRefusal(f)
		if summary == "" {
			continue
		}
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, summary, detail))
	}
	for _, f := range waivedFailing {
		// The every-run warning (bucketWaiverWarnings) is made from the
		// configuration alone and cannot know whether the waiver is hiding
		// anything. This run just read the store, so it can.
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning,
			fmt.Sprintf("The waived %s assertion would have refused this %s", f.Setting, run.noun),
			fmt.Sprintf("%s %q: %s. The %s proceeds because allow_insecure names %q.", label, subject, f.Found, run.noun, f.Setting),
		))
	}
	return diags.Append(contractDiagnostics(checker, refused, run))
}

// BeforeApply is the apply's call into [assertRecordStoreContract], at the
// last point before the apply changes anything.
func (r *statelessRunner) BeforeApply(ctx context.Context) tfdiags.Diagnostics {
	return assertRecordStoreContract(ctx, r.rawStore, r.recordStoreCfg, r.recordEstate, contractRunApply)
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
					name, staterecord.ClusterWaiverCost(staterecord.Setting(name))),
			))
		default:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning,
				fmt.Sprintf("The record store bucket's %s assertion is waived", name),
				fmt.Sprintf("record_store \"s3\" names %q in allow_insecure for bucket %q, so %s. This warning repeats on every run for as long as the waiver is configured.",
					name, rs.Bucket, staterecord.BucketWaiverCost(staterecord.Setting(name))),
			))
		}
	}
	return diags
}

// contractDiagnostics turns failed findings into one error diagnostic each,
// so a store with two things wrong says both in one run. Each closes with
// what this run has not done, which is the command's to say: an apply has
// applied nothing, a refused rename has rewritten no marker.
func contractDiagnostics(checker staterecord.ContractChecker, findings []staterecord.Finding, run recordWriteRun) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, f := range findings {
		summary, detail := checker.ContractRefusal(f)
		if summary == "" {
			continue
		}
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, detail+"\n\n"+run.nothing))
	}
	return diags
}

// liveWaiverLine is one configured allow_insecure name, and is shared by
// live-bucket and live-cluster: the waiver is one idea with one shape
// whichever store it is written in, and the two reports print it with the
// same two sentences and their own noun. The verdicts above are not shared,
// because "unreadable" and "not_checked" are different answers.
type liveWaiverLine struct {
	Setting string `json:"setting"`
	// Hiding is true when the store does fail the waived assertion, so a
	// run proceeds past something this report calls a failure.
	Hiding bool `json:"hiding"`
}

// liveWaiverLines is what the configuration's allow_insecure names, in the
// order it names them, each with whether it is hiding a failure. rs is nil
// when the store was named on the command line rather than read from a
// configuration, and then there is no waiver to report.
func liveWaiverLines(rs *configs.LiveRecordStore, failing map[staterecord.Setting]bool) []liveWaiverLine {
	lines := []liveWaiverLine{}
	if rs == nil {
		return lines
	}
	for _, name := range rs.AllowInsecure {
		lines = append(lines, liveWaiverLine{Setting: name, Hiding: failing[staterecord.Setting(name)]})
	}
	return lines
}

// renderLiveWaiverLines prints them, naming the store with noun ("bucket",
// "cluster").
func renderLiveWaiverLines(b *strings.Builder, lines []liveWaiverLine, noun string) {
	for _, w := range lines {
		if w.Hiding {
			fmt.Fprintf(b, "  waiver: allow_insecure names %q, and the %s DOES fail it. A plan or apply here proceeds past the failure above.\n", w.Setting, noun)
		} else {
			fmt.Fprintf(b, "  waiver: allow_insecure names %q. The %s passes it today, so the waiver is hiding nothing and can be removed.\n", w.Setting, noun)
		}
	}
}
