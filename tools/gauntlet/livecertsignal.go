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
	// Escalated: the OPT-IN bound (LIVECERT_SIGNAL_GRACE_S) expired and
	// the process group was SIGKILLed. A trap cannot run after SIGKILL, so
	// this always accompanies an unconfirmed teardown. Nothing sets it
	// unless someone asked for a bound: a repeat signal never does.
	Escalated bool `json:"escalated,omitempty"`
	// RepeatSignals is how many further stop requests arrived while the
	// teardown was already running and were deliberately ignored. It is
	// recorded because it is the difference between "nobody tried to stop
	// this twice" and "three signals arrived and the teardown was allowed
	// to finish anyway", and only the second explains a long wait.
	RepeatSignals int `json:"repeat_signals,omitempty"`
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

// LiveCertSignalGraceEnv is an OPT-IN bound on how long the supervisor waits
// for the script's teardown trap after forwarding a stop request. Unset, or
// set to anything that is not a positive integer, there is NO bound: the
// supervisor waits for as long as the trap takes and never kills a teardown
// on its own.
//
// It shipped the other way round for a few hours and that was this issue's
// own defect coming back through its fix. A 600-second default sounds
// generous until it meets the run #1324 was filed from: 39,610 seconds
// total, 5,633 of them in the cold apply alone. Tearing a scale-128 estate
// down - destroy, then an independent listing, then a sweep - is far longer
// than ten minutes, so an operator who stopped that run would have got ten
// minutes of teardown and then the harness itself SIGKILLing the destroy
// with thousands of billable resources standing. Before any of this existed
// the trap at least ran to completion once somebody signalled it by hand.
// Making the tool worse than the workaround is not a fix.
//
// The hang case is real - #1048's untrusted destroy step blocked for ~40
// minutes at 0% CPU - but it is already owned where it belongs, inside the
// script: UNTRUSTED_TEARDOWN_TIMEOUT_S bounds that step, and the trusted
// destroy and the sweep carry their own timeouts. A second bound out here
// cannot tell a hung teardown from a slow one, and guessing wrong costs an
// estate. What this side owes the operator instead is to say what it is
// waiting for, which the "still waiting" line below does, and to say how to
// abandon the wait by hand, which the repeat-signal line does.
const LiveCertSignalGraceEnv = "LIVECERT_SIGNAL_GRACE_S"

// liveCertSignalBound reads the opt-in bound. 0 means unbounded, which is
// the default.
func liveCertSignalBound() time.Duration {
	if n, err := strconv.Atoi(os.Getenv(LiveCertSignalGraceEnv)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}

// LiveCertHeartbeatEnv is the interval for the "still waiting for teardown"
// line, shared with the heartbeat live/live-cert/terralith-scale.sh prints
// inside a stage so the two sides of the same run tick at the same rate.
//
// The wait needs this for the reason the stages did (#1324's second defect):
// a teardown that takes an hour and says nothing is indistinguishable from a
// hung one, and an operator who cannot tell will reach for a kill.
const (
	LiveCertHeartbeatEnv      = "LIVECERT_HEARTBEAT_S"
	LiveCertHeartbeatDefaultS = 60
)

func liveCertWaitTick() time.Duration {
	if n, err := strconv.Atoi(os.Getenv(LiveCertHeartbeatEnv)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return LiveCertHeartbeatDefaultS * time.Second
}

// liveCertSupervisor is the state a stop request leaves behind. Its fields
// are read from the goroutine that owns the wait, so they are behind a mutex.
type liveCertSupervisor struct {
	mu        sync.Mutex
	signalled bool
	signal    string
	escalated bool
	// repeats is how many further stop requests arrived while the
	// teardown was already running. None of them did anything; the count
	// is here so the record can say so.
	repeats int
	// ceiling: the stop request was the -timeout-seconds ceiling rather
	// than a signal. RunLiveCert reports such a run's exit as -1, which is
	// what its doc comment has always promised.
	ceiling bool
}

func (s *liveCertSupervisor) noteRepeat() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repeats++
}

func (s *liveCertSupervisor) noteCeiling() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ceiling = true
}

// Repeats is how many stop requests arrived and were deliberately ignored.
func (s *liveCertSupervisor) Repeats() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repeats
}

// HitCeiling reports whether the run was stopped by its own ceiling.
func (s *liveCertSupervisor) HitCeiling() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ceiling
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

// superviseOpts is what superviseLiveCert needs. A struct rather than eight
// positional parameters, because most of them are durations and a caller
// that swapped two of them would compile.
type superviseOpts struct {
	// PGID is the script's own process group.
	PGID int
	// Sigc carries signals the caller registered with signal.Notify; Done
	// is closed once cmd.Wait has returned.
	Sigc <-chan os.Signal
	Done <-chan struct{}
	// Ceiling is the -timeout-seconds bound on the RUN. 0 disables it.
	// It is handled here, as one more kind of stop request, rather than by
	// exec.CommandContext: see RunLiveCert for why that mattered.
	Ceiling time.Duration
	// Bound is the OPT-IN bound on the teardown itself
	// (LIVECERT_SIGNAL_GRACE_S). 0 means the teardown is never killed.
	Bound time.Duration
	// Tick is how often to say that the wait is still going.
	Tick time.Duration
	Say  func(string, ...any)
	// OnStop is called the moment the first stop request is forwarded,
	// before any waiting, so the run record is stamped "signalled,
	// teardown unconfirmed" while the estate is still coming down. The
	// state on disk has to be true at the worst moment, not only at the
	// end.
	OnStop func(signal string)
}

// superviseLiveCert watches for a stop request while the script runs.
//
// The policy, which is the part worth stating plainly: ONE stop request is
// forwarded to the process group, and then this waits for the trap for as
// long as the trap takes. It does not kill a teardown. Not on a second
// signal, not on a third, not after any default interval.
//
// The second-signal case is the one that looks harmless and is not. An
// operator hits Ctrl-C, sees that tearing down 9,477 resources will take an
// hour, and closes the terminal - SIGHUP arrives, and on any "second signal
// escalates" rule that SIGHUP kills the destroy. GitHub's own cancellation
// sends SIGINT and then SIGTERM, which is two signals by itself; so is an
// orphan detection followed by a real signal. Every one of those is a normal
// thing to do and none of them is a request to abandon an estate.
//
// Abandoning it stays possible and stays explicit: the repeat line prints
// the exact `kill -KILL -<pgid>` and what it costs, so a human who really
// does want the teardown dead can have it, by typing it.
func superviseLiveCert(s *liveCertSupervisor, o superviseOpts) {
	startPPID := os.Getppid()
	poll := time.NewTicker(liveCertOrphanPoll)
	defer poll.Stop()

	var ceilingC <-chan time.Time
	if o.Ceiling > 0 {
		ct := time.NewTimer(o.Ceiling)
		defer ct.Stop()
		ceilingC = ct.C
	}
	var boundC <-chan time.Time
	var waitC <-chan time.Time
	var waitTicker *time.Ticker
	defer func() {
		if waitTicker != nil {
			waitTicker.Stop()
		}
	}()
	var stoppedAt time.Time
	orphanFired := false

	// Always SIGTERM to the group, whatever the stop request was.
	//
	// Not a simplification - a measurement. POSIX has a shell without job
	// control set SIGINT and SIGQUIT to SIG_IGN for every command it runs
	// asynchronously, so a backgrounded child does not hear a SIGINT sent
	// to its process group. Measured on bash 3.2.57 / darwin, with a
	// `sleep &` inside a script in its own process group:
	//
	//   kill -INT  -<pgid>  ->  the backgrounded child SURVIVED
	//   kill -TERM -<pgid>  ->  the backgrounded child died
	//
	// live/live-cert/terralith-scale.sh backgrounds cold_deploy's stock
	// apply on purpose ("backgrounded so a signal can interrupt it"), so
	// forwarding a Ctrl-C as SIGINT would reach the script and not the
	// terraform apply underneath it. The script's own on_signal covers
	// that one child by pid, but nothing covers a descendant it does not
	// know about, and #1324's whole subject is a `terraform plan` nobody
	// had signalled. Found by the repeat-signal test, which left a
	// backgrounded grandchild alive after a forwarded SIGINT.
	//
	// Nothing is lost by the substitution: the script traps INT and TERM
	// with the same handler, so its teardown path is identical either way.
	const forwarded = syscall.SIGTERM

	stop := func(name string, why string) {
		if !s.stopRequested(name) {
			since := time.Since(stoppedAt).Round(time.Second)
			s.noteRepeat()
			o.Say("live-cert: %s, %s after the stop request that is already being torn down. NOT forwarding it again and NOT killing anything - a second signal is not a request to abandon an estate (closing a terminal sends SIGHUP; a runner's cancellation sends SIGINT and then SIGTERM). The teardown is still running and this process is still waiting for it. To abandon it anyway, by hand and on purpose: kill -KILL -%d - that leaves every resource this run created live and billing, with no teardown and no verified-empty listing, and you own finding them.", name, since, o.PGID)
			return
		}
		stoppedAt = time.Now()
		o.OnStop(name)
		bound := "no bound: this process waits for the trap for as long as it takes, and never kills a teardown itself"
		if o.Bound > 0 {
			bound = fmt.Sprintf("%s=%s is set, so the group is SIGKILLed after that and teardown is recorded UNCONFIRMED", LiveCertSignalGraceEnv, o.Bound)
		}
		o.Say("live-cert: %s - %s Forwarding to the script's whole process group (pgid %d) and WAITING for its teardown trap; this process does not exit before the teardown it is responsible for (#1324). Teardown at scale is tens of minutes: %s.", name, why, o.PGID, bound)
		if err := signalGroup(o.PGID, forwarded); err != nil {
			o.Say("live-cert: forwarding %s to process group %d failed: %v - signal the script by hand (`ps -eo pid,ppid,pgid,command`) or the estate stays up.", name, o.PGID, err)
		}
		if o.Bound > 0 {
			bt := time.NewTimer(o.Bound)
			boundC = bt.C
		}
		waitTicker = time.NewTicker(o.Tick)
		waitC = waitTicker.C
	}

	for {
		select {
		case <-o.Done:
			return
		case sig, ok := <-o.Sigc:
			if !ok {
				return
			}
			// SIGPIPE is not a stop request and must never be treated
			// as one. It is registered at all because a Go program
			// that has NOT asked for SIGPIPE dies when a write to fd
			// 1 or 2 gets one - so an operator who closes the
			// terminal on a `gauntlet live-cert ... | tee` would kill
			// this process in the middle of the teardown it is
			// waiting for. Receiving it on this channel is what stops
			// that; dropping it here is the rest of the answer. The
			// failed write returns an error to a caller that ignores
			// it, and the run log is a separate file that keeps
			// growing.
			if sys, isSys := sig.(syscall.Signal); isSys && sys == syscall.SIGPIPE {
				continue
			}
			stop(sig.String(), "an operator or a runner asked this run to stop.")
		case <-ceilingC:
			// The -timeout-seconds ceiling, as a stop request like
			// any other, so it gets the same "wait for the trap"
			// treatment. HANDOFF and live-cert.yml both call this the
			// safe way to stop a certification; it has to be that.
			s.noteCeiling()
			stop("the run ceiling expired", "the -timeout-seconds ceiling expired.")
		case <-poll.C:
			// The orphan case (#1324's own measurement): `go run` was
			// signalled, died, and this process reparented to init. No
			// signal ever reached here, so nothing else in this loop
			// can see it; the ppid transition is the only evidence.
			if !orphanFired && startPPID != 1 && os.Getppid() == 1 {
				orphanFired = true
				stop("orphaned", "this process's parent exited and it reparented to init (ppid 1), which is what a signalled `go run` wrapper looks like from in here - `go run` does not pass the signal on to the binary it built (measured, go1.26.5).")
			}
		case <-waitC:
			// The wait is not silent. #1324's second defect is that a
			// long silent stage cannot be told from a hung one, and a
			// long silent teardown is the same thing at the worst
			// moment.
			o.Say("live-cert: still waiting for teardown, %s since the stop request, pgid %d. Nothing here will kill it; `kill -KILL -%d` abandons it by hand and leaves the estate up.",
				time.Since(stoppedAt).Round(time.Second), o.PGID, o.PGID)
		case <-boundC:
			// Only reachable when LIVECERT_SIGNAL_GRACE_S was set on
			// purpose.
			s.escalate()
			o.Say("live-cert: %s=%s expired - SIGKILLing process group %d. A trap cannot run after SIGKILL, so this run's teardown is UNCONFIRMED and the estate must be checked by hand.", LiveCertSignalGraceEnv, o.Bound, o.PGID)
			_ = signalGroup(o.PGID, syscall.SIGKILL)
			boundC = nil
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

// liveCertWaitDelay is cmd.WaitDelay for the script. A var rather than a
// constant so a test can lower it: what it bounds is measured in tens of
// seconds, and a test that had to sit through the real value would be a test
// nobody runs.
var liveCertWaitDelay = 30 * time.Second
