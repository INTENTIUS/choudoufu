// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// selectorFixture is six estates across three lanes, two of them core, with
// rows that are clear or not and measured at commits "old" or "new".
func selectorFixture() (*Manifest, *Artifact) {
	m := &Manifest{Estates: []Estate{
		{Name: "k1", Lane: LaneKubernetes, Set: SetGrowing},
		{Name: "k2", Lane: LaneKubernetes, Set: SetGrowing},
		{Name: "k3", Lane: LaneKubernetes, Set: SetCore},
		{Name: "r1", Lane: "reference", Set: SetCore},
		{Name: "r2", Lane: "reference", Set: SetGrowing},
		{Name: "t1", Lane: "terraform-popular", Set: SetGrowing},
	}}
	a := &Artifact{Estates: []EstateResult{
		{Name: "k1", Clear: true, LastRun: &LastRun{Commit: "new"}},
		{Name: "k2", Clear: false, LastRun: &LastRun{Commit: "old"}},
		{Name: "k3", Clear: false, LastRun: &LastRun{Commit: "new"}},
		{Name: "r1", Clear: true, LastRun: &LastRun{Commit: "old"}},
		{Name: "r2", Clear: false},
		{Name: "t1", Clear: true, LastRun: &LastRun{Commit: "new"}},
	}}
	return m, a
}

// fakeAncestor: "new" is measured at or after the ref, "old" is not, and
// anything else is a commit git does not know.
func fakeAncestor(ref, commit string) (bool, error) {
	switch commit {
	case "new":
		return true, nil
	case "old":
		return false, nil
	}
	return false, errors.New("unknown commit")
}

func names(es []Estate) string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return strings.Join(out, " ")
}

// #2037: filters intersect each other, union with names, narrow -set's base,
// and leave -set's meaning alone when nothing is given.
func TestRunSelectors(t *testing.T) {
	m, a := selectorFixture()
	for _, c := range []struct {
		sel  runSelection
		want string
	}{
		{runSelection{Set: "all"}, "k1 k2 k3 r1 r2 t1"},
		{runSelection{Set: "core"}, "k3 r1"},
		{runSelection{Names: []string{"t1", "k1"}, Set: "all"}, "t1 k1"},
		{runSelection{Set: "all", Lane: LaneKubernetes}, "k1 k2 k3"},
		{runSelection{Set: "all", NotClear: true}, "k2 k3 r2"},
		{runSelection{Set: "all", Lane: LaneKubernetes, NotClear: true}, "k2 k3"},
		{runSelection{Set: "all", Stale: "ref"}, "k2 r1 r2"},
		{runSelection{Set: "all", Lane: LaneKubernetes, NotClear: true, Stale: "ref"}, "k2"},
		{runSelection{Set: "core", NotClear: true}, "k3"},
		{runSelection{Names: []string{"t1"}, Set: "all", Lane: LaneKubernetes, NotClear: true}, "t1 k2 k3"},
		{runSelection{Names: []string{"k2"}, Set: "all", Lane: LaneKubernetes, NotClear: true}, "k2 k3"},
	} {
		got, err := selectEstates(m, a, c.sel, fakeAncestor)
		if err != nil {
			t.Errorf("%+v: %v", c.sel, err)
			continue
		}
		if names(got) != c.want {
			t.Errorf("%+v: selected %q, want %q", c.sel, names(got), c.want)
		}
	}
}

// #2037: a selection that matches nothing is refused, never widened to the
// whole set, and an unknown lane or name is an error rather than an empty
// filter.
func TestRunSelectorsNeverWidenAnEmptySelection(t *testing.T) {
	m, a := selectorFixture()
	for _, sel := range []runSelection{
		{Set: "core", Lane: "terraform-popular"},                // t1 is not core
		{Set: "all", Lane: "terraform-popular", NotClear: true}, // t1 is clear
		{Set: "all", Lane: "opentofu-native"},                   // nobody in the lane
		{Set: "all", Lane: "no-such-lane"},                      // not a lane
		{Set: "all", Names: []string{"no-such-estate"}},         // not an estate
	} {
		got, err := selectEstates(m, a, sel, fakeAncestor)
		if err == nil {
			t.Errorf("%+v selected %q; want a refusal", sel, names(got))
		}
	}
	_, err := selectEstates(m, a, runSelection{Set: "all", Lane: "opentofu-native"}, fakeAncestor)
	if err == nil || !strings.Contains(err.Error(), "selection is empty") || !strings.Contains(err.Error(), "-lane opentofu-native") {
		t.Errorf("an empty selection does not say so and what matched nothing: %v", err)
	}
}

// -stale against real history: a row is stale unless the ref is an
// ancestor-or-equal of its last_run.commit. A row measured on a branch that
// never contained the ref is stale; so is one with no commit, and one whose
// commit git has never seen.
func TestStaleSelectorAgainstRealHistory(t *testing.T) {
	root := initTestRepo(t)
	A := commitTestFile(t, root, "f", "a", "A")
	B := commitTestFile(t, root, "f", "b", "B")
	C := commitTestFile(t, root, "f", "c", "C")
	gitCheckout(t, root, A)
	D := commitTestFile(t, root, "g", "d", "D") // a branch off A, never containing B
	m := &Manifest{}
	a := &Artifact{}
	for name, commit := range map[string]string{
		"at-a": A, "at-b": B, "at-c": C, "branch-d": D, "no-commit": "", "unknown": strings.Repeat("e", 40),
	} {
		m.Estates = append(m.Estates, Estate{Name: name, Lane: "reference", Set: SetGrowing})
		a.Estates = append(a.Estates, EstateResult{Name: name, LastRun: &LastRun{Commit: commit}})
	}
	m.Estates = append(m.Estates, Estate{Name: "never-run", Lane: "reference", Set: SetGrowing})
	a.Estates = append(a.Estates, EstateResult{Name: "never-run"})

	got, err := selectEstates(m, a, runSelection{Set: "all", Stale: B}, gitAncestor(root))
	if err != nil {
		t.Fatal(err)
	}
	stale := map[string]bool{}
	for _, e := range got {
		stale[e.Name] = true
	}
	for name, want := range map[string]bool{
		"at-a": true, "at-b": false, "at-c": false, "branch-d": true, "no-commit": true, "unknown": true, "never-run": true,
	} {
		if stale[name] != want {
			t.Errorf("-stale B: %s selected=%v, want %v", name, stale[name], want)
		}
	}
}

// #2037: -parallel auto's arithmetic, with docker's numbers injected.
func TestAutoParallelArithmetic(t *testing.T) {
	const gb = 1 << 30
	for _, c := range []struct {
		name        string
		c           dockerCapacity
		kind, total int
		want        int
		why         string
	}{
		{"kind: 16 GiB free over 2.5 GiB is 6", dockerCapacity{MemTotal: 16 * gb, NCPU: 16}, 21, 21, 6, "per kind estate = 6"},
		{"in-use memory comes off the top", dockerCapacity{MemTotal: 16 * gb, InUse: 6 * gb, NCPU: 16}, 21, 21, 4, "6.0 GiB in use"},
		{"floci only: 0.5 GiB each, capped by CPUs", dockerCapacity{MemTotal: 16 * gb, NCPU: 8}, 0, 21, 8, "capped by 8 CPUs"},
		{"capped by the number of estates", dockerCapacity{MemTotal: 64 * gb, NCPU: 32}, 1, 3, 3, "capped by 3 estate(s)"},
		{"a mixed selection sizes for its kind estates", dockerCapacity{MemTotal: 8 * gb, NCPU: 16}, 1, 10, 3, "per kind estate"},
		{"never below 1", dockerCapacity{MemTotal: 2 * gb, InUse: 3 * gb, NCPU: 4}, 5, 5, 1, "minimum of 1"},
	} {
		got, why := autoParallel(c.c, c.kind, c.total)
		if got != c.want || !strings.Contains(why, c.why) {
			t.Errorf("%s: got %d (%s), want %d mentioning %q", c.name, got, why, c.want, c.why)
		}
	}
}

// #2037: an explicit -parallel wins without asking docker, and a docker
// that cannot be read leaves auto at 1 and says why.
func TestPlanRunParallelSources(t *testing.T) {
	m, a := selectorFixture()
	root := t.TempDir()
	sel := runSelection{Set: "all", Lane: LaneKubernetes}
	_, n, why, err := planRun(root, m, a, sel, "5", func() (dockerCapacity, error) {
		t.Fatal("an explicit -parallel asked docker anyway")
		return dockerCapacity{}, nil
	})
	if err != nil || n != 5 || !strings.Contains(why, "-parallel") {
		t.Errorf("-parallel 5: n=%d why=%q err=%v", n, why, err)
	}
	_, n, why, err = planRun(root, m, a, sel, "auto", func() (dockerCapacity, error) {
		return dockerCapacity{}, errors.New("Cannot connect to the Docker daemon")
	})
	if err != nil || n != 1 || !strings.Contains(why, "fell back to 1") || !strings.Contains(why, "Cannot connect") {
		t.Errorf("docker down: n=%d why=%q err=%v", n, why, err)
	}
	_, n, _, err = planRun(root, m, a, sel, "auto", func() (dockerCapacity, error) {
		return dockerCapacity{MemTotal: 16 << 30, NCPU: 16}, nil
	})
	if err != nil || n != 3 {
		t.Errorf("auto over 3 kubernetes estates with room for 6: n=%d err=%v, want 3", n, err)
	}
	for _, bad := range []string{"0", "-2", "many", ""} {
		if _, _, _, err := planRun(root, m, a, sel, bad, nil); err == nil {
			t.Errorf("-parallel %q accepted", bad)
		}
	}
}

func TestUsesKindReadsTheScript(t *testing.T) {
	root := t.TempDir()
	writeFakeEstate(t, root, "floci-only", "echo floci\n")
	writeFakeEstate(t, root, "calls-kind", "gauntlet_kind_up a\n")
	for _, c := range []struct {
		e    Estate
		want bool
	}{
		{Estate{Name: "floci-only", Lane: "reference"}, false},
		{Estate{Name: "calls-kind", Lane: "reference"}, true},
		{Estate{Name: "floci-only", Lane: LaneKubernetes}, true},
		{Estate{Name: "floci-only", Lane: "reference", SubstrateOverride: SubstrateFlociEKS}, true},
	} {
		if got := usesKind(root, c.e); got != c.want {
			t.Errorf("%+v: usesKind=%v, want %v", c.e, got, c.want)
		}
	}
}

func TestDockerMemUsageParsing(t *testing.T) {
	got := sumMemUsage("1.5GiB / 7.6GiB\n512MiB / 7.6GiB\n0B / 0B\n800kB / 7.6GiB\ngarbage\n")
	want := int64(1.5*(1<<30)) + 512*(1<<20) + 800000
	if got != want {
		t.Errorf("sumMemUsage = %d, want %d", got, want)
	}
}

// #2037: longest-processing-time scheduling over the slots.
func TestEstimateWall(t *testing.T) {
	for _, c := range []struct {
		d     []float64
		slots int
		want  float64
	}{
		{[]float64{10, 10, 10}, 2, 20},
		{[]float64{30, 20, 10, 10}, 2, 40},
		{[]float64{30, 20, 10, 10}, 8, 30},
		{[]float64{5, 5, 5, 5}, 1, 20},
		{nil, 3, 0},
	} {
		if got := estimateWall(c.d, c.slots); got != c.want {
			t.Errorf("estimateWall(%v, %d) = %v, want %v", c.d, c.slots, got, c.want)
		}
	}
}

// The plan names every estate, says which have no timing, and gives the
// parallel count with its reason and an estimate from the recorded ones.
func TestPrintRunPlan(t *testing.T) {
	a := &Artifact{Estates: []EstateResult{
		{Name: "timed-a", LastRun: &LastRun{DurationS: 3600}},
		{Name: "timed-b", LastRun: &LastRun{DurationS: 1800}},
		{Name: "untimed"},
	}}
	sel := []Estate{{Name: "timed-a", Lane: LaneKubernetes}, {Name: "timed-b", Lane: LaneKubernetes}, {Name: "untimed", Lane: LaneKubernetes}}
	var out bytes.Buffer
	printRunPlan(&out, sel, a, 2, "auto: because")
	s := out.String()
	for _, want := range []string{"3 estate(s)", "timed-a", "last run 1h0m0s", "untimed", "no timing recorded", "parallel: 2 (auto: because)", "estimated wall time: 1h0m0s over 2 slot(s)", "2 of 3", "not counted: untimed"} {
		if !strings.Contains(s, want) {
			t.Errorf("plan does not say %q:\n%s", want, s)
		}
	}
	out.Reset()
	printRunPlan(&out, sel[2:], a, 1, "set by -parallel")
	if !strings.Contains(out.String(), "no timing recorded for any selected estate") {
		t.Errorf("an all-untimed plan does not say so:\n%s", out.String())
	}
}

// #2037: the boundary lines parse (and record nothing), and a run stamps
// each stage's seconds from its verdict line into stage_runs while the
// estate's wall seconds land in last_run.duration_s.
func TestStageDurationsAreRecorded(t *testing.T) {
	res, err := ParseProtocol(strings.NewReader("GAUNTLET protocol=1\nGAUNTLET begin=cold_deploy\nGAUNTLET stage=cold_deploy verdict=pass duration_s=12\nGAUNTLET begin=migrate\nGAUNTLET end_stage=migrate\n"))
	if err != nil {
		t.Fatalf("boundary lines do not parse: %v", err)
	}
	if len(res.Stages) != 1 || res.Stages["migrate"] != "" {
		t.Errorf("a boundary line recorded a verdict: %v", res.Stages)
	}

	root := t.TempDir()
	writeFakeEstate(t, root, "timed", "printf 'GAUNTLET protocol=1\\nGAUNTLET begin=cold_deploy\\nGAUNTLET stage=cold_deploy verdict=pass duration_s=12\\nGAUNTLET stage=migrate verdict=fail duration_s=3.5\\n'\nsleep 0.2\n")
	m := &Manifest{Estates: []Estate{{Name: "timed", Lane: "reference", Set: SetGrowing}}}
	a := &Artifact{Schema: 1}
	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"timed"}, Stdout: &out}, "c", "e"); err != nil {
		t.Fatal(err)
	}
	r, _ := a.Result("timed")
	if r.StageRuns["cold_deploy"].Seconds != 12 || r.StageRuns["migrate"].Seconds != 3.5 {
		t.Errorf("stage_runs seconds = %+v, want cold_deploy 12 and migrate 3.5", r.StageRuns)
	}
	if r.LastRun.DurationS < 0.2 {
		t.Errorf("last_run.duration_s = %v, want the estate's wall time (>= 0.2s)", r.LastRun.DurationS)
	}
	b, _ := json.Marshal(r.StageRuns["cold_deploy"])
	if !strings.Contains(string(b), `"seconds":12`) {
		t.Errorf("stage_runs.cold_deploy serializes as %s, want a seconds field", b)
	}
}

// The library prints the boundary lines: begin always, end_stage only for an
// open stage, and a verdict line closes a stage without a second end_stage.
func TestLibraryPrintsStageBoundaries(t *testing.T) {
	repo, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(repo, "live", "e2e", "lib", "gauntlet.sh")
	out, err := runBash(`source "` + lib + `"; gauntlet_begin; gauntlet_begin_stage cold_deploy; gauntlet_stage cold_deploy pass; gauntlet_end_stage; gauntlet_begin_stage migrate; gauntlet_end_stage; echo "CURRENT=[$CURRENT_STAGE]"`)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var protocol []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "GAUNTLET ") {
			protocol = append(protocol, strings.Fields(l)[1])
		}
	}
	want := "protocol=1 begin=cold_deploy stage=cold_deploy begin=migrate end_stage=migrate"
	if got := strings.Join(protocol, " "); got != want {
		t.Errorf("library printed %q, want %q\n%s", got, want, out)
	}
	if !strings.Contains(string(out), "CURRENT=[]") {
		t.Errorf("CURRENT_STAGE not cleared:\n%s", out)
	}
}

// #2037: stage lines arrive as the script writes them - split across writes
// and with or without a begin line - and become progress lines and a
// progress file `gauntlet status` prints.
func TestProgressFromStageLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-1.json")
	var out bytes.Buffer
	clock := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	p := newRunProgress(&out, path, "/repo", 2, 2)
	p.now = func() time.Time { return clock }
	p.file.Started = clock
	tick := func(d time.Duration) { clock = clock.Add(d) }

	p.estateStarted("k1")
	w := &stageWatcher{p: p, estate: "k1"}
	tick(time.Minute)
	_, _ = w.Write([]byte("noise\nGAUNTLET begin=cold_"))
	_, _ = w.Write([]byte("deploy\n"))
	tick(5 * time.Minute)

	// Mid-stage: the file says k1 is in cold_deploy.
	var f ProgressFile
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Running) != 1 || f.Running[0].Stage != "cold_deploy" || f.Done != 0 || f.Total != 2 {
		t.Fatalf("progress file mid-stage = %+v", f)
	}
	var status bytes.Buffer
	printStatus(&status, path, f, clock, true)
	for _, want := range []string{"0/2 estate(s) done", "k1", "cold_deploy for 5m0s", "estate running 6m0s"} {
		if !strings.Contains(status.String(), want) {
			t.Errorf("status does not say %q:\n%s", want, status.String())
		}
	}
	status.Reset()
	printStatus(&status, path, f, clock, false)
	if !strings.Contains(status.String(), "not running") {
		t.Errorf("status of a dead runner does not say so:\n%s", status.String())
	}

	_, _ = w.Write([]byte("GAUNTLET stage=cold_deploy verdict=pass duration_s=300\r\n"))
	tick(2 * time.Minute)
	_, _ = w.Write([]byte("GAUNTLET stage=migrate verdict=fail duration_s=120\n")) // no begin line
	p.estateFinished("k1", "finished, exit 1,")
	p.end("finished")

	lines := out.String()
	for _, want := range []string{
		"[0s, 0/2 done] k1: started",
		"[1m0s, 0/2 done] k1: cold_deploy started (estate running 1m0s)",
		"[6m0s, 0/2 done] k1: cold_deploy pass after 5m0s",
		"[8m0s, 0/2 done] k1: migrate fail after 2m0s",
		"[8m0s, 1/2 done] k1: finished, exit 1, in 8m0s",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("progress lines do not include %q:\n%s", want, lines)
		}
	}
	b, _ = os.ReadFile(path)
	_ = json.Unmarshal(b, &f)
	if f.State != "finished" || f.Done != 1 || len(f.Running) != 0 || len(f.Finished) != 1 {
		t.Errorf("final progress file = %+v", f)
	}

	// cmdStatus reads a named file without prompting for anything.
	status.Reset()
	if err := cmdStatus([]string{path}, &status); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status.String(), "1/2 estate(s) done") {
		t.Errorf("gauntlet status on the final file:\n%s", status.String())
	}
}

// The runner wires the watcher in: a real script's stage lines reach the
// progress file through RunEstates, and the file is outside the checkout.
func TestRunEstatesReportsProgress(t *testing.T) {
	root := t.TempDir()
	writeFakeEstate(t, root, "p1", passingScript)
	writeFakeEstate(t, root, "p2", "printf 'GAUNTLET protocol=1\\nGAUNTLET begin=cold_deploy\\nGAUNTLET stage=cold_deploy verdict=pass duration_s=0\\n'\n")
	m := &Manifest{Estates: []Estate{{Name: "p1", Lane: "reference", Set: SetGrowing}, {Name: "p2", Lane: "reference", Set: SetGrowing}}}
	a := &Artifact{Schema: 1}
	path := filepath.Join(t.TempDir(), "run.json")
	var out bytes.Buffer
	p := newRunProgress(&out, path, root, 2, 2)
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"p1", "p2"}, Parallel: 2, Stdout: &out, progress: p}, "c", "e"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"p2: cold_deploy started", "p2: cold_deploy pass after", "p1: cold_deploy pass after", "2/2 done] p"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("run output does not include %q:\n%s", want, out.String())
		}
	}
	if rel, err := filepath.Rel(root, progressPath(os.Getpid())); err == nil && !strings.HasPrefix(rel, "..") {
		t.Errorf("the progress file %s is inside the checkout", progressPath(os.Getpid()))
	}
}
