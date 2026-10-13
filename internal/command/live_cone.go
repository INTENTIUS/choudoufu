// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"os"
	"strings"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/cone"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveConeExitFull is live-cone's exit status when the guard says plan the
// whole estate.
const LiveConeExitFull = 2

// LiveConeCommand prints a change's cone within this estate (GitHub issue
// #1956, stage 1). It reads configuration and git and plans nothing.
type LiveConeCommand struct {
	Meta
}

// LiveConeCommander is live-cone's entry in the new CLI's command tree.
func LiveConeCommander() Command {
	cmd := Command{
		Name:  "live-cone",
		Short: (&LiveConeCommand{}).Synopsis(),
	}
	args := arguments.BindLiveCone(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&LiveConeCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&LiveConeCommand{Meta: meta}).Execute(args)
	}
	return cmd
}

func (c *LiveConeCommand) Run(rawArgs []string) int {
	return RunCommand(LiveConeCommander(), c.Meta, rawArgs)
}

func (c *LiveConeCommand) Execute(args *arguments.LiveCone) int {
	var diags tfdiags.Diagnostics
	fail := func(summary, detail string) int {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, detail))
		c.View.Diagnostics(diags)
		return 1
	}
	wd, err := os.Getwd()
	if err != nil {
		return fail("Cannot read the working directory", err.Error())
	}
	ctx := c.CommandContext()
	if ctx == nil {
		ctx = context.Background()
	}
	res, err := cone.Compute(ctx, cone.Options{Dir: wd, Base: args.Base})
	if err != nil {
		return fail("Cannot compute the cone", err.Error())
	}
	out := res.Text()
	if args.JSON {
		if out, err = res.JSON(); err != nil {
			return fail("Cannot render the result", err.Error())
		}
	}
	views.NewLiveAffected(c.View).Output(out)
	if res.Guard.Full {
		return LiveConeExitFull
	}
	return 0
}

func (c *LiveConeCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] live-cone [options] BASE

  Prints the cone of the change between BASE and the working tree, for the
  estate in this directory (GitHub issue #1956): the resource, data and
  module blocks whose source changed, the blocks deleted since BASE, the
  addresses moved, removed and import blocks name, and every block that
  reads one of them. Printed one per line, as -target-file reads them.
  "choudoufu plan -cone=BASE" plans the same cone. Nothing is planned here.

  The diff is the root module's blocks, compared whitespace aside. A
  local module call is changed when a file under its source changed;
  terraform.tfvars and *.auto.tfvars change the variables they set. A
  -var-file is not read. Co-managed type pairs (a security group and its
  rules, a route table and its routes, a role and its policies) widen the
  cone to the other type's blocks.

  The guard answers "plan everything", with the reason, when:
    - BASE is not a commit, or either side's configuration does not parse;
    - a provider block, the terraform block, the live block or the lock
      file changed;
    - the root module has JSON configuration;
    - a moved or import block names an address no head block declares;
    - the cone reaches nothing, or holds over 30% of the estate's blocks.

Options:

  -json   Print one JSON document: schema, base, base_sha, changed,
          deleted, refactor, widened, reach, targets, blocks, cone_blocks
          and guard {full, kind, reason}. Every list is an array.

Exit status:

  0  the cone is printed
  1  error
  2  the guard says plan the whole estate
`)
}

func (c *LiveConeCommand) Synopsis() string {
	return "Print a change's cone within this estate"
}
