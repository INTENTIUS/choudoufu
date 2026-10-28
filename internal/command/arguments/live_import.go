// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// defaultLiveImportParallelism is [liveimport.DefaultParallelism], restated
// here rather than imported: this package cannot import internal/live/
// liveimport at all, because liveimport reaches internal/live/discovery,
// which reaches the root live package, whose own tests import this one - a
// cycle the build refuses. The two are pinned equal by
// TestLiveImportParallelismDefaultMatchesTheStampDefault in internal/command,
// which can see both.
const defaultLiveImportParallelism = 10

// LiveImport represents the command-line arguments for the live-import
// command.
type LiveImport struct {
	// StatePath is the tfstate file to read, once, read-only. There is no
	// terraform.tfstate default, because reading a stray file by accident is
	// exactly what this command's whole design refuses to do. Empty with no
	// AdoptPath means the state the configuration's backend block names
	// (GitHub issue #2293).
	StatePath string

	// AdoptPath is the `adopt: v1` adoption file -adopt names (GitHub issue
	// #2287), read in place of a state file. Exactly one of StatePath and
	// AdoptPath is set.
	AdoptPath string

	// IndexStore is the filesystem index store directory the adoption
	// file's index version is read from: <dir>/index/<scopeKey>/<version>.
	// Required with AdoptPath.
	IndexStore string

	// Estate names the estate this run would stamp. Empty means the name a
	// live run of this configuration resolves (GitHub issue #2293); a
	// configuration with no live block resolves none, and the command
	// refuses.
	Estate string

	// Approve turns the run from a read-only ratification into one that also
	// stamps: without it, the report prints and nothing is written; with it,
	// every VERIFIED or DRIFTED resource from that same report is stamped.
	Approve bool

	// Parallelism is how many resources -approve may stamp at once, spelled
	// and defaulted exactly as stock's apply spells and defaults it, because
	// it is the same budget over the same kind of work: one provider
	// plan+apply round trip per instance. It has no effect without -approve,
	// since a ratification only reads.
	//
	// GitHub issue #583. Stamping was sequential, and #566 measured it as the
	// dominant cost of migrating a terralith: 33.1s for 26 resources, 127.6s
	// for 89.
	Parallelism int

	// ProgressJSON writes one JSON line to stderr per resource as -approve's
	// marker write for it returns, refusals and skips included - GitHub
	// issue #2063. It has no effect without -approve, since a ratification
	// writes nothing. Stdout is unchanged by it.
	ProgressJSON bool
}

// BindLiveImport registers live-import's options on cli.
func BindLiveImport(cli *CommandLine) *LiveImport {
	li := &LiveImport{}
	BindView(cli, viewFlagNone)

	// -input is accepted and ignored: this command never prompts, and
	// scripts pass it to every OpenTofu command out of habit.
	var input bool

	cli.StringVar(&li.StatePath, "state", "", "The tfstate file to read. Defaults to the state the backend block names.").SetDisplay("=path")
	cli.StringVar(&li.AdoptPath, "adopt", "", "An adoption file (adopt: v1) to adopt indexed objects from, in place of -state.").SetDisplay("=path")
	cli.StringVar(&li.IndexStore, "index-store", "", "With -adopt, the index store directory holding the version the adoption file names.").SetDisplay("=dir")
	cli.StringVar(&li.Estate, "estate", "", "The estate this run verifies against and, with -approve, stamps. Defaults to the estate the live block resolves.").SetDisplay("=name")
	cli.BoolVar(&li.Approve, "approve", false, "Stamp every VERIFIED or DRIFTED resource from the ratification report.")
	cli.BoolVar(&input, "input", true, "Accepted and ignored: this command never prompts.").SetDisplay("=false")
	cli.IntVar(&li.Parallelism, "parallelism", defaultLiveImportParallelism, "Limit the number of resources stamped at once.").SetDisplay("=n")
	cli.BoolVar(&li.ProgressJSON, "progress-json", false, "With -approve, write one JSON line to stderr per resource as its marker write returns.")

	var rest []string
	cli.VariadicArg(&rest, "ARGS")

	cli.PreHook(func() tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if len(rest) != 0 {
			return diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Unexpected arguments",
				fmt.Sprintf("live-import takes no positional arguments; got %d. Name the state file with -state and the estate with -estate.", len(rest)),
			))
		}

		switch {
		case li.StatePath != "" && li.AdoptPath != "":
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Both -state and -adopt named",
				"live-import migrates from a tfstate file (-state) or adopts from an adoption file (-adopt), not both in one run.",
			))
		// Neither -state nor -adopt: the state is read from the backend
		// block (GitHub issue #2293), and the command refuses there when
		// the configuration has none.
		case li.AdoptPath != "" && li.IndexStore == "":
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"No index store named",
				"An adoption file names an index version by scope key and version; pass -index-store=<dir> naming the store that holds it (the directory whose index/ subdirectory behold index writes).",
			))
		case li.AdoptPath == "" && li.IndexStore != "":
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"-index-store without -adopt",
				"-index-store says where an adoption file's index version lives, and has no meaning without -adopt.",
			))
		}
		// No -estate: the command resolves it from the configuration the
		// way a live run does (GitHub issue #2293), and refuses there when
		// nothing resolves.

		// The same refusal stock's own -parallelism makes, in the same words:
		// internal/tofu/context.go rejects a non-positive value rather than
		// reading it as "no limit", and a migration that silently stamped an
		// estate with no bound at all would be exactly the wrong reading.
		if li.Parallelism < 1 {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Invalid parallelism value",
				fmt.Sprintf("The parallelism must be a positive value. Not %d.", li.Parallelism),
			))
		}
		return diags
	})
	return li
}

// ParseLiveImport processes CLI arguments through [BindLiveImport],
// returning a LiveImport value and errors. If errors are encountered, a
// LiveImport value is still returned representing the best effort
// interpretation of the arguments.
func ParseLiveImport(args []string) (*LiveImport, tfdiags.Diagnostics) {
	cli := new(CommandLine)
	li := BindLiveImport(cli)
	closer, diags := cli.parseWithHooks("live-import", args)
	closer()
	return li, diags
}
