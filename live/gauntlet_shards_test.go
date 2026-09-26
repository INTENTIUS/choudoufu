// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Issue #1550: the gauntlet measured every estate and published nothing,
// twice, because one job did both and ran out of time in the measuring
// half. The board is sharded now - one estate per job - and what keeps
// that fix from eroding is structural: the job that publishes must not be
// a job that measures.
//
// Proving it red (both run on 2026-09-23): move the create-pull-request
// step into the matrix job, or take `fail-fast: false` off the matrix, and
// the corresponding check below fails.

type wfStep struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`
}

type wfJob struct {
	Needs    any    `yaml:"needs"`
	If       string `yaml:"if"`
	Strategy struct {
		FailFast *bool          `yaml:"fail-fast"`
		Matrix   map[string]any `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []wfStep `yaml:"steps"`
}

func (j wfJob) needs() []string {
	switch v := j.Needs.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, n := range v {
			if s, ok := n.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func (j wfJob) mentions(what string) bool {
	for _, s := range j.Steps {
		if strings.Contains(s.Run, what) || strings.Contains(s.Uses, what) {
			return true
		}
	}
	return false
}

func loadGauntletWorkflow(t *testing.T) map[string]wfJob {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "gauntlet.yml"))
	if err != nil {
		t.Fatalf("read gauntlet.yml: %v", err)
	}
	var wf struct {
		Jobs map[string]wfJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatalf("parse gauntlet.yml: %v", err)
	}
	if len(wf.Jobs) == 0 {
		t.Fatal("gauntlet.yml declares no jobs; this guard is checking nothing")
	}
	return wf.Jobs
}

func TestGauntletPublishesFromAJobThatMeasuresNothing(t *testing.T) {
	jobs := loadGauntletWorkflow(t)

	var publishers, measurers []string
	for name, j := range jobs {
		if j.mentions("peter-evans/create-pull-request") {
			publishers = append(publishers, name)
		}
		if j.mentions("tools/gauntlet run ") {
			measurers = append(measurers, name)
		}
	}
	if len(publishers) != 1 {
		t.Fatalf("gauntlet.yml has %v jobs opening the verdicts pull request, want exactly one", publishers)
	}
	if len(measurers) != 1 {
		t.Fatalf("gauntlet.yml has %v jobs running estates, want exactly one (the matrix)", measurers)
	}
	publish, measure := publishers[0], measurers[0]
	if publish == measure {
		t.Fatalf("job %q both runs estates and opens the verdicts pull request; that is the shape that measured 27 estates and published nothing (#1550)", publish)
	}

	// The measuring job is the matrix, and one estate's failure must not
	// cancel its siblings' measurements.
	m := jobs[measure]
	if len(m.Strategy.Matrix) == 0 {
		t.Errorf("job %q runs estates without a matrix; the board is one estate per job (#1550)", measure)
	}
	if m.Strategy.FailFast == nil || *m.Strategy.FailFast {
		t.Errorf("job %q does not set fail-fast: false; a failing estate is a row in the artifact, and cancelling its siblings throws away measurements already paid for", measure)
	}

	// The publishing job combines the shards and renders before it opens
	// the pull request, and it waits for the shards.
	p := jobs[publish]
	for _, want := range []string{"tools/gauntlet combine-shards", "tools/gauntlet render"} {
		if !p.mentions(want) {
			t.Errorf("job %q opens the verdicts pull request without running `%s`", publish, want)
		}
	}
	if !containsString(p.needs(), measure) {
		t.Errorf("job %q does not need %q, so it would publish a board with no shards in it", publish, measure)
	}
}

func containsString(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// TestGauntletJobsCheckingNeedsResultUseAStatusFunction: issue #1563.
//
// A job's `if:` that compares `needs.<job>.result` but calls none of
// always()/success()/failure()/cancelled() still gets an implicit
// success() ANDed onto it by GitHub Actions - and job-level success()
// looks at the run's WHOLE transitive dependency graph, not just this
// job's own `needs`, so it comes back false whenever anything upstream was
// skipped. `dispatch-approval` is *always* skipped on the nightly
// `schedule` trigger (it exists only to gate a manual `workflow_dispatch`),
// so every job downstream of `plan` that checked `needs.plan.result`
// without also calling a status function was silently skipped every
// night: `estate` and `acceptance` (zero shards ever uploaded, so
// `collect`'s "Combine the shards" step had nothing to combine and died on
// a bare directory read - runs 35975528292, 36115165483, 36230331089).
// `plan` and `collect` already called always() for the same
// needs.dispatch-approval / needs.plan check and ran fine every night;
// `estate` and `acceptance` did not, and that is the entire difference.
//
// Red before #1563's fix: comment out `always() && ` in either job's `if:`
// in gauntlet.yml and this fails, naming the job.
func TestGauntletJobsCheckingNeedsResultUseAStatusFunction(t *testing.T) {
	jobs := loadGauntletWorkflow(t)
	statusFuncs := []string{"always()", "success()", "failure()", "cancelled()"}

	for name, j := range jobs {
		if j.If == "" || !strings.Contains(j.If, "needs.") || !strings.Contains(j.If, ".result") {
			continue
		}
		hasStatusFunc := false
		for _, f := range statusFuncs {
			if strings.Contains(j.If, f) {
				hasStatusFunc = true
				break
			}
		}
		if !hasStatusFunc {
			t.Errorf("job %q's if condition (%q) compares needs.<job>.result without calling always()/success()/failure()/cancelled(); GitHub ANDs an implicit success() onto it, which is false whenever ANYTHING upstream was skipped - not just this job's own needs - and dispatch-approval is always skipped on the nightly schedule (#1563)", name, j.If)
		}
	}
}
