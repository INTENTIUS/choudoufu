// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/states/statemgr"
	"github.com/intentius/choudoufu/internal/tracing"

	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

func UnlockCommander() Command {
	cmd := Command{
		Name:  "force-unlock",
		Short: "Release a stuck lock on the current workspace",
		Long: `Manually unlock the state for the defined configuration.

This will not modify your infrastructure. This command removes the lock on the state for the current workspace. The behavior of this lock is dependent on the backend being used. Local state files cannot be unlocked by another process.`,

		DiagsWithNewline: true,
	}

	args := arguments.BindUnlock(&cmd.CommandLine)
	cmd.Run = func(meta Meta) int {
		return UnlockCommand{meta}.Execute(args, views.NewUnlock(args.View, meta.View))
	}

	return cmd
}

// UnlockCommand is a cli.Command implementation that manually unlocks
// the state.
type UnlockCommand struct {
	Meta
}

func (c UnlockCommand) Execute(args *arguments.Unlock, view views.Unlock) int {
	var diags tfdiags.Diagnostics

	ctx := c.CommandContext()
	ctx, span := tracing.Tracer().Start(ctx, "Unlock")
	defer span.End()

	// Under a live block there is no lock to force open; refuse with the
	// true reason before any backend machinery can produce stock's
	// misleading "State locked by another local process" for a lock that
	// never existed (found by the no-locks claim scenario's probe). This
	// runs after variables are parsed, like every other guarded command
	// (import.go, taint.go, refresh.go, untaint.go) - RunCli has put the
	// -var values on Meta before Execute is called - because the guard's
	// own config load statically evaluates the backend block and a -var
	// this command was given is not visible to it otherwise - not just
	// under a live block, but for any configuration whose backend depends
	// on a variable, live or not.
	if guardDiags := c.liveCommandGuard(ctx, "force-unlock"); len(guardDiags) > 0 {
		view.Diagnostics(guardDiags)
		if guardDiags.HasErrors() {
			return 1
		}
	}

	lockID := args.LockID

	// This gets the current directory as full path.
	configPath := c.WorkingDir.NormalizePath(c.WorkingDir.RootModuleDir())

	// Load the encryption configuration
	enc, encDiags := c.EncryptionFromPath(ctx, configPath)
	if encDiags.HasErrors() {
		view.Diagnostics(encDiags)
		return 1
	}

	backendConfig, backendDiags := c.loadBackendConfig(ctx, configPath)
	diags = diags.Append(backendDiags)
	if diags.HasErrors() {
		view.Diagnostics(diags)
		return 1
	}

	// Load the backend
	b, backendDiags := c.Backend(ctx, &BackendOpts{
		Config: backendConfig,
		View:   view.Backend(),
	}, enc.State())
	diags = diags.Append(backendDiags)
	if backendDiags.HasErrors() {
		view.Diagnostics(diags)
		return 1
	}

	// unlocking is read only when looking at state data
	c.ignoreRemoteVersionConflict(b)

	env, err := c.Workspace(ctx)
	if err != nil {
		view.Diagnostics(diags.Append(fmt.Errorf("Error selecting workspace: %s", err)))
		return 1
	}
	stateMgr, err := b.StateMgr(ctx, env)
	if err != nil {
		view.Diagnostics(diags.Append(fmt.Errorf("Failed to load state: %s", err)))
		return 1
	}

	_, isLocal := stateMgr.(*statemgr.Filesystem)

	if optionalLocker, ok := stateMgr.(statemgr.OptionalLocker); ok {
		// Now we can safely call IsLockingEnabled() on optionalLocker
		if !optionalLocker.IsLockingEnabled() {
			view.LockingDisabledForBackend()
			return 1
		}
	}

	// Proceed with unlocking logic if locking is enabled
	if !args.Force {
		// Forcing this doesn't do anything, but doesn't break anything either,
		// and allows us to run the basic command test too.
		if isLocal {
			view.CannotUnlockByAnotherProcess()
			return 1
		}

		cancelCtx, cancel := c.InterruptibleContext(ctx)
		defer cancel()

		desc := "OpenTofu will remove the lock on the remote state.\n" +
			"This will allow local OpenTofu commands to modify this state, even though it\n" +
			"may still be in use. Only 'yes' will be accepted to confirm."

		v, err := c.UIInput().Input(cancelCtx, &tofu.InputOpts{
			Id:          "force-unlock",
			Query:       "Do you really want to force-unlock?",
			Description: desc,
		})
		if err != nil {
			view.Diagnostics(diags.Append(fmt.Errorf("Error asking for confirmation: %s", err)))
			return 1
		}
		if v != "yes" {
			view.ForceUnlockCancelled()
			return 1
		}
	}

	if err := stateMgr.Unlock(context.TODO(), lockID); err != nil {
		view.Diagnostics(diags.Append(fmt.Errorf("Failed to unlock state: %s", err)))
		return 1
	}
	view.ForceUnlockSucceeded()
	return 0
}
