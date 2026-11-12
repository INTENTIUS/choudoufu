// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveSweepSecrets is the parsed command line of "choudoufu
// live-sweep-secrets" (GitHub issue #2393).
type LiveSweepSecrets struct {
	// Estate names the estate whose parameters are swept. Empty means the
	// live block's.
	Estate string

	// Delete deletes the orphans. Without it the command lists them and
	// changes nothing.
	Delete bool

	// JSON asks for one JSON document on stdout.
	JSON bool
}

// BindLiveSweepSecrets registers live-sweep-secrets' options on cli. See
// [BindLiveBucket] for why -json is the command's own.
func BindLiveSweepSecrets(cli *CommandLine) *LiveSweepSecrets {
	ls := &LiveSweepSecrets{}
	BindView(cli, viewFlagNone)

	cli.StringVar(&ls.Estate, "estate", "", "The estate whose parameters are swept.").SetDisplay("=name")
	cli.BoolVar(&ls.Delete, "delete", false, "Delete the orphans instead of listing them.")
	cli.BoolVar(&ls.JSON, "json", false, "One JSON document on stdout.")

	var rest []string
	cli.VariadicArg(&rest, "ARGS")
	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if len(rest) != 0 {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Too many arguments",
				"live-sweep-secrets takes no positional arguments. Run it in the estate's configuration directory."))
		}
		return diags
	})
	return ls
}
