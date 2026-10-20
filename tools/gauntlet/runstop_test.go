// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// passingScript is a fake estate that speaks the protocol and passes
// cold_deploy at once.
const passingScript = "printf 'GAUNTLET protocol=1\\n'\nprintf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=0\\n'\n"

// checkpointRoot makes a scratch checkout with an empty artifact, so
// saveRunCheckpoint has somewhere to write.
func checkpointRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// #2037: every estate's row is on disk the moment it finishes, and the file
// is self-consistent at every checkpoint - its aggregates are what a fresh
// Rebuild of its own rows derives, never a headline that disagrees with
// them. Serial and parallel both, and under -parallel no two checkpoints
// ever overlap.
func TestRunCheckpointsEachEstateAsItFinishes(t *testing.T) {
	for _, parallel := range []int{1, 3} {
		t.Run(fmt.Sprintf("parallel=%d", parallel), func(t *testing.T) {
			root := checkpointRoot(t)
			names := []string{"ca", "cb", "cc"}
			m := &Manifest{}
			for _, n := range names {
				writeFakeEstate(t, root, n, passingScript)
				m.Estates = append(m.Estates, Estate{Name: n, Source: "s", Lane: "reference", Set: SetCore})
			}
			a := &Artifact{Schema: 1}
			bi := &BehaviorIndex{}
			var inFlight, overlapped int32
			var onDisk []int
			checkpoint := func() error {
				if atomic.AddInt32(&inFlight, 1) > 1 {
					atomic.StoreInt32(&overlapped, 1)
				}
				defer atomic.AddInt32(&inFlight, -1)
				if err := saveRunCheckpoint(root, m, bi, a); err != nil {
					return err
				}
				saved, err := LoadArtifact(root)
				if err != nil {
					return err
				}
				measured := 0
				for _, r := range saved.Estates {
					if r.LastRun != nil && r.LastRun.Commit == "c" {
						measured++
					}
				}
				onDisk = append(onDisk, measured)
				// Self-consistency: rebuilding what is on disk changes nothing.
				before, _ := saved.Canonical()
				saved.Rebuild(m, bi, emulatorPin(root), oracleVersions(root), providerVersions(root), engineVersion(root), substratePins(root))
				after, _ := saved.Canonical()
				if !bytes.Equal(before, after) {
					return fmt.Errorf("checkpoint %d is not self-consistent: a Rebuild of its own rows changes it", len(onDisk))
				}
				return nil
			}
			var out bytes.Buffer
			if _, err := RunEstates(root, m, a, RunOptions{Names: names, Parallel: parallel, Stdout: &out, Checkpoint: checkpoint}, "c", "e"); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "CHECKPOINT FAILED") {
				t.Fatalf("a checkpoint failed:\n%s", out.String())
			}
			if want := []int{1, 2, 3}; fmt.Sprint(onDisk) != fmt.Sprint(want) {
				t.Errorf("estates on disk after each checkpoint = %v, want %v", onDisk, want)
			}
			if overlapped != 0 {
				t.Error("two checkpoints ran at once")
			}
		})
	}
}

// waitForFiles polls until every path exists.
func waitForFiles(t *testing.T, paths ...string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for _, p := range paths {
		for {
			if _, err := os.Stat(p); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never appeared", p)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func readInt(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return n
}

// #2037: a stop request during a run is forwarded to every running script's
// own process group - reaching what the script backgrounded, which a signal
// to bash alone does not - the runner waits for their traps, starts nothing
// further, keeps the estate that already finished on disk, records nothing
// for the interrupted ones, and reports the run as stopped. A repeat signal
// is reported and not forwarded.
func TestRunStopForwardsToEveryGroupAndKeepsFinishedRows(t *testing.T) {
	root := checkpointRoot(t)
	sig := t.TempDir()
	writeFakeEstate(t, root, "quick", passingScript)
	for _, n := range []string{"slow1", "slow2"} {
		writeFakeEstate(t, root, n, fmt.Sprintf(`printf 'GAUNTLET protocol=1\n'
d=%q
on_term() { echo trapped > "$d/%[2]s.trapped"; sleep 1; exit 143; }
trap on_term TERM INT
sleep 300 >/dev/null 2>&1 &
echo $! > "$d/%[2]s.child"
ps -o pgid= -p $$ | tr -d ' ' > "$d/%[2]s.pgid"
echo $$ > "$d/%[2]s.pid"
touch "$d/%[2]s.ready"
wait
printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=0\n'
`, sig, n))
	}
	writeFakeEstate(t, root, "never", "touch "+filepath.Join(sig, "never.started")+"\n"+passingScript)

	names := []string{"quick", "slow1", "slow2", "never"}
	m := &Manifest{}
	for _, n := range names {
		m.Estates = append(m.Estates, Estate{Name: n, Source: "s", Lane: "reference", Set: SetCore})
	}
	a := &Artifact{Schema: 1}
	bi := &BehaviorIndex{}
	t.Cleanup(func() {
		for _, n := range []string{"slow1", "slow2"} {
			if b, err := os.ReadFile(filepath.Join(sig, n+".child")); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
		}
	})

	sigc := make(chan os.Signal, 2)
	go func() {
		waitForFiles(t, filepath.Join(sig, "slow1.ready"), filepath.Join(sig, "slow2.ready"))
		sigc <- syscall.SIGINT
		time.Sleep(200 * time.Millisecond) // inside the traps' one-second teardown
		sigc <- syscall.SIGTERM
	}()
	var out bytes.Buffer
	_, err := RunEstates(root, m, a, RunOptions{
		Names: names, Parallel: 2, Stdout: &out, signals: sigc,
		Checkpoint: func() error { return saveRunCheckpoint(root, m, bi, a) },
	}, "c", "e")

	var stopped *RunInterrupted
	if !errors.As(err, &stopped) {
		t.Fatalf("a stopped run did not report RunInterrupted: err=%v\n%s", err, out.String())
	}
	sort.Strings(stopped.Interrupted)
	if fmt.Sprint(stopped.Finished) != "[quick]" || fmt.Sprint(stopped.Interrupted) != "[slow1 slow2]" || fmt.Sprint(stopped.NotStarted) != "[never]" {
		t.Errorf("finished=%v interrupted=%v not started=%v, want [quick] [slow1 slow2] [never]", stopped.Finished, stopped.Interrupted, stopped.NotStarted)
	}
	if _, err := os.Stat(filepath.Join(sig, "never.started")); err == nil {
		t.Error("an estate started after the stop request")
	}
	for _, n := range []string{"slow1", "slow2"} {
		if _, err := os.Stat(filepath.Join(sig, n+".trapped")); err != nil {
			t.Errorf("%s: its trap never ran - the stop was not forwarded to it", n)
		}
		if pgid, pid := readInt(t, filepath.Join(sig, n+".pgid")), readInt(t, filepath.Join(sig, n+".pid")); pgid != pid || pgid == syscall.Getpgrp() {
			t.Errorf("%s: pgid %d, pid %d, runner's pgid %d - the script does not lead its own process group", n, pgid, pid, syscall.Getpgrp())
		}
		child := readInt(t, filepath.Join(sig, n+".child"))
		gone := false
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if syscall.Kill(child, 0) != nil {
				gone = true
				break
			}
		}
		if !gone {
			t.Errorf("%s: its backgrounded child %d outlived the stop - the signal reached bash and not its group", n, child)
		}
	}
	if c := strings.Count(out.String(), "NOT forwarding it again"); c != 1 {
		t.Errorf("the repeat signal was reported %d times, want once:\n%s", c, out.String())
	}

	saved, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range saved.Estates {
		measured := r.LastRun != nil && r.LastRun.Commit == "c"
		if measured != (r.Name == "quick") {
			t.Errorf("%s: measured on disk = %v; only the finished estate should be", r.Name, measured)
		}
	}
}
