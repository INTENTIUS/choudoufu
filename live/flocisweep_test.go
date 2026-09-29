// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This file is issue #1312.
//
// A crossing script SIGKILLed mid-run leaves its floci container running:
// the EXIT trap cannot fire on SIGKILL, and `--rm` removes a container when
// the container exits, not when the script does. From outside, that leaked
// container and a concurrent run's are identical, so live/e2e/lib/gauntlet.sh
// labels every container with its owner (pid and that pid's start time) and
// gauntlet_sweep_leaked_floci removes only what the labels prove is dead.
//
// live/e2e/selftest-floci-sweep.sh drives the shipped sweeper against real
// throwaway containers, because the property is about what docker does and
// a stub would measure the stub. This test is what runs it - the shape
// live/smoke_teardown_test.go established for the smoke selftests - and its
// wiring into CI is the floci tier: `make test-floci` runs it with
// TF_FLOCI_TEST=1, where docker and the pinned image are promised and a
// missing one is a failure, not a skip. Anywhere else it runs when docker
// and the image are present and skips, saying why, when they are not: a
// developer's `just ci` on a laptop with docker running exercises it, and
// one without docker is not turned red by a check it cannot host.

// flociSweepSelftest is the script, relative to this package.
const flociSweepSelftest = "e2e/selftest-floci-sweep.sh"

// flociSweepBound is how long the selftest gets before it is killed and
// this test fails. Measured at about 25s on a 2026-09-21 laptop: eight
// container starts, one kill with a bounded wait, three sweeps and one
// re-run of a single case against a mutated copy of the library. A run
// anywhere near this bound is a hang rather than a slow machine.
const flociSweepBound = 5 * time.Minute

// flociSweepPrereqs says why the selftest cannot run here, or "" when it can.
func flociSweepPrereqs(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return "docker is not on PATH"
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		return "docker is not running"
	}
	image, err := os.ReadFile("floci-image")
	if err != nil {
		t.Fatalf("reading live/floci-image: %v", err)
	}
	if err := exec.Command("docker", "image", "inspect", strings.TrimSpace(string(image))).Run(); err != nil {
		return "the pinned image " + strings.TrimSpace(string(image)) + " is not present locally, and this check does not pull over the network"
	}
	return ""
}

func TestFlociSweepSelftestPasses(t *testing.T) {
	if _, err := os.Stat(flociSweepSelftest); err != nil {
		t.Fatalf("%s is missing: %v\nIt is the only thing that measures the leaked-container sweeper (#1312); "+
			"if it was renamed, rename it here too rather than dropping the wiring.", flociSweepSelftest, err)
	}
	if reason := flociSweepPrereqs(t); reason != "" {
		if os.Getenv("TF_FLOCI_TEST") != "" {
			t.Fatalf("TF_FLOCI_TEST is set, so this tier promised docker and the pinned image, but %s", reason)
		}
		t.Skipf("skipped: %s. `make test-floci` runs this with TF_FLOCI_TEST=1, where that is a failure.", reason)
	}

	ctx, cancel := context.WithTimeout(context.Background(), flociSweepBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", flociSweepSelftest)
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("%s did not finish within %s and was killed. Its output up to the kill:\n%s",
			flociSweepSelftest, flociSweepBound, out.String())
	}

	// The verdict is the script's own lines, not its exit code. A summary
	// that says PASS over a run that measured nothing is the failure this
	// repository keeps finding, so both are required to agree.
	text := out.String()
	if err != nil {
		t.Fatalf("%s failed (%v). Its own output says which container was decided wrongly:\n%s",
			flociSweepSelftest, err, text)
	}
	if strings.Contains(text, "FAIL:") && !strings.Contains(text, "mutant |") {
		t.Errorf("%s exited 0 while printing a FAIL line:\n%s", flociSweepSelftest, text)
	}
	if !strings.Contains(text, "PASS: selftest-floci-sweep") {
		t.Errorf("%s exited 0 without printing its PASS line, so nothing here shows it measured anything:\n%s",
			flociSweepSelftest, text)
	}
	// Its last case re-runs the first against a copy of the library whose
	// liveness check has been removed, and requires it to fail. Without
	// that line the suite above could be passing over a condition it
	// cannot observe.
	if !strings.Contains(text, "fails against a library that never asks whether the owner is alive") {
		t.Errorf("%s did not run its own mutation case, so nothing shows these checks can go red:\n%s",
			flociSweepSelftest, text)
	}
}
