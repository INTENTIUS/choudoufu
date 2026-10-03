// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// DefaultPlanSetOutDir is where live-plan-set writes its plan files and
// logs when -out-dir is not given.
const DefaultPlanSetOutDir = "choudoufu-plan-set"

// LivePlanSet represents the command-line arguments for live-plan-set
// (GitHub issue #1752).
type LivePlanSet struct {
	// Roots are the estate root directories to plan, as given.
	Roots []string

	// ParallelEstates bounds how many roots run at once.
	ParallelEstates int

	// OutDir receives one plan file and one log per root, named by root.
	OutDir string

	// View carries -json and -no-color.
	View *View
}

// BindLivePlanSet registers live-plan-set's options and its roots on cli.
func BindLivePlanSet(cli *CommandLine) *LivePlanSet {
	set := &LivePlanSet{}

	// -json-into is not offered: the document is the whole of -json's
	// output and there is no second stream for it to go beside.
	set.View = BindView(cli, viewFlagJson)

	cli.IntVar(&set.ParallelEstates, "parallel-estates", 4, "How many roots to run at once.").SetDisplay("=n")
	cli.StringVar(&set.OutDir, "out-dir", DefaultPlanSetOutDir, "Where each root's plan file and log are written.").SetDisplay("=path")

	cli.VariadicArg(&set.Roots, "ROOT")

	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if len(set.Roots) == 0 {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"No roots given",
				"live-plan-set plans the estate root directories named as its arguments. Name at least one.",
			))
		}
		if set.ParallelEstates < 1 {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Invalid -parallel-estates",
				fmt.Sprintf("-parallel-estates bounds how many roots run at once and must be at least 1, got %d.", set.ParallelEstates),
			))
		}
		if set.OutDir == "" {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Invalid -out-dir",
				"-out-dir names the directory each root's plan file is written to and cannot be empty.",
			))
		}
		return diags
	})
	return set
}

// ParseLivePlanSet processes CLI arguments through [BindLivePlanSet].
func ParseLivePlanSet(args []string) (*LivePlanSet, func(), tfdiags.Diagnostics) {
	cli := new(CommandLine)
	set := BindLivePlanSet(cli)
	closer, diags := cli.parseWithHooks("live-plan-set", args)
	return set, closer, diags
}
