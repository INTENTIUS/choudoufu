// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRunEstatesPreservesDetailForUnreachedStages: found re-verifying
// corpus-giantswarm-crossplane after the record-orphan-read sweep
// (610511fb73) - day2_rename regressed from pass to fail there, the script
// now exits before ever reaching day2_remove, and RunEstates's old
// `r.LastRun.Detail = res.Detail` line silently dropped day2_remove's
// previously-recorded wall text from the artifact even though its verdict
// in r.Stages was correctly left untouched. A script whose first run
// reaches every stage and whose second run stops early (an earlier stage
// regressed to fail) must keep the first run's detail for the stage the
// second run never printed a GAUNTLET line for.
func TestRunEstatesPreservesDetailForUnreachedStages(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join("live", "e2e", "x", "run.sh")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	// A script that only ever reports cold_deploy and day2_rename (fail),
	// standing in for a script that aborts before reaching day2_remove.
	script := "#!/usr/bin/env bash\n" +
		"printf 'GAUNTLET protocol=1\\n'\n" +
		"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=5 detail=ok\\n'\n" +
		"printf 'GAUNTLET stage=day2_rename verdict=fail duration_s=7 detail=regressed\\n'\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(root, scriptPath), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{Estates: []Estate{{Name: "x", Source: "s", Lane: "reference", Set: SetGrowing, Script: scriptPath}}}
	a := &Artifact{Schema: 1, Estates: []EstateResult{{
		Name:     "x",
		Protocol: ProtocolGauntlet,
		Stages: map[string]string{
			"cold_deploy": "pass",
			"day2_rename": "pass",
			"day2_remove": "fail",
		},
		LastRun: &LastRun{
			Commit: "priorcommit",
			Detail: map[string]string{
				"cold_deploy": "old cold_deploy detail",
				"day2_rename": "old day2_rename detail (was passing)",
				"day2_remove": "old, precisely-named day2_remove wall - must survive an early abort",
			},
			Seconds: map[string]float64{
				"cold_deploy": 1,
				"day2_rename": 2,
				"day2_remove": 99,
			},
		},
	}}}

	var out bytes.Buffer
	failures, err := RunEstates(root, m, a, RunOptions{Names: []string{"x"}, Stdout: &out}, "newcommit", "newemulator@sha256:new")
	if err != nil {
		t.Fatal(err)
	}
	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}

	r, ok := a.Result("x")
	if !ok {
		t.Fatal("no result for x")
	}
	if r.Stages["day2_rename"] != "fail" {
		t.Errorf("day2_rename verdict = %q, want fail (the regression itself)", r.Stages["day2_rename"])
	}
	if r.Stages["day2_remove"] != "fail" {
		t.Errorf("day2_remove verdict = %q, want fail (untouched, carried forward)", r.Stages["day2_remove"])
	}
	if r.LastRun == nil {
		t.Fatal("LastRun is nil")
	}
	if got := r.LastRun.Detail["day2_remove"]; got != "old, precisely-named day2_remove wall - must survive an early abort" {
		t.Errorf("day2_remove detail was dropped by the early abort: %q", got)
	}
	if got := r.LastRun.Detail["day2_rename"]; got != "regressed" {
		t.Errorf("day2_rename detail = %q, want this run's fresh detail (\"regressed\"), not the stale pass-era text", got)
	}
	if got := r.LastRun.Seconds["day2_remove"]; got != 99 {
		t.Errorf("day2_remove duration_s was dropped by the early abort: %v", got)
	}
	if got := r.LastRun.Seconds["day2_rename"]; got != 7 {
		t.Errorf("day2_rename duration_s = %v, want this run's fresh value (7), not the stale pass-era value (2)", got)
	}
	if got := r.LastRun.Seconds["cold_deploy"]; got != 5 {
		t.Errorf("cold_deploy duration_s = %v, want this run's fresh value (5)", got)
	}
	// DurationS is rounded to 0.1s (roundSeconds, run.go), so this fixture -
	// a script with no real work - legitimately rounds to exactly 0; the
	// invariant this asserts is "measured, never negative", not "nonzero".
	if r.LastRun.DurationS < 0 {
		t.Errorf("LastRun.DurationS = %v, want a non-negative whole-run wall-clock time", r.LastRun.DurationS)
	}
	if got := r.LastRun.Detail["cold_deploy"]; got != "ok" {
		t.Errorf("cold_deploy detail = %q, want this run's fresh detail", got)
	}
}

// TestRunEstatesRecordsDurationS proves LastRun.DurationS (#434) is a real
// measurement of the script's own wall-clock time, not a placeholder that
// happens to be present. A script that sleeps a known amount must produce a
// DurationS in a window around it; a script with two GAUNTLET stage lines
// separated by a known sleep must produce a per-stage duration_s in a
// window around that sleep for the LATER stage specifically (the delta is
// measured from the PRIOR stage line, per live/e2e/lib/gauntlet.sh), not
// for the one before it. This is the check-that-can-fail HANDOFF.md asks
// for: it was run once against a version of run.go that never called
// time.Now() around cmd.Run() and failed exactly as expected (DurationS
// read back as 0).
func TestRunEstatesRecordsDurationS(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join("live", "e2e", "y", "run.sh")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\n" +
		"printf 'GAUNTLET protocol=1\\n'\n" +
		"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=0\\n'\n" +
		"sleep 1.2\n" +
		"printf 'GAUNTLET stage=migrate verdict=pass duration_s=1\\n'\n"
	if err := os.WriteFile(filepath.Join(root, scriptPath), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{Estates: []Estate{{Name: "y", Source: "s", Lane: "reference", Set: SetGrowing, Script: scriptPath}}}
	a := &Artifact{Schema: 1}
	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"y"}, Stdout: &out}, "c", "e"); err != nil {
		t.Fatal(err)
	}
	r, ok := a.Result("y")
	if !ok {
		t.Fatal("no result for y")
	}
	if r.LastRun == nil {
		t.Fatal("LastRun is nil")
	}
	// The process ran for at least the 1.2s sleep; give generous headroom
	// above it for CI scheduling noise without accepting "any nonnegative
	// number", which is what a stub that never measures anything would also
	// satisfy.
	if r.LastRun.DurationS < 1.0 || r.LastRun.DurationS > 10.0 {
		t.Errorf("LastRun.DurationS = %v, want roughly 1.2s (the script's sleep)", r.LastRun.DurationS)
	}
}

// TestRunEstatesWarnsWhenProtocolSpokenButNoStageReported: the shape
// TestRunEstatesPreservesDetailForUnreachedStages does not exercise - a
// script that prints `GAUNTLET protocol=1` and then dies before a single
// `GAUNTLET stage=` line. res.Spoken is true (the protocol marker was seen)
// but res.Stages is an empty map, so the per-key merge loop in RunEstates
// runs zero times. This is corpus-sqs-basic's real reproduction, reduced to
// a fixture: `gauntlet_begin` runs before a step-0 tool/corpus check, so a
// missing prerequisite prints the protocol line and then exits with zero
// stage lines ever emitted - the same shape that killed 8 estates nightly in
// #497 (gauntlet.yml never installing the real tofu binary those estates'
// own step-0 checks hard-require).
//
// Before #497's fix, the ENTIRE prior row - including a full pass on every
// stage - was left byte-for-byte untouched here, stamped with a fresh
// commit/date/exit_code: a stale "pass" wearing a brand new timestamp, the
// exact shape TestNonzeroExitCodeImpliesAFailingStage (gauntlet_test.go)
// exists to catch. recordRunnerFailure (below, and see
// TestRecordRunnerFailure for its own focused unit test) now writes a real
// fail into the earliest active stage (cold_deploy) instead, so this test
// asserts the corrected split: cold_deploy flips to fail with distinguishing
// detail, while every OTHER stage this run never reached (day2_rename,
// day2_remove) is still carried forward untouched - carry-forward is still
// correct for stages the fix has no evidence about, just not for the one
// stage this branch now speaks for.
func TestRunEstatesWarnsWhenProtocolSpokenButNoStageReported(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join("live", "e2e", "x", "run.sh")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	// Speaks the protocol, then dies before its first stage line - standing
	// in for corpus-sqs-basic/run.sh failing its `.corpus` existence check
	// after gauntlet_begin but before CURRENT_STAGE is ever set.
	script := "#!/usr/bin/env bash\n" +
		"printf 'GAUNTLET protocol=1\\n'\n" +
		"echo 'FAIL: prerequisite missing' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(root, scriptPath), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{Estates: []Estate{{Name: "x", Source: "s", Lane: "reference", Set: SetGrowing, Script: scriptPath}}}
	// carriedStages are the stages this run never speaks for at all - they
	// must survive untouched. cold_deploy is deliberately excluded: it is
	// the one stage recordRunnerFailure now overwrites, asserted separately
	// below.
	carriedStages := map[string]string{
		"day2_rename": "pass",
		"day2_remove": "pass",
	}
	a := &Artifact{Schema: 1, Estates: []EstateResult{{
		Name:     "x",
		Protocol: ProtocolGauntlet,
		Clear:    true,
		Stages: map[string]string{
			"cold_deploy": "pass",
			"day2_rename": carriedStages["day2_rename"],
			"day2_remove": carriedStages["day2_remove"],
		},
		LastRun: &LastRun{
			Commit:   "priorcommit",
			Date:     "2020-01-01T00:00:00Z",
			ExitCode: 0,
		},
	}}}

	var out bytes.Buffer
	failures, err := RunEstates(root, m, a, RunOptions{Names: []string{"x"}, Stdout: &out}, "newcommit", "newemulator@sha256:new")
	if err != nil {
		t.Fatal(err)
	}
	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}

	r, ok := a.Result("x")
	if !ok {
		t.Fatal("no result for x")
	}
	// Stages this run has no evidence about at all must still carry forward
	// unchanged, exactly like the legacy branch already does.
	for id, want := range carriedStages {
		if got := r.Stages[id]; got != want {
			t.Errorf("stage %s = %q, want %q (carried forward unchanged)", id, got, want)
		}
	}
	// cold_deploy is the one exception: this run died before confirming it,
	// so its stale "pass" must not survive under a fresh commit/date.
	if got := r.Stages["cold_deploy"]; got != VerdictFail {
		t.Errorf("stage cold_deploy = %q, want %q (this run never confirmed it; #497's fix must not let a stale pass survive under a fresh commit/date)", got, VerdictFail)
	}
	if r.LastRun == nil || r.LastRun.ExitCode != 1 {
		t.Fatalf("LastRun.ExitCode = %+v, want 1", r.LastRun)
	}
	if r.LastRun.Commit != "newcommit" {
		t.Errorf("LastRun.Commit = %q, want %q", r.LastRun.Commit, "newcommit")
	}
	if r.LastRun.Emulator != "newemulator@sha256:new" {
		t.Errorf("LastRun.Emulator = %q, want %q (the pin this run actually launched against, not carried from the prior row)", r.LastRun.Emulator, "newemulator@sha256:new")
	}
	if r.LastRun.Detail["cold_deploy"] == "" {
		t.Error("LastRun.Detail[cold_deploy] is empty, want a runner-failure explanation")
	}

	// What must be different from before the fix: a warning naming this
	// exact shape, the same way the legacy branch already warns.
	const wantSubstr = "spoke the gauntlet protocol but reported no stage verdicts this run"
	if !strings.Contains(out.String(), wantSubstr) {
		t.Errorf("stdout does not warn about the empty-Stages carry-forward; got:\n%s", out.String())
	}

	// TestNonzeroExitCodeImpliesAFailingStage's own invariant, re-asserted
	// directly here rather than only trusted by inference: nonzero exit
	// code implies at least one real fail somewhere in Stages.
	hasFail := false
	for _, v := range r.Stages {
		if v == VerdictFail {
			hasFail = true
		}
	}
	if !hasFail {
		t.Error("no stage reads fail anywhere despite a nonzero exit code - exactly the shape TestNonzeroExitCodeImpliesAFailingStage catches")
	}
}

// TestRecordRunnerFailure is the focused, function-level counterpart to
// TestRunEstatesWarnsWhenProtocolSpokenButNoStageReported above: it calls
// recordRunnerFailure directly rather than going through a real script and
// RunEstates, so it can assert its two obligations precisely - write a real
// fail into the earliest active stage (cold_deploy), and leave every other
// stage's verdict AND detail exactly as they were - without any of
// RunEstates's own merge/carry-forward logic able to mask a bug in either
// direction.
func TestRecordRunnerFailure(t *testing.T) {
	r := &EstateResult{
		Name: "x",
		Stages: map[string]string{
			"cold_deploy": VerdictPass,
			"day2_rename": VerdictFail,
			"day2_remove": VerdictNotRun,
		},
		LastRun: &LastRun{
			Commit:   "priorcommit",
			ExitCode: 0,
			Detail: map[string]string{
				"cold_deploy": "old cold_deploy detail, must be overwritten",
				"day2_rename": "old day2_rename detail, must survive untouched",
			},
		},
	}

	got := recordRunnerFailure(r, 1)

	if got != "cold_deploy" {
		t.Errorf("recordRunnerFailure returned %q, want %q (the earliest active stage)", got, "cold_deploy")
	}
	if r.Stages["cold_deploy"] != VerdictFail {
		t.Errorf("Stages[cold_deploy] = %q, want %q", r.Stages["cold_deploy"], VerdictFail)
	}
	// Every other stage's verdict must be untouched.
	if r.Stages["day2_rename"] != VerdictFail {
		t.Errorf("Stages[day2_rename] = %q, want %q (untouched)", r.Stages["day2_rename"], VerdictFail)
	}
	if r.Stages["day2_remove"] != VerdictNotRun {
		t.Errorf("Stages[day2_remove] = %q, want %q (untouched)", r.Stages["day2_remove"], VerdictNotRun)
	}

	detail := r.LastRun.Detail["cold_deploy"]
	if detail == "" || detail == "old cold_deploy detail, must be overwritten" {
		t.Errorf("Detail[cold_deploy] = %q, want a fresh runner-failure explanation", detail)
	}
	if !strings.Contains(detail, "1") {
		t.Errorf("Detail[cold_deploy] = %q, want it to name the exit code (1)", detail)
	}
	if !strings.Contains(detail, "not a product regression") {
		t.Errorf("Detail[cold_deploy] = %q, want it to explicitly disclaim being a product regression, so a reader cannot mistake this for one", detail)
	}
	// Every other stage's detail must be untouched too.
	if got := r.LastRun.Detail["day2_rename"]; got != "old day2_rename detail, must survive untouched" {
		t.Errorf("Detail[day2_rename] = %q, want the original untouched", got)
	}

	// recordRunnerFailure must not have grown the Detail map beyond the one
	// key it is responsible for plus whatever pre-existed.
	if len(r.Stages) != 3 {
		t.Errorf("Stages has %d entries, want 3 (no new stage ids invented)", len(r.Stages))
	}
}

// TestRecordRunnerFailureCreatesDetailMapWhenNil covers the row shape
// RunEstates actually produces on a brand new estate's very first run: a
// fresh LastRun with Detail left nil (RunEstates only assigns
// r.LastRun.Detail when the carried-forward prevDetail map is non-empty -
// see run.go). recordRunnerFailure must not panic on a nil map, and must
// leave a real entry behind.
func TestRecordRunnerFailureCreatesDetailMapWhenNil(t *testing.T) {
	r := &EstateResult{
		Name:    "y",
		Stages:  map[string]string{"cold_deploy": VerdictPass},
		LastRun: &LastRun{Commit: "c", ExitCode: 0},
	}

	recordRunnerFailure(r, 7)

	if r.LastRun.Detail == nil {
		t.Fatal("LastRun.Detail is still nil after recordRunnerFailure")
	}
	if r.LastRun.Detail["cold_deploy"] == "" {
		t.Error("LastRun.Detail[cold_deploy] is empty")
	}
}

// writeFakeEstate writes a minimal crossing script for the fake estates the
// parallel tests below use, and registers it in a fresh Manifest/Artifact
// pair. body is the script's own logic; it can rely on FLOCI_PORT (unset
// unless the runner injects one), and must speak the gauntlet protocol
// itself since these tests are exercising RunEstates/runResults, not a real
// live/e2e/lib/gauntlet.sh source.
func writeFakeEstate(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, "live", "e2e", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\nset -euo pipefail\n" + body
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestRunEstatesParallelAssignsDistinctPortsPerSlot is #437's proof that
// "-parallel N" really does give N estates N isolated emulators rather than
// N estates racing over one shared FLOCI_PORT: three fake estates each echo
// their own FLOCI_PORT into their stage detail, and each reports a
// DIFFERENT, deliberately estate-specific verdict (pass/fail/not_run) so a
// result landing under the wrong estate's name - a slot/index mixup in the
// concurrent path - would be caught even if the ports happened to come out
// right.
func TestRunEstatesParallelAssignsDistinctPortsPerSlot(t *testing.T) {
	root := t.TempDir()
	verdicts := map[string]string{"pa": "pass", "pb": "fail", "pc": "not_run"}
	var names []string
	for name, verdict := range verdicts {
		names = append(names, name)
		writeFakeEstate(t, root, name, fmt.Sprintf(
			"printf 'GAUNTLET protocol=1\\n'\n"+
				"printf 'GAUNTLET stage=cold_deploy verdict=%s duration_s=0 detail=port=%%s\\n' \"${FLOCI_PORT:-unset}\"\n",
			verdict))
	}

	m := &Manifest{}
	for _, name := range names {
		m.Estates = append(m.Estates, Estate{Name: name, Source: "s", Lane: "reference", Set: SetGrowing})
	}
	a := &Artifact{Schema: 1}
	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: names, Parallel: 3, Stdout: &out}, "c", "e"); err != nil {
		t.Fatal(err)
	}

	seenPorts := map[string]bool{}
	for name, wantVerdict := range verdicts {
		r, ok := a.Result(name)
		if !ok {
			t.Fatalf("no result for %s", name)
		}
		if got := r.Stages["cold_deploy"]; got != wantVerdict {
			t.Errorf("%s: verdict = %q, want %q (own-estate result must land under its own name, not another slot's)", name, got, wantVerdict)
		}
		detail := r.LastRun.Detail["cold_deploy"]
		if !strings.HasPrefix(detail, "port=") || strings.Contains(detail, "unset") {
			t.Fatalf("%s: detail %q does not carry a real FLOCI_PORT - parallel mode did not inject one", name, detail)
		}
		seenPorts[strings.TrimPrefix(detail, "port=")] = true
	}
	if len(seenPorts) != 3 {
		t.Errorf("saw %d distinct FLOCI_PORT values across 3 concurrent estates, want 3: %v", len(seenPorts), seenPorts)
	}
	for port := range seenPorts {
		want := false
		for slot := 0; slot < 3; slot++ {
			if port == fmt.Sprintf("%d", parallelPortBase+slot*parallelPortStride) {
				want = true
			}
		}
		if !want {
			t.Errorf("port %s is not one of the documented per-slot values (base %d, stride %d)", port, parallelPortBase, parallelPortStride)
		}
	}
}

// TestRunEstatesParallelMatchesSerial is the Go-level half of #437's
// equivalence requirement: for a script whose own output does not depend on
// FLOCI_PORT (unlike the fixture above, which deliberately does, to prove
// isolation), running the very same set of estates serially and with
// -parallel N must produce an identical merged artifact row for every
// estate - same stages, same detail, same protocol, same exit code, same
// clear flag - because the concurrency lives entirely in runResults and the
// merge loop that turns a []oneResult into artifact rows never changes
// between the two modes. Commit/date/duration are excluded: those are
// legitimately different measurements taken at different times, not part of
// what "verdicts byte-identical" means (live/GAUNTLET.md's own protocol
// line format also excludes wall-clock noise when comparing runs - only
// stage=/verdict=/detail= are the claim).
func TestRunEstatesParallelMatchesSerial(t *testing.T) {
	root := t.TempDir()
	names := []string{"ma", "mb", "mc"}
	for i, name := range names {
		writeFakeEstate(t, root, name, fmt.Sprintf(
			"printf 'GAUNTLET protocol=1\\n'\n"+
				"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=0 detail=estate-%s-fixed-detail\\n'\n"+
				"printf 'GAUNTLET stage=day2_rename verdict=%s duration_s=0 detail=second-stage\\n'\n",
			name, []string{"pass", "fail", "pass"}[i]))
	}
	m := &Manifest{}
	for _, name := range names {
		m.Estates = append(m.Estates, Estate{Name: name, Source: "s", Lane: "reference", Set: SetGrowing})
	}

	run := func(parallel int) *Artifact {
		a := &Artifact{Schema: 1}
		var out bytes.Buffer
		if _, err := RunEstates(root, m, a, RunOptions{Names: names, Parallel: parallel, Stdout: &out}, "commit", "emulator"); err != nil {
			t.Fatal(err)
		}
		return a
	}

	serial := run(1)
	parallel := run(3)

	normalize := func(a *Artifact) map[string]EstateResult {
		out := map[string]EstateResult{}
		for _, r := range a.Estates {
			if r.LastRun != nil {
				cp := *r.LastRun
				cp.Commit, cp.Date, cp.DurationS = "", "", 0
				r.LastRun = &cp
			}
			out[r.Name] = r
		}
		return out
	}

	got, want := normalize(parallel), normalize(serial)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parallel run's merged rows differ from serial's (commit/date/duration excluded):\nserial:   %+v\nparallel: %+v", want, got)
	}
}

// TestRunEstatesParallelOverlapsInWallClock proves -parallel actually runs
// estates concurrently rather than serialising them behind a lock that
// would make the flag a no-op: N scripts that each sleep a fixed amount
// must finish in roughly one sleep's worth of wall clock at -parallel N,
// not N sleeps' worth.
func TestRunEstatesParallelOverlapsInWallClock(t *testing.T) {
	root := t.TempDir()
	const n = 4
	const sleep = "0.3"
	var names []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("wc%d", i)
		names = append(names, name)
		writeFakeEstate(t, root, name,
			"sleep "+sleep+"\n"+
				"printf 'GAUNTLET protocol=1\\n'\n"+
				"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=0\\n'\n")
	}
	m := &Manifest{}
	for _, name := range names {
		m.Estates = append(m.Estates, Estate{Name: name, Source: "s", Lane: "reference", Set: SetGrowing})
	}

	timeRun := func(parallel int) time.Duration {
		a := &Artifact{Schema: 1}
		var out bytes.Buffer
		start := time.Now()
		if _, err := RunEstates(root, m, a, RunOptions{Names: names, Parallel: parallel, Stdout: &out}, "c", "e"); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}

	serialElapsed := timeRun(1)
	parallelElapsed := timeRun(n)

	// n sleeps serially is at least n*0.3s; n at once is close to one
	// 0.3s sleep plus process overhead. A generous threshold (well under
	// the 2x a broken "-parallel that still runs one at a time" would
	// produce, well above the ~4x a truly parallel run should show) keeps
	// this robust on a loaded CI box while still catching a no-op flag.
	if parallelElapsed >= serialElapsed/2 {
		t.Errorf("parallel(%d) took %v, serial took %v - parallel run did not overlap (expected well under half the serial time for %d estates each sleeping %ss)", n, parallelElapsed, serialElapsed, n, sleep)
	}
}

// TestAllocatedPortRangesNeverOverlap is #520's guard. #520's own history is
// why it is written this way: the pre-#520 hazard was never that anyone
// picked a bad number on purpose - it was 78 script defaults spaced one
// apart with no margin once several scripts started deriving green/oracle
// ports as an offset from their own base. A test that just re-asserted
// "these hand-picked defaults happen not to collide today" would rot the
// moment the next script picked the next free number by hand, which is
// exactly how #520's collisions accumulated in the first place (maintainer
// steer on #520: the assertion has to be a property of the allocator, not
// a proof about today's hand-picked constants).
//
// So this proves that no two concurrency slots this package can ever hand
// out (flociPortEnv - the one function both the serial and the
// -parallel>1 path in runResults call) have overlapping port ranges, where
// a slot's range is [its assigned FLOCI_PORT, that port + the largest
// offset any live/e2e/*/run.sh script derives from FLOCI_PORT today]. That
// largest offset is read from the real scripts (largestFlociPortOffset
// below), not hand-typed, so a future script deriving a bigger offset than
// parallelPortStride clears fails this test instead of silently
// reintroducing #520's hazard the day someone adds it.
//
// No emulator, no Docker, no estate script is ever executed here -
// flociPortEnv is the only allocator code under test, called directly.
func TestAllocatedPortRangesNeverOverlap(t *testing.T) {
	root := testRoot(t)
	maxOffset := largestFlociPortOffset(t, root)
	if maxOffset <= 0 {
		t.Fatal("scanned live/e2e/*/run.sh for a FLOCI_PORT+<N> derivation and found none - the scan itself is broken, not a passing result")
	}
	if parallelPortStride <= maxOffset {
		t.Fatalf("parallelPortStride (%d) does not clear the largest FLOCI_PORT offset any live/e2e/*/run.sh script derives today (%d): two concurrency slots' assigned ranges could overlap", parallelPortStride, maxOffset)
	}

	// Far more concurrency than -parallel is ever actually run at; if the
	// property holds this wide it holds for any N a human would pass.
	const slots = 200
	type portRange struct{ slot, lo, hi int }
	ranges := make([]portRange, slots)
	for slot := 0; slot < slots; slot++ {
		port := parseFlociPort(t, flociPortEnv(slot))
		ranges[slot] = portRange{slot: slot, lo: port, hi: port + maxOffset}
	}
	for i := 0; i < slots; i++ {
		for j := i + 1; j < slots; j++ {
			a, b := ranges[i], ranges[j]
			if a.lo <= b.hi && b.lo <= a.hi {
				t.Fatalf("slot %d's assigned FLOCI_PORT range [%d,%d] overlaps slot %d's [%d,%d] (maxOffset=%d derived from the real live/e2e/*/run.sh scripts) - the runner could launch two estates against the same emulator port", a.slot, a.lo, a.hi, b.slot, b.lo, b.hi, maxOffset)
			}
		}
	}
}

// largestFlociPortOffset scans every live/e2e/*/run.sh for a
// `FLOCI_PORT + N` derivation (however a script spells the whitespace) and
// returns the largest N found - the real number today's scripts derive,
// not a constant copied into this test from a comment that can drift out
// of date the moment a script changes.
func largestFlociPortOffset(t *testing.T, root string) int {
	t.Helper()
	scripts, err := filepath.Glob(filepath.Join(root, "live", "e2e", "*", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) == 0 {
		t.Fatal("no live/e2e/*/run.sh scripts found - the glob itself is broken")
	}
	re := regexp.MustCompile(`FLOCI_PORT\s*\+\s*([0-9]+)`)
	max := 0
	for _, s := range scripts {
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			if n > max {
				max = n
			}
		}
	}
	return max
}

// parseFlociPort extracts the numeric port from a "FLOCI_PORT=<port>"
// environment entry, the exact string shape flociPortEnv produces and
// runOne (via setEnv) passes straight into a script's environment.
func parseFlociPort(t *testing.T, env string) int {
	t.Helper()
	const prefix = "FLOCI_PORT="
	if !strings.HasPrefix(env, prefix) {
		t.Fatalf("env entry %q does not start with %q", env, prefix)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(env, prefix))
	if err != nil {
		t.Fatalf("env entry %q: %v", env, err)
	}
	return n
}
