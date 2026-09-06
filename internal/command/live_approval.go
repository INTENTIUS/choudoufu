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
	"github.com/intentius/choudoufu/internal/configs/configload"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/live/approval"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plans/planfile"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #878, ruled 2026-09-05: stock-form "plan -out=<file>" and
// "apply <planfile>" are admitted under live resource markers, with apply
// re-planning live and refusing when its fresh plan differs from the file's.
//
// The pipeline shape this serves is the one CI has always run - plan on the
// pull request, a human approves, apply exactly what was approved - and the
// property it adds is that "what was approved" and "what ran" are the same
// thing or the run refuses. What it does NOT add is a saved plan that apply
// trusts: the file is never prior state, never consulted for ownership, and
// never a substitute for the live read. HANDOFF.md's foundation holds
// unchanged - live wins, and a record is not permission.

// ExitApprovalRefused is the exit status "apply <planfile>" uses when the
// approved plan and this run's fresh plan of the live system disagree.
//
// It is deliberately neither of the two codes a pipeline already reads on
// this path. 1 is every ordinary failure, and routing a failed provider call
// back to a human reviewer would be wrong. 2 is "-detailed-exitcode"'s "there
// are changes", which the plan half of the very same pipeline returns. 3 is
// the lowest code with no meaning on the plan/apply pair, so a pipeline can
// read 0 as applied, 3 as "send it back to review", and anything else as
// broken. ("choudoufu fmt -check" also uses 3, in a different command's
// namespace; nothing routes an apply's exit code through fmt's meanings.)
const ExitApprovalRefused = 3

// The two refusals this file can raise. Both are hard: there is no
// warn-and-proceed mode, because an approval artifact that warns and applies
// anyway is the artifact lying about what it gates.
const (
	summaryApprovalMismatch    = "The approved plan no longer matches the live system"
	summaryApprovalWrongEstate = "The approved plan belongs to a different estate"
)

// approvedPlan is what an apply keeps from a saved plan file under live
// markers: the change set that was approved, and nothing else. The file's
// prior state, its config snapshot and its backend record are read to answer
// questions about the file and then dropped - the run builds all three for
// itself, from the live system and the working directory.
type approvedPlan struct {
	// Path is the file, for the refusal to name.
	Path string

	// Estate is the estate the live block in the file's config snapshot
	// names, or "" when the snapshot carries no live block at all.
	Estate string

	// Rows is the approved change set.
	Rows []approval.Row
}

// readApprovedPlan opens a saved plan file for its approval content only.
//
// It never returns a *planfile.WrappedPlanFile to the caller, and the caller
// never hands one to the operation: a live-markers apply given a plan file
// runs the ordinary live pipeline, with op.PlanFile nil, so that discovery,
// projection and stamping all happen exactly as they do for a bare "apply".
func (c *ApplyCommand) readApprovedPlan(ctx context.Context, path string, live *configs.Live, enc encryption.Encryption) (*approvedPlan, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	wrapped, loadDiags := c.LoadPlanFile(path, enc)
	diags = diags.Append(loadDiags)
	if loadDiags.HasErrors() {
		return nil, diags
	}
	reader, ok := wrapped.Local()
	if !ok {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"A saved cloud plan cannot be an approval artifact",
			fmt.Sprintf("%q is a saved cloud plan, which records a run on a remote backend. A live-markers estate has no remote run to attach to. Produce the artifact with \"choudoufu plan -out\" in this directory.", path),
		))
		return nil, diags
	}

	plan, err := reader.ReadPlan()
	if err != nil {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid plan file",
			fmt.Sprintf("Failed to read the plan from %q: %s.", path, err),
		))
		return nil, diags
	}
	priorFile, err := reader.ReadStateFile()
	if err != nil {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid plan file",
			fmt.Sprintf("Failed to read the prior state snapshot from %q: %s.", path, err),
		))
		return nil, diags
	}
	if priorFile != nil {
		// ChangeSet reads the prior state only to name the live object each
		// change was computed against. This run does not adopt it, and
		// nothing downstream ever sees it.
		plan.PriorState = priorFile.State
	}

	estate, estateDiags := c.planFileEstate(ctx, path, reader)
	diags = diags.Append(estateDiags)
	if estateDiags.HasErrors() {
		return nil, diags
	}

	declared := ""
	if live != nil {
		declared = live.Estate
	}
	if estate != declared {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			summaryApprovalWrongEstate,
			approvalWrongEstateDetail(path, estate, declared),
		))
		return nil, diags
	}

	return &approvedPlan{Path: path, Estate: estate, Rows: approval.ChangeSet(plan)}, diags
}

// planFileEstate reads the estate named by the live block in the plan file's
// own configuration snapshot.
//
// The snapshot is the only thing in the file that says which estate the
// approval was about. Reading it is what makes "this file was made for
// another estate" a named refusal instead of a change set that happens to
// disagree everywhere, which is the same difference as a lock file's
// "created somewhere else" check.
func (c *ApplyCommand) planFileEstate(ctx context.Context, path string, reader *planfile.Reader) (string, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	snap, err := reader.ReadConfigSnapshot()
	if err != nil {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid plan file",
			fmt.Sprintf("Failed to read the configuration snapshot from %q: %s.", path, err),
		))
		return "", diags
	}
	root, ok := snap.Modules[""]
	if !ok {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid plan file",
			fmt.Sprintf("%q carries no root module in its configuration snapshot, so nothing in it says which estate it was approved for.", path),
		))
		return "", diags
	}

	call, callDiags := c.rootModuleCall(ctx, ".")
	diags = diags.Append(callDiags)
	if callDiags.HasErrors() {
		return "", diags
	}

	// SelectiveLoadBackend is the same narrow load statelessSettings uses
	// for the working directory: enough of the file to see the live block,
	// and none of the resource bodies.
	mod, hclDiags := configload.NewLoaderFromSnapshot(snap).LoadConfigDirSelective(root.Dir, call, configs.SelectiveLoadBackend)
	if hclDiags.HasErrors() {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid plan file",
			fmt.Sprintf("The configuration snapshot in %q could not be read: %s.", path, hclDiags.Error()),
		))
		return "", diags
	}
	if mod == nil || mod.Live == nil {
		return "", diags
	}
	return mod.Live.Estate, diags
}

// approvalGuard is the plan guard a live-markers "apply <planfile>" installs.
//
// It runs at the one moment both plans exist: the fresh plan has been built
// from the live system and rendered in full, and nothing has been applied.
// So an operator refused here has already read the diff the refusal is
// about, and a pipeline capturing the output has the fresh plan to attach to
// the review it is sending the change back to.
//
// refused is set when the guard refuses, so the command can exit with
// [ExitApprovalRefused] rather than the 1 an ordinary failed operation
// returns. A pointer rather than a returned value because the guard is
// called from inside the backend operation, several frames down.
func approvalGuard(approved *approvedPlan, refused *bool) func(*plans.Plan, *tofu.Schemas) tfdiags.Diagnostics {
	return func(plan *plans.Plan, _ *tofu.Schemas) tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if approved == nil || plan == nil {
			return diags
		}
		diff := approval.Compare(approved.Rows, approval.ChangeSet(plan))
		if diff.Empty() {
			return diags
		}
		*refused = true
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			summaryApprovalMismatch,
			approvalMismatchDetail(approved.Path, diff),
		))
	}
}

// approvalMismatchDetail is the paragraph the mismatch refusal carries.
//
// A standalone function for [unmigrateRefusalDetail]'s reason: the wording is
// the product here, so it has to be testable without a plan, a backend or a
// provider.
func approvalMismatchDetail(path string, diff approval.Difference) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		"This apply read the live system and planned against what it found, the way every live-markers run does. The result is not the plan %q describes, so the approval that file carries does not cover what this apply would do, and it is refused before anything changes.\n\n",
		path)
	if len(diff.Extra) > 0 {
		b.WriteString("This apply would do, and the approved plan does not include:\n")
		b.WriteString(approvalRowList(diff.Extra))
		b.WriteString("\n")
	}
	if len(diff.Missing) > 0 {
		b.WriteString("The approved plan includes, and this apply would not do:\n")
		b.WriteString(approvalRowList(diff.Missing))
		b.WriteString("\n")
	}
	b.WriteString(
		"Three things produce this. The live system moved between the plan and the apply. Another apply landed in between, so what that file approved has already happened. Or the configuration changed since the file was written.\n\n")
	fmt.Fprintf(&b,
		"The plan above is what the live system asks for now. Review it, and apply the artifact it produced:\n  choudoufu plan -out=%s\n  choudoufu apply %s\n\nExit status %d says exactly this: send it back to review. Nothing was applied.",
		path, path, ExitApprovalRefused)
	return b.String()
}

// approvalRowList renders rows, capped, for the same reason
// [affectedList] caps: the plan printed above already showed every one of
// them in full, and a refusal that reprints a hundred rows buries its own
// last paragraph.
func approvalRowList(rows []approval.Row) string {
	const limit = 10
	shown := rows
	if len(shown) > limit {
		shown = shown[:limit]
	}
	var b strings.Builder
	for _, r := range shown {
		fmt.Fprintf(&b, "  %s\n", r.String())
	}
	if len(rows) > len(shown) {
		fmt.Fprintf(&b, "  ... and %d more\n", len(rows)-len(shown))
	}
	return b.String()
}

// approvalWrongEstateDetail is the paragraph the wrong-estate refusal
// carries. fileEstate and declared are empty when that side has no live
// block at all, which is a different mistake and says so.
func approvalWrongEstateDetail(path, fileEstate, declared string) string {
	name := func(s string) string {
		if s == "" {
			return "no live block, so it is a state-backed configuration"
		}
		return fmt.Sprintf("estate %q", s)
	}
	return fmt.Sprintf(
		"%q was produced from a configuration with %s. This directory's configuration has %s.\n\n"+
			"An estate is the unit of ownership here, so an approval for one estate says nothing about another: the markers, the records and the live objects are all different. Produce the artifact from this directory:\n  choudoufu plan -out=%s\n\nNothing was applied.",
		path, name(fileEstate), name(declared), path)
}

// diagsHaveSummary reports whether any diagnostic carries the given summary.
//
// The exit status is part of the contract a pipeline reads, so it is decided
// from the refusal that was actually raised rather than from a second
// boolean somebody has to remember to set beside it.
func diagsHaveSummary(diags tfdiags.Diagnostics, summary string) bool {
	for _, d := range diags {
		if d.Description().Summary == summary {
			return true
		}
	}
	return false
}
