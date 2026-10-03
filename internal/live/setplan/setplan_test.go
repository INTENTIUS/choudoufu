// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package setplan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/document.golden.json")

// fakeRunner plans every root in memory. breakAt names, per root base name,
// the stage that fails; changes names the roots whose plan has changes;
// noLive names roots with no live block.
type fakeRunner struct {
	breakAt map[string]Stage
	changes map[string]bool
	noLive  map[string]bool

	// delay holds each stage open long enough for the parallelism bound to
	// be reached.
	delay time.Duration

	mu        sync.Mutex
	running   int
	maxActive int
	inits     []string
}

func (f *fakeRunner) enter() func() {
	f.mu.Lock()
	f.running++
	if f.running > f.maxActive {
		f.maxActive = f.running
	}
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return func() {
		f.mu.Lock()
		f.running--
		f.mu.Unlock()
	}
}

func (f *fakeRunner) fails(dir string, s Stage) error {
	if f.breakAt[filepath.Base(dir)] == s {
		return fmt.Errorf("%s broke at %s on purpose", filepath.Base(dir), s)
	}
	return nil
}

func (f *fakeRunner) Estate(_ context.Context, dir string) (string, bool, error) {
	if err := f.fails(dir, StageEstate); err != nil {
		return "", false, err
	}
	if f.noLive[filepath.Base(dir)] {
		return "", false, nil
	}
	return "est-" + filepath.Base(dir), true, nil
}

func (f *fakeRunner) Init(_ context.Context, dir string, log io.Writer) error {
	defer f.enter()()
	f.mu.Lock()
	f.inits = append(f.inits, filepath.Base(dir))
	f.mu.Unlock()
	fmt.Fprintf(log, "init %s\n", filepath.Base(dir))
	return f.fails(dir, StageInit)
}

func (f *fakeRunner) Plan(_ context.Context, dir, planFile string, log io.Writer) (bool, error) {
	defer f.enter()()
	if err := f.fails(dir, StagePlan); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(planFile), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(planFile, []byte("plan of "+filepath.Base(dir)), 0o644); err != nil {
		return false, err
	}
	return f.changes[filepath.Base(dir)], nil
}

func (f *fakeRunner) Show(_ context.Context, dir, planFile string, log io.Writer) (json.RawMessage, error) {
	if err := f.fails(dir, StageShow); err != nil {
		return nil, err
	}
	if _, err := os.Stat(planFile); err != nil {
		return nil, fmt.Errorf("show was handed a plan file the plan stage never wrote: %w", err)
	}
	action := "no-op"
	if f.changes[filepath.Base(dir)] {
		action = "create"
	}
	return json.RawMessage(fmt.Sprintf(`{"format_version":"1.2","resource_changes":[{"address":"terraform_data.x","change":{"actions":[%q]}}]}`, action)), nil
}

// setup makes roots under a fresh base directory and returns it.
func setup(t *testing.T, roots ...string) string {
	t.Helper()
	base := t.TempDir()
	for _, r := range roots {
		if err := os.MkdirAll(filepath.Join(base, r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func fixedClock() func() time.Time {
	t0 := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return t0 }
}

func run(t *testing.T, base string, runner Runner, parallel int, roots ...string) *Document {
	t.Helper()
	doc, err := Run(context.Background(), Options{
		Roots:          roots,
		BaseDir:        base,
		OutDir:         "out",
		Parallel:       parallel,
		PluginCacheDir: "/shared/plugin-cache",
		Runner:         runner,
		Now:            fixedClock(),
	})
	if err != nil {
		t.Fatalf("Run: %s", err)
	}
	return doc
}

func byRoot(doc *Document) map[string]Root {
	out := map[string]Root{}
	for _, r := range doc.Roots {
		out[r.Root] = r
	}
	return out
}

// TestFailureIsolation is #1752's isolation criterion: a root broken at
// each stage is reported with its own error, and every other root's plan is
// complete.
func TestFailureIsolation(t *testing.T) {
	roots := []string{"estates/a", "estates/bad-init", "estates/c", "estates/bad-plan", "estates/bad-show", "estates/no-live", "estates/e"}
	base := setup(t, roots...)
	f := &fakeRunner{
		breakAt: map[string]Stage{"bad-init": StageInit, "bad-plan": StagePlan, "bad-show": StageShow},
		changes: map[string]bool{"a": true, "c": false, "e": true},
		noLive:  map[string]bool{"no-live": true},
	}
	doc := run(t, base, f, 2, roots...)

	got := byRoot(doc)
	for _, good := range []string{"estates/a", "estates/c", "estates/e"} {
		r := got[good]
		if r.Status != StatusPlanned || r.Error != "" || len(r.Plan) == 0 {
			t.Errorf("%s: status %q error %q plan %d bytes; a broken neighbour must not stop it", good, r.Status, r.Error, len(r.Plan))
		}
		if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(r.PlanFile))); err != nil {
			t.Errorf("%s: plan file %q is not on disk: %s", good, r.PlanFile, err)
		}
	}
	for root, stage := range map[string]Stage{
		"estates/bad-init": StageInit,
		"estates/bad-plan": StagePlan,
		"estates/bad-show": StageShow,
		"estates/no-live":  StageEstate,
	} {
		r := got[root]
		if r.Status != StatusFailed || r.Stage != stage {
			t.Errorf("%s: status %q stage %q, want failed at %q", root, r.Status, r.Stage, stage)
		}
		if r.Error == "" {
			t.Errorf("%s: failed with no error text", root)
		}
		if r.Plan != nil || r.Changes {
			t.Errorf("%s: a failed root carries plan=%s changes=%v", root, r.Plan, r.Changes)
		}
	}
	if !strings.Contains(got["estates/bad-init"].Error, "broke at init on purpose") {
		t.Errorf("bad-init's error is not its own: %q", got["estates/bad-init"].Error)
	}
	if !strings.Contains(got["estates/no-live"].Error, "no live block") {
		t.Errorf("no-live's error does not say why: %q", got["estates/no-live"].Error)
	}
	// A plan file that was written before show failed is still named, so
	// the operator can look at it.
	if got["estates/bad-show"].PlanFile == "" {
		t.Error("bad-show: the plan stage succeeded but its plan file is not named")
	}
	if doc.Summary != (Summary{Roots: 7, Planned: 3, Changed: 2, Failed: 4}) {
		t.Errorf("summary %+v", doc.Summary)
	}
	if doc.ExitCode != ExitRootFailed {
		t.Errorf("exit %d, want %d", doc.ExitCode, ExitRootFailed)
	}
	// Order is the order given, whatever order they finished in.
	for i, r := range doc.Roots {
		if r.Root != roots[i] {
			t.Errorf("roots[%d] = %q, want %q", i, r.Root, roots[i])
		}
	}
}

// TestExitCodes is #1752's exit-code criterion, one case per code.
func TestExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changes map[string]bool
		breakAt map[string]Stage
		want    int
	}{
		{"all clean", nil, nil, ExitClean},
		{"one root changes", map[string]bool{"b": true}, nil, ExitChanges},
		{"one root failed", nil, map[string]Stage{"c": StagePlan}, ExitRootFailed},
		// A failure outranks changes: "some roots were not planned" is the
		// thing a pipeline must not read as "plan ready for review".
		{"changes and a failure", map[string]bool{"a": true}, map[string]Stage{"c": StageInit}, ExitRootFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := setup(t, "a", "b", "c")
			doc := run(t, base, &fakeRunner{changes: tc.changes, breakAt: tc.breakAt}, 3, "a", "b", "c")
			if doc.ExitCode != tc.want {
				t.Errorf("exit %d, want %d", doc.ExitCode, tc.want)
			}
		})
	}
	// The codes are distinct, and 3 is not among them (apply's "plan
	// moved").
	codes := map[int]bool{ExitClean: true, ExitError: true, ExitChanges: true, ExitRootFailed: true}
	if len(codes) != 4 || codes[3] {
		t.Errorf("exit codes are not four distinct values avoiding 3: %v", codes)
	}
}

// TestParallelBound: never more than -parallel-estates roots at once, and
// the bound is actually used.
func TestParallelBound(t *testing.T) {
	roots := []string{"r1", "r2", "r3", "r4", "r5", "r6", "r7"}
	for _, n := range []int{1, 3} {
		base := setup(t, roots...)
		f := &fakeRunner{delay: 20 * time.Millisecond}
		run(t, base, f, n, roots...)
		if f.maxActive != n {
			t.Errorf("-parallel-estates %d: %d stages ran at once", n, f.maxActive)
		}
		if len(f.inits) != len(roots) {
			t.Errorf("-parallel-estates %d: %d inits for %d roots", n, len(f.inits), len(roots))
		}
	}
}

func TestResolveRefuses(t *testing.T) {
	base := setup(t, "a")
	for _, tc := range []struct {
		name  string
		roots []string
		want  string
	}{
		{"none", nil, "no roots"},
		{"twice", []string{"a", "./a/"}, "given twice"},
		{"outside", []string{"../elsewhere"}, "outside the working directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(context.Background(), Options{Roots: tc.roots, BaseDir: base, OutDir: "out", Parallel: 1, Runner: &fakeRunner{}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// TestPlanFilesNamedByRoot: one plan file per root, at OUT/<root>.tfplan,
// and a stale one from an earlier run is never reported as this run's.
func TestPlanFilesNamedByRoot(t *testing.T) {
	base := setup(t, "estates/e01", "estates/e02", "other/e01")
	stale := filepath.Join(base, "out", "estates", "e02.tfplan")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("an earlier run"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := run(t, base, &fakeRunner{breakAt: map[string]Stage{"e02": StagePlan}}, 2, "estates/e01", "estates/e02", "other/e01", ".")
	got := byRoot(doc)
	want := map[string]string{
		"estates/e01": "out/estates/e01.tfplan",
		"other/e01":   "out/other/e01.tfplan",
	}
	for root, file := range want {
		if got[root].PlanFile != file {
			t.Errorf("%s: plan file %q, want %q", root, got[root].PlanFile, file)
		}
	}
	if got["."].PlanFile != "out/_root.tfplan" {
		t.Errorf("the base directory's plan file is %q", got["."].PlanFile)
	}
	if got["estates/e02"].PlanFile != "" {
		t.Errorf("a root whose plan failed names plan file %q", got["estates/e02"].PlanFile)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an earlier run's plan file for a root whose plan failed is still on disk: %v", err)
	}
}

func TestChildEnv(t *testing.T) {
	got := ChildEnv([]string{"HOME=/h", "TF_DATA_DIR=/one", "TF_PLUGIN_CACHE_DIR=/old", "TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=0", "AWS_REGION=us-east-1"}, "/shared")
	want := []string{"HOME=/h", "AWS_REGION=us-east-1", "TF_PLUGIN_CACHE_DIR=/shared", "TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ChildEnv:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestDocumentGolden pins the -json document's shape: field names, order,
// and what a planned and a failed root each carry. #1753's grouped summary
// and #1754's set digest read this document; a change here is a change to
// their input.
func TestDocumentGolden(t *testing.T) {
	base := setup(t, "estates/e01", "estates/e02", "estates/e03")
	doc := run(t, base, &fakeRunner{
		changes: map[string]bool{"e01": true},
		breakAt: map[string]Stage{"e03": StageInit},
	}, 2, "estates/e01", "estates/e02", "estates/e03")
	got, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "document.golden.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the document moved from %s; if the move is intended, run with -update and say why in the PR.\ngot:\n%s", path, got)
	}
}

// TestDocumentContract holds the keys the orchestrator fixed for #1753 on
// every root, planned or failed, independently of the golden file: -update
// can rewrite the golden, but not this list.
func TestDocumentContract(t *testing.T) {
	base := setup(t, "a", "b")
	doc := run(t, base, &fakeRunner{breakAt: map[string]Stage{"b": StagePlan}}, 2, "a", "b")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	var roots []map[string]json.RawMessage
	if err := json.Unmarshal(top["roots"], &roots); err != nil {
		t.Fatalf("top-level \"roots\" is not an array of objects: %s", err)
	}
	if len(roots) != 2 {
		t.Fatalf("%d roots", len(roots))
	}
	for _, r := range roots {
		for _, key := range []string{"root", "estate", "status", "error", "plan"} {
			if _, ok := r[key]; !ok {
				t.Errorf("root %s has no %q key", r["root"], key)
			}
		}
	}
	var planned struct {
		Status string `json:"status"`
		Plan   struct {
			ResourceChanges []json.RawMessage `json:"resource_changes"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(mustMarshal(t, roots[0]), &planned); err != nil {
		t.Fatal(err)
	}
	if planned.Status != "planned" || len(planned.Plan.ResourceChanges) == 0 {
		t.Errorf("a planned root's plan carries no resource_changes: %+v", planned)
	}
	if string(roots[1]["status"]) != `"failed"` || string(roots[1]["plan"]) != "null" || string(roots[1]["error"]) == `""` {
		t.Errorf("a failed root: status %s plan %s error %s", roots[1]["status"], roots[1]["plan"], roots[1]["error"])
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
