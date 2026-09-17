// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// livecertlock_test.go: issue #1150's guard.
//
// The defect these tests were written against is a measured one, not a
// theorised one. Against the code as it stood before livecertlock.go, the
// concurrency test below (with the refusal assertions removed) produced a
// 295,759-byte log of which 280,961 bytes were NUL, carrying lines from two
// different run pids, with the first run's four thousand lines gone - and
// BOTH invocations returned a nil error. Nothing surfaced the overlap, which
// is exactly how a false real-AWS cold_deploy failure got recorded on
// 2026-09-15.

// writeOverlapScript writes a fake live-cert script that produces a large,
// pid-labelled log with a gap in the middle, so a second run's truncation of
// the first run's file is visible in the bytes afterwards.
//
// The two invocations take different shapes by racing for a marker
// directory - mkdir is the atomic primitive here - so the FIRST one writes a
// long prologue and the second a short one. That ordering is what makes the
// old defect show as NUL padding: the first run's later write lands at an
// offset far beyond where the second run's truncation left the file.
func writeOverlapScript(t *testing.T, root, estate string) {
	t.Helper()
	dir := filepath.Join(root, "live", "live-cert")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/usr/bin/env bash\n" +
		"M=$$\n" +
		"if mkdir \"$PWD/.first-run-marker\" 2>/dev/null; then N=4000; else N=100; fi\n" +
		"echo \"GAUNTLET protocol=1\"\n" +
		"for i in $(seq 1 $N); do printf 'RUN=%s line %06d AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\\n' \"$M\" \"$i\"; done\n" +
		"sleep 3\n" +
		"for i in $(seq 1 50); do printf 'RUN=%s tail %06d BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB\\n' \"$M\" \"$i\"; done\n" +
		"echo \"GAUNTLET stage=cold_deploy verdict=pass duration_s=1 detail=pid $M ran\"\n" +
		"echo \"GAUNTLET end=1\"\n"
	if err := os.WriteFile(filepath.Join(dir, estate+".sh"), []byte(body), 0o755); err != nil { //nolint:gosec // a fake script in a test's own temp dir
		t.Fatal(err)
	}
}

func TestSecondLiveCertForOneEstateRefusesAndTheLogStaysOneRun(t *testing.T) {
	root := t.TempDir()
	writeOverlapScript(t, root, "lockestate")

	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i == 1 {
				// Well after the first run has started writing, and well
				// before it finishes: the 166-second overlap of the real
				// incident, scaled down.
				time.Sleep(1500 * time.Millisecond)
			}
			_, _, _, err := RunLiveCert(root, "lockestate", "floci", "us-east-1", 5, 120)
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	var busy *LiveCertBusyError
	ok, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.As(err, &busy):
			refused++
		default:
			t.Fatalf("unexpected error from a concurrent live-cert: %v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("two concurrent live-certs for one estate: %d ran and %d refused, want exactly 1 and 1 (errors: %v)", ok, refused, errs)
	}

	// The refusal names what it found, rather than exiting silently (#1150).
	msg := busy.Error()
	for _, want := range []string{"run_id", "pid", "started", "lock", "#1150", LiveCertLockPath(root, "lockestate")} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q - a refusal that does not name what it found is the silent failure this issue is about:\n%s", want, msg)
		}
	}
	if busy.Info.RunID == "" || busy.Info.PID == 0 || busy.Info.Started == "" {
		t.Errorf("the refusal carries run_id=%q pid=%d started=%q - #1150 asks for all three", busy.Info.RunID, busy.Info.PID, busy.Info.Started)
	}
	if !busy.Alive {
		t.Errorf("the holding run was still running, but the refusal reported its pid as gone")
	}

	// The evidence itself: one run's log, whole, with no truncation.
	b, err := os.ReadFile(filepath.Join(root, LogDir, "live-cert-lockestate.log"))
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(b, []byte{0}); n != 0 {
		t.Errorf("the log holds %d NUL byte(s) out of %d - that is the truncate-while-the-other-fd-writes shape #1150 describes", n, len(b))
	}
	pids := map[string]bool{}
	for _, m := range regexp.MustCompile(`RUN=(\d+)`).FindAllSubmatch(b, -1) {
		pids[string(m[1])] = true
	}
	if len(pids) != 1 {
		t.Errorf("the log carries lines from %d different runs (%v); a live-cert log must be one run's alone", len(pids), pids)
	}
	if lines := bytes.Count(b, []byte("\n")); lines < 4050 {
		t.Errorf("the log holds %d lines, want the whole 4,050-line run - a short log means the other run truncated this one", lines)
	}
}

func TestLiveCertLockIsReleasedSoTheNextRunCanStart(t *testing.T) {
	root := t.TempDir()
	writeOverlapScript(t, root, "seqestate")
	for i := range 2 {
		if _, _, _, err := RunLiveCert(root, "seqestate", "floci", "us-east-1", 5, 120); err != nil {
			t.Fatalf("sequential run %d refused: %v - the lock is not being released", i, err)
		}
	}
	if _, err := os.Stat(LiveCertLockPath(root, "seqestate")); !os.IsNotExist(err) {
		t.Errorf("the lock file survived the run (stat err = %v); a lock nobody removes blocks every later run", err)
	}
}

func TestLiveCertLockDoesNotBlockADifferentEstate(t *testing.T) {
	root := t.TempDir()
	writeOverlapScript(t, root, "estate-a")
	writeOverlapScript(t, root, "estate-b")
	lock, err := AcquireLiveCertLock(root, "estate-a", "aws", "us-east-2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	other, err := AcquireLiveCertLock(root, "estate-b", "aws", "us-east-2")
	if err != nil {
		t.Fatalf("estate-b refused while estate-a held its own lock: %v - the lock is per estate, not global", err)
	}
	if err := other.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveCertLockRefusalNamesAStaleLockRatherThanBreakingIt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, LogDir), 0o755); err != nil {
		t.Fatal(err)
	}
	// A pid that cannot be running: the kernel reserves 0 and this writes a
	// deliberately impossible one, so the check cannot be a coin flip.
	stale := "run_id=lc1757000000-424242\npid=2147483646\nstarted=2026-09-15T04:00:00Z\nestate=terralith-scale\ntarget=aws\nregion=us-east-2\nhost=somewhere\n"
	path := LiveCertLockPath(root, "terralith-scale")
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil { //nolint:gosec // a test's own temp dir
		t.Fatal(err)
	}
	_, err := AcquireLiveCertLock(root, "terralith-scale", "aws", "us-east-2")
	var busy *LiveCertBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("a stale lock did not refuse the run: err = %v", err)
	}
	if busy.Alive {
		t.Errorf("pid 2147483646 was reported as running")
	}
	msg := busy.Error()
	if !strings.Contains(msg, "NO SUCH PROCESS") || !strings.Contains(msg, "delete the lock file") {
		t.Errorf("a stale lock's refusal must say it looks stale and how to clear it:\n%s", msg)
	}
	if !strings.Contains(msg, "lc1757000000-424242") || !strings.Contains(msg, "2026-09-15T04:00:00Z") {
		t.Errorf("a stale lock's refusal must still name the run it found:\n%s", msg)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("the refusal removed the lock file it found (stat: %v) - it must leave it for a human to clear", statErr)
	}
}

func TestLiveCertLockReleaseLeavesSomeoneElsesLockAlone(t *testing.T) {
	root := t.TempDir()
	lock, err := AcquireLiveCertLock(root, "estate-c", "floci", "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	// Somebody else's run now owns the path (ours crashed, theirs started).
	other := "run_id=lc1757000001-99\npid=1\nstarted=2026-09-16T04:00:00Z\n"
	if err := os.WriteFile(lock.Path(), []byte(other), 0o644); err != nil { //nolint:gosec // a test's own temp dir
		t.Fatal(err)
	}
	if err := lock.Release(); err == nil {
		t.Errorf("Release removed a lock carrying a different run_id, which unlocks somebody else's run")
	}
	if _, err := os.Stat(lock.Path()); err != nil {
		t.Errorf("the other run's lock is gone: %v", err)
	}
}
