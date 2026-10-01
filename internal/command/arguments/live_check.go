// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveCheck is the parsed command line of "choudoufu live-check".
type LiveCheck struct {
	// Dir is the configuration directory to check. "." when none was given.
	Dir string

	// JSON asks for GitHub issue #790's declared roster instead of the prose
	// report.
	JSON bool
}

// BindLiveCheck registers live-check's options on cli: the shared view
// options, -json, and at most one directory. GitHub issue #114's rule is
// that live-check accepts no options of its own; #790 widened that by -json
// alone, and any other flag is a usage error.
func BindLiveCheck(cli *CommandLine) *LiveCheck {
	lc := &LiveCheck{Dir: "."}
	BindView(cli, viewFlagNone)

	cli.BoolVar(&lc.JSON, "json", false, "Print the declared roster as one JSON document instead of the prose report.")

	var rest []string
	cli.VariadicArg(&rest, "DIR")

	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		switch len(rest) {
		case 0:
		case 1:
			lc.Dir = rest[0]
		default:
			diags = diags.Append(fmt.Errorf("live-check takes at most one argument, the directory to check; got %d", len(rest)))
		}
		return diags
	})
	return lc
}

// ParseLiveCheck processes CLI arguments through [BindLiveCheck].
func ParseLiveCheck(args []string) (*LiveCheck, tfdiags.Diagnostics) {
	cli := new(CommandLine)
	lc := BindLiveCheck(cli)
	closer, diags := cli.parseWithHooks("live-check", args)
	closer()
	return lc, diags
}
