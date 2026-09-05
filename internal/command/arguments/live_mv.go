// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveMv represents the command-line arguments for the live-mv command.
type LiveMv struct {
	// RawOldAddr is the address the live resource carries now, and
	// RawNewAddr is the one this run writes onto it. Both are unparsed here;
	// the command parses them as resource instance addresses, which is where
	// the diagnostic about a malformed one belongs.
	RawOldAddr string
	RawNewAddr string

	// Estate names the estate that owns the resource. Empty means the flag
	// was absent, and the name is derived from the configuration instead -
	// from a live block, or from the tofu-estate tags the configuration
	// itself stamps. Unlike live-plan, this command cannot run without one.
	Estate string

	// FromEstate, when set, makes the rename a cross-estate move: the live
	// resource is found under this estate's tag and rewritten to carry the
	// configuration's own. Empty is an ordinary rename within one estate.
	FromEstate string

	// DryRun makes every check and reports what would be rewritten, without
	// writing.
	DryRun bool

	// AllowMissingConfig permits a destination address the configuration does
	// not declare, for a rename whose configuration edit has not happened yet.
	AllowMissingConfig bool

	// JSON asks for the move as one document rather than the labelled-rows
	// report - GitHub issue #791. Everything the human report already says
	// is in it, plus what only this flag exposes: the followers that move
	// with no write of their own, a refusal's stable code alongside its
	// text, and (on a real write) whatever the provider handed back for a
	// receipt to match against. See internal/command/views/live_mv.go's
	// StatelessMvJSONReport.
	JSON bool
}

// ParseLiveMv processes CLI arguments, returning a LiveMv value and errors.
// If errors are encountered, a LiveMv value is still returned representing
// the best effort interpretation of the arguments.
//
// There is no closer and no ViewOptions here, unlike most of this package.
// This command's view is configured from [ParseView] in the ordinary way, so
// the -no-color and -compact-warnings it accepts are gone from the arguments
// before this parser sees them, and there is nothing to close.
//
// Options come before the two addresses, because the flag set stops at the
// first operand the way every other command's does.
func ParseLiveMv(args []string) (*LiveMv, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	liveMv := &LiveMv{}

	// -input is accepted and ignored: this command never prompts, and
	// scripts pass it to every OpenTofu command out of habit.
	var input bool

	cmdFlags := defaultFlagSet("live-mv")
	cmdFlags.StringVar(&liveMv.Estate, "estate", "", "estate")
	cmdFlags.StringVar(&liveMv.FromEstate, "from-estate", "", "from estate")
	cmdFlags.BoolVar(&liveMv.DryRun, "dry-run", false, "dry run")
	cmdFlags.BoolVar(&liveMv.AllowMissingConfig, "allow-missing-config", false, "allow missing config")
	cmdFlags.BoolVar(&liveMv.JSON, "json", false, "json")
	cmdFlags.BoolVar(&input, "input", true, "input")

	// Neither refusal below repeats the usage line. The command answers an
	// argument error with cli.RunResultHelp, the way state mv does, so the
	// help text is printed straight after the diagnostic and there is one
	// copy of it to keep true.
	if err := cmdFlags.Parse(args); err != nil {
		return liveMv, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid option",
			fmt.Sprintf("%s.", err),
		))
	}

	rest := cmdFlags.Args()
	if len(rest) != 2 {
		return liveMv, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Two resource addresses are required",
			fmt.Sprintf(
				"A rename names the address the live resource carries now and the one to write onto it, in that order. Got %d argument(s); options come before the addresses.",
				len(rest)),
		))
	}
	liveMv.RawOldAddr = rest[0]
	liveMv.RawNewAddr = rest[1]

	return liveMv, diags
}
