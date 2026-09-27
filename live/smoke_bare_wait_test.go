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

var bareWait = regexp.MustCompile(`(^|[;&|(]|\bthen|\bdo|\belse)\s*wait\s*($|[;&|)#])`)

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
	for _, s := range []string{"wait", "wait # both", ") & wait", "foo; wait", "then wait"} {
		if !bareWait.MatchString(s) {
			t.Errorf("pattern misses bare wait in %q", s)
		}
	}
	for _, s := range []string{`wait "$PID_A" "$PID_B" || true`, "wait $pid 2>/dev/null || true", `wait "$APPLY_PID" 2>/dev/null || APPLY_RC=$?`, "await", "wait_for_it"} {
		if bareWait.MatchString(s) {
			t.Errorf("pattern flags a PID wait or a non-wait: %q", s)
		}
	}
}
