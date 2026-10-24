// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// liveWorkspaceAccept is the workspace commands' side of the rule that a
// workspace means nothing under a live block (GitHub issue #2221).
//
// It replaces the refusal #2215 made, which told an Atmos user to switch
// workspaces off in atmos.yaml: a config change, where the requirement is to
// switch only the binary. Atmos selects a workspace named after the stack by
// default, so the command has to succeed.
//
// The estate named in the live block, not the workspace, is the authority for
// identity and records. So "workspace new" and "workspace select" are accepted
// for any valid name, record the selection in the working directory the way
// any workspace command does (so "workspace show" returns what the tool
// selected), create no state, and warn that the name is ignored. The caller
// returns early when live is true and skips preparing a backend, so nothing
// is written but the selection itself.
func (m *Meta) liveWorkspaceAccept(ctx context.Context, workspace string) (live bool, diags tfdiags.Diagnostics) {
	settings, diags := m.liveSettings(ctx, false)
	if diags.HasErrors() || settings == nil {
		return false, diags
	}
	if workspace == backend.DefaultStateName {
		// The way back; nothing to warn about.
		return true, diags
	}
	return true, diags.Append(liveWorkspaceIgnoredWarning(workspace, liveEstateLabel(settings)))
}

// liveEstateLabel names the estate (or estates) a live block settles on, as
// far as the configuration alone can say. A block that names no estate gets its
// name from the directory, which only a run settles.
func liveEstateLabel(settings *configs.Live) string {
	switch {
	case settings.IsComposed():
		return "each estate this root composes"
	case settings.EstateSet:
		return fmt.Sprintf("%q", settings.Estate)
	default:
		return "the one the live block names"
	}
}

// liveWorkspaceIgnoredWarning is the one sentence both the workspace commands
// and plan/apply give when a non-default workspace is selected.
func liveWorkspaceIgnoredWarning(workspace, estate string) tfdiags.Diagnostic {
	return tfdiags.Sourceless(
		tfdiags.Warning,
		"Workspace ignored under live resource markers",
		fmt.Sprintf("Workspace %q ignored under live resource markers; this estate is %s. Separate environments need separate estate names.", workspace, estate),
	)
}

// ignoreLiveWorkspace resets a plan or apply operation to the default
// workspace and returns the name it had selected ("" when it was the default).
// With the workspace reset, the run uses the estate's records exactly as a
// default-workspace run does, and creates no per-workspace state.
func ignoreLiveWorkspace(opReq *backend.Operation) string {
	if opReq.Workspace == backend.DefaultStateName {
		return ""
	}
	selected := opReq.Workspace
	opReq.Workspace = backend.DefaultStateName
	return selected
}
