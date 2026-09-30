// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// No bare `wait` in a smoke scenario. Since #1593 smoke.sh starts its stall
// watchdog (lib.sh's smoke_timer) as a background child of the shell that
// sources the scenario, so a scenario's bare `wait` waits on the watchdog
// too: it blocks for the whole bound, and then the watchdog kills the run
// as stalled. That is how no-self-managed-locks and two-estates-at-once
// both died at 600s on claims-smoke run 36339857046, their applies long
// finished. A scenario waits on the PIDs it started.
//
// Proving it red: put a bare `wait` back in either scenario.

// bareWait is `wait` with no operand, in command position, followed only
// by redirections before the command ends. #1741: the first version wanted
// the command to end right after the word, so `wait 2>/dev/null` and
// `if true; then wait >/dev/null; fi` - both the #1718 stall - passed. A
// redirection is not an operand: it names no PID, and the wait still waits
// on the watchdog. `{` and `!` open a command position like `(` and `then`.
var bareWait = regexp.MustCompile(`(^|[;&|({!]|\bthen|\bdo|\belse)\s*wait(\s*(?:[0-9]*|&)(?:>>?|<)(?:&[0-9-]|\s*[^\s;&|)<>]+))*\s*($|[;&|)}#])`)

func TestSmokeScenariosNeverWaitBare(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("smoke", "scenarios", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no smoke scenarios found; the guard would pass on nothing")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "#") {
				continue
			}
			if bareWait.MatchString(code) {
				t.Errorf("%s:%d: bare `wait` also waits on smoke.sh's stall watchdog and stalls the run for its whole bound; wait on the PIDs you started: %s", f, i+1, code)
			}
		}
	}
}

func TestBareWaitPattern(t *testing.T) {
	for _, s := range []string{"wait", "wait # both", ") & wait", "foo; wait", "then wait",
		// #1741: redirections are not operands.
		"wait 2>/dev/null", "if true; then wait >/dev/null; fi", "wait &>/dev/null", "wait > /dev/null 2>&1",
		"wait >>log || true", "{ wait; }", "wait 2>&1"} {
		if !bareWait.MatchString(s) {
			t.Errorf("pattern misses bare wait in %q", s)
		}
	}
	for _, s := range []string{`wait "$PID_A" "$PID_B" || true`, "wait $pid 2>/dev/null || true", `wait "$APPLY_PID" 2>/dev/null || APPLY_RC=$?`, "await", "wait_for_it",
		"wait $pid >/dev/null 2>&1", `wait "$PID" 2>/dev/null`, "echo wait 2>/dev/null", "waited=1"} {
		if bareWait.MatchString(s) {
			t.Errorf("pattern flags a PID wait or a non-wait: %q", s)
		}
	}
}
