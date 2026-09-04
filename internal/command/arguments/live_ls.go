// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveLs represents the command-line arguments for the live-ls command.
type LiveLs struct {
	// Estate is the tofu-estate value to list. Required: unlike live-plan,
	// there is no configuration this command must be run against at all, so
	// there is nothing to derive the name from the way live-mv falls back to
	// a live block's own estate setting.
	Estate string

	// Region narrows where the Tagging API and IAM calls go. Empty defers to
	// the AWS SDK's own default region resolution (AWS_REGION, the shared
	// config file, or an endpoint override's own region), the same as every
	// other AWS client this fork builds.
	Region string

	// Consistent asks the listing to re-read itself until two consecutive
	// reads agree, rather than returning the first read as-is. See
	// [command.pollConsistent] for why this exists at all: the Resource
	// Groups Tagging API's index lags a tag write by about a minute
	// (live-mv's own tag rewrite included), so a listing taken right after a
	// move can show a resource under both its old and new estate, or under
	// neither, and every caller of this command would otherwise reinvent the
	// same wait by hand.
	Consistent bool

	// ConfigDir is the configuration directory to cross-reference the
	// listing against, naming which declared instances the listing's own
	// mechanism structurally cannot see - the record rung and the
	// declaration-carried rung, see live/MARKERS.md's tier definitions
	// (#417) - so that a reader can tell a rung from a genuine absence.
	// Empty means no directory was given, and the listing prints with no
	// such cross-reference at all: this command's core promise (list what
	// the account holds under an estate) needs no configuration to keep.
	ConfigDir string

	// ViewOptions carries -json (and, for parity with every other command
	// that supports it, -json-into is deliberately NOT offered here - see
	// ParseLiveLs's own comment).
	ViewOptions ViewOptions
}

// ParseLiveLs processes CLI arguments, returning a LiveLs value and errors.
// If errors are encountered, a LiveLs value is still returned representing
// the best effort interpretation of the arguments.
func ParseLiveLs(args []string) (*LiveLs, func(), tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	ls := &LiveLs{}

	// -input is not offered at all: this command never prompts, reads no
	// variables and has nothing to prompt for even in principle.
	cmdFlags := defaultFlagSet("live-ls")
	cmdFlags.StringVar(&ls.Estate, "estate", "", "estate")
	cmdFlags.StringVar(&ls.Region, "region", "", "region")
	cmdFlags.BoolVar(&ls.Consistent, "consistent", false, "consistent")
	// jsonInto=false: this is a listing command with one shape of output,
	// not a plan whose JSON a pipeline stage consumes separately from the
	// text a human reads on the same run - see arguments/live_plan.go's own
	// -json-into for the case that pattern exists for.
	ls.ViewOptions.AddGranularFlags(cmdFlags, false, false)

	if err := cmdFlags.Parse(args); err != nil {
		return ls, func() {}, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid option",
			fmt.Sprintf("%s.", err),
		))
	}

	rest := cmdFlags.Args()
	switch len(rest) {
	case 0:
		// No configuration directory: the listing prints with no declared-
		// instance cross-reference. See LiveLs.ConfigDir's own doc comment.
	case 1:
		ls.ConfigDir = rest[0]
	default:
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Too many arguments",
			fmt.Sprintf("live-ls takes at most one argument, the configuration directory to cross-reference the listing against; got %d.", len(rest)),
		))
	}

	if ls.Estate == "" {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No estate named",
			"live-ls lists what the account holds under one estate, and has no configuration it must be run against to derive the name from. Pass -estate=<name>.",
		))
	}

	closer, viewDiags := ls.ViewOptions.Parse()
	diags = diags.Append(viewDiags)

	return ls, closer, diags
}
