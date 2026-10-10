// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveHistory is the parsed command line of "choudoufu live-history".
type LiveHistory struct {
	// RawAddr is the resource instance address whose record's versions are
	// listed, unparsed; the command parses it.
	RawAddr string

	// Estate names the estate whose record store is read. Empty means the
	// live block's, or the one tofu-estate value the configuration stamps.
	Estate string

	// JSON asks for the history as one JSON document.
	JSON bool
}

// BindLiveHistory registers live-history's options on cli. See
// [BindLiveBucket] for why -json is the command's own.
func BindLiveHistory(cli *CommandLine) *LiveHistory {
	lh := &LiveHistory{}
	BindView(cli, viewFlagNone)

	cli.StringVar(&lh.Estate, "estate", "", "The estate whose record store is read.").SetDisplay("=name")
	cli.BoolVar(&lh.JSON, "json", false, "The history as one JSON document.")

	var rest []string
	cli.VariadicArg(&rest, "ADDRESS")

	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if len(rest) != 1 {
			return diags.Append(tfdiags.Sourceless(tfdiags.Error, "One resource address is required",
				fmt.Sprintf("live-history lists the versions of one resource instance's record. Got %d argument(s).", len(rest))))
		}
		lh.RawAddr = rest[0]
		return diags
	})
	return lh
}

// ParseLiveHistory processes CLI arguments through [BindLiveHistory].
func ParseLiveHistory(args []string) (*LiveHistory, tfdiags.Diagnostics) {
	cli := new(CommandLine)
	lh := BindLiveHistory(cli)
	closer, diags := cli.parseWithHooks("live-history", args)
	closer()
	return lh, diags
}
