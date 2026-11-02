// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveGraph is the parsed command line of "choudoufu live-graph" (GitHub
// issue #2285).
type LiveGraph struct {
	// Dir is the root module's directory. "." when none is given.
	Dir string

	// JSON must be set: the graph is printed as one JSON document and in
	// no other form. Asking for it keeps the command line the same if a
	// second form is ever added.
	JSON bool

	// FromPlan is a live-plan -json document of the same estate. Its rows
	// are the only source of live facts; without it nothing live is read.
	FromPlan string

	// History adds each record's versions, read through the record store.
	History bool

	// Estate overrides the estate the configuration names.
	Estate string
}

// BindLiveGraph registers live-graph's options on cli.
func BindLiveGraph(cli *CommandLine) *LiveGraph {
	lg := &LiveGraph{}
	BindView(cli, viewFlagNone)

	cli.BoolVar(&lg.JSON, "json", false, "Print the graph as one JSON document (required).")
	cli.StringVar(&lg.FromPlan, "from-plan", "", "A live-plan -json document of the same estate, the source of live facts.").SetDisplay("=file")
	cli.BoolVar(&lg.History, "history", false, "Add each record's versions, with no record values.")
	cli.StringVar(&lg.Estate, "estate", "", "The estate the graph is of. Defaults to the configuration's.").SetDisplay("=name")

	var rest []string
	cli.VariadicArg(&rest, "DIR")

	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		switch len(rest) {
		case 0:
			lg.Dir = "."
		case 1:
			lg.Dir = rest[0]
		default:
			return diags.Append(tfdiags.Sourceless(tfdiags.Error, "Too many arguments",
				fmt.Sprintf("live-graph reads one root module directory. Got %d arguments.", len(rest))))
		}
		if !lg.JSON {
			return diags.Append(tfdiags.Sourceless(tfdiags.Error, "live-graph prints JSON only",
				"live-graph prints one JSON document and has no other output form. Pass -json."))
		}
		return diags
	})
	return lg
}

// ParseLiveGraph processes CLI arguments through [BindLiveGraph].
func ParseLiveGraph(args []string) (*LiveGraph, tfdiags.Diagnostics) {
	cli := new(CommandLine)
	lg := BindLiveGraph(cli)
	closer, diags := cli.parseWithHooks("live-graph", args)
	closer()
	return lg, diags
}
