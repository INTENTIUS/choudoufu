// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// statelessWorkspaceGuard refuses the workspace commands that would put a
// stateless working directory into a workspace it cannot run in, and is a
// no-op for every other configuration.
//
// The hole this closes (audit finding F-WS): a live directory refused a
// non-default workspace at plan and apply time - the right refusal in the
// wrong place. "choudoufu workspace new staging" succeeded, selected the new
// workspace as a side effect, and left the operator with a directory where
// every plan and every apply errored out, with the refusal describing a
// choice they no longer appeared to have made. The failure was recoverable
// only by knowing to run "workspace select default", which nothing said.
//
// So the guard is on the two commands that CHANGE which workspace is
// selected, and it is deliberately not symmetric:
//
//   - "workspace new" is refused outright. Its whole product is a second
//     state file, and creating one selects it.
//   - "workspace select" is refused for every workspace except the default,
//     which is always allowed. That is the recovery path out of a directory
//     stranded by a version of OpenTofu before this guard, or by a workspace
//     created before the live block was added, and refusing it would
//     make this guard the thing that strands people.
//
// "workspace list", "workspace show" and "workspace delete" are left alone.
// The first two read and print; the third removes a workspace that already
// exists, which is cleanup rather than stranding, and it cannot run against
// the selected workspace anyway.
//
// Like the other stateless guards, this runs before a backend is prepared,
// so a refused command cannot leave a state file, a workspace directory or a
// lock behind.
func (m *Meta) statelessWorkspaceGuard(ctx context.Context, subcommand string, workspace string) tfdiags.Diagnostics {
	settings, diags := m.statelessSettings(ctx, false)
	if diags.HasErrors() || settings == nil {
		return diags
	}
	if subcommand == "select" && workspace == backend.DefaultStateName {
		// The way back. Always available, in every configuration.
		return diags
	}
	// Same shape as the other two guards: the summary is a noun phrase and
	// the command that was refused opens the detail. See
	// [Meta.statelessStateGuard] for why.
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Error,
		"Command not available under live resource markers",
		statelessWorkspaceDetail(subcommand),
	))
}

func statelessWorkspaceDetail(subcommand string) string {
	shared := fmt.Sprintf("\"choudoufu workspace %s\" is not available here. ", subcommand) +
		"A workspace is a second state file under a different name, and this configuration's live block says there is no first one. " +
		"The unit of ownership under live resource markers is the estate, named by the live block and recorded on the resources themselves as the tofu-estate marker (live/MARKERS.md). " +
		"Give each environment its own configuration directory with its own estate name, which is the separation workspaces were standing in for. "

	switch subcommand {
	case "new":
		return shared +
			"Creating a workspace here would also select it, and plan and apply refuse to run in any workspace but the default, " +
			"so the directory would be left unable to do anything until \"choudoufu workspace select default\" put it back."
	case "select":
		return shared +
			"Selecting the default workspace is always allowed, and is how a directory that is already in another one gets back: run \"choudoufu workspace select default\"."
	default:
		return shared
	}
}
