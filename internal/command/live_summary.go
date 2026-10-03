// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/plansummary"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveSummaryCommand prints a grouped summary of many plans (GitHub issue
// #1753, part of #1749): the set plan's -json document, or one stock plan's
// JSON, read from a file or stdin. Units with identical changes, after the
// names that identify each unit are stripped, are one group, so a reviewer
// reads each distinct change once. The rules are
// [github.com/intentius/choudoufu/internal/live/plansummary]'s package doc.
//
// It reads a document and prints; it touches no configuration, no cloud and
// no record. It is output only: it approves and refuses nothing.
type LiveSummaryCommand struct {
	Meta

	// stdin is what "-" reads; nil means os.Stdin.
	stdin io.Reader
}

// LiveSummaryCommander is live-summary's entry in the new CLI's command
// tree. See [LiveCommanders].
func LiveSummaryCommander() Command {
	return liveSummaryCommander(nil)
}

// liveSummaryCommander builds the commander; stdin, when non-nil, is what
// "-" reads, which is how a test feeds it.
func liveSummaryCommander(stdin io.Reader) Command {
	cmd := Command{
		Name:  "live-summary",
		Short: (&LiveSummaryCommand{}).Synopsis(),
	}

	args := arguments.BindLiveSummary(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&LiveSummaryCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&LiveSummaryCommand{Meta: meta, stdin: stdin}).Execute(args)
	}
	return cmd
}

func (c *LiveSummaryCommand) Run(rawArgs []string) int {
	return RunCommand(liveSummaryCommander(c.stdin), c.Meta, rawArgs)
}

func (c *LiveSummaryCommand) Execute(args *arguments.LiveSummary) int {
	var diags tfdiags.Diagnostics
	data, err := c.read(args.Path)
	if err != nil {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read the document", err.Error()))
		c.View.Diagnostics(diags)
		return 1
	}
	in, err := plansummary.Parse(data)
	if err != nil {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Not a set document or a plan",
			fmt.Sprintf("%s: %s. live-summary reads the set plan's -json document or the JSON of one plan (tofu show -json <planfile>).", args.Path, err)))
		c.View.Diagnostics(diags)
		return 1
	}
	s := plansummary.Summarize(in)

	var out string
	switch {
	case args.JSON:
		out, err = s.JSON()
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot render the summary", err.Error()))
			c.View.Diagnostics(diags)
			return 1
		}
	case args.Markdown:
		out = s.Markdown(args.MarkdownLimit)
	default:
		out = s.Text()
	}
	views.NewLiveSummary(c.View).Output(out)
	return 0
}

func (c *LiveSummaryCommand) read(path string) ([]byte, error) {
	if path == "-" {
		r := c.stdin
		if r == nil {
			r = os.Stdin
		}
		return io.ReadAll(r)
	}
	return os.ReadFile(path)
}

func (c *LiveSummaryCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] live-summary [options] FILE

  Summarizes many plans as groups of identical changes: the set plan's
  -json document, one group per set of roots that change the same way, or
  one plan's JSON (tofu show -json <planfile>), one group per set of
  instances of one for_each or count expansion that change the same way.
  FILE is a path, or - for stdin. It reads the document and nothing else,
  and exits 0 whatever the plans say.

  What "identical" means. A change is its action, its address with every
  instance key removed, and, for each top-level attribute it changes, the
  value on each side. Attributes equal on both sides are left out, so ids
  and ARNs never split a group. Before comparing, each unit loses what
  names it and nothing else: a root's estate name and directory base name,
  an instance's own keys, each stripped only as a whole word, and a
  tofu-estate or tofu-address marker only when it holds the unit's own
  estate or address. A different size, CIDR, policy or tag is a different
  change. The full rules are internal/live/plansummary's package doc.

  Never folded: every destroy and replace is listed by address, however
  large its group, and a root that failed, or has no plan, is a line of its
  own with its reason. Attribute names are shown, never their values.

Options:

  -json          The summary as one JSON document.
  -markdown      The summary as a merge-request note: whole groups, failed
                 roots first, cut from the end to fit -markdown-limit, with
                 a closing line saying what was cut.
  -markdown-limit=n
                 The most characters the note may hold. Defaults to
                 1000000, GitLab's documented limit for a note body.
`)
}

func (c *LiveSummaryCommand) Synopsis() string {
	return "Summarize many plans as groups of identical changes"
}
