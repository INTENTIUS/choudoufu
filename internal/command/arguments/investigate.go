// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Investigate is the parsed command line of "choudoufu investigate"
// (GitHub issue #2280).
type Investigate struct {
	// Name is the investigation: a native lookup or a template id. Empty
	// lists them.
	Name string

	// Primary is the first positional value after Name: the investigation's
	// first input (a template's resource, a lookup's cloud ID, address, plan
	// file or range).
	Primary string

	// Inputs are the other inputs, given as name=value.
	Inputs map[string]string

	// List asks for the catalog instead of an answer.
	List bool

	// JSON asks for the answer as one JSON document.
	JSON bool

	// Neighbours shared with "choudoufu mcp".
	Neighbours Neighbours
}

// Neighbours are the options naming chant and behold, shared by
// "choudoufu investigate" and "choudoufu mcp".
type Neighbours struct {
	Chant     string
	Behold    string
	Templates string
	Dir       string
}

func bindNeighbours(cli *CommandLine, n *Neighbours) {
	cli.StringVar(&n.Chant, "chant", "", "The chant binary, instead of the one on PATH; none for no chant.").SetDisplay("=path")
	cli.StringVar(&n.Behold, "behold", "", "The behold binary, instead of the one on PATH; none for no behold.").SetDisplay("=path")
	cli.StringVar(&n.Templates, "templates", "", "A templates.json to read instead of chant's catalog.").SetDisplay("=file")
	cli.StringVar(&n.Dir, "project", "", "The chant project or workspace chant search, behold and chant's Ops run in. Defaults to the current directory.").SetDisplay("=dir")
}

// BindInvestigate registers investigate's options on cli.
func BindInvestigate(cli *CommandLine) *Investigate {
	inv := &Investigate{Inputs: map[string]string{}}
	BindView(cli, viewFlagNone)
	cli.BoolVar(&inv.List, "list", false, "List the investigations and whether each can run here.")
	cli.BoolVar(&inv.JSON, "json", false, "The answer as one JSON document.")
	bindNeighbours(cli, &inv.Neighbours)

	var rest []string
	cli.VariadicArg(&rest, "NAME")

	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if len(rest) == 0 {
			inv.List = true
			return diags
		}
		inv.Name = rest[0]
		for i, a := range rest[1:] {
			k, v, ok := strings.Cut(a, "=")
			if i == 0 && (!ok || strings.ContainsAny(k, "/:.[")) {
				// The first value is the investigation's first input, given
				// bare. An ARN or an address can hold "=", so a first value
				// whose "name" could not be an input name is bare too.
				inv.Primary = a
				continue
			}
			if !ok || k == "" {
				return diags.Append(tfdiags.Sourceless(tfdiags.Error, "Inputs are name=value",
					fmt.Sprintf("After the investigation and its first input, each input is name=value; got %q.", a)))
			}
			if _, dup := inv.Inputs[k]; dup {
				return diags.Append(tfdiags.Sourceless(tfdiags.Error, "Input given twice", fmt.Sprintf("%s is given twice.", k)))
			}
			inv.Inputs[k] = v
		}
		return diags
	})
	return inv
}

// Mcp is the parsed command line of "choudoufu mcp".
type Mcp struct {
	// AllowOpApprove passes chant's op-approve through. Off by default, so
	// an agent cannot run an Op and approve its own gate in one session.
	AllowOpApprove bool

	Neighbours Neighbours
}

// BindMcp registers mcp's options on cli.
func BindMcp(cli *CommandLine) *Mcp {
	m := &Mcp{}
	BindView(cli, viewFlagNone)
	cli.BoolVar(&m.AllowOpApprove, "allow-op-approve", false, "Pass chant's op-approve through.")
	bindNeighbours(cli, &m.Neighbours)
	var rest []string
	cli.VariadicArg(&rest, "ARGS")
	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if len(rest) > 0 {
			return diags.Append(tfdiags.Sourceless(tfdiags.Error, "Unexpected argument",
				fmt.Sprintf("choudoufu mcp takes options only; got %q.", rest[0])))
		}
		return diags
	})
	return m
}
