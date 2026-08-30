// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestBehaviorsProvenRequiresAMappedFixture: a stage with zero fixtures
// mapped to it is NOT proven, even though "no failing fixture exists" could
// be read as vacuous agreement. This is the #522 foundation unit's own
// state today - every fixture ships with Stage == "" - so the test also
// pins that live/behaviors.json (as committed) proves nothing yet, honestly,
// rather than defaulting BehaviorsProven's denominator logic to "unmapped
// counts as passing".
func TestBehaviorsProvenRequiresAMappedFixture(t *testing.T) {
	bi := &BehaviorIndex{Fixtures: []BehaviorFixture{
		{ID: "a", Stage: "", LastRun: &BehaviorLastRun{Verdict: VerdictPass}},
	}}
	proven, total := BehaviorsProven(bi)
	if proven != 0 {
		t.Fatalf("proven = %d, want 0: an unmapped fixture must not count toward any stage", proven)
	}
	if total != len(Stages()) {
		t.Fatalf("total = %d, want %d (len(Stages()))", total, len(Stages()))
	}
}

// TestBehaviorsProvenPassRequiresEveryMappedFixtureToPass: the mutation half
// of the check above - if BehaviorsProven silently treated a single mapped,
// passing fixture as sufficient regardless of a SECOND fixture mapped to the
// same stage, it would pass this. Both must pass for the stage to count.
func TestBehaviorsProvenPassRequiresEveryMappedFixtureToPass(t *testing.T) {
	stageID := Stages()[0].ID
	bi := &BehaviorIndex{Fixtures: []BehaviorFixture{
		{ID: "a", Stage: stageID, LastRun: &BehaviorLastRun{Verdict: VerdictPass}},
		{ID: "b", Stage: stageID, LastRun: &BehaviorLastRun{Verdict: VerdictFail}},
	}}
	proven, _ := BehaviorsProven(bi)
	if proven != 0 {
		t.Fatalf("proven = %d, want 0: one of the two fixtures mapped to %s failed", proven, stageID)
	}

	// BREAK=1 equivalent: fix the failing fixture and confirm the count
	// actually moves. A check that reports 0 either way is not measuring
	// anything (HANDOFF.md: "prove it is load-bearing by making it fail on
	// purpose", and the reverse - prove a fix is visible too).
	bi.Fixtures[1].LastRun.Verdict = VerdictPass
	proven, total := BehaviorsProven(bi)
	if proven != 1 {
		t.Fatalf("proven = %d, want 1 once both fixtures mapped to %s pass", proven, stageID)
	}
	if total != len(Stages()) {
		t.Fatalf("total = %d, want %d", total, len(Stages()))
	}
}

// TestBehaviorsProvenNoRunIsNotProven: a fixture that has never been run
// (LastRun nil) must not count as passing. Mirrors the "a row with no
// last_run has never run at all" rule EstateResult.IsStale documents for
// estates (artifact.go).
func TestBehaviorsProvenNoRunIsNotProven(t *testing.T) {
	stageID := Stages()[1].ID
	bi := &BehaviorIndex{Fixtures: []BehaviorFixture{
		{ID: "a", Stage: stageID, LastRun: nil},
	}}
	proven, _ := BehaviorsProven(bi)
	if proven != 0 {
		t.Fatalf("proven = %d, want 0: a fixture with no last_run has never proven anything", proven)
	}
}

// TestBehaviorsProvenNilIndex: Rebuild is called with bi == nil by every
// existing test and by any command that has not loaded live/behaviors.json;
// it must never panic and must report the honest zero, not a fabricated
// count.
func TestBehaviorsProvenNilIndex(t *testing.T) {
	proven, total := BehaviorsProven(nil)
	if proven != 0 || total != len(Stages()) {
		t.Fatalf("BehaviorsProven(nil) = (%d, %d), want (0, %d)", proven, total, len(Stages()))
	}
}

// TestRebuildSetsBehaviorsFields checks the field is actually wired into
// Artifact.Rebuild, not just computable in isolation - the #522 ask is that
// this be "computed in Rebuild, never stored by hand".
func TestRebuildSetsBehaviorsFields(t *testing.T) {
	stageID := Stages()[0].ID
	bi := &BehaviorIndex{Fixtures: []BehaviorFixture{
		{ID: "a", Stage: stageID, LastRun: &BehaviorLastRun{Verdict: VerdictPass}},
	}}
	a := &Artifact{}
	m := &Manifest{}
	a.Rebuild(m, bi, "img", OracleVersions{})
	if a.BehaviorsProven != 1 {
		t.Fatalf("a.BehaviorsProven = %d, want 1", a.BehaviorsProven)
	}
	if a.BehaviorsTotal != len(Stages()) {
		t.Fatalf("a.BehaviorsTotal = %d, want %d", a.BehaviorsTotal, len(Stages()))
	}
	// A stale value left over from a previous Rebuild must not survive: call
	// again with an index that proves nothing and confirm it drops back to 0
	// rather than a max-so-far ratchet leaking in from somewhere.
	a.Rebuild(m, &BehaviorIndex{}, "img", OracleVersions{})
	if a.BehaviorsProven != 0 {
		t.Fatalf("a.BehaviorsProven = %d after an empty index, want 0 (must not carry the previous run's count forward)", a.BehaviorsProven)
	}
}

// TestRunBehaviorsRecordsPassAndFail runs two real bash scripts (one exit 0,
// one exit 1) through RunBehaviors end to end - the same shape
// TestRunEstatesPreservesDetailForUnreachedStages (run_test.go) uses for the
// estate runner - and checks the recorded verdict, exit code and the
// returned failure count. It also checks the shared FLOCI_PORT is exported
// identically to both fixtures, which is the whole mechanism #522 asks for
// ("ONE shared emulator... assign one port for the whole run") without
// touching either script.
func TestRunBehaviorsRecordsPassAndFail(t *testing.T) {
	root := t.TempDir()
	writeScript := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeScript("live/e2e/ok/run.sh", "#!/usr/bin/env bash\n"+
		"[ \"$FLOCI_PORT\" = \"4900\" ] || { echo \"wrong port: $FLOCI_PORT\" >&2; exit 2; }\n"+
		"exit 0\n")
	writeScript("live/e2e/bad/run.sh", "#!/usr/bin/env bash\n"+
		"[ \"$FLOCI_PORT\" = \"4900\" ] || { echo \"wrong port: $FLOCI_PORT\" >&2; exit 2; }\n"+
		"exit 1\n")

	bi := &BehaviorIndex{Fixtures: []BehaviorFixture{
		{ID: "ok", Script: "live/e2e/ok/run.sh", Runnable: true, Runner: true},
		{ID: "bad", Script: "live/e2e/bad/run.sh", Runnable: true, Runner: true},
	}}

	var out bytes.Buffer
	failures, err := RunBehaviors(root, bi, BehaviorsRunOptions{Port: 4900, Stdout: &out}, "testcommit")
	if err != nil {
		t.Fatalf("RunBehaviors: %v", err)
	}
	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}

	ok, found := bi.ByID("ok")
	if !found || ok.LastRun == nil || ok.LastRun.Verdict != VerdictPass || ok.LastRun.ExitCode != 0 {
		t.Fatalf("ok fixture's last_run = %+v, want verdict=pass exit_code=0", ok.LastRun)
	}
	bad, found := bi.ByID("bad")
	if !found || bad.LastRun == nil || bad.LastRun.Verdict != VerdictFail || bad.LastRun.ExitCode != 1 {
		t.Fatalf("bad fixture's last_run = %+v, want verdict=fail exit_code=1", bad.LastRun)
	}
	if ok.LastRun.Commit != "testcommit" || bad.LastRun.Commit != "testcommit" {
		t.Fatalf("commit not stamped onto both fixtures' last_run")
	}
}

// TestRunBehaviorsDefaultSelectionIsRunnerOnly: with no Names given and
// -all not set, RunBehaviors must select exactly the Runner==true fixtures -
// the tier-1 default matrix - and skip everything else (adoption,
// legacy-demo, and the non-runnable estate-block shape).
func TestRunBehaviorsDefaultSelectionIsRunnerOnly(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"live/e2e/a/run.sh", "live/e2e/b/run.sh"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bi := &BehaviorIndex{Fixtures: []BehaviorFixture{
		{ID: "a", Script: "live/e2e/a/run.sh", Category: CategoryShape, Runnable: true, Runner: true},
		{ID: "b", Script: "live/e2e/b/run.sh", Category: CategoryAdoption, Runnable: true, Runner: false},
	}}
	var out bytes.Buffer
	if _, err := RunBehaviors(root, bi, BehaviorsRunOptions{Port: 4901, Stdout: &out}, "c"); err != nil {
		t.Fatalf("RunBehaviors: %v", err)
	}
	a, _ := bi.ByID("a")
	b, _ := bi.ByID("b")
	if a.LastRun == nil {
		t.Fatalf("fixture a (Runner=true) was not run")
	}
	if b.LastRun != nil {
		t.Fatalf("fixture b (Runner=false, category=adoption) was run by default; only Runner=true fixtures may run without -all or explicit names")
	}
}

// TestBehaviorIndexRoundTrips: Canonical -> LoadBehaviorIndex must
// reproduce the same fixtures. Guards the same class of defect
// TestManifestIsCanonical guards for estates.json.
func TestBehaviorIndexRoundTrips(t *testing.T) {
	root := t.TempDir()
	bi := &BehaviorIndex{Fixtures: []BehaviorFixture{
		{ID: "x", Script: "live/e2e/x/run.sh", Category: CategoryShape, Runnable: true, Runner: true, Seam: "seam text", Shapes: []string{"count"}, Resources: 2},
	}}
	if err := SaveBehaviorIndex(root, bi); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBehaviorIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := got.ByID("x")
	if !ok {
		t.Fatalf("fixture x not found after round trip")
	}
	if f.Seam != "seam text" || f.Resources != 2 || len(f.Shapes) != 1 || f.Shapes[0] != "count" {
		t.Fatalf("fixture x round-tripped wrong: %+v", f)
	}
}

// TestLoadBehaviorIndexMissingFileIsEmpty mirrors LoadArtifact's rule for a
// missing live/gauntlet.json: a missing live/behaviors.json is an empty
// index, not an error, so a fresh checkout with no behaviors work done yet
// still builds and renders.
func TestLoadBehaviorIndexMissingFileIsEmpty(t *testing.T) {
	root := t.TempDir()
	bi, err := LoadBehaviorIndex(root)
	if err != nil {
		t.Fatalf("LoadBehaviorIndex on a missing file returned an error: %v", err)
	}
	if len(bi.Fixtures) != 0 {
		t.Fatalf("bi.Fixtures = %v, want empty", bi.Fixtures)
	}
}

// TestCommittedBehaviorIndexHasNoStageMapped pins the #522 foundation
// unit's own honest state: live/behaviors.json ships with every fixture's
// Stage empty. If this ever fails because a fixture gained a Stage, that is
// good news (the next unit's cell-mapping work has started) and this test
// should be updated to check the SPECIFIC mapping is sound, not deleted
// silently - see BehaviorsProven's own doc comment for why an unmapped
// stage must never be read as vacuously proven.
func TestCommittedBehaviorIndexHasNoStageMapped(t *testing.T) {
	root := repoRootForTest(t)
	bi, err := LoadBehaviorIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(bi.Fixtures) == 0 {
		t.Fatal("live/behaviors.json has no fixtures; the #522 foundation index is missing")
	}
	for _, f := range bi.Fixtures {
		if f.Stage != "" {
			t.Fatalf("fixture %q carries stage %q; if cell-mapping has started, this test's purpose has changed - see its doc comment", f.ID, f.Stage)
		}
	}
}

// repoRootForTest finds the checkout root the same way repoRoot() does, for
// a test that needs the real, committed live/behaviors.json rather than a
// t.TempDir() fixture.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}
