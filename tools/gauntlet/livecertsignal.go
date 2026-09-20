// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// This file is issue #1324: a signalled live-cert run reported done while it
// kept spending.
//
// What happened, measured: a scale-128 run was stopped with `kill -TERM` on
// the pid that `pgrep -f "gauntlet live-cert"` matched, which was the `go
// run` wrapper. The wrapper died, wrote 143 into the operator's rc file, and
// every "did it finish" check read that as the run being over. It was not:
// `go run` execs the binary it built as a CHILD, so the binary reparented to
// init and carried on with live/live-cert/terralith-scale.sh and its
// `terraform plan` underneath it, with 9,477 billable resources standing and
// the script's teardown trap never signalled.
//
// Two separate holes, and each one alone is enough to lose an estate:
//
//  1. The gauntlet binary had no signal handling at all. Nothing under
//     tools/ called signal.Notify, so a SIGTERM to the binary killed it
//     outright, under the bash script it had started and was responsible
//     for. The script would then be orphaned exactly as it was here, with
//     its trap unrun.
//
//  2. `go run` does not pass a signal on to the binary it built. Measured on
//     go1.26.5 darwin/arm64, 2026-09-19, with a probe that logged its own
//     ppid every second and held a trapping bash grandchild:
//
//     kill -TERM <go run pid>   ->  wrapper gone; probe logged
//     "go-alive 6 ppid=1" and kept running;
//     bash grandchild kept running; its trap
//     never ran.
//     kill -INT  <go run pid>   ->  wrapper still alive, probe still its
//     child, nothing reached either.
//
//     So signal forwarding INSIDE the binary fixes nothing when only the
//     wrapper was signalled: the binary receives no signal to forward.
//
// The answer here is both halves. Against (1), RunLiveCert starts bash in its
// own process group (Setpgid), catches INT/TERM/HUP, forwards to that whole
// group and then WAITS for the trap instead of exiting under it. Against (2),
// this file watches for the binary's parent going away (ppid becomes 1) and
// treats that as a stop request, because that transition is exactly what the
// orphaning looks like from the inside; and .github/workflows/live-cert.yml
// and HANDOFF.md's recipe stop using `go run` altogether, so the pid an
// operator or a runner signals is the pid holding the work. Orphan detection
// is the backstop, not the fix: the SIGINT measurement above shows a case it
// cannot see, since the wrapper survives and the ppid never changes.
//
// The third piece is the run record below. #1307 is the same defect one file
// over - a stale ci.rc read green from a run that never finished - and
// scripts/ci-gate.sh is the shape of the answer: delete the gate files at the
// start, stamp the identity of what was tested, and have the reader refuse
// what it cannot trust rather than believe it. An exit code has no vocabulary
// for "stopped while nine thousand resources were live", so this record
// carries the STATE the run reached, and a signalled run says "signalled,
// teardown unconfirmed" until teardown's own verified-empty line upgrades it.

// LiveCertRunState is what state a live-cert run reached. It is the thing an
// exit code cannot express: 143 from a wrapper and 0 from a finished run are
// both "the process is gone", and only one of them means the estate is down.
type LiveCertRunState string

const (
	// RunStateRunning: the script was started and has not been waited on
	// yet. A record left in this state is a run that died without its
	// supervisor getting to write anything, which is itself a finding - it
	// is NOT a finished measurement.
	RunStateRunning LiveCertRunState = "running"
	// RunStateFinished: the script ran to its own exit with no stop
	// request. This is the only state that may be read as a measurement.
	RunStateFinished LiveCertRunState = "finished"
	// RunStateSignalledUnconfirmed: a stop request was forwarded and
	// teardown did not confirm. Resources may still be live and billing.
	RunStateSignalledUnconfirmed LiveCertRunState = "signalled-teardown-unconfirmed"
	// RunStateSignalledTornDown: a stop request was forwarded, the trap
	// ran, and teardown printed its own verified-empty verdict. The estate
	// is down; the run is still not a measurement.
	RunStateSignalledTornDown LiveCertRunState = "signalled-teardown-confirmed"
)

// Signalled reports whether this state came from a stop request rather than
// the script reaching its own end.
func (s LiveCertRunState) Signalled() bool {
	return s == RunStateSignalledUnconfirmed || s == RunStateSignalledTornDown
}

// Human is the one line a reader gets, in the vocabulary #1324 asks for.
func (s LiveCertRunState) Human() string {
	switch s {
	case RunStateFinished:
		return "finished: the script reached its own exit with no stop request"
	case RunStateSignalledUnconfirmed:
		return "signalled, teardown unconfirmed: a stop request was forwarded to the script and its teardown did NOT print a verified-empty verdict - resources may still be live and billing"
	case RunStateSignalledTornDown:
		return "signalled, teardown confirmed: a stop request was forwarded, the trap ran, and teardown verified the estate empty by listing. This is not a measurement; the run did not finish its stages"
	case RunStateRunning:
		return "running: the script was started and nothing has written an end state - a record still saying this is a run whose supervisor died, not a run that finished"
	default:
		return "unknown state " + string(s)
	}
}

// LiveCertRun is the run record: one file per estate, beside the run log,
// saying what state the run reached and what tree it was measuring.
//
// It is modelled on scripts/ci-gate.sh's ci.meta rather than on an rc file.
// The identity fields (Commit, Estate, Target) are what let a reader refuse a
// record written for something other than what it is being asked about,
// which is the whole difference between ci-gate.sh and the bare ci.rc that
// #1307 caught reading green from a run that never finished.
type LiveCertRun struct {
	State  LiveCertRunState `json:"state"`
	Estate string           `json:"estate"`
	Target string           `json:"target"`
	Region string           `json:"region"`
	// Commit is the tree the run started from, the same value the
	// LiveCertResult row carries, resolved before the script starts.
	Commit string `json:"commit"`
	// Pid is the gauntlet process that supervised the run, and PGID the
	// process group the script was started in. Both are here so a human
	// with a leaked estate can find what to signal by hand.
	Pid  int `json:"pid"`
	PGID int `json:"pgid,omitempty"`
	// RunID is the live-cert lock's run id when there is one, so this
	// record and the lock file name the same run.
	RunID      string `json:"run_id,omitempty"`
	StartedUTC string `json:"started_utc"`
	UpdatedUTC string `json:"updated_utc"`
	// Signal is what the stop request was: "SIGTERM", "SIGINT", "SIGHUP",
	// or "orphaned" when the supervisor's own parent went away.
	Signal string `json:"signal,omitempty"`
	// Escalated: the grace period expired (or a second signal arrived) and
	// the process group was SIGKILLed. A trap cannot run after SIGKILL, so
	// this always accompanies an unconfirmed teardown.
	Escalated bool `json:"escalated,omitempty"`
	// TeardownConfirmed is teardown's OWN verdict line, read off the
	// script's output - never this tool's opinion, and never an exit code.
	TeardownConfirmed bool   `json:"teardown_confirmed"`
	ExitCode          int    `json:"exit_code"`
	Note              string `json:"note,omitempty"`
}

// LiveCertRunPath is where the record for one estate lives: beside the run
// log, in the same gitignored directory (/live/gauntlet/logs/), so a record
// can never be committed as if it were evidence in the artifact.
func LiveCertRunPath(root, estate string) string {
	return filepath.Join(root, LogDir, "live-cert-"+estate+".run.json")
}

// ClearLiveCertRun deletes the record before a run starts.
//
// This is ci-gate.sh's first move and it is the half that is easy to skip:
// without it, a run that dies before writing anything leaves the PREVIOUS
// run's record sitting there, and a reader gets a green answer about a run
// that never happened. That is #1307 verbatim.
func ClearLiveCertRun(root, estate string) error {
	err := os.Remove(LiveCertRunPath(root, estate))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clearing the live-cert run record %s: %w", LiveCertRunPath(root, estate), err)
	}
	return nil
}

// WriteLiveCertRun stamps the record. Called at the start, again the instant
// a stop request is forwarded, and once more at the end: the middle one is
// the one that matters, because it puts "signalled, teardown unconfirmed" on
// disk BEFORE the wait that may never return.
func WriteLiveCertRun(root string, rec LiveCertRun) error {
	rec.UpdatedUTC = time.Now().UTC().Format(time.RFC3339)
	if err := os.MkdirAll(filepath.Join(root, LogDir), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(LiveCertRunPath(root, rec.Estate), data, 0o644) //nolint:gosec // a gitignored path under the checkout
}

// ReadLiveCertRun reads the record for estate, if there is one.
func ReadLiveCertRun(root, estate string) (*LiveCertRun, error) {
	data, err := os.ReadFile(LiveCertRunPath(root, estate))
	if err != nil {
		return nil, err
	}
	var rec LiveCertRun
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("%s is not a readable live-cert run record: %w", LiveCertRunPath(root, estate), err)
	}
	return &rec, nil
}

// FinishedMeasurement is the reader that refuses. It answers one question -
// may this record be read as a finished measurement of wantCommit? - and
// every "no" comes with the sentence a human needs to act on.
//
// wantCommit may be empty, which skips the identity check. That is for a
// caller that genuinely has no commit to compare against; a caller that has
// one and passes "" is choosing to believe a record that might belong to
// another tree, which is the thing ci-gate.sh's check refuses to do.
func (r *LiveCertRun) FinishedMeasurement(wantCommit string) (bool, string) {
	if r == nil {
		return false, "there is no run record: nothing says what state the last run reached, so nothing here may be read as a finished measurement"
	}
	if wantCommit != "" && r.Commit != wantCommit {
		return false, fmt.Sprintf("this record was written for commit %s, not %s - it measures a different tree (scripts/ci-gate.sh refuses the same way, #1307)", short(r.Commit), short(wantCommit))
	}
	if r.State != RunStateFinished {
		return false, r.State.Human()
	}
	return true, "finished: the script reached its own exit with no stop request"
}

// ── teardown's own verdict ──────────────────────────────────────────────

// Both live-cert estate scripts print the same three lines from the same
// teardown shape (live/live-cert/terralith-scale.sh's teardown() and
// live/live-cert/reference-ec2-vpc.sh's), so these needles are the scripts'
// own vocabulary rather than a parallel description of it.
const (
	teardownBanner       = "=== TEARDOWN "
	teardownVerifiedLine = "VERIFIED EMPTY by listing"
	teardownDirtyLine    = "STILL NOT EMPTY"
)

// TeardownConfirmed reads teardown's own verdict out of the script's output.
//
// It is deliberately the SCRIPT's line and not this tool's inference. The
// whole lesson of #1324 is that a process-level fact (an exit code, a pid
// going away) says nothing about whether an estate is down; the only thing
// that does is the independent listing teardown runs and reports on, and
// live/live-cert/lib/live-cert.sh's livecert_verify_empty is what produces
// it.
//
// "STILL NOT EMPTY" wins over "VERIFIED EMPTY" wherever both appear: teardown
// prints the verified line on the happy path and, after a sweep, may print
// both a verified line for one namespace and the dirty line for the estate.
// Reading the optimistic one would be this issue's own defect in miniature.
func TeardownConfirmed(output string) bool {
	sawBanner, sawVerified := false, false
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.Contains(line, teardownDirtyLine):
			return false
		case strings.Contains(line, teardownBanner):
			sawBanner = true
		case strings.Contains(line, teardownVerifiedLine):
			sawVerified = true
		}
	}
	return sawBanner && sawVerified
}

// ── the supervisor ──────────────────────────────────────────────────────

// liveCertOrphanPoll is how often the supervisor looks at its own ppid. A
// second is fast enough that an orphaned run loses a second of spend, and
// slow enough to cost nothing over a four-hour run.
const liveCertOrphanPoll = 1 * time.Second

// LiveCertSignalGraceEnv bounds how long the supervisor waits for the
// script's teardown trap after forwarding a stop request.
//
// There has to be a bound. Without one, a trap that hangs - #1048's untrusted
// destroy step blocked for ~40 minutes at 0% CPU, which is why
// UNTRUSTED_TEARDOWN_TIMEOUT_S exists - turns a stop request into a process
// that never comes back, and the operator is back to signalling pids by hand.
// The default is generous because a real teardown at scale is minutes of
// destroy plus an independent listing; live/live-cert/run.sh's own `timeout
// --signal=TERM --kill-after=30` is the same idea with a tighter number,
// around a script that has not yet been asked to tear down.
const (
	LiveCertSignalGraceEnv      = "LIVECERT_SIGNAL_GRACE_S"
	LiveCertSignalGraceDefaultS = 600
)

func liveCertSignalGrace() time.Duration {
	if v := os.Getenv(LiveCertSignalGraceEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return LiveCertSignalGraceDefaultS * time.Second
}

// liveCertSupervisor is the state a stop request leaves behind. Its fields
// are read from the goroutine that owns the wait, so they are behind a mutex.
type liveCertSupervisor struct {
	mu        sync.Mutex
	signalled bool
	signal    string
	escalated bool
}

func (s *liveCertSupervisor) stopRequested(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signalled {
		return false
	}
	s.signalled = true
	s.signal = name
	return true
}

func (s *liveCertSupervisor) escalate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.escalated = true
}

// Report is what the run ended up being: whether a stop request happened,
// what it was, and whether the group had to be killed outright.
func (s *liveCertSupervisor) Report() (signalled bool, signal string, escalated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.signalled, s.signal, s.escalated
}

// State turns the report plus teardown's own verdict into the run state.
func (s *liveCertSupervisor) State(teardownConfirmed bool) LiveCertRunState {
	signalled, _, _ := s.Report()
	switch {
	case !signalled:
		return RunStateFinished
	case teardownConfirmed:
		return RunStateSignalledTornDown
	default:
		return RunStateSignalledUnconfirmed
	}
}

// signalGroup sends sig to the whole process group pgid. The negative pid is
// the point: the script's `terraform plan` and its own background jobs are in
// that group too, and signalling the bash leader alone leaves them running -
// which is the shape the #1324 process listing shows.
func signalGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 1 {
		return fmt.Errorf("refusing to signal process group %d", pgid)
	}
	return syscall.Kill(-pgid, sig)
}

// superviseLiveCert watches for a stop request while the script runs, and
// returns a function the caller runs after cmd.Wait() to shut the watcher
// down.
//
// sigc carries signals the caller has already registered with signal.Notify;
// done is closed by the caller once the wait has returned. say writes one
// line to wherever the operator is looking. onStop is called the moment a
// stop request is forwarded, before any waiting, so the run record can be
// stamped "signalled, teardown unconfirmed" while the estate is still coming
// down - the state on disk has to be true at the worst moment, not only at
// the end.
func superviseLiveCert(s *liveCertSupervisor, pgid int, sigc <-chan os.Signal, done <-chan struct{}, grace time.Duration, say func(string, ...any), onStop func(signal string)) {
	startPPID := os.Getppid()
	tick := time.NewTicker(liveCertOrphanPoll)
	defer tick.Stop()
	var graceC <-chan time.Time
	// The ppid stays 1 for the rest of the run, so the orphan check
	// matches on every tick from here on. It is ONE event and has to fire
	// once: without this, the second tick arrives a second later, looks
	// like a second stop request, and escalates to SIGKILL on a teardown
	// that had barely started. Measured while writing this - the trap got
	// 1 second of its 2-second teardown and the run recorded
	// "signalled, teardown unconfirmed" on a fix that was working.
	//
	// A second real SIGNAL still escalates, and should: that is an
	// operator pressing Ctrl-C twice because they are done waiting.
	orphanFired := false

	stop := func(name string, sig syscall.Signal, why string) {
		if !s.stopRequested(name) {
			// A second stop request means the operator is not waiting
			// any more. Escalate rather than pretend to.
			s.escalate()
			say("live-cert: %s again - the first stop request is still being torn down. Escalating to SIGKILL on process group %d; the teardown trap cannot run after this and the run record stays \"signalled, teardown unconfirmed\".", name, pgid)
			_ = signalGroup(pgid, syscall.SIGKILL)
			return
		}
		onStop(name)
		say("live-cert: %s - %s Forwarding to the script's whole process group (pgid %d) and WAITING for its teardown trap; this process does not exit before the teardown it is responsible for (#1324). Grace: %s, after which the group is SIGKILLed and teardown is recorded UNCONFIRMED.", name, why, pgid, grace)
		if err := signalGroup(pgid, sig); err != nil {
			say("live-cert: forwarding %s to process group %d failed: %v - signal the script by hand (`ps -eo pid,ppid,pgid,command`) or the estate stays up.", name, pgid, err)
		}
		graceC = time.After(grace)
	}

	for {
		select {
		case <-done:
			return
		case sig, ok := <-sigc:
			if !ok {
				return
			}
			sys, isSys := sig.(syscall.Signal)
			if !isSys {
				sys = syscall.SIGTERM
			}
			stop(sig.String(), sys, "an operator or a runner asked this run to stop.")
		case <-tick.C:
			// The orphan case (#1324's own measurement): `go run` was
			// signalled, died, and this process reparented to init. No
			// signal ever reached here, so nothing else in this loop
			// can see it; the ppid transition is the only evidence.
			if !orphanFired && startPPID != 1 && os.Getppid() == 1 {
				orphanFired = true
				stop("orphaned", syscall.SIGTERM, "this process's parent exited and it reparented to init (ppid 1), which is what a signalled `go run` wrapper looks like from in here - `go run` does not pass the signal on to the binary it built (measured, go1.26.5).")
			}
		case <-graceC:
			s.escalate()
			say("live-cert: teardown did not finish within %s of the stop request - SIGKILLing process group %d. A trap cannot run after SIGKILL, so this run's teardown is UNCONFIRMED and the estate must be checked by hand.", grace, pgid)
			_ = signalGroup(pgid, syscall.SIGKILL)
			graceC = nil
		}
	}
}

// sayTo builds the say function the supervisor uses: one line to the
// operator's terminal and the same line into the run log, so the log a
// certification uploads carries the stop request too.
//
// The log writer is the synchronized one RunLiveCert hands over, because the
// exec package's own copy goroutine is writing to it at the same time and
// neither a strings.Builder nor an os.File is safe for two writers at once.
func sayTo(logw io.Writer) func(string, ...any) {
	return func(format string, args ...any) {
		line := fmt.Sprintf(format, args...) + "\n"
		fmt.Print(line)
		if logw != nil {
			_, _ = io.WriteString(logw, line)
		}
	}
}

// ── the reader ──────────────────────────────────────────────────────────

// cmdLiveCertState is `gauntlet live-cert-state <estate>`: the reader half of
// #1324, and the half that makes the record worth writing.
//
// scripts/ci-gate.sh has the same two commands for the same reason - `run`
// writes the gate, `check` refuses a gate it cannot trust - and the lesson
// there was that the writing alone changes nothing, because whatever was
// already reading the rc file keeps reading the rc file. This command exits 0
// only for a run that finished, and prints the reason on every other path.
//
// -commit defaults to the checkout's HEAD, so the ordinary invocation asks
// "did a run finish against the tree I am looking at" rather than "did a run
// finish", which is the question #1307 showed people actually mean.
func cmdLiveCertState(root string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("live-cert-state", flag.ContinueOnError)
	commit := fs.String("commit", "", "refuse a record written for a different commit (default: this checkout's HEAD; \"any\" to skip the check)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("live-cert-state needs exactly one estate name, got %d", fs.NArg())
	}
	estate := fs.Arg(0)

	want := *commit
	switch want {
	case "any":
		want = ""
	case "":
		head, err := headCommit(root)
		if err != nil {
			return fmt.Errorf("live-cert-state %s: cannot resolve HEAD to check the record against (pass -commit any to skip): %w", estate, err)
		}
		want = head
	}

	rec, err := ReadLiveCertRun(root, estate)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	ok, why := rec.FinishedMeasurement(want)
	fmt.Fprintf(stdout, "live-cert-state %s: %s\n", estate, why)
	if rec != nil {
		fmt.Fprintf(stdout, "live-cert-state %s: record %s state=%s target=%s commit=%s signal=%s teardown_confirmed=%v exit=%d\n",
			estate, LiveCertRunPath(root, estate), rec.State, rec.Target, short(rec.Commit), orNone(rec.Signal), rec.TeardownConfirmed, rec.ExitCode)
	}
	if !ok {
		return fmt.Errorf("live-cert-state %s: REFUSED - this is not a finished measurement", estate)
	}
	return nil
}
