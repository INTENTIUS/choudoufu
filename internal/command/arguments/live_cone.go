// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveCone is the parsed command line of "choudoufu live-cone" (GitHub
// issue #1956).
type LiveCone struct {
	// Base is the git revision the change is measured against.
	Base string
	// JSON asks for one JSON document on stdout.
	JSON bool
}

// BindLiveCone registers live-cone's options on cli.
func BindLiveCone(cli *CommandLine) *LiveCone {
	lc := &LiveCone{}
	BindView(cli, viewFlagNone)
	cli.BoolVar(&lc.JSON, "json", false, "One JSON document on stdout.")
	var rest []string
	cli.VariadicArg(&rest, "BASE")
	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if len(rest) != 1 {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "One base revision",
				"live-cone takes exactly one git revision to measure the working tree against, such as origin/main."))
		} else {
			lc.Base = rest[0]
		}
		return diags
	})
	return lc
}
