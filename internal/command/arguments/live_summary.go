// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"github.com/intentius/choudoufu/internal/live/plansummary"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveSummary is the parsed command line of "choudoufu live-summary".
type LiveSummary struct {
	// Path is the document to summarize, or "-" for stdin.
	Path string

	// JSON asks for the summary as one JSON document.
	JSON bool

	// Markdown asks for a merge-request note of at most MarkdownLimit
	// characters.
	Markdown      bool
	MarkdownLimit int
}

// BindLiveSummary registers live-summary's options on cli. See
// [BindLiveBucket] for why -json is the command's own.
func BindLiveSummary(cli *CommandLine) *LiveSummary {
	ls := &LiveSummary{}
	BindView(cli, viewFlagNone)

	cli.BoolVar(&ls.JSON, "json", false, "The summary as one JSON document.")
	cli.BoolVar(&ls.Markdown, "markdown", false, "The summary as a merge-request note.")
	cli.IntVar(&ls.MarkdownLimit, "markdown-limit", plansummary.GitLabNoteLimit, "The most characters the note may hold.").SetDisplay("=n")

	var rest []string
	cli.VariadicArg(&rest, "FILE")

	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		switch {
		case len(rest) != 1:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Wrong number of arguments",
				"live-summary takes one argument: the set plan's -json document or a plan's JSON (tofu show -json), as a path, or - for stdin."))
		case ls.JSON && ls.Markdown:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Conflicting options",
				"-json and -markdown each choose the output format; give one."))
		case ls.MarkdownLimit < 1:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Invalid -markdown-limit",
				"-markdown-limit must be a positive number of characters."))
		default:
			ls.Path = rest[0]
		}
		return diags
	})
	return ls
}

// ParseLiveSummary processes CLI arguments through [BindLiveSummary].
func ParseLiveSummary(args []string) (*LiveSummary, tfdiags.Diagnostics) {
	cli := new(CommandLine)
	ls := BindLiveSummary(cli)
	closer, diags := cli.parseWithHooks("live-summary", args)
	closer()
	return ls, diags
}
