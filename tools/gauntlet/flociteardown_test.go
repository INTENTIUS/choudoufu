// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gauntlet_floci_teardown (issue #1299) is exercised here the way
// gauntlet_k8s_wait_all is in k8swaitall_test.go: a stub `docker` first on
// PATH, sourcing the real library and calling the real function.
//
// A stub rather than Docker, deliberately, and not only for speed. The three
// states that matter - container up, container dead, container never started
// - are a nuisance to produce for real and trivial to state here, and the
// stub also records the ORDER of the calls, which is the one property the
// whole fix rests on: read the container, then remove it. Reverse those and
// every postmortem is empty while every other check still passes.

// fakeDocker writes a stub `docker` into a temp dir and returns a PATH with
// that dir first, plus the path of the file the stub logs its calls to.
//
// state is what `docker inspect` will report for any container name: the
// literal output of the --format string, or the empty string to make inspect
// fail the way it does for a container that does not exist.
func fakeDocker(t *testing.T, state string) (path, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls.log")
	script := fmt.Sprintf(`#!/usr/bin/env bash
printf '%%s\n' "$1" >> %q
case "$1" in
  inspect)
    state=%q
    if [ -z "$state" ]; then
      echo "Error: No such object: $4" >&2
      exit 1
    fi
    printf '%%s\n' "$state"
    ;;
  logs)
    printf 'emulator line one\nemulator line two\n'
    ;;
  rm)
    ;;
  *)
    echo "fake docker: unexpected call: $*" >&2
    exit 97
    ;;
esac
`, calls, state)
	p := filepath.Join(dir, "docker")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH"), calls
}

// callTeardown sources the real library and calls the real function under
// `set -euo pipefail`, which is what almost every crossing script runs under,
// and from an EXIT trap, which is where every crossing script calls it from.
// Both matter: a trap's last command supplies the process's exit status, so a
// teardown that returns nonzero can turn a passing estate red.
func callTeardown(t *testing.T, path string, names ...string) (out string, rc int) {
	t.Helper()
	root := testRoot(t)
	lib := filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh")
	if _, err := os.Stat(lib); err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, fmt.Sprintf("%q", n))
	}
	script := fmt.Sprintf(`set -euo pipefail
export PATH=%q
source %q
cleanup() { gauntlet_floci_teardown %s; }
trap cleanup EXIT
exit 0
`, path, lib, strings.Join(quoted, " "))
	b, err := runBash(script)
	rc = 0
	if err != nil {
		rc = 1
	}
	return string(b), rc
}

// TestFlociTeardownIsSilentForARunningContainer: the happy path. Every estate
// that passes ends with its containers still up, and this runs in 61 of them,
// so a line here would be 61 logs of noise for no signal.
func TestFlociTeardownIsSilentForARunningContainer(t *testing.T) {
	path, calls := fakeDocker(t, "running 0 false 2026-09-18T00:00:00Z 0001-01-01T00:00:00Z")

	out, rc := callTeardown(t, path, "choudoufu-x-1")
	if rc != 0 {
		t.Errorf("teardown made a passing script exit nonzero: %s", out)
	}
	if strings.Contains(out, "FLOCI-POSTMORTEM") {
		t.Errorf("a container that was still running produced a postmortem:\n%s", out)
	}
	if !strings.Contains(readFile(t, calls), "rm") {
		t.Error("the container was never removed")
	}
}

// TestFlociTeardownIsSilentForAContainerThatNeverStarted: the commonest
// failure there is - a missing tool, an unfetched corpus module - exits
// before `docker run`, and its trap still fires.
//
// Found by running it: an earlier draft printed a line for an absent
// container, and corpus-leynos-monitoring's missing-corpus exit produced
// three of them on the very first real run. Absent now means "never started",
// because without --rm a container that ever existed is still listed even
// after it dies.
func TestFlociTeardownIsSilentForAContainerThatNeverStarted(t *testing.T) {
	path, _ := fakeDocker(t, "")

	out, rc := callTeardown(t, path, "choudoufu-x-1", "choudoufu-x-green-1", "choudoufu-x-oracle-1")
	if rc != 0 {
		t.Errorf("teardown failed for containers that were never started: %s", out)
	}
	if strings.Contains(out, "FLOCI-POSTMORTEM") {
		t.Errorf("a script that exited before starting any container got a postmortem anyway:\n%s", out)
	}
}

// TestFlociTeardownReportsAContainerThatDied is #1299 itself: this is the
// output that did not exist before, on the run where it is the only thing
// that can tell a dead emulator from a wrong assertion.
func TestFlociTeardownReportsAContainerThatDied(t *testing.T) {
	path, calls := fakeDocker(t, "exited 137 true 2026-09-18T13:48:19Z 2026-09-18T13:48:33Z")

	out, rc := callTeardown(t, path, "choudoufu-x-1")
	if rc != 0 {
		t.Errorf("teardown turned a dead container into a failing script: %s", out)
	}
	for _, want := range []string{
		"FLOCI-POSTMORTEM choudoufu-x-1: DIED BEFORE TEARDOWN",
		"exit_code=137",
		"oom_killed=true",
		"finished=2026-09-18T13:48:33Z",
		"LOG emulator line one",
		"LOG emulator line two",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the postmortem does not carry %q:\n%s", want, out)
		}
	}

	// The ordering, observed rather than read off the source: inspect and
	// logs must both come before rm, or there is nothing left to read.
	seq := strings.Fields(readFile(t, calls))
	iInspect, iLogs, iRm := indexOf(seq, "inspect"), indexOf(seq, "logs"), indexOf(seq, "rm")
	if iInspect < 0 || iLogs < 0 || iRm < 0 {
		t.Fatalf("expected inspect, logs and rm to all be called; got %v", seq)
	}
	if iInspect > iRm || iLogs > iRm {
		t.Errorf("the container was removed before it was read (calls in order: %v)", seq)
	}
}

// TestFlociTeardownSurvivesADockerThatIsNotThere: a script can fail before
// Docker is even checked for, and its trap still runs. The teardown must not
// become the failure.
func TestFlociTeardownSurvivesADockerThatIsNotThere(t *testing.T) {
	dir := t.TempDir() // an empty dir as the entire PATH: no docker anywhere
	out, rc := callTeardown(t, dir, "choudoufu-x-1")
	if rc != 0 {
		t.Errorf("teardown failed a script on a machine with no docker: %s", out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("teardown printed something with no docker present: %q", out)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}
