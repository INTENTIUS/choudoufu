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
//
// Both fixtures carry the mandatory shapes (Stages()[0] is cold_deploy,
// which identityTouchingStages does not name, so no identity_kind is
// needed) purely so this test isolates the pass/fail property - the shape-
// and identity-kind-coverage gate itself is
// TestBehaviorsProvenRequiresMandatoryShapeCoverage's job.
func TestBehaviorsProvenPassRequiresEveryMappedFixtureToPass(t *testing.T) {
	stageID := Stages()[0].ID
	bi := &BehaviorIndex{Fixtures: []BehaviorFixture{
		{ID: "a", Stage: stageID, Shapes: mandatoryShapes, LastRun: &BehaviorLastRun{Verdict: VerdictPass}},
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

// TestBehaviorsProvenRequiresMandatoryShapeCoverage: the #522 ruling's
// first coverage clause - "a real count block, a real for_each map, a
// module-nested resource" - required of EVERY proven stage, not only
// identity-touching ones. A stage whose mapped fixtures all pass but are
// all "scalar" shape (exactly day2_remove's real, committed state today:
// tagging-sweep and record-store are both scalar-only) must NOT read as
// proven - a passing, scalar-only set is real evidence the mechanism
// works, but not the representative set the ruling asks for. Found by
// review: BehaviorsProven originally counted a stage proven from an
// all-passing set with no shape check at all, which is exactly how
// day2_remove was misread as proven.
func TestBehaviorsProvenRequiresMandatoryShapeCoverage(t *testing.T) {
	stageID := Stages()[0].ID // cold_deploy - not in identityTouchingStages, so this isolates the shape gate alone
	bi := &BehaviorIndex{Fixtures: []BehaviorFixture{
		{ID: "a", Stage: stageID, Shapes: []string{"scalar"}, LastRun: &BehaviorLastRun{Verdict: VerdictPass}},
		{ID: "b", Stage: stageID, Shapes: []string{"scalar"}, LastRun: &BehaviorLastRun{Verdict: VerdictPass}},
	}}
	proven, _ := BehaviorsProven(bi)
	if proven != 0 {
		t.Fatalf("proven = %d, want 0: both mapped fixtures pass but neither covers count/for_each/module-nested - a scalar-only set must not read as proven", proven)
	}

	// Fill in the three mandatory shapes one at a time and confirm the
	// count stays 0 until coverage is genuinely complete, not merely
	// "some shape got added" - the same "prove it is load-bearing" rule
	// HANDOFF.md asks of every check.
	bi.Fixtures[0].Shapes = append(bi.Fixtures[0].Shapes, "count")
	if proven, _ := BehaviorsProven(bi); proven != 0 {
		t.Fatalf("proven = %d, want 0: count added but for_each and module-nested are still missing", proven)
	}
	bi.Fixtures[0].Shapes = append(bi.Fixtures[0].Shapes, "for_each")
	if proven, _ := BehaviorsProven(bi); proven != 0 {
		t.Fatalf("proven = %d, want 0: module-nested is still missing", proven)
	}
	bi.Fixtures[0].Shapes = append(bi.Fixtures[0].Shapes, "module-nested")
	proven, total := BehaviorsProven(bi)
	if proven != 1 {
		t.Fatalf("proven = %d, want 1: all three mandatory shapes are now covered (by fixture a alone) and both fixtures pass", proven)
	}
	if total != len(Stages()) {
		t.Fatalf("total = %d, want %d", total, len(Stages()))
	}
}

// TestBehaviorsProvenRequiresIdentityKindCoverageForIdentityTouchingStages:
// the ruling's SECOND coverage clause, for a stage identityTouchingStages
// names. Full shape coverage must not be enough on its own for such a
// stage; all three identity kinds (server-minted, deterministic, none)
// must be represented too.
func TestBehaviorsProvenRequiresIdentityKindCoverageForIdentityTouchingStages(t *testing.T) {
	stageID := "test_plan"
	if !identityTouchingStages[stageID] {
		t.Fatalf("test premise broken: %q is no longer classified identity-touching", stageID)
	}
	bi := &BehaviorIndex{Fixtures: []BehaviorFixture{
		// Full shape coverage lives on this one fixture so only the
		// identity-kind gate is under test below.
		{ID: "a", Stage: stageID, Shapes: mandatoryShapes, IdentityKind: "server-minted", LastRun: &BehaviorLastRun{Verdict: VerdictPass}},
	}}
	proven, _ := BehaviorsProven(bi)
	if proven != 0 {
		t.Fatalf("proven = %d, want 0: full shape coverage but only one of three identity kinds (server-minted)", proven)
	}

	bi.Fixtures = append(bi.Fixtures, BehaviorFixture{ID: "b", Stage: stageID, IdentityKind: "deterministic", LastRun: &BehaviorLastRun{Verdict: VerdictPass}})
	if proven, _ := BehaviorsProven(bi); proven != 0 {
		t.Fatalf("proven = %d, want 0: still missing the \"none\" identity kind", proven)
	}

	bi.Fixtures = append(bi.Fixtures, BehaviorFixture{ID: "c", Stage: stageID, IdentityKind: "none", LastRun: &BehaviorLastRun{Verdict: VerdictPass}})
	proven, total := BehaviorsProven(bi)
	if proven != 1 {
		t.Fatalf("proven = %d, want 1: all three identity kinds now covered, all mapped fixtures pass, shapes covered by fixture a", proven)
	}
	if total != len(Stages()) {
		t.Fatalf("total = %d, want %d", total, len(Stages()))
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
		{ID: "a", Stage: stageID, Shapes: mandatoryShapes, LastRun: &BehaviorLastRun{Verdict: VerdictPass}},
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

// TestCommittedBehaviorIndexStageMappingIsSound replaces
// TestCommittedBehaviorIndexHasNoStageMapped now that the #522 ruling's
// per-stage cell-mapping unit has landed - that test's own doc comment
// asked for exactly this update rather than a silent deletion, "check the
// SPECIFIC mapping is sound".
//
// It pins the committed live/behaviors.json's fixture -> stage mapping BY
// VALUE, so a future edit that reassigns, adds, or drops a mapping is
// caught here rather than discovered only by behaviors_proven's number
// quietly moving. Every mapping was decided by reading the fixture's own
// run.sh end to end against the target stage's Proves/Oracle text in
// tools/gauntlet/stages.go (see the #522 stage-mapping unit's PR body for
// the per-fixture reasoning) - none is a guess from the "seam" summary
// alone.
//
// Two tier-1 shape fixtures are DELIBERATELY left unmapped, pinned as a
// negative case rather than left to be "discovered" as an oversight:
//
//   - dataread-projection proves a data source's projected live read, which
//     no gauntlet stage's Proves text names at all.
//   - provisioner-taint proves the fork's own create-time-provisioner taint
//     tracking (record-primary identity's Provisioned bit surviving a
//     failed apply). That is a different mechanism from every stage above,
//     including the planned day2_crash: day2_crash is about an interrupted
//     CREATE-BEFORE-DESTROY replace's deposed key, not a provisioner
//     failure, and nothing in this fixture uses create_before_destroy.
//
// Both are catalogued, well-tested fixtures; they are simply not evidence
// for any of the 14 stages as those stages are worded today.
//
// Being MAPPED to a stage is not the same as making that stage PROVEN:
// BehaviorsProven (behaviors.go) additionally requires the mapped,
// passing fixtures to collectively cover the ruling's three mandatory
// shapes, plus all three named identity kinds for a stage that touches
// identity resolution. day2_remove is mapped with genuine, passing
// evidence (see the day2RemoveFixtures block below) but does not meet
// that bar - only test_plan does - so BehaviorsProven(committed
// live/behaviors.json) is 1, not 2.
func TestCommittedBehaviorIndexStageMappingIsSound(t *testing.T) {
	root := repoRootForTest(t)
	bi, err := LoadBehaviorIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(bi.Fixtures) == 0 {
		t.Fatal("live/behaviors.json has no fixtures; the #522 foundation index is missing")
	}

	// The exact, hand-decided mapping. Exhaustive over every fixture that
	// carries a non-empty Stage - not a subset - so an unlisted mapped
	// fixture fails this test rather than passing silently.
	want := map[string]string{
		"counted-module":         "test_plan",
		"create-over":            "test_plan",
		"deterministic-recreate": "test_plan",
		"lambda-residue":         "test_plan",
		"per-element":            "test_plan",
		"record-located":         "test_plan",
		"repeated-module":        "test_plan",
		"tagging-sweep":          "day2_remove",
		"record-store":           "day2_remove",
	}
	unmapped := []string{"dataread-projection", "provisioner-taint"}

	validStage := map[string]bool{}
	for _, s := range Stages() {
		validStage[s.ID] = true
	}

	got := map[string]string{}
	for _, f := range bi.Fixtures {
		if f.Stage == "" {
			continue
		}
		if !validStage[f.Stage] {
			t.Errorf("fixture %q maps to %q, which is not a stage id in Stages()", f.ID, f.Stage)
		}
		got[f.ID] = f.Stage
	}
	if len(got) != len(want) {
		t.Fatalf("mapped fixtures = %v, want exactly %v", got, want)
	}
	for id, stage := range want {
		if got[id] != stage {
			t.Errorf("fixture %q maps to %q, want %q", id, got[id], stage)
		}
	}
	for _, id := range unmapped {
		f, ok := bi.ByID(id)
		if !ok {
			t.Fatalf("fixture %q not found", id)
		}
		if f.Stage != "" {
			t.Errorf("fixture %q now carries stage %q; if this is a deliberate new mapping, update this test's own reasoning in its doc comment rather than just relaxing the check", id, f.Stage)
		}
	}

	// test_plan is the one stage tier-1 maps with full coverage of the
	// #522 ruling's mandatory shapes (count, for_each, module-nested) and
	// all three named identity kinds (server-minted, deterministic, none) -
	// pinned so that claim stays true rather than merely plausible at
	// review time.
	shapes := map[string]bool{}
	kinds := map[string]bool{}
	for _, f := range bi.Fixtures {
		if f.Stage != "test_plan" {
			continue
		}
		for _, s := range f.Shapes {
			shapes[s] = true
		}
		kinds[f.IdentityKind] = true
	}
	for _, s := range []string{"count", "for_each", "module-nested"} {
		if !shapes[s] {
			t.Errorf("test_plan's mapped fixtures do not cover mandatory shape %q", s)
		}
	}
	for _, k := range []string{"server-minted", "deterministic", "none"} {
		if !kinds[k] {
			t.Errorf("test_plan's mapped fixtures do not cover identity_kind %q", k)
		}
	}

	// day2_remove, by contrast, is mapped and its two fixtures genuinely
	// exercise the stage (tagging-sweep, record-store both pass) - but it
	// covers only the "scalar" shape (neither fixture is count, for_each,
	// or module-nested) and only two of the three named identity kinds
	// (server-minted via tagging-sweep, none via record-store; no
	// deterministic-identity fixture removes a block today). Both gaps
	// are real and named for the next unit, not papered over here: mapped
	// evidence that a mechanism works is not the same as the
	// representative set the ruling requires to call the stage PROVEN.
	// Asserted directly, not just inferred from the aggregate count below,
	// so a future change that accidentally makes day2_remove "proven"
	// without actually closing either gap (e.g. a shapes/identity_kind
	// typo) is caught here by name.
	var day2RemoveFixtures []BehaviorFixture
	for _, f := range bi.Fixtures {
		if f.Stage == "day2_remove" {
			day2RemoveFixtures = append(day2RemoveFixtures, f)
		}
	}
	if len(day2RemoveFixtures) == 0 {
		t.Fatal("day2_remove has no mapped fixtures; the mapping above is stale")
	}
	for _, f := range day2RemoveFixtures {
		if f.LastRun == nil || f.LastRun.Verdict != VerdictPass {
			t.Fatalf("day2_remove's fixture %q is not passing; the mapping above no longer describes genuine, passing evidence", f.ID)
		}
	}
	if meetsShapeAndIdentityCoverage("day2_remove", day2RemoveFixtures) {
		t.Fatal("day2_remove's mapped fixtures now meet the ruling's shape+identity-kind coverage bar - that is good news (the gap closed), but this test's own commentary above is now stale and must be rewritten, not left claiming a gap that no longer exists")
	}

	proven, total := BehaviorsProven(bi)
	if total != len(Stages()) {
		t.Fatalf("BehaviorsProven total = %d, want %d", total, len(Stages()))
	}
	if proven != 1 {
		t.Fatalf("BehaviorsProven(committed live/behaviors.json) = %d, want 1 (test_plan only - day2_remove is mapped with genuine, passing evidence but does not meet the ruling's shape+identity-kind coverage bar) - if a fixture's last_run now fails, or the mapping or coverage changed, update this pin deliberately rather than silencing it", proven)
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
