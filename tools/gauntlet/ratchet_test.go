// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRatchetViolationsCatchesPassToFail is #553's red demonstration for
// the gauntlet layer's regression ratchet, the direct counterpart to
// acceptance's TestRatchetViolationsCatchesPassToFail (#539/#552): a stage
// the committed artifact recorded as passing for an estate, and this run
// reports anything else for, must be reported - and, unlike a cohort's
// docker-gated live test, this fires from a plain `go test`, no docker or
// floci required, because RatchetViolations takes plain []EstateResult in
// and returns data out.
func TestRatchetViolationsCatchesPassToFail(t *testing.T) {
	committed := []EstateResult{{
		Name:   "corpus-x",
		Stages: map[string]string{"cold_deploy": "pass", "migrate": "pass", "test_plan": "pass"},
	}}
	current := []EstateResult{{
		Name:   "corpus-x",
		Stages: map[string]string{"cold_deploy": "pass", "migrate": "pass", "test_plan": "fail"},
	}}

	violations := RatchetViolations(committed, current)
	if len(violations) != 1 {
		t.Fatalf("RatchetViolations = %v, want exactly 1 violation for test_plan's regression", violations)
	}
	got := violations[0].Error()
	for _, want := range []string{"corpus-x", "test_plan", "pass", "fail"} {
		if !strings.Contains(got, want) {
			t.Errorf("violation message %q does not mention %q", got, want)
		}
	}
	t.Logf("guard fired: %s", got)
}

// TestRatchetViolationsAllowsEveryNonRegressingMove is the negative case:
// holding at pass, holding at fail, fail -> pass (a real fix), and
// not_run -> anything are all moves the committed artifact never promised
// against, so none of them may fire. On its own this proves little (it
// would also pass if the guard could never fire) - see
// TestRatchetViolationsCatchesPassToFail for that half.
func TestRatchetViolationsAllowsEveryNonRegressingMove(t *testing.T) {
	committed := []EstateResult{{
		Name: "corpus-x",
		Stages: map[string]string{
			"flat_pass": "pass",
			"flat_fail": "fail",
			"fixed":     "fail",
			"new_stage": "not_run",
		},
	}}
	current := []EstateResult{{
		Name: "corpus-x",
		Stages: map[string]string{
			"flat_pass": "pass",
			"flat_fail": "fail",
			"fixed":     "pass",
			"new_stage": "fail",
		},
	}}

	if violations := RatchetViolations(committed, current); len(violations) > 0 {
		t.Errorf("RatchetViolations fired on non-regressing moves: %v", violations)
	}
}

// TestRatchetViolationsSkipsEstatesAbsentFromCurrent is the estate-level
// analog of cohorts' "a -run filter left it out; nothing to say": a
// caller that only hands in a subset of estates (RunEstates only mutates
// the rows it was asked to run) must not have every OTHER committed pass
// read back as a board-wide regression.
func TestRatchetViolationsSkipsEstatesAbsentFromCurrent(t *testing.T) {
	committed := []EstateResult{
		{Name: "untouched", Stages: map[string]string{"cold_deploy": "pass"}},
		{Name: "touched", Stages: map[string]string{"cold_deploy": "pass"}},
	}
	current := []EstateResult{
		{Name: "touched", Stages: map[string]string{"cold_deploy": "fail"}},
	}

	violations := RatchetViolations(committed, current)
	if len(violations) != 1 || violations[0].Estate != "touched" {
		t.Fatalf("RatchetViolations = %v, want exactly 1 violation naming only \"touched\"", violations)
	}
}

// TestUnacknowledgedViolationsSuppressesMatchingAck proves the landing
// path: an acknowledgment naming the exact estate/stage removes it from
// what a run must fail on.
func TestUnacknowledgedViolationsSuppressesMatchingAck(t *testing.T) {
	violations := []RegressionViolation{
		{Estate: "corpus-x", Stage: "test_plan", From: "pass", To: "fail"},
	}
	acks := []Regression{
		{Estate: "corpus-x", Stage: "test_plan", Reason: "floci repin surfaced a real defect, see #999"},
	}
	if got := UnacknowledgedViolations(violations, acks); len(got) != 0 {
		t.Errorf("UnacknowledgedViolations = %v, want none - the ack matches exactly", got)
	}
}

// TestUnacknowledgedViolationsLeavesUnmatchedViolations proves the guard
// side of the same mechanism: an ack for a DIFFERENT estate or stage must
// not swallow a real, unrelated violation - otherwise one acknowledgment
// anywhere in the ledger would silently blank the whole ratchet.
func TestUnacknowledgedViolationsLeavesUnmatchedViolations(t *testing.T) {
	violations := []RegressionViolation{
		{Estate: "corpus-x", Stage: "test_plan", From: "pass", To: "fail"},
		{Estate: "corpus-y", Stage: "test_plan", From: "pass", To: "fail"},
	}
	acks := []Regression{
		{Estate: "corpus-x", Stage: "day2_remove", Reason: "wrong stage"},
		{Estate: "corpus-z", Stage: "test_plan", Reason: "wrong estate"},
	}
	got := UnacknowledgedViolations(violations, acks)
	if len(got) != 2 {
		t.Fatalf("UnacknowledgedViolations = %v, want both violations to survive an ack that names neither", got)
	}
}

func TestLoadRegressionsMissingFileIsEmptyNotError(t *testing.T) {
	root := t.TempDir()
	acks, err := LoadRegressions(root)
	if err != nil {
		t.Fatalf("LoadRegressions on a missing file: %v", err)
	}
	if len(acks) != 0 {
		t.Fatalf("LoadRegressions on a missing file = %v, want none", acks)
	}
}

func TestLoadRegressionsRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, RegressionsPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[{"estate": "corpus-x", "stage": "test_plan", "reason": "floci repin, see #999", "issue": "#999"}]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	acks, err := LoadRegressions(root)
	if err != nil {
		t.Fatalf("LoadRegressions: %v", err)
	}
	if len(acks) != 1 || acks[0].Estate != "corpus-x" || acks[0].Stage != "test_plan" || acks[0].Reason == "" {
		t.Fatalf("LoadRegressions round trip = %+v", acks)
	}
}

// TestRegressionAcknowledgmentsNameRealEstatesAndStages is a static guard
// on the committed ledger itself, live/gauntlet/regressions.json: every
// entry must name a real estate in the manifest and a real stage id, so a
// typo does not silently do nothing (an ack that matches nothing acked
// forever explains nothing and suppresses nothing). Runs against the real
// checked-out files, no docker required - like every other manifest/
// artifact-shape guard in this package.
func TestRegressionAcknowledgmentsNameRealEstatesAndStages(t *testing.T) {
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	acks, err := LoadRegressions(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acks {
		if _, ok := m.ByName(a.Estate); !ok {
			t.Errorf("%s: acknowledgment names estate %q, not in %s", RegressionsPath, a.Estate, ManifestPath)
		}
		if _, ok := StageByID(a.Stage); !ok {
			t.Errorf("%s: acknowledgment names stage %q, not a known stage id", RegressionsPath, a.Stage)
		}
		if strings.TrimSpace(a.Reason) == "" {
			t.Errorf("%s: acknowledgment for %s/%s has no reason", RegressionsPath, a.Estate, a.Stage)
		}
	}
}

// TestRunEstatesRegressionIsCaughtByRatchet proves the real plumbing, not
// just the pure function in isolation: a fake estate script (no docker) is
// run through the actual RunEstates path this repo's cmdRun uses, and the
// committed/current comparison is taken across a real merge - including
// RunEstates's in-place map mutation (see cmdRun's own comment on why
// "committed" must be an independent read, never a copy of what RunEstates
// is about to mutate). committed here is built as its own independent
// literal, with its own map, precisely to model what cmdRun does by
// re-reading the artifact from disk before RunEstates runs.
func TestRunEstatesRegressionIsCaughtByRatchet(t *testing.T) {
	root := t.TempDir()
	writeFakeEstate(t, root, "regressor", "printf 'GAUNTLET protocol=1\\n'\n"+
		"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=0\\n'\n"+
		"printf 'GAUNTLET stage=migrate verdict=pass duration_s=0\\n'\n"+
		"printf 'GAUNTLET stage=test_plan verdict=fail duration_s=0 detail=corrupted\\n'\n")

	m := &Manifest{Estates: []Estate{{Name: "regressor", Source: "s", Lane: "reference", Set: SetGrowing}}}
	committed := []EstateResult{{
		Name:   "regressor",
		Stages: map[string]string{"cold_deploy": "pass", "migrate": "pass", "test_plan": "pass"},
	}}
	// a starts as a SEPARATE copy of the same facts committed holds - not
	// committed itself - exactly like cmdRun's a (from loadAll) and its
	// independently re-read committed are two separate Artifact values
	// that happen to agree before RunEstates mutates a.
	a := &Artifact{Schema: 1, Estates: []EstateResult{{
		Name:     "regressor",
		Protocol: ProtocolGauntlet,
		Stages:   map[string]string{"cold_deploy": "pass", "migrate": "pass", "test_plan": "pass"},
	}}}

	failures, err := RunEstates(root, m, a, RunOptions{Names: []string{"regressor"}, Stdout: os.Stdout}, "c", "e")
	if err != nil {
		t.Fatalf("RunEstates: %v", err)
	}
	if failures != 0 {
		t.Fatalf("failures = %d, want 0 (the script itself exits 0; the regression is in a stage verdict, not the exit code)", failures)
	}

	violations := RatchetViolations(committed, a.Estates)
	if len(violations) != 1 {
		t.Fatalf("RatchetViolations after a real RunEstates run = %v, want exactly 1 violation for regressor/test_plan", violations)
	}
	if violations[0].Estate != "regressor" || violations[0].Stage != "test_plan" || violations[0].From != "pass" || violations[0].To != "fail" {
		t.Errorf("violation = %+v, want regressor/test_plan pass->fail", violations[0])
	}
}
