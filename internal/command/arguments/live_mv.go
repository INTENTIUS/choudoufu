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

// BindLiveMv registers live-mv's options and its two addresses on cli.
//
// Options may sit before or after the two addresses under the new CLI;
// the legacy CLI's stdlib parser still stops at the first address.
// The addresses are left unparsed here; the command parses them as resource
// instance addresses, which is where the diagnostic about a malformed one
// belongs. See [BindLiveBucket] for why -json is the command's own.
func BindLiveMv(cli *CommandLine) *LiveMv {
	liveMv := &LiveMv{}
	BindView(cli, viewFlagNone)

	// -input is accepted and ignored: this command never prompts, and
	// scripts pass it to every OpenTofu command out of habit.
	var input bool

	cli.StringVar(&liveMv.Estate, "estate", "", "The estate that owns the resource.").SetDisplay("=name")
	cli.StringVar(&liveMv.FromEstate, "from-estate", "", "Move the resource into this estate from the named one.").SetDisplay("=name")
	cli.BoolVar(&liveMv.DryRun, "dry-run", false, "Make every check and report what would be rewritten, without writing.")
	cli.BoolVar(&liveMv.AllowMissingConfig, "allow-missing-config", false, "Permit a destination address the configuration does not declare.")
	cli.BoolVar(&liveMv.JSON, "json", false, "Print the move as one JSON document.")
	cli.BoolVar(&input, "input", true, "Accepted and ignored: this command never prompts.").SetDisplay("=false")

	var rest []string
	cli.VariadicArg(&rest, "ADDRESSES")

	// The refusal below does not repeat the usage line; the command's help
	// says what it accepts, and only one copy of that has to be maintained.
	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if len(rest) != 2 {
			return diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Two resource addresses are required",
				fmt.Sprintf(
					"A rename names the address the live resource carries now and the one to write onto it, in that order. Got %d argument(s).",
					len(rest)),
			))
		}
		liveMv.RawOldAddr = rest[0]
		liveMv.RawNewAddr = rest[1]
		return diags
	})
	return liveMv
}

// ParseLiveMv processes CLI arguments through [BindLiveMv], returning a
// LiveMv value and errors. If errors are encountered, a LiveMv value is
// still returned representing the best effort interpretation of the
// arguments.
func ParseLiveMv(args []string) (*LiveMv, tfdiags.Diagnostics) {
	cli := new(CommandLine)
	liveMv := BindLiveMv(cli)
	closer, diags := cli.parseWithHooks("live-mv", args)
	closer()
	return liveMv, diags
}
