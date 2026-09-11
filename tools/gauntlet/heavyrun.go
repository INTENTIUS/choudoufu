// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "fmt"

// Heavy runs are the maintainer's, never a local session's (CLAUDE.md
// "Heavy and paid runs are the maintainer's, by hand"; the 2026-09-11
// incident that made it a rule). `gauntlet run` (a full estate pass against
// the pinned emulator, minutes to hours) and `gauntlet live-cert` (a
// real-AWS certification that spends real money) both refuse outside CI
// unless CheckMaintainerAllow (maintainerguard.go) finds
// ~/.config/choudoufu/allow-heavy-runs naming a still-future instant - see
// that file for the guard itself.
//
// What CheckMaintainerAllow cannot know is that a laptop is no longer the
// only place either command runs: .github/workflows/live-cert.yml and
// .github/workflows/gauntlet.yml's `dispatch-approval` job now run them in
// GitHub Actions, each gated behind a required-reviewer environment, so a
// human who hits this refusal has a real next step besides waiting for the
// nightly schedule or writing the allow file. withDispatchHint appends
// exactly that - the workflow file and the `gh workflow run` line - to
// whatever CheckMaintainerAllow already said, at the two call sites
// (cmdRun in main.go, RunLiveCert in livecert.go) rather than teaching the
// guard itself about workflows it predates.
func withDispatchHint(err error, workflow, dispatchLine string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(
		"%w\nheavy runs are dispatched, never local (HANDOFF.md \"Heavy runs are dispatched, "+
			"approved and never local\"): .github/workflows/%s runs this in GitHub Actions and "+
			"waits for the maintainer's own approval on its environment before anything starts.\n"+
			"Dispatch it instead:\n  %s",
		err, workflow, dispatchLine,
	)
}
