// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"

	"github.com/opentofu/opentofu/internal/tfdiags"
)

// statelessStateGuard refuses one "choudoufu state" subcommand when the
// configuration in the working directory has a live block, and is a
// no-op for every other configuration. Callers invoke it before they load a
// backend or ask for a state manager, so that a refused command cannot leave
// a state file, a backup or a lock file behind.
//
// Every subcommand in the family is refused, including the read-only "state
// list" and "state show". Answering those two from the projection would mean
// building a whole projection - launching providers, listing and reading the
// live system - behind a command whose contract is "read the local record",
// and the answer would differ from run to run because the live system does.
// The projection is already printed by "choudoufu plan", which is where a
// stateless configuration asks that question. Refusing all of them keeps one
// answer for the whole subcommand family instead of a rule an operator has to
// remember the shape of.
//
// The parent "choudoufu state" command has no guard because it reads nothing: it
// prints the subcommand help and exits.
//
// The receiver is [Meta] rather than [StateMeta] on purpose. Four of the
// subcommands embed both, and only the outer Meta is filled in when they are
// constructed, so a StateMeta method would read the working directory out of
// the empty one.
func (m *Meta) statelessStateGuard(ctx context.Context, subcommand string) tfdiags.Diagnostics {
	settings, diags := m.statelessSettings(ctx, false)
	if diags.HasErrors() || settings == nil {
		return diags
	}
	// The summary names no subcommand, because a summary that opened with a
	// quoted command name would sort and read badly beside every other
	// diagnostic in the program - upstream summaries are capitalized noun
	// phrases and never start with punctuation. Which command was refused is
	// the detail's first words instead, where it is still the first thing
	// read.
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Error,
		"Command not available in stateless mode",
		fmt.Sprintf(
			"\"choudoufu state %s\" operates on a stored state file, and this configuration's live block says there is no stored state. %s",
			subcommand, statelessStateReplacement(subcommand),
		),
	))
}

// statelessStateReplacement is the sentence that tells the operator what to do
// instead, one per subcommand. It lives here rather than at each call site so
// that the shape of the refusal is written once.
func statelessStateReplacement(subcommand string) string {
	if s, ok := statelessStateReplacements[subcommand]; ok {
		return s
	}
	// A subcommand added later and not listed here still gets refused, with
	// the answer that is true of all of them.
	return "Run \"choudoufu plan\", which reads the live system on every run."
}

var statelessStateReplacements = map[string]string{
	"list": "Run \"choudoufu plan\", which builds the projection from the live system and prints what it read and what it could not.",
	"show": "Run \"choudoufu plan\", which builds the projection from the live system and prints what it read and what it could not.",
	"mv":   "Run \"choudoufu live-mv OLD NEW\", which rewrites the tofu-address marker on the live resource. That is the whole rename.",
	"rm": "Delete the resource block from the configuration, or, to stop managing a resource without destroying it, remove its tofu-estate and tofu-address tags. " +
		"A resource block deleted from the configuration is planned as a destroy by the estate sweep, so forgetting a resource without destroying it is a tag removal here rather than an edit to a record.",
	"pull": "There is no state to pull. Prior state in stateless mode is a projection built by reading the live system, and it is discarded when the run ends.",
	"push": "There is no state to push, and the record it would install is exactly the authority stateless mode removes.",
	"replace-provider": "Provider addresses live in a state file, and a stateless run derives each resource's provider from the configuration on every run, " +
		"so changing the configuration is the whole operation.",
}
