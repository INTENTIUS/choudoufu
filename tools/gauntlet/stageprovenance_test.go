// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// stageprovenance_test.go: issue #1069's second half.
//
// The first half (9525811174) stopped per-stage SECONDS being carried
// across an early abort. This is the same defect one level up, on the
// verdicts themselves: main's terralith-scale row read `greenfield: fail`,
// `clear: false` and `day2_remove: pass` at once, because a run that dies
// at greenfield never reaches day2_remove and the row kept the last run to
// reach it. The carry-forward stays - a stale verdict is still the best
// thing known about that stage - but it must be labelled with the run that
// measured it and must stop reading as a current pass.
//
// Every guard below was proven red before it was proven green; each says in
// its own comment what the red looked like.

// provenanceEstate is the one-estate manifest these tests Rebuild against.
func provenanceEstate() *Manifest {
	return &Manifest{Estates: []Estate{{
		Name: "x", Source: "s", Lane: "reference", Set: SetCore,
		Reason: "fixture", Script: "live/e2e/x/run.sh",
	}}}
}

// allHeadlinePass is a stage map where every headline stage passes, so a
// test can flip exactly one cell and know nothing else moved it.
func allHeadlinePass() map[string]string {
	stages := map[string]string{}
	for _, s := range Stages() {
		stages[s.ID] = VerdictNotRun
	}
	for _, s := range HeadlineStages() {
		stages[s.ID] = VerdictPass
	}
	return stages
}

// TestCarriedPassDoesNotCountTowardClear is the important half of #1069's
// fix: a headline stage whose pass was measured by a DIFFERENT run than the
// one the row records must not clear that stage.
//
// Proven red first by reverting isClearAgainst's provenance check (the
// `v == VerdictPass && current(s.ID)` line back to a bare
// `v == VerdictPass`): the carried row came out clear=true - a row whose
// recorded run never reached day2_remove claiming the whole board's
// headline promise on a verdict from some earlier run. That is exactly the
// shape the terralith-scale row shipped in.
func TestCarriedPassDoesNotCountTowardClear(t *testing.T) {
	m := provenanceEstate()

	// The control: identical stages, identical provenance stamps, all of
	// them naming the run the row records. Clear.
	current := EstateResult{
		Name: "x", Protocol: ProtocolGauntlet, Stages: allHeadlinePass(),
		LastRun: &LastRun{Commit: "runcommit", Date: "2026-09-14T00:00:00Z"},
	}
	current.StageRuns = map[string]StageRun{}
	for id, v := range current.Stages {
		if v == VerdictPass {
			current.StageRuns[id] = StageRun{Commit: "runcommit", Date: "2026-09-14T00:00:00Z"}
		}
	}
	a := &Artifact{Schema: 1, Estates: []EstateResult{current}}
	a.Rebuild(m, nil, "img", OracleVersions{})
	got, _ := a.Result("x")
	if !got.Clear {
		t.Fatalf("control row (every headline stage measured by the recorded run) is not clear; the fixture, not the fix, is wrong: %v", got.Stages)
	}

	// Now carry one headline stage's pass from an earlier run. Nothing else
	// about the row changes: same verdicts, same last_run.
	carriedID := HeadlineStages()[len(HeadlineStages())-1].ID
	carried := current
	carried.StageRuns = map[string]StageRun{}
	for id, sr := range current.StageRuns {
		carried.StageRuns[id] = sr
	}
	carried.StageRuns[carriedID] = StageRun{Commit: "oldercommit", Date: "2026-09-01T00:00:00Z"}
	b := &Artifact{Schema: 1, Estates: []EstateResult{carried}}
	b.Rebuild(m, nil, "img", OracleVersions{})
	got, _ = b.Result("x")
	if got.Clear {
		t.Errorf("row is clear although headline stage %q reads pass from commit oldercommit, not from the run the row records (runcommit) - a carried verdict must not clear a stage (#1069)", carriedID)
	}
	if got.Stages[carriedID] != VerdictPass {
		t.Errorf("stage %q verdict = %q, want the carried pass kept untouched: #1069 labels a carried verdict, it never deletes one", carriedID, got.Stages[carriedID])
	}

	// And the set summary follows the rows, derived rather than written.
	if sum := b.Sets["core"]; sum.Clear != 0 || sum.Estates != 1 {
		t.Errorf("core summary = %d/%d, want 0/1 - the bar is recomputed from the rows", sum.Clear, sum.Estates)
	}
}

// TestUnknownProvenanceIsNotStale draws the line the fix must not cross. A
// row with NO stage_runs at all - every row written before #1069 - keeps
// exactly the clear flag and cells it had. "We never wrote it down" is not
// evidence of staleness, and treating it as such would silently retract 28
// clear rows the day this lands.
//
// Proven red by making StageCarried return true for a missing entry: the
// core bar went from 1/1 to 0/1 on a row nothing had measured differently.
func TestUnknownProvenanceIsNotStale(t *testing.T) {
	m := provenanceEstate()
	a := &Artifact{Schema: 1, Estates: []EstateResult{{
		Name: "x", Protocol: ProtocolGauntlet, Stages: allHeadlinePass(),
		LastRun: &LastRun{Commit: "runcommit", Date: "2026-09-14T00:00:00Z"},
	}}}
	a.Rebuild(m, nil, "img", OracleVersions{})
	got, _ := a.Result("x")
	if !got.Clear {
		t.Errorf("a row with no stage_runs at all lost its clear flag; unknown provenance must read as unknown, not as stale (#1069)")
	}
	if got.StageCarried(HeadlineStages()[0].ID) {
		t.Errorf("StageCarried is true for a stage with no recorded provenance; that is an assertion the artifact cannot support")
	}
	if got.StageMeasuredByLastRun(HeadlineStages()[0].ID) {
		t.Errorf("StageMeasuredByLastRun is true for a stage with no recorded provenance; the strict form must refuse the unknown state")
	}
}

// TestSameCommitDifferentRunIsStillCarried: two runs at one commit are two
// runs. The board's own repeated-run loop (re-run an estate, fix nothing,
// re-run again) never changes the commit, so a commit-only comparison would
// let the exact carry-forward this issue is about through untouched.
func TestSameCommitDifferentRunIsStillCarried(t *testing.T) {
	r := EstateResult{
		Name: "x", Stages: map[string]string{"day2_remove": VerdictPass},
		StageRuns: map[string]StageRun{"day2_remove": {Commit: "same", Date: "2026-09-01T00:00:00Z"}},
		LastRun:   &LastRun{Commit: "same", Date: "2026-09-14T00:00:00Z"},
	}
	if !r.StageCarried("day2_remove") {
		t.Error("a stage measured by an earlier run at the SAME commit reads as current; provenance compares date as well as commit (#1069)")
	}
}

// TestCarriedVerdictRendersStaleNotPass is the board half. A carried pass
// must not print the word "pass" anywhere a reader scans, on the index row
// or on the estate page, and the estate page must say which verdict was
// carried and from when.
//
// Proven red first by leaving boardEstate calling verdictMark instead of
// verdictMarkFor: the index cell read "pass" for a stage the recorded run
// never reached, which is the finding restated.
func TestCarriedVerdictRendersStaleNotPass(t *testing.T) {
	m := provenanceEstate()
	stages := allHeadlinePass()
	carriedID := "day2_remove"
	stages[carriedID] = VerdictPass
	a := &Artifact{
		Schema: 1, Emulator: "img",
		Stages: Stages(),
		Estates: []EstateResult{{
			Name: "x", Protocol: ProtocolGauntlet, Set: SetCore, Lane: "reference",
			Stages: stages,
			StageRuns: map[string]StageRun{
				"cold_deploy": {Commit: "runcommit", Date: "2026-09-14T00:00:00Z"},
				carriedID:     {Commit: "0123456789abcdef", Date: "2026-09-01T00:00:00Z"},
			},
			LastRun: &LastRun{Commit: "runcommit", Date: "2026-09-14T00:00:00Z", Emulator: "img"},
		}},
	}
	board := buildBoard(m, a)
	if len(board.Estates) != 1 {
		t.Fatalf("board has %d estates, want 1", len(board.Estates))
	}
	e := board.Estates[0]

	var carriedCell string
	i := 0
	for _, s := range a.Stages {
		if s.Status != StatusActive {
			continue
		}
		if s.ID == carriedID {
			carriedCell = e.Cells[i]
		}
		i++
	}
	if carriedCell != VerdictMarkStale {
		t.Errorf("index cell for carried stage %q = %q, want %q - a verdict from another run must not read as a pass (#1069)", carriedID, carriedCell, VerdictMarkStale)
	}

	var row BoardStageRow
	for _, sr := range e.StageRows {
		if sr.ID == carriedID {
			row = sr
		}
	}
	if row.Verdict != VerdictMarkStale {
		t.Errorf("estate page verdict for %q = %q, want %q", carriedID, row.Verdict, VerdictMarkStale)
	}
	if !strings.Contains(row.Provenance, "0123456789") || !strings.Contains(row.Provenance, "2026-09-01") {
		t.Errorf("estate page provenance for %q = %q, want the commit and date that actually measured it", carriedID, row.Provenance)
	}
	if !strings.Contains(e.StaleNote, carriedID) {
		t.Errorf("estate stale_note = %q, want it to name the carried stage %q", e.StaleNote, carriedID)
	}

	// A stage the recorded run DID measure is untouched, and so is a row
	// with no provenance at all.
	for _, sr := range e.StageRows {
		if sr.ID == "cold_deploy" {
			if sr.Verdict != "pass" {
				t.Errorf("cold_deploy, measured by the recorded run, renders %q; only a carried verdict goes stale", sr.Verdict)
			}
			if sr.Provenance != "" {
				t.Errorf("cold_deploy carries a provenance note (%q) although it is current; the note is for carried cells only", sr.Provenance)
			}
		}
	}
}

// TestCarriedNotRunIsNotMarkedStale: only a pass or a fail can mislead. A
// carried not_run or n/a asserts nothing, so it prints exactly as before -
// otherwise every kubernetes-lane n/a cell would grow a stale marker for a
// stage that can never run there at all (#1067).
func TestCarriedNotRunIsNotMarkedStale(t *testing.T) {
	for _, v := range []string{VerdictNotRun, VerdictNA} {
		if got := verdictMarkFor(v, true); got != verdictMark(v) {
			t.Errorf("verdictMarkFor(%q, carried) = %q, want %q", v, got, verdictMark(v))
		}
	}
	if got := verdictMarkFor(VerdictPass, true); got != VerdictMarkStale {
		t.Errorf("verdictMarkFor(pass, carried) = %q, want %q", got, VerdictMarkStale)
	}
	if got := verdictMarkFor(VerdictFail, true); got != VerdictMarkStale {
		t.Errorf("verdictMarkFor(fail, carried) = %q, want %q", got, VerdictMarkStale)
	}
}

// TestRunEstatesStampsEveryStageItReported: `gauntlet run` writes the
// provenance. A run that reports two stages and aborts must stamp exactly
// those two with its own commit and date, and must leave the stages it
// never reached carrying whatever stamp they already had - never backdated,
// never silently promoted to this run.
//
// Proven red first by removing the stampStage call from RunEstates's merge
// loop: StageRuns came back nil and every carried verdict was
// indistinguishable from a measured one, which is the pre-fix artifact.
func TestRunEstatesStampsEveryStageItReported(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join("live", "e2e", "x", "run.sh")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\n" +
		"printf 'GAUNTLET protocol=1\\n'\n" +
		"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=5 detail=ok\\n'\n" +
		"printf 'GAUNTLET stage=migrate verdict=fail duration_s=7 detail=died here\\n'\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(root, scriptPath), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{Estates: []Estate{{Name: "x", Source: "s", Lane: "reference", Set: SetGrowing, Script: scriptPath}}}
	a := &Artifact{Schema: 1, Estates: []EstateResult{{
		Name:     "x",
		Protocol: ProtocolGauntlet,
		Stages: map[string]string{
			"cold_deploy": VerdictPass,
			"migrate":     VerdictPass,
			"day2_remove": VerdictPass,
		},
		StageRuns: map[string]StageRun{
			"cold_deploy": {Commit: "priorcommit", Date: "2026-09-01T00:00:00Z"},
			"migrate":     {Commit: "priorcommit", Date: "2026-09-01T00:00:00Z"},
			"day2_remove": {Commit: "priorcommit", Date: "2026-09-01T00:00:00Z"},
		},
		LastRun: &LastRun{Commit: "priorcommit", Date: "2026-09-01T00:00:00Z"},
	}}}

	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"x"}, Stdout: &out}, "newcommit", "img"); err != nil {
		t.Fatal(err)
	}
	r, _ := a.Result("x")
	if r.LastRun == nil {
		t.Fatal("LastRun is nil")
	}
	for _, id := range []string{"cold_deploy", "migrate"} {
		sr, ok := r.StageRuns[id]
		if !ok {
			t.Errorf("stage %q was reported by this run but carries no stage_runs entry", id)
			continue
		}
		if sr.Commit != "newcommit" || sr.Date != r.LastRun.Date {
			t.Errorf("stage %q stamped %+v, want this run (%s / %s)", id, sr, "newcommit", r.LastRun.Date)
		}
		if r.StageCarried(id) {
			t.Errorf("stage %q reads as carried although this run reported it", id)
		}
	}
	if got := r.StageRuns["day2_remove"]; got.Commit != "priorcommit" {
		t.Errorf("day2_remove's stamp = %+v, want the prior run's - this run never reached it and must not claim it", got)
	}
	if !r.StageCarried("day2_remove") {
		t.Error("day2_remove reads as current although this run never reported it (#1069)")
	}
	if r.Stages["day2_remove"] != VerdictPass {
		t.Errorf("day2_remove verdict = %q, want the carried pass kept - #1069 labels, it does not delete", r.Stages["day2_remove"])
	}
	if got := r.CarriedStages(); !reflect.DeepEqual(got, []string{"day2_remove"}) {
		t.Errorf("CarriedStages() = %v, want [day2_remove]", got)
	}
}

// TestRunEstatesStampsTheStageARunnerFailureWrites: recordRunnerFailure
// (run.go) writes a real fail for the stage a run died before reaching.
// That verdict is THIS run's finding, so it must be stamped as this run's -
// left unstamped it would keep an older stamp and render stale, hiding the
// very failure #497 added it to surface.
func TestRunEstatesStampsTheStageARunnerFailureWrites(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join("live", "e2e", "x", "run.sh")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	// Speaks the protocol, then dies before a single stage line: #497's
	// shape, where recordRunnerFailure writes cold_deploy=fail.
	script := "#!/usr/bin/env bash\nprintf 'GAUNTLET protocol=1\\n'\nexit 3\n"
	if err := os.WriteFile(filepath.Join(root, scriptPath), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Estates: []Estate{{Name: "x", Source: "s", Lane: "reference", Set: SetGrowing, Script: scriptPath}}}
	a := &Artifact{Schema: 1, Estates: []EstateResult{{
		Name: "x", Protocol: ProtocolGauntlet,
		Stages:    map[string]string{"cold_deploy": VerdictPass},
		StageRuns: map[string]StageRun{"cold_deploy": {Commit: "priorcommit", Date: "2026-09-01T00:00:00Z"}},
		LastRun:   &LastRun{Commit: "priorcommit", Date: "2026-09-01T00:00:00Z"},
	}}}
	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"x"}, Stdout: &out}, "newcommit", "img"); err != nil {
		t.Fatal(err)
	}
	r, _ := a.Result("x")
	failed := ActiveStages()[0].ID
	if r.Stages[failed] != VerdictFail {
		t.Fatalf("%s = %q, want fail (recordRunnerFailure's own write)", failed, r.Stages[failed])
	}
	if r.StageCarried(failed) {
		t.Errorf("%s reads as carried although this run is what wrote its fail; a runner failure must not render stale", failed)
	}
}

// TestBackfillStampsOnlyWhatTheArtifactSupports covers the migration. A row
// whose recorded run emitted per-stage seconds has a witness for which
// stages that run measured; a row with none has no witness and must be left
// alone rather than stamped on a guess.
//
// Proven red by having the backfill stamp every stage on every row with
// last_run.commit: the witness-less legacy row came out claiming its
// recorded run measured all fourteen stages, which is precisely the
// unfounded assertion this issue exists to stop.
func TestBackfillStampsOnlyWhatTheArtifactSupports(t *testing.T) {
	a := &Artifact{Schema: 1, Estates: []EstateResult{
		{
			// Aborted early: seconds for two stages, a third carrying a
			// pass from some run this artifact cannot name.
			Name: "aborted", Protocol: ProtocolGauntlet,
			Stages: map[string]string{
				"cold_deploy": VerdictPass,
				"migrate":     VerdictFail,
				"day2_remove": VerdictPass,
				"day2_crash":  VerdictNotRun,
			},
			LastRun: &LastRun{
				Commit: "runcommit", Date: "2026-09-14T00:00:00Z",
				Seconds: map[string]float64{"cold_deploy": 5, "migrate": 7},
			},
		},
		{
			// No per-stage seconds at all: no witness, no stamp.
			Name: "witnessless", Protocol: ProtocolGauntlet,
			Stages:  map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass},
			LastRun: &LastRun{Commit: "runcommit", Date: "2026-09-14T00:00:00Z"},
		},
		{
			// Legacy protocol: verdicts were transcribed by hand, never
			// measured by a run this artifact records.
			Name: "legacy", Protocol: ProtocolLegacy,
			Stages:  map[string]string{"cold_deploy": VerdictPass},
			LastRun: &LastRun{Commit: "runcommit", Date: "2026-09-14T00:00:00Z", Seconds: map[string]float64{"cold_deploy": 5}},
		},
	}}

	changed := backfillStageProvenance(a)
	if len(changed) != 1 || !strings.HasPrefix(changed[0], "aborted:") {
		t.Fatalf("backfill changed %v, want exactly the aborted row", changed)
	}

	aborted, _ := a.Result("aborted")
	want := map[string]StageRun{
		"cold_deploy": {Commit: "runcommit", Date: "2026-09-14T00:00:00Z"},
		"migrate":     {Commit: "runcommit", Date: "2026-09-14T00:00:00Z"},
		"day2_remove": {},
	}
	if !reflect.DeepEqual(aborted.StageRuns, want) {
		t.Errorf("aborted stage_runs = %+v, want %+v (day2_crash is not_run and asserts nothing, so it gets no entry)", aborted.StageRuns, want)
	}
	if !aborted.StageCarried("day2_remove") {
		t.Error("day2_remove is not carried after the backfill, although the recorded run emitted no duration_s for it")
	}
	if aborted.StageCarried("cold_deploy") {
		t.Error("cold_deploy is carried after the backfill, although the recorded run measured it")
	}

	for _, name := range []string{"witnessless", "legacy"} {
		r, _ := a.Result(name)
		if len(r.StageRuns) != 0 {
			t.Errorf("%s was stamped (%+v) although the artifact has no witness for which stages its run measured", name, r.StageRuns)
		}
	}

	// Idempotent: a second pass changes nothing, so `just` and CI can run
	// it as often as they like without producing a second, different
	// answer.
	if again := backfillStageProvenance(a); len(again) != 0 {
		t.Errorf("second backfill pass reported changes %v, want none", again)
	}
}

// TestBuildScaleRecordFromEstateUsesProvenanceWitness: the scale record is
// keyed by (estate, target, SCALE), so a verdict from a different run is a
// claim about a different size. Once a row carries provenance, that - not
// the seconds map - decides what goes in.
//
// Proven red by leaving BuildScaleRecordFromEstate on the seconds witness
// alone and giving the fixture a carried stage that DOES have a recorded
// duration (the shape a pre-9525811174 row has): the record admitted
// day2_remove's pass, measured eleven days earlier, as though this run had
// produced it.
func TestBuildScaleRecordFromEstateUsesProvenanceWitness(t *testing.T) {
	e := EstateResult{
		Name: "terralith-scale", Protocol: ProtocolGauntlet,
		Stages: map[string]string{
			"cold_deploy": VerdictPass,
			"migrate":     VerdictPass,
			"day2_remove": VerdictPass,
		},
		StageRuns: map[string]StageRun{
			"cold_deploy": {Commit: "runcommit", Date: "2026-09-14T00:00:00Z"},
			"migrate":     {Commit: "runcommit", Date: "2026-09-14T00:00:00Z"},
			"day2_remove": {Commit: "oldcommit", Date: "2026-09-01T00:00:00Z"},
		},
		LastRun: &LastRun{
			Commit: "runcommit", Date: "2026-09-14T00:00:00Z", DurationS: 100,
			Detail: map[string]string{
				"cold_deploy": "stock terraform applied 79 resources at scale=1 resources=79 scale=1",
				"day2_remove": "the carried sentence",
			},
			// A pre-fix row: seconds for the carried stage too.
			Seconds: map[string]float64{"cold_deploy": 40, "migrate": 30, "day2_remove": 60},
		},
	}
	rec, ok := BuildScaleRecordFromEstate(e, "test")
	if !ok {
		t.Fatal("BuildScaleRecordFromEstate declined a scale-measured row")
	}
	if _, in := rec.Stages["day2_remove"]; in {
		t.Errorf("the record admitted day2_remove, measured at oldcommit, into a record about this run's scale (#1069)")
	}
	for _, id := range []string{"cold_deploy", "migrate"} {
		if _, in := rec.Stages[id]; !in {
			t.Errorf("the record dropped %q, which this run measured", id)
		}
	}
	// And the arithmetic closes, which it could not while a foreign run's
	// 60s was billed against this run's 100s total.
	if rec.UnaccountedSeconds == nil {
		t.Fatal("no unaccounted_seconds computed")
	}
	if *rec.UnaccountedSeconds != 30 {
		t.Errorf("unaccounted_seconds = %v, want 30 (100 total - 40 - 30); a negative remainder is #1069's own finding", *rec.UnaccountedSeconds)
	}
}

// TestCommittedRowsAgreeWithTheirOwnProvenance reads live/gauntlet.json
// exactly as committed. Every carried verdict in it must render stale, and
// no carried pass may be holding a row's clear flag up.
//
// Proven red by hand-carrying a stamp in a copy of the loaded artifact: the
// check reported the row still clear before isClearAgainst consulted
// provenance.
func TestCommittedRowsAgreeWithTheirOwnProvenance(t *testing.T) {
	root := testRoot(t)
	a, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	board := buildBoard(m, a)
	marks := map[string][]string{}
	notes := map[string]string{}
	for _, e := range board.Estates {
		for _, sr := range e.StageRows {
			if sr.Verdict == VerdictMarkStale {
				marks[e.Name] = append(marks[e.Name], sr.ID)
			}
		}
		notes[e.Name] = e.StaleNote
	}
	for _, r := range a.Estates {
		carried := r.CarriedStages()
		if len(carried) == 0 {
			if notes[r.Name] != "" {
				t.Errorf("%q carries nothing but the board prints a stale note: %q", r.Name, notes[r.Name])
			}
			continue
		}
		sort.Strings(carried)
		got := append([]string(nil), marks[r.Name]...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, carried) {
			t.Errorf("%q: board marks %v stale, row carries %v - every carried verdict must render stale and nothing else may", r.Name, got, carried)
		}
		if notes[r.Name] == "" {
			t.Errorf("%q carries %v but the board prints no stale note", r.Name, carried)
		}
		if r.Clear {
			// A carried verdict cannot be part of a clear claim: Rebuild
			// would already have refused it, so this can only fire if a
			// row were hand-edited.
			for _, id := range carried {
				if s, ok := StageByID(id); ok && s.Headline {
					t.Errorf("%q is clear although headline stage %q reads a carried verdict", r.Name, id)
				}
			}
		}
	}
}

// TestCarriedVerdictTalliesAsStaleNotPass is the set-summary half of the
// same rule the board already follows. tallyRows switched on the raw
// verdict string, so a cell the board rendered as "stale" was still counted
// in Tally.Pass - two published surfaces disagreeing about one cell, with
// the artifact's number being the one that reads as a measurement.
//
// Proven red first, against tallyRows as it stood (the Stale field existed,
// nothing wrote it):
//
//	stage "day2_count": tally = {Pass:2 Fail:0 NotRun:0 NA:0 Stale:0}, want
//	  {Pass:1 Fail:0 NotRun:0 NA:0 Stale:1} - a carried verdict belongs in
//	  its own bucket, not in pass (#1069)
//
// The sum check below did NOT fire in that red, and that is the point of
// the bucket: counting the carried cell as a pass kept the breakdown
// summing to the estate count while making it say the wrong thing. Simply
// dropping the cell would break the sum instead. It has to go somewhere,
// and somewhere is its own name.
func TestCarriedVerdictTalliesAsStaleNotPass(t *testing.T) {
	m := &Manifest{Estates: []Estate{
		{Name: "measured", Source: "s", Lane: "reference", Set: SetCore, Reason: "r", Script: "live/e2e/measured/run.sh"},
		{Name: "carried", Source: "s", Lane: "reference", Set: SetCore, Reason: "r", Script: "live/e2e/carried/run.sh"},
	}}
	stamp := StageRun{Commit: "runcommit", Date: "2026-09-14T00:00:00Z"}
	row := func(name string, carriedStage string) EstateResult {
		r := EstateResult{
			Name: name, Protocol: ProtocolGauntlet, Stages: allHeadlinePass(),
			StageRuns: map[string]StageRun{},
			LastRun:   &LastRun{Commit: "runcommit", Date: "2026-09-14T00:00:00Z"},
		}
		for id, v := range r.Stages {
			if v == VerdictPass {
				r.StageRuns[id] = stamp
			}
		}
		if carriedStage != "" {
			r.StageRuns[carriedStage] = StageRun{Commit: "oldercommit", Date: "2026-09-01T00:00:00Z"}
		}
		return r
	}
	carriedID := "day2_count"
	if _, ok := StageByID(carriedID); !ok {
		t.Skipf("%s is not in the stage registry", carriedID)
	}
	a := &Artifact{Schema: 1, Estates: []EstateResult{row("measured", ""), row("carried", carriedID)}}
	a.Rebuild(m, nil, "img", OracleVersions{})

	sum := a.Sets["core"]
	got := sum.Stages[carriedID]
	want := Tally{Pass: 1, Stale: 1}
	if got != want {
		t.Errorf("stage %q: tally = %+v, want %+v - a carried verdict belongs in its own bucket, not in pass (#1069)", carriedID, got, want)
	}
	if total := got.Pass + got.Fail + got.NotRun + got.NA + got.Stale; total != sum.Estates {
		t.Errorf("stage %q: pass+fail+not_run+n/a+stale = %d, want %d (the set's own estate count)", carriedID, total, sum.Estates)
	}

	// Every other headline stage is measured on both rows and untouched.
	for _, s := range HeadlineStages() {
		if s.ID == carriedID {
			continue
		}
		if g := sum.Stages[s.ID]; g != (Tally{Pass: 2}) {
			t.Errorf("stage %q: tally = %+v, want {Pass:2} - only the carried cell moves", s.ID, g)
		}
	}
}

// TestUnknownProvenanceTalliesAsItAlwaysDid is the tally's copy of the
// three-state rule TestUnknownProvenanceIsNotStale holds the board and the
// clear flag to. A row with no stage_runs at all - every row written before
// #1069 - must tally exactly as before, or the day this lands 28 rows'
// worth of passes silently move into a bucket on the strength of a field
// that was never written.
func TestUnknownProvenanceTalliesAsItAlwaysDid(t *testing.T) {
	m := provenanceEstate()
	a := &Artifact{Schema: 1, Estates: []EstateResult{{
		Name: "x", Protocol: ProtocolGauntlet, Stages: allHeadlinePass(),
		LastRun: &LastRun{Commit: "runcommit", Date: "2026-09-14T00:00:00Z"},
	}}}
	a.Rebuild(m, nil, "img", OracleVersions{})
	sum := a.Sets["core"]
	for _, s := range HeadlineStages() {
		if g := sum.Stages[s.ID]; g != (Tally{Pass: 1}) {
			t.Errorf("stage %q: tally = %+v, want {Pass:1} - unknown provenance is not stale", s.ID, g)
		}
	}
}

// TestCommittedTallyAgreesWithTheBoard is the cross-surface guard for the
// drift this bucket exists to close. live/gauntlet.json's per-stage tally
// and site/data/gauntlet_board.json's per-stage cells are two published
// descriptions of the same cells, written by two functions (tallyRows,
// artifact.go; verdictMarkFor via boardEstate, board.go). They disagreed:
// the board rendered terralith-scale's day2_count as stale while
// sets.core.stages.day2_count.pass still counted it as a pass, and of the
// two the artifact is the one that reads as a measurement.
//
// So: for every set and lane, Tally.Stale must equal the number of that
// set's rows whose board cell for that stage reads "stale", and the five
// buckets must sum to the set's own estate count.
//
// Proven red first by reverting tallyRows to switching on the raw verdict
// string:
//
//	set "core" stage "day2_count": tally.Stale = 0, board renders 1 row(s)
//	  stale - the artifact and the board must not disagree about a cell
//	set "core" stage "day2_count": pass+fail+not_run+n/a+stale = 27, want 26
//	  (the set's own estate count)
func TestCommittedTallyAgreesWithTheBoard(t *testing.T) {
	root := testRoot(t)
	a, err := LoadArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	board := buildBoard(m, a)
	// Stage cells by estate name, as the board renders them.
	cells := map[string]map[string]string{}
	for _, e := range board.Estates {
		cells[e.Name] = map[string]string{}
		for _, sr := range e.StageRows {
			cells[e.Name][sr.ID] = sr.Verdict
		}
	}

	check := func(label string, sum SetSummary, member func(EstateResult) bool) {
		for _, s := range Stages() {
			want := 0
			for _, r := range a.Estates {
				if !member(r) {
					continue
				}
				if cells[r.Name][s.ID] == VerdictMarkStale {
					want++
				}
			}
			got := sum.Stages[s.ID]
			if got.Stale != want {
				t.Errorf("set %q stage %q: tally.Stale = %d, board renders %d row(s) stale - the artifact and the board must not disagree about a cell", label, s.ID, got.Stale, want)
			}
			if total := got.Pass + got.Fail + got.NotRun + got.NA + got.Stale; total != sum.Estates {
				t.Errorf("set %q stage %q: pass+fail+not_run+n/a+stale = %d, want %d (the set's own estate count)", label, s.ID, total, sum.Estates)
			}
		}
	}

	for key := range SetLabels {
		key := key
		check(key, a.Sets[key], func(r EstateResult) bool {
			if r.Substrate != "" {
				return false
			}
			return key != "core" || r.Set == SetCore
		})
	}
	for lane, sum := range a.Lanes {
		lane := lane
		check(lane+" lane", sum, func(r EstateResult) bool { return r.Lane == lane })
	}
}
