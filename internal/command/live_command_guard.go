// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// statelessCommandGuard refuses one whole command when the configuration in
// the working directory has a live block, and is a no-op for every other
// configuration.
//
// This is the third of the three stateless refusals, beside
// [Meta.statelessStateGuard] for the "choudoufu state" family and
// [Meta.statelessWorkspaceGuard] for the two workspace commands that change
// which workspace is selected. It serves the commands whose whole product is
// a write to a state file and which have no subcommand to name: import,
// refresh, taint and untaint.
//
// Callers invoke it early in Run, before a backend is prepared and before
// anything can reach a state manager, so that a refused command cannot leave
// a state file, a backup or a lock file behind. The returned diagnostics
// carry both halves of the answer - whatever reading the live block itself
// produced, and the refusal - so a caller appends them and returns when they
// have errors.
//
// The name is the command as it is typed. What each refusal says is in
// statelessCommandRefusals below, one entry per command, so that the shape of
// the refusal is written once and the sentence that tells an operator what to
// do instead stays specific to the command they ran.
func (m *Meta) statelessCommandGuard(ctx context.Context, command string) tfdiags.Diagnostics {
	settings, diags := m.statelessSettings(ctx, false)
	if diags.HasErrors() || settings == nil {
		return diags
	}
	refusal, ok := statelessCommandRefusals[command]
	if !ok {
		// A command wired to this guard and not listed below still gets
		// refused, with the answer that is true of all of them.
		refusal = statelessCommandRefusal{
			summary: "Command not available under live resource markers",
			detail: fmt.Sprintf("\"choudoufu %s\" operates on an authoritative state file, and under a live block the state file is only a disposable cache - it is never consulted for ownership, so there is nothing here this command could correctly change. ", command) +
				"Run \"choudoufu plan\", which reads the live system on every run.",
		}
	}
	return diags.Append(tfdiags.Sourceless(tfdiags.Error, refusal.summary, refusal.detail))
}

// statelessCommandRefusal is what one command says when it is refused: the
// headline, and the paragraph that names what to do instead.
type statelessCommandRefusal struct {
	summary string
	detail  string
}

var statelessCommandRefusals = map[string]statelessCommandRefusal{
	"force-unlock": {
		summary: "There is no lock to force open",
		detail:  "\"choudoufu force-unlock\" releases the lock protecting an authoritative state file, and under a live block no such lock exists: there is no shared record to protect, so concurrent runs are not serialized by this tool at all. Contention settles at the platform API - a duplicate client-named create is rejected by the cloud's own uniqueness constraint and the loser converges on its next plan; a genuine duplicate of a server-assigned resource is reported as a named collision for a human to resolve; record-store writes are conditional and a losing writer gets a named version conflict. Nothing is held that a crash could leave stuck, so there is nothing here to force.",
	},
	"import": {
		summary: "Import is not available under live resource markers",
		detail:  "\"choudoufu import\" writes a record of an existing resource into an authoritative state file, and under a live block no state file carries that authority - a record written into the disposable cache would be discarded the moment the live system disagreed. Ownership under live resource markers is the tofu-estate and tofu-address tag pair described in live/MARKERS.md. Adopt the resource instead: add its resource block to the configuration and run \"choudoufu plan\", which prints the live resource under \"Adoptable\" together with the exact command that stamps its markers. The marker pair is the whole contract, so any tool that writes those two tags adopts the resource.",
	},
	"refresh": {
		summary: "Refresh is not available under live resource markers",
		detail:  "\"choudoufu refresh\" updates an authoritative state file to match the live system. Under a live block the cache is rewritten from the live system by every plan and apply, so refresh has nothing separate to do. Run \"choudoufu plan\": it reads the live system on every run, which is the comparison refresh exists to make possible.",
	},
	"taint": {
		summary: "Taint is not available under live resource markers",
		detail:  "\"choudoufu taint\" marks an object in an authoritative state file as degraded so that the next plan replaces it. Under a live block the state file is a cache with no authority: prior state is rebuilt by reading the live system, so a mark written into the cache would not survive to the next plan. Ask for the replacement in the run that performs it: \"choudoufu plan -replace=ADDRESS\" and \"choudoufu apply -replace=ADDRESS\".",
	},
	"untaint": {
		summary: "Untaint is not available under live resource markers",
		detail:  "\"choudoufu untaint\" removes a degraded mark from an authoritative state file. Under a live block no state carries authority, taint marks are never recorded, and nothing in a live-markers run can leave an object tainted - so there is nothing to remove.",
	},
}
