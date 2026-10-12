// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// sides_test.go: #2037's guard over live/e2e/lib/gauntlet.sh's
// gauntlet_side_start, gauntlet_side_wait and gauntlet_sides_stop - the
// helpers a kubernetes-lane script uses to run stock's oracle work on
// cluster B while its own work on cluster A runs in the foreground.
//
// No cluster and no Docker: the sides here are sleeps and echoes, because
// what is under test is the shell plumbing - that the two sides overlap,
// that a failure on the side reaches the foreground with the sentence the
// side's line was written with, that a side can never print a verdict, and
// that a script that exits or is killed takes the side and everything under
// it down with it before its clusters would be deleted.

// sidesScript is the head of every arm: the real library, a fail() and a
// cleanup() shaped exactly like every kind script's (CURRENT_STAGE set, so a
// fail() that reached gauntlet_stage would print a verdict line).
func sidesScript(t *testing.T, work, body string) string {
	t.Helper()
	lib := filepath.Join(testRoot(t), "live", "e2e", "lib", "gauntlet.sh")
	if _, err := os.Stat(lib); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`set -uo pipefail
source %q
WORK=%q
CURRENT_STAGE=""
fail() {
  printf 'FAIL: %%s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
cleanup() {
  gauntlet_sides_stop
  echo "CLEANUP_RAN"
}
trap cleanup EXIT
gauntlet_begin
gauntlet_begin_stage cold_deploy
%s
`, lib, work, body)
}

func TestGauntletSidesRunTogetherAndJoinInAFixedOrder(t *testing.T) {
	work := t.TempDir()
	// Each side sleeps 2s. One after the other that is 4s; together, 2s.
	body := `
oracle() { echo "oracle: applying on B"; sleep 2; echo "oracle: done on B"; }
gauntlet_side_start oracle_cold oracle || fail "could not start the oracle"
echo "estate: applying on A"; sleep 2; echo "estate: done on A"
gauntlet_side_wait oracle_cold || fail "$GAUNTLET_SIDE_FAIL"
gauntlet_stage cold_deploy pass "both sides"
gauntlet_end
`
	start := time.Now()
	b, err := runBash(sidesScript(t, work, body))
	elapsed := time.Since(start)
	out := string(b)
	if err != nil {
		t.Fatalf("the script failed: %v\n%s", err, out)
	}
	if elapsed > 3500*time.Millisecond {
		t.Errorf("two 2s sides took %s; they did not overlap", elapsed)
	}
	if !strings.Contains(out, "GAUNTLET stage=cold_deploy verdict=pass") {
		t.Errorf("want the stage's pass verdict; got:\n%s", out)
	}
	// The foreground's lines, then the side's under its header, indented.
	a := strings.Index(out, "estate: done on A")
	h := strings.Index(out, "--- oracle_cold, run alongside, exit 0 ---")
	s1 := strings.Index(out, "  | oracle: applying on B")
	s2 := strings.Index(out, "  | oracle: done on B")
	if a < 0 || h < 0 || s1 < 0 || s2 < 0 || !(a < h && h < s1 && s1 < s2) {
		t.Errorf("want A's lines, then the side's header, then its log indented in order; got:\n%s", out)
	}
}

func TestGauntletSideFailureKeepsItsSentenceAndPrintsNoVerdict(t *testing.T) {
	work := t.TempDir()
	body := `
oracle() { false || fail "stock's main apply failed on B"; echo "never reached"; }
gauntlet_side_start oracle_cold oracle
echo "estate: fine on A"
gauntlet_side_wait oracle_cold || fail "$GAUNTLET_SIDE_FAIL"
gauntlet_stage cold_deploy pass "must not be reached"
`
	b, _ := runBash(sidesScript(t, work, body))
	out := string(b)
	// Exactly one verdict line, the foreground's, with the side's sentence.
	if n := strings.Count(out, "GAUNTLET stage="); n != 1 {
		t.Errorf("want exactly one verdict line; got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "GAUNTLET stage=cold_deploy verdict=fail duration_s=") ||
		!strings.Contains(out, "detail=stock's main apply failed on B") {
		t.Errorf("the stage must fail with the side's own sentence; got:\n%s", out)
	}
	if strings.Contains(out, "never reached") || strings.Contains(out, "must not be reached") {
		t.Errorf("a fail() must end the side, and the failed join must end the script; got:\n%s", out)
	}
	if !strings.Contains(out, "CLEANUP_RAN") {
		t.Errorf("the EXIT trap must run; got:\n%s", out)
	}
}

func TestGauntletSideThatNeverCalledFailStillFails(t *testing.T) {
	work := t.TempDir()
	body := `
gauntlet_side_start kind_b false
gauntlet_side_wait kind_b || fail "kind cluster B (b) did not come up"
gauntlet_stage cold_deploy pass "must not be reached"
`
	b, _ := runBash(sidesScript(t, work, body))
	out := string(b)
	if !strings.Contains(out, "verdict=fail") || !strings.Contains(out, "detail=kind cluster B (b) did not come up") {
		t.Errorf("a side's non-zero exit must fail the caller's line with the caller's sentence; got:\n%s", out)
	}
}

func TestGauntletSideCannotPrintAVerdict(t *testing.T) {
	work := t.TempDir()
	body := `
oracle() { gauntlet_stage day2_remove pass "from the background"; }
gauntlet_side_start oracle_cold oracle
gauntlet_side_wait oracle_cold || fail "$GAUNTLET_SIDE_FAIL"
gauntlet_stage cold_deploy pass "must not be reached"
`
	b, _ := runBash(sidesScript(t, work, body))
	out := string(b)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "GAUNTLET stage=day2_remove") {
			t.Errorf("a side's protocol line reached the runner unindented: %q\n%s", line, out)
		}
	}
	if !strings.Contains(out, "verdict=fail") || !strings.Contains(out, "printed a GAUNTLET protocol line from the background") {
		t.Errorf("a side that prints a protocol line must be refused at the join; got:\n%s", out)
	}
}

// sideProcAlive reports whether a process exists (signal 0).
func sideProcAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// sideReadPid waits up to 10s for a pid file a side wrote.
func sideReadPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no pid in %s", path)
	return 0
}

func TestGauntletSidesStopWhenTheForegroundFails(t *testing.T) {
	work := t.TempDir()
	pidfile := filepath.Join(work, "grandchild.pid")
	// The side's long-running work is a grandchild, the way terraform or
	// kind sits under the side's subshell.
	body := fmt.Sprintf(`
oracle() { bash -c 'echo $$ > %q; exec sleep 300'; }
gauntlet_side_start oracle_cold oracle
while [ ! -s %q ]; do sleep 0.1; done
false || fail "stock's main apply failed on A"
`, pidfile, pidfile)
	start := time.Now()
	b, _ := runBash(sidesScript(t, work, body))
	out := string(b)
	if time.Since(start) > 20*time.Second {
		t.Errorf("the script took %s to exit; the side was not stopped", time.Since(start))
	}
	if !strings.Contains(out, "detail=stock's main apply failed on A") {
		t.Errorf("the foreground's own failure must be the verdict; got:\n%s", out)
	}
	pid := sideReadPid(t, pidfile)
	if sideProcAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("the side's grandchild %d outlived the script\n%s", pid, out)
	}
}

func TestGauntletSidesStopWhenTheScriptIsInterrupted(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			work := t.TempDir()
			pidfile := filepath.Join(work, "grandchild.pid")
			ready := filepath.Join(work, "ready")
			body := fmt.Sprintf(`
oracle() { bash -c 'echo $$ > %q; exec sleep 300'; }
gauntlet_side_start oracle_cold oracle
while [ ! -s %q ]; do sleep 0.1; done
: > %q
sleep 300
`, pidfile, pidfile, ready)
			cmd := exec.Command("bash", "-c", sidesScript(t, work, body))
			// Its own group, as the runner starts an estate script (#2037 PR 1),
			// so the signal below goes to every process in it - the
			// foreground's sleep, the script, the side and its grandchild.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			var buf strings.Builder
			cmd.Stdout = &buf
			cmd.Stderr = &buf
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					t.Fatalf("the script never got going\n%s", buf.String())
				}
				time.Sleep(100 * time.Millisecond)
			}
			grandchild := sideReadPid(t, pidfile)
			_ = syscall.Kill(-cmd.Process.Pid, sig)
			done := make(chan struct{})
			go func() { _ = cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				t.Fatalf("the script did not exit within 30s of %s\n%s", sig, buf.String())
			}
			// The side starts with SIGINT ignored (a non-interactive bash's
			// background job does), so a group SIGINT alone would leave it;
			// the EXIT trap's gauntlet_sides_stop is what must take it down.
			time.Sleep(200 * time.Millisecond)
			if sideProcAlive(grandchild) {
				_ = syscall.Kill(grandchild, syscall.SIGKILL)
				t.Errorf("the side's grandchild %d outlived %s to the script's group\n%s", grandchild, sig, buf.String())
			}
		})
	}
}
