// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package waves

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeRunner plans each root as its approved plan unless told otherwise,
// and records every plan and apply.
type fakeRunner struct {
	approved  map[string]RootPlan
	moved     map[string]bool   // root -> fresh plan differs
	planFail  map[string]string // root -> fresh plan error
	applyFail map[string]string // root -> apply error
	planned   [][]string
	applied   []string
}

func (f *fakeRunner) PlanRoots(_ context.Context, roots []string) (map[string]FreshPlan, error) {
	f.planned = append(f.planned, append([]string(nil), roots...))
	out := map[string]FreshPlan{}
	for _, r := range roots {
		p := f.approved[r]
		if f.moved[r] {
			p.Plan = json.RawMessage(`{"resource_changes":[{"address":"aws_sqs_queue.q","change":{"actions":["update"],"after":{"name":"moved"}}}]}`)
		}
		if e, ok := f.planFail[r]; ok {
			p = RootPlan{Root: r, Status: "failed", Error: e}
		}
		out[r] = FreshPlan{RootPlan: p, PlanFile: "fresh/" + r + ".tfplan"}
	}
	return out, nil
}

func (f *fakeRunner) Apply(_ context.Context, root, planFile string) error {
	f.applied = append(f.applied, root)
	if planFile != "fresh/"+root+".tfplan" {
		return fmt.Errorf("applied %s, not the fresh plan file", planFile)
	}
	if e, ok := f.applyFail[root]; ok {
		return errors.New(e)
	}
	return nil
}

// applyFixture is the chain at N=5 with e01, e02 and e04 as canaries: wave
// 1 is e01 then e02 (e02 reads e01) beside e04, which reads nothing; wave 2
// is e03 (reads e02) and e05.
func applyFixture(t *testing.T) (*SetDocument, *Document, string) {
	t.Helper()
	set := &SetDocument{}
	for _, r := range chain() {
		set.Roots = append(set.Roots, RootPlan{
			Root: r.Root, Estate: r.Estate, Status: StatusPlanned,
			Plan: json.RawMessage(`{"resource_changes":[{"address":"aws_sqs_queue.q","change":{"actions":["create"],"after":{"name":"` + r.Estate + `"}}}]}`),
		})
	}
	w, err := Split(chain(), []string{"estates/e01", "estates/e02", "estates/e04"})
	if err != nil {
		t.Fatal(err)
	}
	_, digest, err := DocumentDigests(set)
	if err != nil {
		t.Fatal(err)
	}
	return set, &Document{Waves: w.Waves, Edges: w.Edges}, digest
}

type applyRun struct {
	set      *SetDocument
	doc      *Document
	approved string
	resume   *Resume
	saves    int
}

func newApplyRun(t *testing.T) *applyRun {
	set, doc, d := applyFixture(t)
	return &applyRun{set: set, doc: doc, approved: d, resume: &Resume{FormatVersion: ResumeFormatVersion, Roots: []ResumeEntry{}}}
}

func (a *applyRun) runner() *fakeRunner {
	f := &fakeRunner{approved: map[string]RootPlan{}, moved: map[string]bool{}, planFail: map[string]string{}, applyFail: map[string]string{}}
	for _, r := range a.set.Roots {
		f.approved[r.Root] = r
	}
	return f
}

func (a *applyRun) apply(t *testing.T, wave int, f *fakeRunner) *ApplyResult {
	t.Helper()
	res, err := Apply(t.Context(), ApplyOptions{
		Set: a.set, Approved: a.approved, Waves: a.doc, Wave: wave,
		Resume: a.resume, Runner: f,
		Save: func(*Resume) error { a.saves++; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func outcomes(r *Resume) []string {
	var out []string
	for _, e := range r.Roots {
		out = append(out, e.Root+" "+e.Outcome)
	}
	return out
}

func TestApplyWaveInOrder(t *testing.T) {
	a := newApplyRun(t)
	if got := a.doc.Waves[0].Roots; !reflect.DeepEqual(got, []string{"estates/e01", "estates/e02", "estates/e04"}) {
		t.Fatalf("wave 1 is %v", got)
	}
	f := a.runner()
	res := a.apply(t, 1, f)
	if res.ExitCode != ExitApplied {
		t.Fatalf("exit %d: %s", res.ExitCode, res.Error)
	}
	if !reflect.DeepEqual(f.applied, []string{"estates/e01", "estates/e02", "estates/e04"}) {
		t.Errorf("applied %v, want e01 before e02, then e04", f.applied)
	}
	res = a.apply(t, 2, f)
	if res.ExitCode != ExitApplied {
		t.Fatalf("wave 2 exit %d: %s", res.ExitCode, res.Error)
	}
	if want := []string{"estates/e01 landed", "estates/e02 landed", "estates/e03 landed", "estates/e04 landed", "estates/e05 landed"}; !reflect.DeepEqual(outcomes(a.resume), want) {
		t.Errorf("resume %v", outcomes(a.resume))
	}
}

// TestApplyRefusesAMovedSet is #1754's changed-plan criterion: one root's
// fresh plan differs, the wave exits 3 naming it, and nothing in the wave
// is applied, including roots whose plans still match.
func TestApplyRefusesAMovedSet(t *testing.T) {
	a := newApplyRun(t)
	f := a.runner()
	f.moved["estates/e04"] = true
	res := a.apply(t, 1, f)
	if res.ExitCode != ExitSetMoved {
		t.Fatalf("exit %d, want 3: %s", res.ExitCode, res.Error)
	}
	if len(f.applied) != 0 {
		t.Errorf("applied %v in a wave whose set moved", f.applied)
	}
	if len(res.Moved) != 1 || res.Moved[0].Root != "estates/e04" || !strings.Contains(res.Error, "estates/e04") {
		t.Errorf("moved %+v, error %q: want estates/e04 named", res.Moved, res.Error)
	}
	if a.saves != 0 || len(a.resume.Roots) != 0 {
		t.Errorf("a refused wave wrote the resume file: %v", outcomes(a.resume))
	}
}

func TestApplyRefusesADocumentThatIsNotTheApprovedSet(t *testing.T) {
	a := newApplyRun(t)
	a.set.Roots[0].Plan = json.RawMessage(`{"resource_changes":[]}`)
	f := a.runner()
	res := a.apply(t, 1, f)
	if res.ExitCode != ExitSetMoved || !strings.Contains(res.Error, "not the approved") {
		t.Fatalf("exit %d: %s", res.ExitCode, res.Error)
	}
	if len(f.planned)+len(f.applied) != 0 {
		t.Errorf("planned %v, applied %v", f.planned, f.applied)
	}
}

// TestApplyFailureSkipsDependents is #1754's failure-handling criterion: a
// root that fails skips its dependents, in its wave and the next, while an
// independent root completes; the resume file holds all three outcomes,
// and a resumed run applies only what is left.
func TestApplyFailureSkipsDependents(t *testing.T) {
	a := newApplyRun(t)
	path := filepath.Join(t.TempDir(), "resume.json")
	save := func(r *Resume) error { a.saves++; return WriteResume(path, r) }

	f := a.runner()
	f.applyFail["estates/e01"] = "Error: creating VPC: RequestLimitExceeded"
	res, err := Apply(t.Context(), ApplyOptions{Set: a.set, Approved: a.approved, Waves: a.doc, Wave: 1, Resume: a.resume, Runner: f, Save: save})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitRootFailed {
		t.Fatalf("exit %d, want 4", res.ExitCode)
	}
	if !reflect.DeepEqual(f.applied, []string{"estates/e01", "estates/e04"}) {
		t.Errorf("applied %v: want e01 tried, e02 not, e04 completed", f.applied)
	}
	onDisk, err := ReadResume(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"estates/e01 failed", "estates/e02 skipped", "estates/e04 landed"}; !reflect.DeepEqual(outcomes(onDisk), want) {
		t.Fatalf("resume file %v, want %v", outcomes(onDisk), want)
	}
	if e, _ := onDisk.outcome("estates/e02"); !strings.Contains(e.Reason, "estates/e01 (wave 1) failed") {
		t.Errorf("skip reason %q", e.Reason)
	}
	if e, _ := onDisk.outcome("estates/e01"); !strings.Contains(e.Reason, "RequestLimitExceeded") {
		t.Errorf("failure reason %q", e.Reason)
	}

	// Wave 2 before wave 1 is fixed: e03 reads e02, which was skipped, so
	// e03 is skipped too; e05 reads nothing and lands.
	f2 := a.runner()
	res = a.apply(t, 2, f2)
	if res.ExitCode != ExitRootFailed || !reflect.DeepEqual(f2.applied, []string{"estates/e05"}) {
		t.Errorf("wave 2: exit %d, applied %v", res.ExitCode, f2.applied)
	}
	if e, _ := a.resume.outcome("estates/e03"); e.Outcome != OutcomeSkipped || !strings.Contains(e.Reason, "estates/e02 (wave 1) skipped") {
		t.Errorf("e03: %+v", e)
	}

	// The resumed run of wave 1, read back from disk: e04 landed and is
	// neither planned nor applied again.
	resumed, err := ReadResume(path)
	if err != nil {
		t.Fatal(err)
	}
	resumed.set(ResumeEntry{Root: "estates/e03", Wave: 2, Outcome: OutcomeSkipped})
	resumed.set(ResumeEntry{Root: "estates/e05", Wave: 2, Outcome: OutcomeLanded})
	a.resume = resumed
	f3 := a.runner()
	res = a.apply(t, 1, f3)
	if res.ExitCode != ExitApplied {
		t.Fatalf("resumed wave 1: exit %d %s %+v", res.ExitCode, res.Error, res.Outcomes)
	}
	if !reflect.DeepEqual(f3.applied, []string{"estates/e01", "estates/e02"}) || !reflect.DeepEqual(f3.planned, [][]string{{"estates/e01", "estates/e02"}}) {
		t.Errorf("resumed run planned %v and applied %v; want only e01 and e02", f3.planned, f3.applied)
	}
	// And wave 2 resumed: only e03 is left.
	f4 := a.runner()
	res = a.apply(t, 2, f4)
	if res.ExitCode != ExitApplied || !reflect.DeepEqual(f4.applied, []string{"estates/e03"}) {
		t.Errorf("resumed wave 2: exit %d, applied %v", res.ExitCode, f4.applied)
	}
}

func TestApplyFreshPlanFailureIsARootFailure(t *testing.T) {
	a := newApplyRun(t)
	f := a.runner()
	f.planFail["estates/e01"] = "Error: no valid credential sources"
	res := a.apply(t, 1, f)
	if res.ExitCode != ExitRootFailed || !reflect.DeepEqual(f.applied, []string{"estates/e04"}) {
		t.Fatalf("exit %d applied %v", res.ExitCode, f.applied)
	}
	if want := []string{"estates/e01 failed", "estates/e02 skipped", "estates/e04 landed"}; !reflect.DeepEqual(outcomes(a.resume), want) {
		t.Errorf("resume %v", outcomes(a.resume))
	}
}

func TestApplyRefusesAnotherSetsResumeFile(t *testing.T) {
	a := newApplyRun(t)
	a.resume.SetDigest = "sha256:other"
	res := a.apply(t, 1, a.runner())
	if res.ExitCode != ExitError || !strings.Contains(res.Error, "resume file belongs to the set sha256:other") {
		t.Fatalf("exit %d: %s", res.ExitCode, res.Error)
	}
}
