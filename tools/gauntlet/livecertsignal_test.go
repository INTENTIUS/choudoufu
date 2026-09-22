// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This file is issue #1324's proof. It is the FAST half: a stub script with
// a real teardown trap and a real grandchild, signalled for real, in seconds.
// The slow half - one floci scale-1 signalled run of the actual harness - is
// run by hand and reported in the pull request, because a full live-cert run
// is minutes and needs docker, terraform and the AWS CLI.
//
// What the stub buys is not speed alone. live/live-cert/terralith-scale.sh
// must never be executed by a test (issue #1380: a guard inside it that fails
// to fire does not print a red line, it lets the run continue into a cold
// deploy), and every property #1324 is about - the process group, the
// forwarding, the waiting, the run record, the orphan - is a property of the
// Go supervisor and not of any particular script.

// signalStubScript writes a fake live-cert script shaped like the real ones
// in the two ways this issue turns on.
//
// It holds a BARE grandchild - `sleep &`, with no trap of its own and
// deliberately NOT killed by the script's own trap. That is the assertion
// that Setpgid and the negative-pgid kill are doing something: signalling
// bash alone leaves such a child running, which is exactly what the #1324
// process listing shows (a `terraform plan` two levels down that nothing had
// signalled). If the grandchild is gone afterwards, the signal reached the
// GROUP.
//
// And its trap prints the same teardown vocabulary the real scripts do -
// `=== TEARDOWN ... ===` and `VERIFIED EMPTY by listing` - because that is
// what TeardownConfirmed reads. Nothing here fakes the confirmation: the
// trap has to actually run to print it.
func signalStubScript(t *testing.T, root, estate string, opts stubOpts) (readyFile, pidFile, trapFile string) {
	t.Helper()
	dir := filepath.Join(root, "live", "live-cert")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	readyFile = filepath.Join(work, "ready")
	pidFile = filepath.Join(work, "pids")
	trapFile = filepath.Join(work, "trap-finished")

	teardown := "  VERIFIED EMPTY by listing: nothing matching this run remains"
	if opts.teardownDirty {
		teardown = "  STILL NOT EMPTY after destroy and sweep - see the listing above"
	}

	body := "#!/usr/bin/env bash\n" +
		"set -uo pipefail\n" +
		"echo \"GAUNTLET protocol=1\"\n" +
		// A bare grandchild in this script's process group. No trap, and
		// the teardown below does not touch it on purpose.
		"sleep 120 &\n" +
		"GRAND=$!\n" +
		"printf 'bash=%s grand=%s\\n' \"$$\" \"$GRAND\" > " + shQuote(pidFile) + "\n" +
		"teardown() {\n" +
		// The script's OWN stamp of when its trap began: the file's
		// mtime, taken by the trap's first command, before it has
		// printed anything. It is the ground truth a re-send is
		// placed against (#1464); the watcher's time is later by a
		// pipe and a goroutine. bash 3.2 has no $EPOCHREALTIME, and
		// an mtime is nanoseconds on APFS, ext4 and tmpfs alike.
		"  touch " + shQuote(trapStartedStamp(trapFile)) + "\n" +
		"  echo \"=== TEARDOWN (target=floci run=stub) ===\"\n" +
		// A real teardown takes time. This one takes enough that a
		// supervisor which exited under the trap instead of waiting for
		// it would be measurably gone before the trap finished.
		"  sleep " + strconv.Itoa(opts.teardownSeconds) + "\n" +
		"  echo " + shQuote(teardown) + "\n" +
		"  touch " + shQuote(trapFile) + "\n" +
		"  echo \"GAUNTLET stage=cold_deploy verdict=fail duration_s=1 detail=stopped by a signal\"\n" +
		"}\n" +
		"trap 'teardown; trap - EXIT; exit 130' INT TERM\n" +
		"trap teardown EXIT\n" +
		"touch " + shQuote(readyFile) + "\n" +
		"sleep " + strconv.Itoa(opts.runSeconds) + "\n" +
		// The clean path reaps its own background child. The signalled
		// path deliberately does NOT - there it has to be the group
		// signal that gets it, which is the assertion. This asymmetry
		// is also a real property of the harness: a background child
		// that outlives the script holds the stdout pipe open, and
		// cmd.WaitDelay then turns a finished run into "WaitDelay
		// expired before I/O complete" 30 seconds later. That is why
		// terralith-scale.sh's teardown stops the heartbeat rather
		// than leaving it to be reaped.
		"kill \"$GRAND\" 2>/dev/null; wait \"$GRAND\" 2>/dev/null\n" +
		"echo \"GAUNTLET stage=cold_deploy verdict=pass duration_s=1 detail=ran to the end\"\n"

	if err := os.WriteFile(filepath.Join(dir, estate+".sh"), []byte(body), 0o755); err != nil { //nolint:gosec // a fake script in a test's own temp dir
		t.Fatal(err)
	}
	return readyFile, pidFile, trapFile
}

// trapStartedStamp is the file signalStubScript's trap touches as its first
// act, beside trapFile, which it touches as its last. The two mtimes bracket
// the teardown.
func trapStartedStamp(trapFile string) string {
	return filepath.Join(filepath.Dir(trapFile), "trap-started")
}

// mtimeOf is a file's mtime, and fails the test when the file is missing.
func mtimeOf(t *testing.T, path string) time.Time {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return st.ModTime()
}

type stubOpts struct {
	runSeconds      int
	teardownSeconds int
	teardownDirty   bool
}

// shQuote is single-quoting for a path this test wrote into a temp dir.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func waitForFile(t *testing.T, path string, bound time.Duration) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared within %s", path, bound)
}

func readStubPids(t *testing.T, pidFile string) (bashPid, grandPid int) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("reading the stub's pid file: %v", err)
	}
	for _, field := range strings.Fields(string(data)) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("pid file field %q: %v", field, err)
		}
		switch k {
		case "bash":
			bashPid = n
		case "grand":
			grandPid = n
		}
	}
	if bashPid == 0 || grandPid == 0 {
		t.Fatalf("pid file %q did not name both pids: %s", pidFile, data)
	}
	return bashPid, grandPid
}

// alive reports whether pid still exists. A zombie is not alive for this
// test's purposes, but a child of THIS process that has exited is reaped by
// the exec package before RunLiveCert returns, so signal 0 is enough here.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// TestSignalledLiveCertForwardsToTheGroupAndWaitsForTheTrap is the whole of
// #1324's first proposal in one test.
//
// It asserts four things, and each one is a separate way the 2026-09-11
// incident happened:
//
//	(a) the teardown banner appears           - the trap was reached at all
//	(b) the trap FINISHED before the wait returned - the supervisor did not
//	    exit out from under the teardown it started
//	(c) no process from the run survives      - the whole group was
//	    signalled, not just bash, so the bare grandchild is gone too
//	(d) the run record says signalled          - and PlanLiveCertWrites
//	    writes no row for it
//
// The signal is a real SIGTERM to this test process, which is how an
// operator's `kill -TERM` and a runner's cancellation both arrive. That is
// safe because RunLiveCert installs a handler before it waits and takes it
// down again afterwards; it is also the only way to exercise the handler
// rather than a stand-in for it.
func TestSignalledLiveCertForwardsToTheGroupAndWaitsForTheTrap(t *testing.T) {
	absorbStraySignals(t)
	root := tempCheckout(t)
	ready, pidFile, trapFile := signalStubScript(t, root, "signalestate", stubOpts{runSeconds: 120, teardownSeconds: 2})

	type outcome struct {
		r   *LiveCertResult
		res *ProtocolResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		r, res, _, err := RunLiveCert(root, "signalestate", "floci", "us-east-1", 5, 120)
		done <- outcome{r, res, err}
	}()

	waitForFile(t, ready, 30*time.Second)
	waitForFile(t, pidFile, 30*time.Second)
	bashPid, grandPid := readStubPids(t, pidFile)

	// The process group itself, asserted directly rather than inferred.
	// With Setpgid the script leads its own group, so its pgid is its own
	// pid and is not this test's.
	pgid, err := syscall.Getpgid(bashPid)
	if err != nil {
		t.Fatalf("the stub script (pid %d) has no process group: %v", bashPid, err)
	}
	if pgid != bashPid {
		t.Errorf("the script's process group is %d, not its own pid %d: it was NOT started with Setpgid, so a kill to the negative pgid cannot reach its children (#1324)", pgid, bashPid)
	}
	if mine, _ := syscall.Getpgid(os.Getpid()); pgid == mine {
		t.Errorf("the script shares this process's group (%d); a signal to one is a signal to both, which is how the harness ends up exiting out from under a trap that is still running", pgid)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("sending this test process a SIGTERM: %v", err)
	}

	var got outcome
	select {
	case got = <-done:
	case <-time.After(90 * time.Second):
		t.Errorf("RunLiveCert never returned after the SIGTERM")
		t.Fatalf("diagnostics at the moment of the timeout:\n%s\n%s",
			processGroupTable(bashPid), goroutineDump())
	}
	if got.err != nil {
		t.Fatalf("RunLiveCert: %v", got.err)
	}

	// (a) and (b). The trap file is written at the END of teardown, after
	// its sleep, so its existence at the moment the wait returned is the
	// assertion that RunLiveCert waited rather than exited under it.
	if _, err := os.Stat(trapFile); err != nil {
		t.Errorf("the script's teardown trap did not finish before RunLiveCert returned (%v).\n"+
			"That is the defect: the gauntlet binary exited out from under the teardown it was responsible for, "+
			"which is what left 9,477 resources standing on 2026-09-11 (#1324).", err)
	}
	log := runLogText(t, root, "signalestate")
	if !strings.Contains(log, "=== TEARDOWN ") {
		t.Errorf("the run log has no teardown banner, so the trap never ran at all.\nLog:\n%s", log)
	}

	// (c). The bare grandchild is the one the trap does not touch.
	for _, p := range []struct {
		pid  int
		what string
	}{
		{bashPid, "the script itself"},
		{grandPid, "the script's bare grandchild (the stand-in for the `terraform plan` the incident's process listing found still running)"},
	} {
		if alive(p.pid) {
			t.Errorf("pid %d - %s - is still alive after RunLiveCert returned.\n"+
				"A signal that reaches only the immediate child leaves the work running; Setpgid plus a kill to the "+
				"negative pgid is what makes one stop request reach all of it (#1324).", p.pid, p.what)
		}
	}

	// (d).
	if got.r.State != RunStateSignalledTornDown {
		t.Errorf("run state is %q, want %q: the run was signalled and its teardown printed a verified-empty verdict",
			got.r.State, RunStateSignalledTornDown)
	}
	rec, err := ReadLiveCertRun(root, "signalestate")
	if err != nil {
		t.Fatalf("reading the run record: %v", err)
	}
	if rec.State != RunStateSignalledTornDown || rec.Signal == "" || !rec.TeardownConfirmed {
		t.Errorf("run record says state=%q signal=%q teardown_confirmed=%v; want the signalled-and-confirmed shape with a named signal",
			rec.State, rec.Signal, rec.TeardownConfirmed)
	}
	if ok, why := rec.FinishedMeasurement(rec.Commit); ok {
		t.Errorf("the reader accepted a signalled run as a finished measurement (%q). rc=143 being indistinguishable from success is the whole of #1324.", why)
	}
	if w := PlanLiveCertWrites("aws", got.res, got.r.State); w.LiveCertRow || w.ScaleRecord {
		t.Errorf("a signalled run would write live_cert=%v scale=%v; it must write neither.\nWhy said: %s", w.LiveCertRow, w.ScaleRecord, w.Why)
	}
}

// TestUnsignalledLiveCertStillFinishes is the control. Without it every
// assertion above is satisfied by a change that makes RunLiveCert always
// report a signal, and the suite would be green on a tool that could no
// longer record a certification at all.
func TestUnsignalledLiveCertStillFinishes(t *testing.T) {
	root := tempCheckout(t)
	signalStubScript(t, root, "cleanestate", stubOpts{runSeconds: 0, teardownSeconds: 0})

	r, res, exit, err := RunLiveCert(root, "cleanestate", "floci", "us-east-1", 5, 120)
	if err != nil {
		t.Fatalf("RunLiveCert: %v", err)
	}
	if r.State != RunStateFinished {
		t.Errorf("an unsignalled run reports state %q, want %q (exit %d)", r.State, RunStateFinished, exit)
	}
	rec, err := ReadLiveCertRun(root, "cleanestate")
	if err != nil {
		t.Fatalf("reading the run record: %v", err)
	}
	if ok, why := rec.FinishedMeasurement(rec.Commit); !ok {
		t.Errorf("the reader refused a run that finished: %s", why)
	}
	if w := PlanLiveCertWrites("aws", res, r.State); !w.LiveCertRow {
		t.Errorf("a finished run that spoke a stage writes no live_cert row: %s", w.Why)
	}
}

// runLogText reads the per-estate run log RunLiveCert streams the script into.
func runLogText(t *testing.T, root, estate string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, LogDir, "live-cert-"+estate+".log"))
	if err != nil {
		t.Fatalf("reading the run log: %v", err)
	}
	return string(data)
}

// ── the orphan ──────────────────────────────────────────────────────────

// orphanHelperEnv makes this test binary run one supervised live-cert instead
// of a test suite, so the orphaning can be done to a real process. Re-exec of
// the test binary is the standard way to get a second process without
// building one (os/exec's own tests use it); the alternative here would be
// `go build ./tools/gauntlet` inside a test, which is slower and tests a
// different binary than the one under test.
const (
	orphanHelperEnv   = "GAUNTLET_ORPHAN_HELPER_ROOT"
	orphanHelperName  = "orphanestate"
	orphanHelperOnEnv = "GAUNTLET_ORPHAN_HELPER"
)

// TestOrphanHelperProcess is not a test. It is the child half of
// TestOrphanedLiveCertRunTearsItselfDown, and it returns immediately unless
// that test started it.
func TestOrphanHelperProcess(t *testing.T) {
	if os.Getenv(orphanHelperOnEnv) != "1" {
		t.Skip("not the orphan helper")
	}
	root := os.Getenv(orphanHelperEnv)
	if _, _, _, err := RunLiveCert(root, orphanHelperName, "floci", "us-east-1", 5, 300); err != nil {
		// Written where the parent test can read it; t.Fatalf's output
		// goes nowhere useful from a re-exec.
		_ = os.WriteFile(filepath.Join(root, "helper-error"), []byte(err.Error()), 0o644) //nolint:gosec // a test's own temp dir
	}
	os.Exit(0)
}

// TestOrphanedLiveCertRunTearsItselfDown is the `go run` case, which is the
// case that actually happened.
//
// Measured on go1.26.5 darwin/arm64 while writing this (2026-09-19): a
// `kill -TERM` on a `go run` pid kills the wrapper only. The compiled binary
// reparents to init - the probe logged `go-alive 6 ppid=1` - and keeps going
// with its bash grandchild and that child's own grandchild, trap unrun. No
// signal reaches the binary, so there is nothing for the forwarding above to
// forward: the binary's own ppid is the only evidence it has.
//
// This test builds that shape without `go run`: a bash wrapper backgrounds
// the supervisor and then is killed, which reparents the supervisor to init
// exactly the same way. The assertion is that the supervisor NOTICES, tears
// the script down, and leaves a record saying it was signalled.
func TestOrphanedLiveCertRunTearsItselfDown(t *testing.T) {
	root := tempCheckout(t)
	ready, pidFile, trapFile := signalStubScript(t, root, orphanHelperName, stubOpts{runSeconds: 120, teardownSeconds: 2})

	// The wrapper stands in for `go run`: it starts the real work as a
	// child and holds it. Killing the wrapper is the operator's `kill
	// -TERM <the pid pgrep matched>`.
	wrapper := exec.Command("bash", "-c", `"$1" -test.run=TestOrphanHelperProcess -test.timeout=5m > "$3" 2>&1 & echo $! > "$2"; wait`, "bash", os.Args[0], filepath.Join(root, "helper-pid"), filepath.Join(root, "helper-out")) //nolint:gosec // this test binary, a temp dir
	wrapper.Env = append(os.Environ(),
		orphanHelperOnEnv+"=1",
		orphanHelperEnv+"="+root,
		// Short, so the test does not sit through the production grace
		// period if the fix regresses into a hung wait.
		LiveCertSignalGraceEnv+"=30",
	)
	if err := wrapper.Start(); err != nil {
		t.Fatalf("starting the wrapper: %v", err)
	}
	defer func() { _, _ = wrapper.Process.Wait() }()

	waitForFile(t, ready, 60*time.Second)
	waitForFile(t, pidFile, 60*time.Second)
	bashPid, grandPid := readStubPids(t, pidFile)

	helperPid := 0
	data, err := os.ReadFile(filepath.Join(root, "helper-pid"))
	if err != nil {
		t.Fatalf("reading the helper pid: %v", err)
	}
	if helperPid, err = strconv.Atoi(strings.TrimSpace(string(data))); err != nil {
		t.Fatalf("the helper pid file held %q: %v", data, err)
	}

	// Kill the WRAPPER alone, the way `kill -TERM <go run pid>` does.
	// Nothing is sent to the helper; if it stops, it is because it
	// noticed.
	if err := wrapper.Process.Kill(); err != nil {
		t.Fatalf("killing the wrapper: %v", err)
	}

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(helperPid) && !alive(bashPid) && !alive(grandPid) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := os.Stat(trapFile); err != nil {
		t.Errorf("the script's teardown trap never finished after the wrapper was killed (%v).\n"+
			"This is the 2026-09-11 incident exactly: the wrapper died, the supervisor reparented to init, and the "+
			"script kept running with its trap unrun while the estate stayed up. `go run` does not forward the signal "+
			"(measured, go1.26.5), so the supervisor's own ppid going to 1 is the only evidence it gets.", err)
	}
	for _, p := range []struct {
		pid  int
		what string
	}{
		{helperPid, "the orphaned supervisor"},
		{bashPid, "the script it was holding"},
		{grandPid, "the script's bare grandchild"},
	} {
		if alive(p.pid) {
			t.Errorf("pid %d - %s - survived the wrapper being killed. Nothing from a stopped run may outlive it; "+
				"an orphaned process with a live estate under it is what this issue is named for.", p.pid, p.what)
		}
	}

	if body, err := os.ReadFile(filepath.Join(root, "helper-error")); err == nil {
		t.Errorf("the orphan helper reported an error: %s", body)
	}
	if t.Failed() {
		// The helper's own output is the only account of what it did,
		// and it is written to a temp dir this test is about to delete -
		// the same defect #1267 found in selftest-kill.sh, where the
		// whole evidence of a failure was "see above" with nothing
		// above.
		body, _ := os.ReadFile(filepath.Join(root, "helper-out"))
		t.Logf("the orphaned supervisor's own output:\n%s", body)
		rec, _ := os.ReadFile(LiveCertRunPath(root, orphanHelperName))
		t.Logf("its run record:\n%s", rec)
	}
	rec, err := ReadLiveCertRun(root, orphanHelperName)
	if err != nil {
		t.Fatalf("reading the run record after the orphaning: %v", err)
	}
	if !rec.State.Signalled() {
		t.Errorf("the run record says state=%q. An orphaned run is a stopped run, and the record has to say so: "+
			"a record reading %q is the file version of rc=143 reading as success (#1324).", rec.State, RunStateFinished)
	}
	if rec.Signal != "orphaned" {
		t.Errorf("the run record says signal=%q, want \"orphaned\": what stopped the run is part of what a reader needs", rec.Signal)
	}
	if ok, _ := rec.FinishedMeasurement(rec.Commit); ok {
		t.Errorf("the reader accepted an orphaned run as a finished measurement")
	}
}

// ── the record, read on its own ─────────────────────────────────────────

// TestLiveCertRunRecordRefusesWhatItCannotTrust is scripts/ci-gate.sh's
// `check` held against this record. Each case is a way a reader could be
// told "the run is over" by something that is not evidence of it.
func TestLiveCertRunRecordRefusesWhatItCannotTrust(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  *LiveCertRun
		want string
		ok   bool
	}{
		{
			name: "no record at all",
			rec:  nil,
			want: "no run record",
		},
		{
			name: "a record for a different commit",
			rec:  &LiveCertRun{State: RunStateFinished, Commit: "1111111111111111"},
			want: "measures a different tree",
		},
		{
			name: "signalled, teardown unconfirmed",
			rec:  &LiveCertRun{State: RunStateSignalledUnconfirmed, Commit: "abc"},
			want: "signalled, teardown unconfirmed",
		},
		{
			name: "signalled, teardown confirmed is still not a measurement",
			rec:  &LiveCertRun{State: RunStateSignalledTornDown, Commit: "abc"},
			want: "signalled, teardown confirmed",
		},
		{
			name: "a record still saying running",
			rec:  &LiveCertRun{State: RunStateRunning, Commit: "abc"},
			want: "running",
		},
		{
			name: "finished",
			rec:  &LiveCertRun{State: RunStateFinished, Commit: "abc"},
			want: "finished",
			ok:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, why := tc.rec.FinishedMeasurement("abc")
			if ok != tc.ok {
				t.Errorf("FinishedMeasurement = %v, want %v (%s)", ok, tc.ok, why)
			}
			if !strings.Contains(why, tc.want) {
				t.Errorf("the reason was %q, which does not contain %q - a refusal a human cannot act on is most of the way back to an exit code", why, tc.want)
			}
		})
	}
}

// TestTeardownConfirmedReadsTeardownsOwnVerdict pins the one thing that may
// upgrade a signalled run: teardown's own verified-empty line.
//
// The dirty case is the one that matters. teardown() prints VERIFIED EMPTY
// for the record store and then, after a failed sweep, STILL NOT EMPTY for
// the estate; a reader that stopped at the first optimistic line would report
// an estate that is still up as torn down, which is this issue's own defect
// moved one file over.
func TestTeardownConfirmedReadsTeardownsOwnVerdict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   bool
	}{
		{"nothing at all", "GAUNTLET protocol=1\n", false},
		{"a banner but no verdict", "=== TEARDOWN (target=aws run=x) ===\n  record store: 3 object(s) to delete\n", false},
		{"a verdict with no banner", "  VERIFIED EMPTY by listing: nothing remains\n", false},
		{"banner and verdict", "=== TEARDOWN (target=aws run=x) ===\n  VERIFIED EMPTY by listing: nothing remains\n", true},
		{"verified after the sweep", "=== TEARDOWN (target=aws run=x) ===\n  VERIFIED EMPTY by listing after the sweep\n", true},
		{
			name:   "a verified line followed by STILL NOT EMPTY",
			output: "=== TEARDOWN (target=aws run=x) ===\n  VERIFIED EMPTY by listing: the record store prefix is empty\n  STILL NOT EMPTY after destroy and sweep - see the listing above\n",
			want:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TeardownConfirmed(tc.output); got != tc.want {
				t.Errorf("TeardownConfirmed = %v, want %v for:\n%s", got, tc.want, tc.output)
			}
		})
	}
}

// TestSignalledRunWritesNoRow is #1324's second proposal stated on its own,
// away from any process: whatever a signalled run managed to say, it does not
// get a row.
//
// live_cert holds exactly ONE row per estate and writing it REPLACES the last
// certification with no earlier version to fall back on - that is #1100's
// lesson, and a signalled run is a strictly worse thing to replace it with
// than the refusal #1151 already keeps out.
func TestSignalledRunWritesNoRow(t *testing.T) {
	spoke := &ProtocolResult{Spoken: true, Stages: map[string]string{"cold_deploy": VerdictPass}}
	for _, state := range []LiveCertRunState{RunStateSignalledUnconfirmed, RunStateSignalledTornDown, RunStateRunning} {
		w := PlanLiveCertWrites("aws", spoke, state)
		if w.LiveCertRow || w.ScaleRecord {
			t.Errorf("state %q writes live_cert=%v scale=%v; a run that did not finish writes neither", state, w.LiveCertRow, w.ScaleRecord)
		}
		if !strings.Contains(w.Why, "#1324") {
			t.Errorf("state %q refuses with %q, which does not say why - the reason is the whole value of the refusal", state, w.Why)
		}
	}
	if w := PlanLiveCertWrites("aws", spoke, RunStateFinished); !w.LiveCertRow || !w.ScaleRecord {
		t.Errorf("a finished run that spoke a stage must still be recorded: live_cert=%v scale=%v (%s)", w.LiveCertRow, w.ScaleRecord, w.Why)
	}
}

// TestClearLiveCertRunRunsBeforeTheRun is the ci-gate.sh half that is easy to
// leave out: without the delete, a run that dies before writing anything
// leaves the PREVIOUS run's record in place and a reader gets a green answer
// about a run that never happened (#1307).
func TestClearLiveCertRunRunsBeforeTheRun(t *testing.T) {
	root := tempCheckout(t)
	stale := LiveCertRun{State: RunStateFinished, Estate: "staleestate", Target: "aws", Commit: "deadbeefdeadbeef"}
	if err := WriteLiveCertRun(root, stale); err != nil {
		t.Fatal(err)
	}
	signalStubScript(t, root, "staleestate", stubOpts{runSeconds: 0, teardownSeconds: 0})

	if _, _, _, err := RunLiveCert(root, "staleestate", "floci", "us-east-1", 5, 120); err != nil {
		t.Fatalf("RunLiveCert: %v", err)
	}
	rec, err := ReadLiveCertRun(root, "staleestate")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Commit == "deadbeefdeadbeef" {
		t.Errorf("the record still carries the previous run's commit: the new run did not clear it first, so a reader can be told about a run that never happened (#1307's stale ci.rc, one file over)")
	}
}

// TestCeilingGivesTheTrapAsLongAsItNeeds is the -timeout-seconds path, which
// HANDOFF and live-cert.yml both now call the safe way to stop a
// certification. It has to actually be that.
func TestCeilingGivesTheTrapAsLongAsItNeeds(t *testing.T) {
	root := tempCheckout(t)
	// A teardown longer than WaitDelay, which is the whole question.
	old := liveCertWaitDelay
	liveCertWaitDelay = 2 * time.Second
	t.Cleanup(func() { liveCertWaitDelay = old })

	_, pidFile, trapFile := signalStubScript(t, root, "ceilingestate", stubOpts{runSeconds: 120, teardownSeconds: 6})
	r, _, exit, err := RunLiveCert(root, "ceilingestate", "floci", "us-east-1", 5, 2)
	if err != nil {
		t.Fatalf("RunLiveCert: %v", err)
	}
	if _, err := os.Stat(trapFile); err != nil {
		t.Errorf("the ceiling cut the teardown short: the trap never finished (%v).\n"+
			"exit=%d state=%q. -timeout-seconds is documented as the SAFE way to stop a certification, "+
			"because it is the path that gives the trap time; a WaitDelay that kills bash partway through a "+
			"destroy makes it the unsafe one, with the estate still up (#1324).", err, exit, r.State)
	}
	bashPid, grandPid := readStubPids(t, pidFile)
	for _, p := range []int{bashPid, grandPid} {
		if alive(p) {
			t.Errorf("pid %d survived the ceiling", p)
		}
	}
	if !r.State.Signalled() {
		t.Errorf("state after a ceiling stop is %q; a ceiling is a stop request and the run did not finish", r.State)
	}
}

// runSignalledStub starts a stub run, waits until it is mid-stage, and hands
// back a channel carrying the outcome plus the stub's pids. Every test below
// signals the test process itself, which is how an operator's kill and a
// runner's cancellation both arrive.
func runSignalledStub(t *testing.T, estate string, opts stubOpts) (root string, out <-chan liveCertOutcome, trapFile string, bashPid, grandPid int) {
	t.Helper()
	root = tempCheckout(t)
	ready, pidFile, trapFile := signalStubScript(t, root, estate, opts)
	ch := make(chan liveCertOutcome, 1)
	go func() {
		r, res, exit, err := RunLiveCert(root, estate, "floci", "us-east-1", 5, 0)
		ch <- liveCertOutcome{r, res, exit, err}
	}()
	waitForFile(t, ready, 30*time.Second)
	waitForFile(t, pidFile, 30*time.Second)
	bashPid, grandPid = readStubPids(t, pidFile)
	return root, ch, trapFile, bashPid, grandPid
}

// awaitStub is awaitOutcome with the script's process group known, since the
// script leads its own group and its pid is therefore its pgid.
func awaitStub(t *testing.T, ch <-chan liveCertOutcome, bound time.Duration, bashPid int) liveCertOutcome {
	t.Helper()
	return awaitOutcomeOf(t, ch, bound, bashPid)
}

// absorbStraySignals keeps a signal this test sends to itself from killing
// the test binary when RunLiveCert is not the one that ends up handling it.
//
// signal.Notify delivers to every registered channel, so this does not take
// anything away from the supervisor under test - it only stops the default
// action. Without it a break arm that makes RunLiveCert return EARLY turns
// the next signal into "signal: hangup" and the test's own assertion message
// is never printed, which is the one moment it is worth having.
func absorbStraySignals(t *testing.T) {
	t.Helper()
	sink := make(chan os.Signal, 16)
	signal.Notify(sink, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sink:
			case <-done:
				return
			}
		}
	}()
	t.Cleanup(func() {
		signal.Stop(sink)
		close(done)
	})
}

type liveCertOutcome struct {
	r    *LiveCertResult
	res  *ProtocolResult
	exit int
	err  error
}

func (o liveCertOutcome) must(t *testing.T) liveCertOutcome {
	t.Helper()
	if o.err != nil {
		t.Fatalf("RunLiveCert: %v", o.err)
	}
	return o
}

func awaitOutcome(t *testing.T, ch <-chan liveCertOutcome, bound time.Duration) liveCertOutcome {
	t.Helper()
	return awaitOutcomeOf(t, ch, bound, 0)
}

// awaitOutcomeOf waits for the run and, if it does not come back, prints what
// a reader needs to tell the three candidate causes apart before failing.
//
// "RunLiveCert never returned within 1m30s" on its own is not actionable: it
// says a wait did not end and nothing about which side wedged. The gate on
// main hit exactly that (2026-09-20) and the only way to find out what had
// happened was to reproduce it. Everything below is the state that was
// missing, captured at the moment the test gives up:
//
//   - the process table for the script's own process group, which
//     distinguishes "the script is gone and this process is stuck" from
//     "the script is sitting in a sleep nothing signalled", including each
//     process's STAT and elapsed time;
//   - every goroutine's stack, which says whether cmd.Wait is blocked on the
//     process or on copying its output, and whether the supervisor loop is
//     still running.
//
// pgid may be 0 when the caller has not learned it yet.
func awaitOutcomeOf(t *testing.T, ch <-chan liveCertOutcome, bound time.Duration, pgid int) liveCertOutcome {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(bound):
		t.Errorf("RunLiveCert never returned within %s", bound)
		t.Fatalf("diagnostics at the moment of the timeout:\n%s\n%s",
			processGroupTable(pgid), goroutineDump())
	}
	return liveCertOutcome{}
}

// processGroupTable prints every process in pgid, and says so plainly when
// there are none - an empty group means the script is gone and whatever is
// still waiting is on this side.
func processGroupTable(pgid int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== processes in the script's process group (pgid %d) ===\n", pgid)
	if pgid == 0 {
		b.WriteString("  (the test had not learned the pgid yet)\n")
		return b.String()
	}
	out, err := exec.Command("ps", "-eo", "pid,ppid,pgid,stat,etime,command").CombinedOutput() //nolint:gosec // fixed arguments
	if err != nil {
		fmt.Fprintf(&b, "  (ps failed: %v)\n", err)
		return b.String()
	}
	lines := strings.Split(string(out), "\n")
	found := 0
	for i, line := range lines {
		f := strings.Fields(line)
		if i == 0 || (len(f) > 2 && f[2] == strconv.Itoa(pgid)) {
			fmt.Fprintf(&b, "  %s\n", strings.TrimRight(line, " "))
			if i > 0 {
				found++
			}
		}
	}
	if found == 0 {
		b.WriteString("  (none: every process in the group has exited, so the wait that did not end is on the Go side)\n")
	}
	return b.String()
}

// goroutineDump is every goroutine's stack, which is what says whether
// cmd.Wait is blocked on the process itself or on the copy of its output.
func goroutineDump() string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return "=== all goroutine stacks ===\n" + string(buf[:n])
}

// TestNoDefaultBoundKillsATeardown is the policy, stated as a test.
//
// The first version of this fix shipped a 600-second default after which the
// supervisor SIGKILLed the process group. That is this issue's own defect
// coming back through its fix: the run #1324 was filed from took 39,610
// seconds, 5,633 of them in the cold apply alone, and tearing a scale-128
// estate down is far longer than ten minutes. An operator stopping that run
// would have got ten minutes of teardown and then the harness killing its
// own destroy with thousands of billable resources standing - strictly worse
// than the hand-signalling workaround it replaced.
//
// A 6-second teardown cannot prove a 600-second one is waited for. What it
// proves is the thing that makes the length irrelevant: no bound is armed at
// all unless someone asks for one, so nothing here escalates, whatever the
// teardown costs.
func TestNoDefaultBoundKillsATeardown(t *testing.T) {
	absorbStraySignals(t)
	t.Setenv(LiveCertSignalGraceEnv, "")
	if b := liveCertSignalBound(); b != 0 {
		t.Fatalf("with %s unset the bound is %s, want 0 (unbounded): a default bound is a default SIGKILL on a teardown", LiveCertSignalGraceEnv, b)
	}
	root, ch, trapFile, bashPid, grandPid := runSignalledStub(t, "unboundedestate", stubOpts{runSeconds: 120, teardownSeconds: 6})

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	got := awaitStub(t, ch, 90*time.Second, bashPid).must(t)

	if _, err := os.Stat(trapFile); err != nil {
		t.Errorf("the teardown did not finish (%v): something killed it, and nothing is supposed to", err)
	}
	if got.r.State != RunStateSignalledTornDown {
		t.Errorf("state is %q, want %q: the teardown ran to its own verified-empty verdict", got.r.State, RunStateSignalledTornDown)
	}
	rec, err := ReadLiveCertRun(root, "unboundedestate")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Escalated {
		t.Errorf("the record says the group was SIGKILLed. With no bound asked for, nothing may escalate: a teardown at scale is tens of minutes and this process's job is to wait for it")
	}
	for _, p := range []int{bashPid, grandPid} {
		if alive(p) {
			t.Errorf("pid %d survived", p)
		}
	}
}

// TestRepeatSignalsDoNotKillARunningTeardown is the second half of the same
// policy, and the case that looks harmless.
//
// An operator hits Ctrl-C, sees that tearing down 9,477 resources will take
// an hour, and closes the terminal. SIGHUP arrives. On any "a second signal
// escalates" rule, that SIGHUP kills the destroy. GitHub's own cancellation
// sends SIGINT and then SIGTERM, which is two signals by itself; so is an
// orphan detection followed by a real signal. None of those is a request to
// abandon an estate, and the way to abandon one stays available and explicit:
// the repeat line prints the `kill -KILL -<pgid>` that does it.
func TestRepeatSignalsDoNotKillARunningTeardown(t *testing.T) {
	absorbStraySignals(t)
	t.Setenv(LiveCertSignalGraceEnv, "")
	root, ch, trapFile, bashPid, grandPid := runSignalledStub(t, "repeatestate", stubOpts{runSeconds: 120, teardownSeconds: 6})

	// The stop request, then the two that must change nothing: a runner's
	// second signal, and the SIGHUP of a closed terminal.
	for i, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if i > 0 {
			time.Sleep(700 * time.Millisecond)
		}
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			t.Fatalf("sending %v: %v", sig, err)
		}
	}
	got := awaitStub(t, ch, 90*time.Second, bashPid).must(t)

	if _, err := os.Stat(trapFile); err != nil {
		t.Errorf("the teardown was killed by a repeat signal (%v).\n"+
			"A second signal is not a request to abandon an estate - closing a terminal sends SIGHUP, and a "+
			"runner's cancellation sends SIGINT and then SIGTERM. Only an explicit `kill -KILL -<pgid>` may "+
			"end a teardown (#1324).", err)
	}
	if got.r.State != RunStateSignalledTornDown {
		t.Errorf("state is %q, want %q", got.r.State, RunStateSignalledTornDown)
	}
	rec, err := ReadLiveCertRun(root, "repeatestate")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Escalated {
		t.Errorf("the record says the group was SIGKILLed after a repeat signal")
	}
	if rec.RepeatSignals < 2 {
		t.Errorf("the record counted %d repeat signal(s), want at least 2: the count is what explains a long wait to whoever reads this afterwards", rec.RepeatSignals)
	}
	// The line that makes abandoning possible has to actually name the
	// command and the cost, or the policy is just a refusal.
	log := runLogText(t, root, "repeatestate")
	for _, want := range []string{
		fmt.Sprintf("kill -KILL -%d", bashPid),
		"NOT forwarding it again and NOT killing anything",
		"live and billing",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the repeat-signal line does not contain %q.\nLog:\n%s", want, log)
		}
	}
	for _, p := range []int{bashPid, grandPid} {
		if alive(p) {
			t.Errorf("pid %d survived", p)
		}
	}
}

// TestSigpipeIsNotAStopRequest covers the operator who closes the terminal on
// a `gauntlet live-cert ... | tee` while the run is going.
//
// A Go program that has NOT asked for SIGPIPE dies when a write to fd 1 or 2
// gets one. This process asks for it, so it arrives on the signal channel
// instead - and the supervisor has to drop it there, because treating it as a
// stop request would turn a dead terminal into a stopped certification.
func TestSigpipeIsNotAStopRequest(t *testing.T) {
	absorbStraySignals(t)
	root, ch, _, bashPid, _ := runSignalledStub(t, "sigpipeestate", stubOpts{runSeconds: 3, teardownSeconds: 0})

	for range 3 {
		if err := syscall.Kill(os.Getpid(), syscall.SIGPIPE); err != nil {
			t.Fatalf("SIGPIPE: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	got := awaitStub(t, ch, 60*time.Second, bashPid).must(t)

	if got.r.State != RunStateFinished {
		t.Errorf("state is %q, want %q: SIGPIPE is not a stop request, and a dead stdout must not stop a certification", got.r.State, RunStateFinished)
	}
	rec, err := ReadLiveCertRun(root, "sigpipeestate")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Signal != "" {
		t.Errorf("the record names signal=%q; SIGPIPE must leave no stop request behind", rec.Signal)
	}
	if alive(bashPid) {
		t.Errorf("pid %d survived", bashPid)
	}
}

// TestOptInBoundStillKills is the escape hatch, kept because someone running
// this by hand may genuinely want a bound and should not have to reach for
// another terminal to get one. It is opt-in precisely so that choosing it is
// a decision with a name on it.
func TestOptInBoundStillKills(t *testing.T) {
	absorbStraySignals(t)
	t.Setenv(LiveCertSignalGraceEnv, "1")
	if b := liveCertSignalBound(); b != time.Second {
		t.Fatalf("%s=1 gives a bound of %s, want 1s", LiveCertSignalGraceEnv, b)
	}
	root, ch, trapFile, bashPid, grandPid := runSignalledStub(t, "boundedestate", stubOpts{runSeconds: 120, teardownSeconds: 30})

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	got := awaitStub(t, ch, 90*time.Second, bashPid).must(t)

	if _, err := os.Stat(trapFile); err == nil {
		t.Errorf("the 30s teardown finished under a 1s bound, so the bound did nothing and this test proves nothing")
	}
	if got.r.State != RunStateSignalledUnconfirmed {
		t.Errorf("state is %q, want %q: a teardown that was SIGKILLed partway through is exactly the case that must NOT read as confirmed", got.r.State, RunStateSignalledUnconfirmed)
	}
	rec, err := ReadLiveCertRun(root, "boundedestate")
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Escalated {
		t.Errorf("the record does not say the group was SIGKILLed, so whoever reads it cannot tell why teardown is unconfirmed")
	}
	for _, p := range []int{bashPid, grandPid} {
		if alive(p) {
			t.Errorf("pid %d survived the SIGKILL of its group", p)
		}
	}
}

// TestTheWaitIsNotSilent is #1324's second defect held against the teardown
// wait itself. A teardown that takes an hour and says nothing is
// indistinguishable from a hung one, and an operator who cannot tell will
// reach for a kill - which is the outcome the whole policy above exists to
// avoid.
func TestTheWaitIsNotSilent(t *testing.T) {
	absorbStraySignals(t)
	t.Setenv(LiveCertSignalGraceEnv, "")
	t.Setenv(LiveCertHeartbeatEnv, "1")
	root, ch, _, bashPid, _ := runSignalledStub(t, "waitlineestate", stubOpts{runSeconds: 120, teardownSeconds: 4})

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	awaitStub(t, ch, 90*time.Second, bashPid).must(t)

	log := runLogText(t, root, "waitlineestate")
	n := strings.Count(log, "still waiting for teardown")
	if n < 2 {
		t.Errorf("the log carries %d \"still waiting for teardown\" line(s) over a 4s teardown at a 1s interval, want at least 2.\n"+
			"Without them a long teardown is silent, and a silent wait is what an operator kills (#1324).\nLog:\n%s", n, log)
	}
	if !strings.Contains(log, fmt.Sprintf("pgid %d", bashPid)) {
		t.Errorf("the waiting line does not name the process group, which is what a human needs to act on it")
	}
}

// deferredTrapStub writes a stub whose trap is NOT armed when the first
// SIGTERM arrives, which is the shape the fork race produces.
//
// The race itself is a timing accident and cannot be asked for: a signal to
// a process group reaches the processes in it at that instant, and a command
// bash forks microseconds later misses it while bash holds the trap pending
// until that command finishes. Measured with a probe that spin-waits for the
// script to reach a foreground `sleep` and then signals the group, the trap
// was lost 19 times in 200 runs on an idle machine.
//
// This stub reproduces the CONSEQUENCE deterministically - a first SIGTERM
// that does nothing - by ignoring TERM outright for its first couple of
// seconds and arming the real trap afterwards. What the supervisor has to do
// about it is identical either way: keep asking until the script answers.
func deferredTrapStub(t *testing.T, root, estate string, ignoreSeconds int) (readyFile, pidFile, trapFile string) {
	t.Helper()
	dir := filepath.Join(root, "live", "live-cert")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	readyFile = filepath.Join(work, "ready")
	pidFile = filepath.Join(work, "pids")
	trapFile = filepath.Join(work, "trap-finished")

	body := "#!/usr/bin/env bash\n" +
		"set -uo pipefail\n" +
		"echo \"GAUNTLET protocol=1\"\n" +
		"printf 'bash=%s grand=%s\\n' \"$$\" \"$$\" > " + shQuote(pidFile) + "\n" +
		"teardown() {\n" +
		"  echo \"=== TEARDOWN (target=floci run=deferred) ===\"\n" +
		"  echo \"  VERIFIED EMPTY by listing: nothing matching this run remains\"\n" +
		"  touch " + shQuote(trapFile) + "\n" +
		"  echo \"GAUNTLET stage=cold_deploy verdict=fail duration_s=1 detail=stopped by a signal\"\n" +
		"}\n" +
		// The window. A SIGTERM arriving here is discarded by the
		// kernel, exactly as one arriving before a foreground child
		// exists is discarded by that child.
		"trap '' TERM INT\n" +
		"touch " + shQuote(readyFile) + "\n" +
		"sleep " + strconv.Itoa(ignoreSeconds) + "\n" +
		"trap 'teardown; trap - EXIT; exit 130' INT TERM\n" +
		"trap teardown EXIT\n" +
		"sleep 120\n"

	if err := os.WriteFile(filepath.Join(dir, estate+".sh"), []byte(body), 0o755); err != nil { //nolint:gosec // a fake script in a test's own temp dir
		t.Fatal(err)
	}
	return readyFile, pidFile, trapFile
}

// TestTheStopRequestIsRepeatedUntilTheTrapAnswers is the fix for the gate
// failure on main (2026-09-20): "RunLiveCert never returned within 1m30s".
//
// One kill to a process group is not a reliable way to reach a bash script,
// and when it misses, the teardown does not start until whatever foreground
// command bash forked next has finished - at scale, a `terraform plan` is
// thirteen minutes of an estate the operator already asked to tear down.
// With no default bound, which is deliberate, that is thirteen minutes of
// "still waiting for teardown" and nothing happening.
//
// So the supervisor keeps asking until the script's own output says its trap
// is running, and stops asking the moment it does - re-sending after that
// would land on whatever destroy the teardown has forked, which is the thing
// this file exists to protect.
func TestTheStopRequestIsRepeatedUntilTheTrapAnswers(t *testing.T) {
	absorbStraySignals(t)
	t.Setenv(LiveCertSignalGraceEnv, "")
	root := tempCheckout(t)
	ready, pidFile, trapFile := deferredTrapStub(t, root, "deferredestate", 3)

	ch := make(chan liveCertOutcome, 1)
	go func() {
		r, res, exit, err := RunLiveCert(root, "deferredestate", "floci", "us-east-1", 5, 0)
		ch <- liveCertOutcome{r, res, exit, err}
	}()
	waitForFile(t, ready, 30*time.Second)
	waitForFile(t, pidFile, 30*time.Second)
	bashPid, _ := readStubPids(t, pidFile)

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	got := awaitStub(t, ch, 60*time.Second, bashPid).must(t)

	if _, err := os.Stat(trapFile); err != nil {
		t.Errorf("the teardown never ran (%v).\n"+
			"The first SIGTERM was discarded, and nothing asked again - so the script sat in its 120s "+
			"foreground sleep with the stop request lost. That is the gate failure on main, and at scale it "+
			"is an estate still billing while the log says the run was signalled (#1324).", err)
	}
	if got.r.State != RunStateSignalledTornDown {
		t.Errorf("state is %q, want %q", got.r.State, RunStateSignalledTornDown)
	}
	rec, err := ReadLiveCertRun(root, "deferredestate")
	if err != nil {
		t.Fatal(err)
	}
	if rec.TrapResends < 1 {
		t.Errorf("the record says %d re-send(s); the first signal was discarded by design here, so reaching teardown took at least one more", rec.TrapResends)
	}
	if alive(bashPid) {
		t.Errorf("pid %d survived", bashPid)
	}
}

// TestNoResendOnceTheTrapHasAnswered is the other half, and the one that
// matters for spend: once teardown is running, another SIGTERM to the group
// lands on whatever destroy it has forked.
//
// It asserts on ORDER, not on a count (#1464). The ordinary stub answers as
// soon as its foreground sleep dies, so on an idle machine there is no
// re-send at all; but under load the script can take longer than
// liveCertResendEvery to reach its trap, and then a re-send is the
// supervisor doing exactly what it should. This test failed once in three
// gate runs at load average 23 on a `TrapResends != 0` assertion whose log
// could not say which of those it was looking at. So the fixture now stamps
// the moment its trap begins, the record carries when the watcher saw the
// answer and when each re-send went out, and every re-send is placed on that
// line: before the trap began is legitimate and is logged as such; after it
// is #1324's defect and fails with both timestamps.
//
// One more check costs nothing and does not depend on any clock agreeing
// with any other: the trap's `sleep 6` can only take LONGER under load. A
// teardown that finished in under six seconds had its sleep killed, and the
// only thing in a position to do that is a signal to the group.
func TestNoResendOnceTheTrapHasAnswered(t *testing.T) {
	absorbStraySignals(t)
	t.Setenv(LiveCertSignalGraceEnv, "")
	const teardownSeconds = 6
	root, ch, trapFile, bashPid, _ := runSignalledStub(t, "noresendestate", stubOpts{runSeconds: 120, teardownSeconds: teardownSeconds})

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	sentAt := time.Now()
	awaitStub(t, ch, 90*time.Second, bashPid).must(t)

	if _, err := os.Stat(trapFile); err != nil {
		t.Fatalf("the teardown did not finish: %v", err)
	}
	trapBegan := mtimeOf(t, trapStartedStamp(trapFile))
	trapEnded := mtimeOf(t, trapFile)
	rec, err := ReadLiveCertRun(root, "noresendestate")
	if err != nil {
		t.Fatal(err)
	}

	// The watcher's observation of the answer. Teardown was confirmed off
	// the same output, so an empty stamp here is the record not carrying
	// what it was just taught to carry.
	if rec.TrapAnsweredUTC == "" {
		t.Fatalf("the record has no trap_answered_utc although teardown_confirmed=%v: the watcher saw the banner and did not say when", rec.TeardownConfirmed)
	}
	answered, err := time.Parse(time.RFC3339Nano, rec.TrapAnsweredUTC)
	if err != nil {
		t.Fatalf("trap_answered_utc %q: %v", rec.TrapAnsweredUTC, err)
	}
	if len(rec.TrapResendsUTC) != rec.TrapResends {
		t.Errorf("the record counts %d re-send(s) but stamps %d: %v", rec.TrapResends, len(rec.TrapResendsUTC), rec.TrapResendsUTC)
	}

	// What happened, in order, in every run's log - this is the line
	// #1464 asked for, so the next failure needs no reproduction.
	t.Logf("stop request sent %s; the script's trap began %s later (%s) and the watcher saw its first line %s after that (%s); teardown took %s; %d re-send(s)",
		sentAt.UTC().Format(time.RFC3339Nano),
		trapBegan.Sub(sentAt).Round(time.Millisecond), trapBegan.UTC().Format(time.RFC3339Nano),
		answered.Sub(trapBegan).Round(time.Millisecond), rec.TrapAnsweredUTC,
		trapEnded.Sub(trapBegan).Round(time.Millisecond), rec.TrapResends)

	for i, stamp := range rec.TrapResendsUTC {
		resent, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			t.Fatalf("trap_resends_utc[%d] %q: %v", i, stamp, err)
		}
		switch {
		case resent.Before(trapBegan):
			// The script had not reached its trap: the machine was
			// slow, the supervisor asked again, and asking again was
			// right. Not a failure - this is (a) of #1464.
			t.Logf("re-send %d at %s went out %s BEFORE the script's trap began: the script took more than %s to reach its trap under load, and repeating the stop request to it was correct. Not a defect.",
				i+1, stamp, trapBegan.Sub(resent).Round(time.Millisecond), liveCertResendEvery)
		case !resent.Before(answered):
			// The supervisor's own guard had already seen the answer
			// and it re-sent anyway: the loop is wrong.
			t.Errorf("re-send %d at %s went out %s AFTER the watcher had seen the trap answer at %s (the script's trap began at %s). The re-send is gated on that very observation, so the supervisor re-sent past its own guard, and the SIGTERM landed on a teardown in progress (#1324).",
				i+1, stamp, resent.Sub(answered).Round(time.Millisecond), rec.TrapAnsweredUTC, trapBegan.UTC().Format(time.RFC3339Nano))
		default:
			// Between the script's stamp and the watcher's: the trap
			// was running, its banner had not crossed the pipe yet,
			// and the re-send went out in that window. The guard did
			// what it was built to do and it was not enough.
			t.Errorf("re-send %d at %s went out %s AFTER the script's trap began at %s but %s BEFORE the watcher saw its first line at %s. The guard reads the banner off a pipe, and this SIGTERM went out in the gap between the trap starting and its banner arriving - it landed on a teardown in progress (#1324).",
				i+1, stamp, resent.Sub(trapBegan).Round(time.Millisecond), trapBegan.UTC().Format(time.RFC3339Nano),
				answered.Sub(resent).Round(time.Millisecond), rec.TrapAnsweredUTC)
		}
	}
	if rec.TrapResendsAfterAnswer != 0 {
		t.Errorf("the record's own monotonic count says %d re-send(s) went out after the trap answered", rec.TrapResendsAfterAnswer)
	}
	if took := trapEnded.Sub(trapBegan); took < teardownSeconds*time.Second {
		t.Errorf("the teardown ran for %s, but its `sleep %d` cannot finish in less than %ds on any machine at any load: something killed that sleep, and the only thing signalling this group is the supervisor (#1324).",
			took.Round(time.Millisecond), teardownSeconds, teardownSeconds)
	}
}

// TestTrapWatcherReadsTheScriptsOwnFirstLine pins what counts as the trap
// answering, including a needle split across two writes.
func TestTrapWatcherReadsTheScriptsOwnFirstLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes []string
		want   bool
	}{
		{"nothing", []string{"GAUNTLET protocol=1\n"}, false},
		{"ordinary apply output", []string{"aws_iam_role.x: Creation complete after 0s\n"}, false},
		{"the teardown banner", []string{"=== TEARDOWN (target=aws run=x) ===\n"}, true},
		{"on_signal's own line", []string{"=== caught TERM - forwarding to in-flight child (pid 9) ===\n"}, true},
		{"split across two writes", []string{"=== TEAR", "DOWN (target=aws run=x) ===\n"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sink strings.Builder
			w := newTrapWatcher(&sink)
			for _, chunk := range tc.writes {
				if _, err := w.Write([]byte(chunk)); err != nil {
					t.Fatal(err)
				}
			}
			if got := w.Started(); got != tc.want {
				t.Errorf("Started() = %v, want %v", got, tc.want)
			}
			if sink.String() != strings.Join(tc.writes, "") {
				t.Errorf("the watcher changed the output it passed through: %q", sink.String())
			}
		})
	}
}
