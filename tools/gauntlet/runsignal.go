// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Stopping a `gauntlet run` (#2037).
//
// On 2026-10-12 a 21-estate run was killed at 3h18m and the estate it was
// running (cloudnative-pg) kept going: `gauntlet run` started every script
// in the runner's own process group and caught no signals, so the runner
// died and its children were left to whatever the terminal happened to send
// them. live-cert solved the same problem for one script in #1324
// (livecertsignal.go); this is that policy for several scripts at once:
//
//   - every script leads its own process group (runOne's Setpgid), so one
//     kill to the negative pgid reaches the script, the tofu it is blocked on
//     and anything it backgrounded;
//   - INT, TERM and HUP are caught, and the FIRST one is forwarded as
//     stopSignal to every running group, once. Repeats are reported and not
//     forwarded, for live-cert's reason: a second signal is a closed
//     terminal or a runner's own INT-then-TERM, not a request to abandon a
//     teardown;
//   - the runner then waits for every group's script to exit, starts no
//     further estate, keeps every row that finished (RunOptions.Checkpoint
//     has already saved each one), records nothing for an estate the stop
//     interrupted, and exits non-zero.
//
// What it deliberately leaves out of live-cert's supervisor: the -timeout
// ceiling, the opt-in SIGKILL bound and the re-send until the trap answers.
// An estate script's teardown is a docker rm or a kind delete, nothing bills,
// and no e2e script prints a trap banner the re-send could wait for - so a
// re-send could only ever land on a teardown that had already started.

// stopSignal is what a stop request is forwarded to a script's group as,
// whatever arrived. Always SIGTERM: a shell without job control ignores
// SIGINT in its asynchronous children, so a forwarded SIGINT reaches the
// script and misses what it backgrounded. Measured for live-cert and
// recorded where its supervisor uses the same constant
// (superviseLiveCert, livecertsignal.go).
const stopSignal = syscall.SIGTERM

// runStop is one run's stop request and the process groups it has to reach.
// Every method is safe from any goroutine: runResults starts and finishes
// scripts on one goroutine per slot, and watch runs on its own.
type runStop struct {
	mu     sync.Mutex
	groups map[int]string // pgid -> estate, for every script running now
	signal string         // "" until the first stop request
	at     time.Time
	// reached is every group the stop request was forwarded to, so finish
	// can tell an estate the stop interrupted from one that ended on its own
	// a moment before the signal.
	reached map[int]bool
	say     func(string, ...any)
}

func newRunStop(say func(string, ...any)) *runStop {
	return &runStop{groups: map[int]string{}, reached: map[int]bool{}, say: say}
}

// Requested is the stop request's signal name, or "" when there has been
// none. runResults checks it before starting each estate.
func (s *runStop) Requested() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.signal
}

// start registers a group that has just started. A script that started in
// the instant after a stop request was forwarded is signalled here, so no
// group outlives the request because it was a moment late.
func (s *runStop) start(pgid int, estate string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups[pgid] = estate
	if s.signal != "" {
		s.reached[pgid] = true
		s.forwardLocked(pgid, estate)
	}
}

// finish deregisters a group whose script has exited and reports whether
// the stop request reached it while it ran.
func (s *runStop) finish(pgid int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.groups, pgid)
	hit := s.reached[pgid]
	delete(s.reached, pgid)
	return hit
}

func (s *runStop) forwardLocked(pgid int, estate string) {
	if err := signalGroup(pgid, stopSignal); err != nil {
		s.say("gauntlet run: forwarding the stop to %s (pgid %d) failed: %v - signal it by hand (`ps -eo pid,ppid,pgid,command`) or it keeps running.", estate, pgid, err)
	}
}

// running is the estates still running, sorted, with their pgids, for the
// operator's lines.
func (s *runStop) runningLocked() string {
	var parts []string
	for pgid, estate := range s.groups {
		parts = append(parts, fmt.Sprintf("%s (pgid %d)", estate, pgid))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// stop is one stop request. The first is forwarded to every running group;
// a repeat is said and dropped.
func (s *runStop) stop(name, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signal != "" {
		s.say("gauntlet run: %s, %s after the stop request already being handled. NOT forwarding it again: every running script already has it and is tearing down, and this process waits for them. Still running: %s. To abandon a teardown anyway, by hand: kill -KILL -<pgid>, which leaves its containers or cluster behind.", name, time.Since(s.at).Round(time.Second), s.runningLocked())
		return
	}
	s.signal, s.at = name, time.Now()
	s.say("gauntlet run: %s - %s Starting no further estate, forwarding %s to every running script's process group and WAITING for their teardown (#2037). Running: %s. Every estate that already finished is saved in %s.", name, why, stopSignal, s.runningLocked(), ArtifactPath)
	for pgid, estate := range s.groups {
		s.reached[pgid] = true
		s.forwardLocked(pgid, estate)
	}
}

// watch turns signals and orphaning into stop requests until done closes.
// sigc is what the caller registered with signal.Notify (or a test's own
// channel). startPPID is this process's parent when the run began, captured
// by the caller before this goroutine was started, for #1618's reason (see
// superviseOpts.StartPPID): `just gauntlet-run` is `go run`, which does not
// pass a signal on to the binary it built, so a killed wrapper shows up here
// only as this process reparenting to init.
func (s *runStop) watch(sigc <-chan os.Signal, done <-chan struct{}, startPPID int, tick time.Duration) {
	poll := time.NewTicker(liveCertOrphanPoll)
	defer poll.Stop()
	heartbeat := time.NewTicker(tick)
	defer heartbeat.Stop()
	orphaned := false
	for {
		select {
		case <-done:
			return
		case sig, ok := <-sigc:
			if !ok {
				return
			}
			// SIGPIPE is registered only so that a write to a dead
			// stdout (`gauntlet run ... | tee`, terminal closed) does not
			// kill this process mid-wait; it is not a stop request.
			if sys, isSys := sig.(syscall.Signal); isSys && sys == syscall.SIGPIPE {
				continue
			}
			s.stop(sig.String(), "an operator or a runner asked this run to stop.")
		case <-poll.C:
			if !orphaned && startPPID != 1 && currentPPID() == 1 {
				orphaned = true
				s.stop("orphaned", "this process's parent exited and it reparented to init, which is what a killed `go run` wrapper looks like from in here.")
			}
		case <-heartbeat.C:
			s.mu.Lock()
			if s.signal != "" && len(s.groups) > 0 {
				s.say("gauntlet run: still waiting for teardown, %s since the stop request: %s.", time.Since(s.at).Round(time.Second), s.runningLocked())
			}
			s.mu.Unlock()
		}
	}
}

// RunInterrupted is RunEstates's error when a stop request ended the run
// early. The rows that finished are already in the artifact; cmdRun renders
// them and exits non-zero.
type RunInterrupted struct {
	Signal      string
	Finished    []string // merged into the artifact
	Interrupted []string // running when the stop arrived; nothing recorded
	NotStarted  []string
}

func (e *RunInterrupted) Error() string {
	return fmt.Sprintf("gauntlet run: stopped by %s. %d estate(s) finished and are saved (%s); %d interrupted and recorded nothing (%s); %d never started (%s). Re-run the interrupted and unstarted ones by name.",
		e.Signal, len(e.Finished), listOrNone(e.Finished), len(e.Interrupted), listOrNone(e.Interrupted), len(e.NotStarted), listOrNone(e.NotStarted))
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, " ")
}
