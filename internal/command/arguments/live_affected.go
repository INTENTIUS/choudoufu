// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"strings"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveAffected is the parsed command line of "choudoufu live-affected".
type LiveAffected struct {
	// Range is the git range: "A..B", "A...B" or "A".
	Range string

	// Roots restrict the run to these root directories. Empty means every
	// directory that declares an estate.
	Roots []string

	// Ignore are path patterns whose changes name nothing.
	Ignore []string

	// JSON asks for one JSON document on stdout.
	JSON bool
}

// BindLiveAffected registers live-affected's options on cli. See
// [BindLiveBucket] for why -json is the command's own.
func BindLiveAffected(cli *CommandLine) *LiveAffected {
	la := &LiveAffected{}
	BindView(cli, viewFlagNone)

	var roots, ignore []string
	cli.StringArrayVar(&roots, "root", nil, "A root directory to consider. Repeatable, or comma-separated.")
	cli.StringArrayVar(&ignore, "ignore", nil, "A path pattern whose changes name nothing. Repeatable.")
	cli.BoolVar(&la.JSON, "json", false, "One JSON document on stdout.")

	var rest []string
	cli.VariadicArg(&rest, "RANGE")

	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		for _, r := range roots {
			for _, part := range strings.Split(r, ",") {
				if part = strings.TrimSpace(part); part != "" {
					la.Roots = append(la.Roots, part)
				}
			}
		}
		for _, p := range ignore {
			if p = strings.TrimSpace(p); p != "" {
				la.Ignore = append(la.Ignore, p)
			}
		}
		if len(rest) != 1 {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "One range",
				"live-affected takes exactly one git range: A..B, A...B (against their merge base), or A (against HEAD)."))
		} else {
			la.Range = rest[0]
		}
		return diags
	})
	return la
}

// ParseLiveAffected processes CLI arguments through [BindLiveAffected].
func ParseLiveAffected(args []string) (*LiveAffected, tfdiags.Diagnostics) {
	cli := new(CommandLine)
	la := BindLiveAffected(cli)
	closer, diags := cli.parseWithHooks("live-affected", args)
	closer()
	return la, diags
}
