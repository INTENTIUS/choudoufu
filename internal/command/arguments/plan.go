// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"github.com/intentius/choudoufu/internal/collections"
	"github.com/intentius/choudoufu/internal/linting"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Plan represents the command-line arguments for the plan command.
type Plan struct {
	// State, Operation, and Vars are the common extended flags
	State     *State
	Operation *Operation
	Vars      *Vars

	// DetailedExitCode enables different exit codes for error, success with
	// changes, and success with no changes.
	DetailedExitCode bool

	// OutPath contains an optional path to store the plan file
	OutPath string

	// GenerateConfigPath tells OpenTofu that config should be generated for
	// unmatched import target paths and which path the generated file should
	// be written to.
	GenerateConfigPath string

	// View represents the global view options
	View *View

	// Verbose asks a command to print detail it would otherwise summarize.
	// Live resource markers is the first consumer: the "Not swept for
	// removal" section's full type-by-type breakdown, collapsed to a
	// one-line count by default (GitHub issue #78, "First plan drowns a
	// small estate in the not-swept type list"). It lives on Plan itself,
	// not added by [BindLivePlan] the way -estate is (see that
	// function's own comment), because -verbose does nothing to a stock
	// plan rather than naming a live-only concept, and because it also
	// needs to reach "choudoufu apply" against a live block
	// (arguments.Apply.Verbose), where -estate has no equivalent need
	// (arguments.Apply has none either).
	Verbose bool

	// AdoptionOnly asks for GitHub issue #587's adoption-only view: the plan
	// runs exactly as it would otherwise, and what it PRINTS is the adoption
	// ledger alone - what can be adopted, what cannot, and why - with the
	// resource diff and the other live-markers sections suppressed.
	//
	// It sits on Plan for -verbose's reason and not for -estate's. It has to
	// reach plain "choudoufu plan", because under a live block that is the
	// live-markers pipeline (LivePlanCommand.Execute delegates to PlanCommand),
	// and only [ParsePlan] parses that command's flags; -estate needs the
	// opposite, since a live block naming the estate is precisely when -estate
	// must be refused. Unlike -verbose it does name a live-only concept,
	// so a stock, state-backed plan refuses it outright rather than ignoring
	// it - see planRejectAdoptionOnly in the command package. Registering it
	// here and refusing it there is what makes "choudoufu plan
	// -adoption-only" against a state file say so, instead of "flag provided
	// but not defined".
	AdoptionOnly bool

	// Filter narrows the live-markers report to the named categories
	// (GitHub issue #1197): see [ReportFilter]. It sits on Plan for
	// -adoption-only's reason - under a live block plain "choudoufu plan" is
	// the live-markers pipeline, and only [ParsePlan] parses that command's
	// flags - and a stock, state-backed plan refuses it the same way
	// (planRejectReportFilter in the command package).
	Filter ReportFilter
}

// BindPlan registers CLI arguments, returning a Plan value and it's corresponding hooks.
func BindPlan(cli *CommandLine) *Plan {
	plan := Plan{
		View:      BindView(cli, viewFlagAll|viewFlagLint),
		Operation: BindOperation(cli),
		Vars:      BindVars(cli),
		State:     BindState(cli, stateFlagAll),
	}

	cli.BoolVar(&plan.DetailedExitCode, "detailed-exitcode", false,
		`Return detailed exit codes when the command exits. The detailed exit codes are:
  0 - Succeeded but no changes proposed
  1 - Planning failed with an error
  2 - Succeeded and changes are proposed`)
	cli.StringVar(&plan.OutPath, "out", "",
		`Write a plan file to the given path. This can be used as input to the "apply" command.`,
	).SetDisplay("=path")
	cli.StringVar(&plan.GenerateConfigPath, "generate-config-out", "",
		`(Experimental) If import blocks are present in configuration, instructs OpenTofu to generate HCL for any imported resources not already present. The configuration is written to a new file at PATH, which must not already exist.
OpenTofu may still attempt to write configuration if planning fails with an error.`,
	).SetDisplay("=path")

	// This fork's three. -verbose and -adoption-only are plain booleans;
	// -filter validates each word as it is parsed (bindReportFilter). They
	// are on every plan, not only a live one, so that a state-backed
	// "choudoufu plan -adoption-only" is refused by name in the command
	// (planRejectAdoptionOnly, planRejectReportFilter) instead of failing as
	// a flag that does not exist.
	cli.BoolVar(&plan.Verbose, "verbose", false, "Under a live block, print in full the detail a live-markers run summarizes by default, such as the type-by-type list of what was not swept for removal.")
	cli.BoolVar(&plan.AdoptionOnly, "adoption-only", false, "Under a live block, print only the adoption ledger: what can be adopted, what cannot, and why. The plan itself runs as it would otherwise.")
	bindReportFilter(cli, &plan.Filter)

	// Special handling for flag groups!
	for _, name := range []string{"destroy", "refresh-only", "refresh", "replace", "target", "target-file", "exclude", "exclude-file", "var", "var-file"} {
		cli.Flags[name].SetGroup("plan")
	}
	cli.FlagGroups = []FlagGroup{{
		ID:          "plan",
		Title:       "Plan Customization Options:",
		Description: `The following options customize how OpenTofu will produce its plan. You can also use these options when you run "tofu apply" without passing it a saved plan, in order to plan and apply in a single command.`,
	}, {
		Title: "Other Options:",
	}}
	cli.PreHook(func() tfdiags.Diagnostics {
		// Linting is not meant to run during destroy since the graph generated by it is not as complete as most of the
		// linting rules need to run properly.
		if cli.Operation.PlanMode == plans.DestroyMode {
			cli.View.LintExclude = collections.NewSet[linting.RuleAddr]()
			cli.View.LintInclude = collections.NewSet[linting.RuleAddr]()
		}
		return nil
	})

	return &plan
}

// ParsePlan processes CLI arguments, returning a Plan value, a closer function, and errors.
// If errors are encountered, a Plan value is still returned representing
// the best effort interpretation of the arguments.
func ParsePlan(args []string) (*Plan, func(), tfdiags.Diagnostics) {
	cli := new(CommandLine)
	plan := BindPlan(cli)
	closer, diags := cli.parseWithHooks("plan", args)
	return plan, closer, diags
}
