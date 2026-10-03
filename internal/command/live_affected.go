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
	"github.com/intentius/choudoufu/internal/live/affected"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveAffectedExitIndeterminate is live-affected's exit status when some
// change could not be attributed and the consumer should plan every root.
const LiveAffectedExitIndeterminate = 2

// LiveAffectedCommand names the estate roots a git range touches (GitHub
// issue #1751, part of #1749; it takes over #1106 section 4). It reads
// configuration at both ends of the range and plans nothing.
type LiveAffectedCommand struct {
	Meta
}

// LiveAffectedCommander is live-affected's entry in the new CLI's command
// tree. See [LiveCommanders].
func LiveAffectedCommander() Command {
	cmd := Command{
		Name:  "live-affected",
		Short: (&LiveAffectedCommand{}).Synopsis(),
	}
	args := arguments.BindLiveAffected(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&LiveAffectedCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&LiveAffectedCommand{Meta: meta}).Execute(args)
	}
	return cmd
}

func (c *LiveAffectedCommand) Run(rawArgs []string) int {
	return RunCommand(LiveAffectedCommander(), c.Meta, rawArgs)
}

func (c *LiveAffectedCommand) Execute(args *arguments.LiveAffected) int {
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
	res, err := affected.Compute(ctx, affected.Options{
		RepoDir: wd,
		Spec:    args.Range,
		Roots:   args.Roots,
		Ignore:  args.Ignore,
	})
	if err != nil {
		return fail("Cannot read the range", err.Error())
	}
	out := res.Text()
	if args.JSON {
		if out, err = res.JSON(); err != nil {
			return fail("Cannot render the result", err.Error())
		}
	}
	views.NewLiveAffected(c.View).Output(out)
	if res.Outcome == affected.Indeterminate {
		return LiveAffectedExitIndeterminate
	}
	return 0
}

func (c *LiveAffectedCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] live-affected [options] RANGE

  Names the estate roots a git range touches, and why, so a pipeline plans
  those roots and no others. It reads the configuration at both ends of the
  range from git and plans nothing. RANGE is A..B, A...B (B against the
  merge base of A and B), or A (HEAD against A). Uncommitted changes are
  not read.

  A root is a directory that declares an estate (a live block, or an
  estate.chdf.hcl sidecar). Each root is named with one or more reasons:

    changed            a file in the root's own directory changed
    uses <module>      a file changed in a module the root calls by a
                       local path, directly or through nested modules
    pin <call> A -> B  a module call outside the working tree (oci://,
                       registry, git) changed its tag, digest, version or
                       source. A change under that module's own directory
                       in this repository names no root: no root reads it.
    reads <estate>     the root reads a named root's estate through a
                       cross-estate data source, followed transitively

  A changed file belongs to the nearest directory at or above it holding
  configuration (.tf, .tofu, .tf.json, .tofu.json), so a template beside a
  module belongs to the module. Deleted files and the old side of a rename
  are read against the base revision's configuration.

  Indeterminate, with the reason printed: plan every root.
    - a .terraform.lock.hcl changed;
    - a required_providers version constraint changed in a directory a
      root reaches;
    - module code changed while some root calls a module by a floating
      source (a registry source without an exact version, an OCI source
      without a tag or digest, a git ref that is neither a commit nor a
      version tag, any other remote kind). This command does not read the
      installed version (.terraform/modules/modules.json): that records a
      working directory's last init, not what the range changes;
    - a file outside every module directory that is not documentation,
      since a plan can read one (a -var-file, a file() call, a wrapper's
      configuration) and nothing says which;
    - a root's configuration does not load at either revision.

  Named as touching no root, and not indeterminate: documentation outside
  every module (.md, .markdown, .rst, .adoc, LICENSE, COPYING, NOTICE),
  paths matching -ignore, and module code that no root calls by a local
  path. A cross-estate read inside a module called from outside the
  working tree is not seen.

Options:

  -root=DIR      Consider only this root. Repeatable, or comma-separated.
                 Default: every directory declaring an estate.

  -ignore=GLOB   Changes to paths matching GLOB name nothing (doublestar
                 syntax, against the path from the repository top; a
                 directory covers what is under it). Repeatable. For
                 files a plan never reads, such as CI definitions.

  -json          Print one JSON document:

                   schema         1
                   range          {spec, base, head}: the commits read
                   outcome        "determinate" or "indeterminate"
                   roots_total    roots at either end of the range
                   roots          [{dir, estate, removed?, reasons}]
                     reasons      [{kind, module?, from?, to?, estate?,
                                    paths?, text}]; kind is changed,
                                    uses, pin or reads
                   indeterminate  [{kind, path, text}]; kind is
                                  lock-file, provider-version,
                                  floating-module, unplaced-file or
                                  load-error
                   unplaced       [{path, why, text}]; why is
                                  documentation, ignored or
                                  unread-module

                 Every list is an array, never null.

Exit status:

  0  determinate: the roots printed are the whole affected set (possibly
     none)
  1  error: the range or the repository could not be read
  2  indeterminate: plan every root
`)
}

func (c *LiveAffectedCommand) Synopsis() string {
	return "Name the estate roots a git range touches"
}
