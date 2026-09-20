// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
		t.Fatalf("RunLiveCert never returned after the SIGTERM; the supervisor's own grace period should have bounded this")
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
