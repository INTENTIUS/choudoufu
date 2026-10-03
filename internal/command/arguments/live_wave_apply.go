// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"strings"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// DefaultWaveApplyOutDir is where live-wave-apply writes each wave's fresh
// plans and every root's apply log when -out-dir is not given.
const DefaultWaveApplyOutDir = "choudoufu-wave-apply"

// LiveWaveApply is the parsed command line of "choudoufu live-wave-apply"
// (GitHub issue #1754).
type LiveWaveApply struct {
	// PlanSet is the approved set plan document.
	PlanSet string
	// Digest is the approved set digest.
	Digest string
	// Wave is the wave to apply, from 1.
	Wave int
	// Resume is the resume file, created when it does not exist.
	Resume string
	// Canaries are live-waves' -canary, and must be the same canaries the
	// waves were approved with.
	Canaries []string
	// OutDir receives fresh plans and logs.
	OutDir string
	// ParallelEstates bounds how many roots plan at once.
	ParallelEstates int
	// JSON prints the result as one JSON document.
	JSON bool
}

// BindLiveWaveApply registers live-wave-apply's options on cli.
func BindLiveWaveApply(cli *CommandLine) *LiveWaveApply {
	a := &LiveWaveApply{}
	BindView(cli, viewFlagNone)

	var canaries []string
	cli.StringVar(&a.PlanSet, "plan-set", "", "The approved set plan document.")
	cli.StringVar(&a.Digest, "digest", "", "The approved set digest.")
	cli.IntVar(&a.Wave, "wave", 0, "The wave to apply.").SetDisplay("=n")
	cli.StringVar(&a.Resume, "resume", "", "The resume file.")
	cli.StringArrayVar(&canaries, "canary", nil, "A root, by directory or estate, for wave 1.")
	cli.StringVar(&a.OutDir, "out-dir", DefaultWaveApplyOutDir, "Where fresh plans and logs go.").SetDisplay("=path")
	cli.IntVar(&a.ParallelEstates, "parallel-estates", 4, "How many roots plan at once.").SetDisplay("=n")
	cli.BoolVar(&a.JSON, "json", false, "The result as one JSON document.")

	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		for _, c := range canaries {
			for _, part := range strings.Split(c, ",") {
				if part = strings.TrimSpace(part); part != "" {
					a.Canaries = append(a.Canaries, part)
				}
			}
		}
		var missing []string
		if a.PlanSet == "" {
			missing = append(missing, "-plan-set")
		}
		if a.Digest == "" {
			missing = append(missing, "-digest")
		}
		if a.Wave < 1 {
			missing = append(missing, "-wave (from 1)")
		}
		if a.Resume == "" {
			missing = append(missing, "-resume")
		}
		if len(missing) > 0 {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Missing options",
				"live-wave-apply needs "+strings.Join(missing, ", ")+": the approved set plan document, the digest that was approved, the wave to apply, and the resume file that records what earlier waves landed."))
		}
		if a.ParallelEstates < 1 {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Invalid -parallel-estates", "-parallel-estates must be at least 1."))
		}
		if a.OutDir == "" {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Invalid -out-dir", "-out-dir cannot be empty."))
		}
		return diags
	})
	return a
}

// ParseLiveWaveApply processes CLI arguments through [BindLiveWaveApply].
func ParseLiveWaveApply(args []string) (*LiveWaveApply, tfdiags.Diagnostics) {
	cli := new(CommandLine)
	a := BindLiveWaveApply(cli)
	closer, diags := cli.parseWithHooks("live-wave-apply", args)
	closer()
	return a, diags
}
