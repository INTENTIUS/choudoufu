// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package cigate tests scripts/ci-gate.sh, issue #519's fix for a ci.rc that
// can read green from a run that never finished (or finished for a commit
// that is no longer HEAD). The script itself has to stay a shell script - it
// wraps `just ci` - so it is exercised here the way tools/gauntlet's
// bash_test.go exercises live/e2e/lib/gauntlet.sh: shell out to the real
// script against a throwaway git repository and assert on its real output,
// rather than reimplementing its logic in Go and testing that instead.
package cigate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scriptPath finds scripts/ci-gate.sh relative to this test's package
// directory (tools/ci-gate), so the test does not depend on the working
// directory `go test` happens to be invoked from.
func scriptPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ci-gate.sh"))
	if err != nil {
		t.Fatalf("resolving scripts/ci-gate.sh: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("scripts/ci-gate.sh not found at %s: %v", abs, err)
	}
	return abs
}

// newRepo makes a throwaway git repository in a temp directory and returns
// its path. ci-gate.sh reads `git rev-parse --show-toplevel` and HEAD from
// wherever it is run, so a real repo - not a fixture of files - is what lets
// this test exercise the actual freshness check rather than a stand-in for
// it.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init", "-q")
	run(t, dir, "git", "config", "user.email", "ci-gate-test@example.com")
	run(t, dir, "git", "config", "user.name", "ci-gate-test")
	return dir
}

// commit writes content to name and commits it, returning the new HEAD sha.
func commit(t *testing.T, dir, name, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	run(t, dir, "git", "add", name)
	run(t, dir, "git", "commit", "-q", "-m", message)
	return headSHA(t, dir)
}

func headSHA(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// ciGate runs scripts/ci-gate.sh with the given subcommand and args inside
// dir, returning combined output and the exit code (0 when it exits clean).
func ciGate(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{scriptPath(t)}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("running ci-gate.sh %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	return string(out), code
}

// TestCheckRefusesWithNoGate is the base case: nothing has run at all, so
// `check` must refuse rather than treat an absent ci.rc as anything but "no
// completed run to trust".
func TestCheckRefusesWithNoGate(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")

	out, code := ciGate(t, dir, "check")
	if code == 0 {
		t.Fatalf("check exited 0 with no ci.rc at all; want a refusal.\noutput: %s", out)
	}
	if !strings.Contains(out, "NO GATE") {
		t.Errorf("check's refusal did not name the reason (NO GATE): %s", out)
	}
}

// TestRunThenCheckPassesAtCurrentHead is the happy path: a real `run`
// against the current HEAD, immediately followed by `check`, must pass.
func TestRunThenCheckPassesAtCurrentHead(t *testing.T) {
	dir := newRepo(t)
	sha := commit(t, dir, "f.txt", "one", "initial")

	runOut, runCode := ciGate(t, dir, "run", "--", "true")
	if runCode != 0 {
		t.Fatalf("run -- true exited %d, want 0.\noutput: %s", runCode, runOut)
	}

	for _, f := range []string{"ci.rc", "ci.out", "ci.meta"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("run left no %s: %v", f, err)
		}
	}
	meta, err := os.ReadFile(filepath.Join(dir, "ci.meta"))
	if err != nil {
		t.Fatalf("reading ci.meta: %v", err)
	}
	if !strings.Contains(string(meta), "sha="+sha) {
		t.Errorf("ci.meta does not record the HEAD sha it ran at (%s): %s", sha, meta)
	}

	out, code := ciGate(t, dir, "check")
	if code != 0 {
		t.Fatalf("check exited %d after a fresh passing run at HEAD, want 0.\noutput: %s", code, out)
	}
	if !strings.Contains(out, "GREEN") {
		t.Errorf("check's success did not say GREEN: %s", out)
	}
}

// TestCheckRefusesRedRun is the companion to the happy path: a completed,
// fresh run that failed must still read as a refusal, never as a pass.
func TestCheckRefusesRedRun(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")

	if _, code := ciGate(t, dir, "run", "--", "false"); code == 0 {
		t.Fatalf("run -- false exited 0, want nonzero (it should mirror the wrapped command's exit code)")
	}

	out, code := ciGate(t, dir, "check")
	if code == 0 {
		t.Fatalf("check exited 0 for a fresh but failing run; want a refusal.\noutput: %s", out)
	}
	if !strings.Contains(out, "RED") {
		t.Errorf("check's refusal did not name the reason (RED): %s", out)
	}
}

// TestCheckRefusesGateFromOlderCommit is the acceptance criterion from #519
// that deleting ci.rc before the run does NOT cover on its own: a `ci.rc`
// that genuinely completed, and reads 0, but for a commit the worktree has
// since moved past. This is the subtler case the issue calls out by name.
func TestCheckRefusesGateFromOlderCommit(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")

	if _, code := ciGate(t, dir, "run", "--", "true"); code != 0 {
		t.Fatalf("run -- true at the first commit did not exit 0")
	}

	// More work lands in the same worktree; nobody re-ran the gate.
	newSHA := commit(t, dir, "f.txt", "two", "second commit, gate not re-run")

	out, code := ciGate(t, dir, "check")
	if code == 0 {
		t.Fatalf("check exited 0 for a ci.rc written at an OLDER commit than HEAD (%s); want a refusal.\noutput: %s", newSHA, out)
	}
	if !strings.Contains(out, "STALE") {
		t.Errorf("check's refusal did not name the reason (STALE): %s", out)
	}
}

// TestRunDeletesStaleGateBeforeStarting is #519's headline scenario,
// reproduced deterministically: an old, complete, green ci.rc/ci.meta from
// an EARLIER commit sits in the worktree (exactly what the bug found on
// 2026-08-28/29). A new `run` starts. Before the wrapped command's first
// observable side effect even happens, the stale files must already be
// gone - so a kill at any point after that has nothing stale left to read
// as a pass.
//
// The wrapped command touches a sentinel file as its very first action, and
// the test polls only for that sentinel (never a fixed sleep) before
// asserting ci.rc is absent, so the ordering this test relies on - delete,
// then start the command - is the same ordering the script's own source
// enforces, not a timing coincidence.
func TestRunDeletesStaleGateBeforeStarting(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")

	// Seed a stale, complete, GREEN gate as if an earlier run had finished
	// here - the exact shape #519 found sitting in a worker's worktree.
	if err := os.WriteFile(filepath.Join(dir, "ci.rc"), []byte("0\n"), 0o644); err != nil {
		t.Fatalf("seeding stale ci.rc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ci.meta"), []byte("sha=deadbeef\nstart=x\nend=x\n"), 0o644); err != nil {
		t.Fatalf("seeding stale ci.meta: %v", err)
	}

	sentinel := filepath.Join(dir, "started")
	cmd := exec.Command("bash", scriptPath(t), "run", "--", "bash", "-c", "touch started; sleep 100")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting ci-gate.sh run: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(sentinel); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("wrapped command never touched its sentinel file within 10s; run may not have started it")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The sentinel exists, which - by the script's own instruction order
	// (delete, THEN run the command) - proves the delete already happened.
	if _, err := os.Stat(filepath.Join(dir, "ci.rc")); err == nil {
		t.Errorf("ci.rc still exists after the wrapped command started; the stale gate was not deleted before the run began")
	}
	if _, err := os.Stat(filepath.Join(dir, "ci.meta")); err == nil {
		t.Errorf("ci.meta still exists after the wrapped command started; the stale gate was not deleted before the run began")
	}

	// Now kill it mid-run, simulating the SIGTERM-under-load #519 was found
	// by, and confirm the gate stays refused rather than reading the killed
	// run's leftovers as a pass.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing the mid-run process: %v", err)
	}
	_ = cmd.Wait()

	out, code := ciGate(t, dir, "check")
	if code == 0 {
		t.Fatalf("check exited 0 after the run was killed mid-flight; want a refusal.\noutput: %s", out)
	}
	if !strings.Contains(out, "NO GATE") {
		t.Errorf("check's refusal after a mid-run kill did not say NO GATE: %s", out)
	}
}

// TestCheckRefusesIncompleteGate covers the narrower window between ci.rc
// being written and ci.meta being written: a kill exactly there must not
// read as a pass either, since ci.rc alone carries no run identity.
func TestCheckRefusesIncompleteGate(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")

	// Simulate the script having written ci.rc but never reached the
	// ci.meta rename (a kill between the two writes).
	if err := os.WriteFile(filepath.Join(dir, "ci.rc"), []byte("0\n"), 0o644); err != nil {
		t.Fatalf("seeding ci.rc: %v", err)
	}

	out, code := ciGate(t, dir, "check")
	if code == 0 {
		t.Fatalf("check exited 0 with ci.rc present and no ci.meta; want a refusal.\noutput: %s", out)
	}
	if !strings.Contains(out, "INCOMPLETE") {
		t.Errorf("check's refusal did not name the reason (INCOMPLETE): %s", out)
	}
}
