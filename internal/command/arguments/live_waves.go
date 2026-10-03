// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"strings"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveWaves is the parsed command line of "choudoufu live-waves".
type LiveWaves struct {
	// Roots are the root directories, as given. Empty means the roots of
	// PlanSet.
	Roots []string

	// Canaries name roots by directory or estate; they form wave 1.
	Canaries []string

	// PlanSet is a set plan document (live-plan-set -json) whose plans give
	// every wave its digest. Empty means no digests.
	PlanSet string

	// JSON asks for the waves as one JSON document.
	JSON bool

	// Wave, when positive, prints only that wave: its roots one per line,
	// or its object under -json.
	Wave int
}

// BindLiveWaves registers live-waves' options on cli. See [BindLiveBucket]
// for why -json is the command's own.
func BindLiveWaves(cli *CommandLine) *LiveWaves {
	lw := &LiveWaves{}
	BindView(cli, viewFlagNone)

	var canaries []string
	cli.StringArrayVar(&canaries, "canary", nil, "A root, by directory or estate, for wave 1. Repeatable, or comma-separated.")
	cli.StringVar(&lw.PlanSet, "plan-set", "", "A set plan document whose plans give each wave its digest.")
	cli.BoolVar(&lw.JSON, "json", false, "The waves as one JSON document.")
	cli.IntVar(&lw.Wave, "wave", 0, "Print only this wave.").SetDisplay("=n")

	cli.VariadicArg(&lw.Roots, "ROOT")

	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		for _, c := range canaries {
			for _, part := range strings.Split(c, ",") {
				if part = strings.TrimSpace(part); part != "" {
					lw.Canaries = append(lw.Canaries, part)
				}
			}
		}
		switch {
		case len(lw.Roots) == 0 && lw.PlanSet == "":
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "No roots",
				"live-waves takes the root directories to split, or -plan-set naming a set plan document whose roots it splits."))
		case lw.Wave < 0:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Invalid -wave",
				"-wave counts from 1."))
		}
		return diags
	})
	return lw
}

// ParseLiveWaves processes CLI arguments through [BindLiveWaves].
func ParseLiveWaves(args []string) (*LiveWaves, tfdiags.Diagnostics) {
	cli := new(CommandLine)
	lw := BindLiveWaves(cli)
	closer, diags := cli.parseWithHooks("live-waves", args)
	closer()
	return lw, diags
}
