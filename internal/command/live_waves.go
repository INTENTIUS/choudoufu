// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/waves"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveWavesCommand splits a set of estate roots into ordered waves (GitHub
// issue #1754, part of #1749): canaries first, then every root after each
// root whose estate it reads. With a set plan document it also prints the
// set digest and each wave's. It applies nothing; the 2026-10-01 scope
// addition is that another tool can read one wave's roots from it and open
// one change per wave.
type LiveWavesCommand struct {
	Meta
}

// LiveWavesCommander is live-waves' entry in the new CLI's command tree.
// See [LiveCommanders].
func LiveWavesCommander() Command {
	cmd := Command{
		Name:  "live-waves",
		Short: (&LiveWavesCommand{}).Synopsis(),
	}
	args := arguments.BindLiveWaves(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&LiveWavesCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&LiveWavesCommand{Meta: meta}).Execute(args)
	}
	return cmd
}

func (c *LiveWavesCommand) Run(rawArgs []string) int {
	return RunCommand(LiveWavesCommander(), c.Meta, rawArgs)
}

func (c *LiveWavesCommand) Execute(args *arguments.LiveWaves) int {
	var diags tfdiags.Diagnostics
	fail := func(summary, detail string) int {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, detail))
		c.View.Diagnostics(diags)
		return 1
	}

	opts := waves.Options{Roots: args.Roots, Canaries: args.Canaries}
	base, err := os.Getwd()
	if err != nil {
		return fail("Cannot read the working directory", err.Error())
	}
	opts.BaseDir = base
	if args.PlanSet != "" {
		data, err := os.ReadFile(args.PlanSet)
		if err != nil {
			return fail("Cannot read the set plan document", err.Error())
		}
		opts.Set, err = waves.ParseSetDocument(data)
		if err != nil {
			return fail("Not a set plan document", fmt.Sprintf("%s: %s. -plan-set reads the document live-plan-set -json prints.", args.PlanSet, err))
		}
	}

	ctx := c.CommandContext()
	if ctx == nil {
		ctx = context.Background()
	}
	doc, err := waves.Build(ctx, opts)
	if err != nil {
		return fail("Cannot split this set into waves", err.Error())
	}

	var out string
	switch {
	case args.Wave > 0:
		if args.Wave > len(doc.Waves) {
			return fail("No such wave", fmt.Sprintf("-wave=%d, and this set splits into %d waves.", args.Wave, len(doc.Waves)))
		}
		w := doc.Waves[args.Wave-1]
		if args.JSON {
			b, err := json.MarshalIndent(w, "", "  ")
			if err != nil {
				return fail("Cannot render the wave", err.Error())
			}
			out = string(b)
		} else {
			out = waves.WaveLines(w)
		}
	case args.JSON:
		out, err = doc.JSON()
		if err != nil {
			return fail("Cannot render the waves", err.Error())
		}
	default:
		out = doc.Text()
	}
	views.NewLiveWaves(c.View).Output(out)
	return 0
}

func (c *LiveWavesCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] live-waves [options] [ROOT...]

  Splits a set of estate roots into ordered waves and prints them. It reads
  each ROOT's configuration and applies nothing.

  A root reads another when one of its data sources is filtered on that
  estate's tofu-estate marker (a filter block or a tags argument), or is a
  terraform_estate_outputs read naming it. A reader always lands in a later
  wave than the root it reads. The -canary roots, if any, are wave 1; every
  other root lands in the earliest wave after everything it reads. A read
  of an estate outside the set orders nothing and is listed.

  Refused, with the roots named: a cycle of reads; a canary that reads a
  root of the set that is not a canary; two roots owning one estate; a
  read whose estate is not a literal, since it could not be ordered.

  A module called by a local path is read from that path, so no init is
  needed for one; any other module is read where init installed it.

Options:

  -canary=ROOT   A root, by directory or by estate, for wave 1. Repeatable,
                 or comma-separated.

  -plan-set=FILE The set plan document (live-plan-set -json). Each wave,
                 and the whole set, then carries its set digest: SHA-256
                 over each root's planned changes. With no ROOT, the
                 document's roots are the set.

  -wave=n        Print only wave n: its roots one per line, or its object
                 with -json.

  -json          Print the waves as one JSON document.
`)
}

func (c *LiveWavesCommand) Synopsis() string {
	return "Split a set of estate roots into ordered waves"
}
