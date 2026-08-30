// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/live/liveimport"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveImport represents the command-line arguments for the live-import
// command.
type LiveImport struct {
	// StatePath is the tfstate file to read, once, read-only. Required:
	// there is no default the way -state defaults to terraform.tfstate for
	// state-backed commands, because reading one by accident is exactly what
	// this command's whole design refuses to do.
	StatePath string

	// Estate names the estate this run would stamp. Required: unlike
	// live-plan and live-mv, there is no configuration for this command to
	// derive it from - the state file being imported may belong to a
	// configuration that has never heard of markers at all.
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
}

// ParseLiveImport processes CLI arguments, returning a LiveImport value and
// errors. If errors are encountered, a LiveImport value is still returned
// representing the best effort interpretation of the arguments.
func ParseLiveImport(args []string) (*LiveImport, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	li := &LiveImport{}

	// -input is accepted and ignored: this command never prompts, and
	// scripts pass it to every OpenTofu command out of habit.
	var input bool

	cmdFlags := defaultFlagSet("live-import")
	cmdFlags.StringVar(&li.StatePath, "state", "", "state")
	cmdFlags.StringVar(&li.Estate, "estate", "", "estate")
	cmdFlags.BoolVar(&li.Approve, "approve", false, "approve")
	cmdFlags.BoolVar(&input, "input", true, "input")
	cmdFlags.IntVar(&li.Parallelism, "parallelism", liveimport.DefaultParallelism, "parallelism")

	if err := cmdFlags.Parse(args); err != nil {
		return li, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid option",
			fmt.Sprintf("%s.", err),
		))
	}

	if rest := cmdFlags.Args(); len(rest) != 0 {
		return li, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Unexpected arguments",
			fmt.Sprintf("live-import takes no positional arguments; got %d. Name the state file with -state and the estate with -estate.", len(rest)),
		))
	}

	if li.StatePath == "" {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No state file named",
			"live-import reads an existing tfstate file once, read-only. Pass -state=<path> naming it.",
		))
	}
	if li.Estate == "" {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No estate named",
			"live-import has no configuration to derive an estate name from - the state file it reads may belong to a configuration that has never used markers. Pass -estate=<name>.",
		))
	}
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

	return li, diags
}
