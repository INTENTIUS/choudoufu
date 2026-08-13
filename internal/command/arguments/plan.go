// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"flag"

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

	// ViewOptions specifies which view options to use
	ViewOptions ViewOptions

	// ShowSensitive is used to display the value of variables marked as sensitive.
	ShowSensitive bool

	// Verbose asks a command to print detail it would otherwise summarize.
	// Live resource markers is the first consumer: the "Not swept for
	// removal" section's full type-by-type breakdown, collapsed to a
	// one-line count by default (GitHub issue #78, "First plan drowns a
	// small estate in the not-swept type list"). It lives on Plan itself,
	// not behind the [parsePlan] extraFlags hook the way -estate is (see
	// that hook's own comment), because -verbose does nothing to a stock
	// plan rather than naming a stateless-only concept, and because it also
	// needs to reach "choudoufu apply" against a live block
	// (arguments.Apply.Verbose), where -estate has no equivalent need
	// (arguments.Apply has none either).
	Verbose bool
}

// ParsePlan processes CLI arguments, returning a Plan value, a closer function, and errors.
// If errors are encountered, a Plan value is still returned representing
// the best effort interpretation of the arguments.
func ParsePlan(args []string) (*Plan, func(), tfdiags.Diagnostics) {
	return parsePlan(args, nil)
}

// parsePlan is ParsePlan with a hook for a command that is the plan command
// plus one option of its own. The hook registers those extra flags on the
// same flag set, which is what makes them parse and interleave with -target,
// -var and friends the way any other plan option does. Nil means the stock
// plan command, whose flag set is exactly what it always was.
//
// The one caller is [ParseLivePlan]; see there for why -estate is added at
// this seam rather than to the Plan struct itself.
func parsePlan(args []string, extraFlags func(*flag.FlagSet)) (*Plan, func(), tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	plan := &Plan{
		State:     &State{},
		Operation: &Operation{},
		Vars:      &Vars{},
	}

	cmdFlags := extendedFlagSet("plan", plan.Operation, plan.Vars)
	plan.State.addFlags(cmdFlags, stateFlagAll)
	cmdFlags.BoolVar(&plan.DetailedExitCode, "detailed-exitcode", false, "detailed-exitcode")
	cmdFlags.StringVar(&plan.OutPath, "out", "", "out")
	cmdFlags.StringVar(&plan.GenerateConfigPath, "generate-config-out", "", "generate-config-out")
	cmdFlags.BoolVar(&plan.ShowSensitive, "show-sensitive", false, "displays sensitive values")
	cmdFlags.BoolVar(&plan.Verbose, "verbose", false, "verbose")

	plan.ViewOptions.AddFlags(cmdFlags, true)

	if extraFlags != nil {
		extraFlags(cmdFlags)
	}

	if err := cmdFlags.Parse(args); err != nil {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Failed to parse command-line flags",
			err.Error(),
		))
	}

	args = cmdFlags.Args()

	if len(args) > 0 {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Too many command line arguments",
			"To specify a working directory for the plan, use the global -chdir flag.",
		))
	}

	diags = diags.Append(plan.Operation.Parse())
	closer, moreDiags := plan.ViewOptions.Parse()
	diags = diags.Append(moreDiags)

	return plan, closer, diags
}
