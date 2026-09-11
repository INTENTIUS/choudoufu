// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "fmt"

// Heavy runs are the maintainer's, never a local session's (rule added
// 2026-09-11, worker-common.md). `gauntlet run` (a full estate pass against
// the pinned emulator, minutes to hours) and `gauntlet live-cert` (a real-AWS
// certification that spends real money) are the two commands this repo
// classifies as "heavy": slow enough, or dangerous enough, that starting one
// from a laptop or an unattended local agent session is the exact failure
// mode .github/workflows/gauntlet.yml and live-cert.yml exist to remove.
// Both workflows are workflow_dispatch-only and gated behind a GitHub
// environment with a required reviewer, so dispatching one still waits on
// the maintainer's own approval click - this guard is the second,
// independent layer that stops the command from ever starting outside that
// path in the first place, the same "belt and suspenders" shape
// LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY already uses (livecert.go).
//
// localHeavyRunAllowed takes getenv rather than reading os.Getenv itself so
// the decision is a pure function a test can drive against a fake
// environment, with no process env to mutate and restore.
//
// Allowed when either holds:
//
//   - This process is running inside CI. GitHub Actions sets both
//     GITHUB_ACTIONS=true and CI=true on every runner (documented default
//     environment variables); checking both means a differently-configured
//     CI system that only sets the generic CI=true still counts, without
//     this guard having to special-case GitHub by name.
//   - CHOUDOUFU_LOCAL_HEAVY_RUN=maintainer is set by hand. This is the
//     env-var half of the guard only: the second half - an allow-file the
//     maintainer plants locally - lives on live/maintainer-run-guard
//     (unmerged as of this writing; see that branch for the file itself).
//     Until it merges, the env var alone is deliberately enough to run
//     locally, matching the existing LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY
//     guard's own shape: a single, explicit, hard-to-typo opt-in rather than
//     a silent default.
func localHeavyRunAllowed(getenv func(string) string) bool {
	if getenv("GITHUB_ACTIONS") == "true" || getenv("CI") == "true" {
		return true
	}
	return getenv("CHOUDOUFU_LOCAL_HEAVY_RUN") == "maintainer"
}

// refuseLocalHeavyRun returns nil when localHeavyRunAllowed(getenv), and
// otherwise a refusal naming the workflow file that runs this command for
// real and the exact `gh workflow run` line that dispatches it - the two
// things the message needs so refusing does not just stop the run, it also
// says what to do instead.
func refuseLocalHeavyRun(getenv func(string) string, workflow, dispatchLine string) error {
	if localHeavyRunAllowed(getenv) {
		return nil
	}
	return fmt.Errorf(
		"refusing: this is a heavy run (real time or real money) and this is not CI "+
			"(GITHUB_ACTIONS/CI are unset) and CHOUDOUFU_LOCAL_HEAVY_RUN=maintainer is not set.\n"+
			"Heavy runs are dispatched, never local (worker-common.md, 2026-09-11): "+
			".github/workflows/%s runs this in GitHub Actions and waits for the maintainer's "+
			"approval on the environment before anything starts.\n"+
			"Dispatch it instead:\n  %s",
		workflow, dispatchLine,
	)
}
