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

// ---------------------------------------------------------------------------
// #1307: the wait, and the recipe it replaces.
//
// CLAUDE.md documented `while [ ! -f ci.rc ]; do sleep 15; done` as a correct
// wait, on the strength of `run` deleting the gate files first. That holds
// once `run` has started. The only reason to start such a loop is to run it
// ALONGSIDE the gate, and in the window before `run` reaches its `rm -f` the
// loop matches whatever gate file the worktree was already carrying - which
// is routinely one, because every worker is told to leave ci.rc/ci.out/ci.meta
// behind for the orchestrator to read.
// ---------------------------------------------------------------------------

// seedStaleGate writes a complete, green gate for a commit that is not HEAD -
// the exact residue #1307 found, and the shape every worker's worktree holds
// between units.
func seedStaleGate(t *testing.T, dir, sha string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "ci.rc"), []byte("0\n"), 0o644); err != nil {
		t.Fatalf("seeding ci.rc: %v", err)
	}
	meta := "sha=" + sha + "\nrun=seeded\nstart=x\nend=x\n"
	if err := os.WriteFile(filepath.Join(dir, "ci.meta"), []byte(meta), 0o644); err != nil {
		t.Fatalf("seeding ci.meta: %v", err)
	}
}

// oldRecipe is CLAUDE.md's wait, verbatim except for a 1s poll instead of 15
// so the test is quick. It is here to be RUN, not described: the claim that
// it returns a stale green is only worth making if the test watches it do so.
func oldRecipe(t *testing.T, dir string) (string, time.Duration) {
	t.Helper()
	start := time.Now()
	cmd := exec.Command("bash", "-c", `while [ ! -f ci.rc ]; do sleep 1; done; echo "ci.rc=$(cat ci.rc)"`)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("old recipe failed to run at all: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out)), time.Since(start)
}

// TestOldWaitRecipeReadsAStaleGateAndWaitDoesNot is #1307's red arm and its
// green arm in one test, against one constructed state: a stale complete
// green gate from an earlier commit sits in the worktree, and no run has
// started yet.
//
// This is the "prove your check can fail" half. If a future edit made `wait`
// return on mere existence again, the second half of this test would go green
// while the first stayed green, and the two would agree - which is the point:
// the assertion is on the DIFFERENCE between the two waits, so it cannot pass
// by both of them being wrong the same way.
func TestOldWaitRecipeReadsAStaleGateAndWaitDoesNot(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")
	seedStaleGate(t, dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	// RED: the documented recipe. It must return at once, reporting a pass,
	// off a file no run in this test ever wrote.
	got, took := oldRecipe(t, dir)
	if got != "ci.rc=0" {
		t.Fatalf("the documented recipe did not read the stale gate as a green (%q) - the reproduction no longer holds and this test is not measuring #1307", got)
	}
	if took > 3*time.Second {
		t.Errorf("the documented recipe took %s; it was expected to match the leftover file immediately", took)
	}

	// GREEN: the replacement, same state, must refuse rather than report
	// that leftover, and must say why.
	out, code := ciGate(t, dir, "wait", "--timeout", "3", "--interval", "1")
	if code == 0 {
		t.Fatalf("wait exited 0 against a stale gate with no run started; want a refusal.\noutput: %s", out)
	}
	if !strings.Contains(out, "TIMED OUT") {
		t.Errorf("wait's refusal did not name the reason (TIMED OUT): %s", out)
	}
	if strings.Contains(out, "GREEN") {
		t.Errorf("wait reported GREEN off a gate it never saw written: %s", out)
	}
}

// TestWaitReturnsTheRunItWaitedFor is the case the whole subcommand exists
// for: the stale gate is present AND a real run is starting at the same
// moment. The wait must step over the leftover and report the run's own
// verdict.
func TestWaitReturnsTheRunItWaitedFor(t *testing.T) {
	dir := newRepo(t)
	sha := commit(t, dir, "f.txt", "one", "initial")
	seedStaleGate(t, dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	// A run that is deliberately slower than the wait's first poll, so the
	// wait genuinely has to survive an iteration in which the only gate
	// present is the stale one.
	runCmd := exec.Command("bash", scriptPath(t), "run", "--", "bash", "-c", "sleep 3; true")
	runCmd.Dir = dir
	if err := runCmd.Start(); err != nil {
		t.Fatalf("starting ci-gate.sh run: %v", err)
	}
	t.Cleanup(func() {
		if runCmd.Process != nil {
			_ = runCmd.Process.Kill()
			_ = runCmd.Wait()
		}
	})

	out, code := ciGate(t, dir, "wait", "--timeout", "60", "--interval", "1")
	if code != 0 {
		t.Fatalf("wait exited %d for a run that completed green at HEAD, want 0.\noutput: %s", code, out)
	}
	if !strings.Contains(out, "GREEN") || !strings.Contains(out, sha) {
		t.Errorf("wait did not report the new run's own green at %s: %s", sha, out)
	}
	_ = runCmd.Wait()
}

// TestWaitDoesNotAcceptACompletedGateAtTheSameSha is the residual hole that
// keying on the sha ALONE would leave, and the reason ci.meta now carries a
// per-run id. Re-gating an unchanged HEAD - a flake, a re-measure - leaves a
// leftover that names exactly the right commit. Only its identity says it
// belongs to the previous run.
//
// The new run is red, so the two verdicts differ: if the wait returned the
// leftover it would say GREEN, and the test can tell.
func TestWaitDoesNotAcceptACompletedGateAtTheSameSha(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")

	if _, code := ciGate(t, dir, "run", "--", "true"); code != 0 {
		t.Fatalf("the first run did not exit 0")
	}
	if out, code := ciGate(t, dir, "check"); code != 0 {
		t.Fatalf("setup: check should read the first run as GREEN at HEAD, got %d: %s", code, out)
	}

	// Same HEAD, same worktree, a second run that fails.
	runCmd := exec.Command("bash", scriptPath(t), "run", "--", "bash", "-c", "sleep 3; false")
	runCmd.Dir = dir
	if err := runCmd.Start(); err != nil {
		t.Fatalf("starting the second run: %v", err)
	}
	t.Cleanup(func() {
		if runCmd.Process != nil {
			_ = runCmd.Process.Kill()
			_ = runCmd.Wait()
		}
	})

	out, code := ciGate(t, dir, "wait", "--timeout", "60", "--interval", "1")
	_ = runCmd.Wait()
	if strings.Contains(out, "GREEN") {
		t.Fatalf("wait returned the PREVIOUS run's green for an unchanged HEAD; the second run was red.\noutput: %s", out)
	}
	if code == 0 {
		t.Fatalf("wait exited 0 while the run it waited for was red.\noutput: %s", out)
	}
	if !strings.Contains(out, "RED") {
		t.Errorf("wait did not report the second run's own RED verdict: %s", out)
	}
}

// TestWaitRefusesAGateForAnOlderCommit: HEAD moves while the wait is running
// and the gate that lands is for the commit before it. `check` calls that
// STALE; the wait must not paper over it by returning early on a gate that
// merely appeared.
func TestWaitRefusesAGateForAnOlderCommit(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")

	// A completed gate for the first commit, then HEAD moves on.
	if _, code := ciGate(t, dir, "run", "--", "true"); code != 0 {
		t.Fatalf("the first run did not exit 0")
	}
	newSHA := commit(t, dir, "f.txt", "two", "more work, gate not re-run")

	out, code := ciGate(t, dir, "wait", "--timeout", "3", "--interval", "1")
	if code == 0 {
		t.Fatalf("wait exited 0 with only a gate for an older commit than %s present.\noutput: %s", newSHA, out)
	}
	if !strings.Contains(out, "TIMED OUT") {
		t.Errorf("wait's refusal did not name the reason (TIMED OUT): %s", out)
	}
}

// TestWaitRejectsNonsenseArguments: a mistyped flag must stop the wait rather
// than be silently ignored, because an ignored --timeout is an unbounded
// foreground call and an ignored --interval is a busy loop.
func TestWaitRejectsNonsenseArguments(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")

	for _, args := range [][]string{
		{"wait", "--timeout", "soon"},
		{"wait", "--interval", "0"},
		{"wait", "--forever"},
		{"wait", "--timeout"},
	} {
		out, code := ciGate(t, dir, args...)
		if code == 0 {
			t.Errorf("ci-gate.sh %s exited 0; want a refusal.\noutput: %s", strings.Join(args, " "), out)
		}
	}
}

// TestRunStampsAPerRunIdentity pins the field `wait`'s third condition reads.
// Two runs at the same commit must produce different ci.meta content, and
// `check` must still be indifferent to it.
func TestRunStampsAPerRunIdentity(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")

	read := func() string {
		b, err := os.ReadFile(filepath.Join(dir, "ci.meta"))
		if err != nil {
			t.Fatalf("reading ci.meta: %v", err)
		}
		return string(b)
	}

	if _, code := ciGate(t, dir, "run", "--", "true"); code != 0 {
		t.Fatalf("first run did not exit 0")
	}
	first := read()
	if !strings.Contains(first, "\nrun=") {
		t.Fatalf("ci.meta carries no run= line, so two runs at one sha are indistinguishable: %q", first)
	}

	if _, code := ciGate(t, dir, "run", "--", "true"); code != 0 {
		t.Fatalf("second run did not exit 0")
	}
	second := read()
	if first == second {
		t.Errorf("two runs at the same commit wrote byte-identical ci.meta, so wait cannot tell them apart:\n%q", first)
	}

	if out, code := ciGate(t, dir, "check"); code != 0 {
		t.Errorf("check exited %d for a fresh green gate carrying a run= line: %s", code, out)
	}
}
