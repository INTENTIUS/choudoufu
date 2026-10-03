// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The stall bound must fire before the job timeout (#1741). smoke.sh bounds
// every non-real-AWS scenario (#1457, #1593) so a stall fails by name, with
// the step it was in, instead of being cancelled by GitHub with no verdict
// at all. That only works while the bound is below the job's
// timeout-minutes with room left for the named FAIL to print.
// k8s-records-in-the-cluster had claims.json minutes 20, so a 40-minute
// bound, inside a 40-minute job: a stall there was cancelled before it
// could say where it stalled, exactly the #1457 shape the bound exists to
// end, and nothing checked it.
//
// The margin is what has to happen inside the job besides the stalled run
// itself. Measured on k8s-smoke run 36703292075 (2026-09-30): the job's
// setup took 0.4 minutes before the scenario step, and the scenario arm of
// k8s-records-in-the-cluster, the longest in any smoke matrix, ran 6.7
// minutes before its BREAK=1 control started - a stall in the control fires
// after both. Then smoke.sh's cleanup deletes the cluster. Ten minutes
// covers the three with room.
const stallBoundMarginSecs = 10 * 60

// smokeBoundFormula is smoke.sh's scenario_bound_secs, which the guard
// reproduces below. If the formula changes, this line stops matching and
// the guard says so rather than checking an old rule.
const smokeBoundFormula = `print(max(600, 2 * 60 * max(minutes + [0])))`

var stallBoundWorkflows = []string{
	"../.github/workflows/bucket-smoke.yml",
	"../.github/workflows/claims-smoke.yml",
	"../.github/workflows/k8s-smoke.yml",
}

// smokeClaimCell is what the bound reads off claims.json for one scenario.
type smokeClaimCell struct {
	minutes []int
	realAWS bool
}

func smokeClaimCells(t *testing.T) map[string]smokeClaimCell {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("smoke", "claims.json"))
	if err != nil {
		t.Fatal(err)
	}
	type proof struct {
		Scenario    string `json:"scenario"`
		Minutes     int    `json:"minutes"`
		RealService bool   `json:"real_service"`
	}
	var doc struct {
		Claims []struct {
			Providers map[string]struct {
				Proofs []proof `json:"proofs"`
			} `json:"providers"`
		} `json:"claims"`
		Demos []proof `json:"demos"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]smokeClaimCell{}
	add := func(p proof) {
		if p.Scenario == "" {
			return
		}
		name := strings.TrimSuffix(filepath.Base(p.Scenario), ".sh")
		e := out[name]
		e.minutes = append(e.minutes, p.Minutes)
		e.realAWS = e.realAWS || p.RealService
		out[name] = e
	}
	for _, c := range doc.Claims {
		for _, cell := range c.Providers {
			for _, p := range cell.Proofs {
				add(p)
			}
		}
	}
	for _, d := range doc.Demos {
		add(d)
	}
	return out
}

// smokeBoundSecs is scenario_bound_secs: SMOKE_TIMEOUT_SECS when set,
// otherwise twice the claim's minutes with a floor of ten minutes.
func smokeBoundSecs(cell smokeClaimCell, override string) (int, error) {
	if override != "" {
		return strconv.Atoi(override)
	}
	m := 0
	for _, x := range cell.minutes {
		if x > m {
			m = x
		}
	}
	return max(600, 2*60*m), nil
}

// stallBoundFindings checks one smoke workflow: every job whose steps run
// live/smoke/ci-run.sh over a matrix of scenarios must have a
// timeout-minutes that holds each bounded scenario's bound plus the margin.
func stallBoundFindings(name string, src []byte, cells map[string]smokeClaimCell) (findings []string, checked int) {
	var wf struct {
		Env  map[string]string
		Jobs map[string]struct {
			TimeoutMinutes int `yaml:"timeout-minutes"`
			Env            map[string]string
			Strategy       struct {
				Matrix struct {
					Scenario []string
				}
			}
			Steps []struct {
				Run string
				Env map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(src, &wf); err != nil {
		return []string{fmt.Sprintf("%s does not parse: %v", name, err)}, 0
	}
	jobs := make([]string, 0, len(wf.Jobs))
	for j := range wf.Jobs {
		jobs = append(jobs, j)
	}
	sort.Strings(jobs)
	for _, jn := range jobs {
		job := wf.Jobs[jn]
		override := ""
		runsScenarios := false
		for _, m := range []map[string]string{wf.Env, job.Env} {
			if v, ok := m["SMOKE_TIMEOUT_SECS"]; ok {
				override = v
			}
		}
		for _, st := range job.Steps {
			if !strings.Contains(st.Run, "live/smoke/ci-run.sh") && !strings.Contains(st.Run, "live/smoke/smoke.sh") {
				continue
			}
			runsScenarios = true
			// A step's own override replaces the job's for that step; the
			// largest one bounds the job.
			if v, ok := st.Env["SMOKE_TIMEOUT_SECS"]; ok {
				a, _ := strconv.Atoi(v)
				b, _ := strconv.Atoi(override)
				if override == "" || a > b {
					override = v
				}
			}
		}
		if !runsScenarios {
			continue
		}
		if len(job.Strategy.Matrix.Scenario) == 0 {
			findings = append(findings, fmt.Sprintf("%s job %s runs smoke scenarios but has no matrix.scenario list the guard can read", name, jn))
			continue
		}
		if job.TimeoutMinutes == 0 {
			findings = append(findings, fmt.Sprintf("%s job %s runs smoke scenarios with no timeout-minutes, so GitHub's six-hour default is the only bound on the job", name, jn))
			continue
		}
		for _, sc := range job.Strategy.Matrix.Scenario {
			cell := cells[sc]
			if cell.realAWS {
				continue // unbounded by design (#1593)
			}
			bound, err := smokeBoundSecs(cell, override)
			if err != nil {
				findings = append(findings, fmt.Sprintf("%s job %s: SMOKE_TIMEOUT_SECS %q is not a number", name, jn, override))
				continue
			}
			checked++
			if bound+stallBoundMarginSecs > job.TimeoutMinutes*60 {
				findings = append(findings, fmt.Sprintf("%s job %s: scenario %s's stall bound is %d minutes and the job's timeout-minutes is %d; the job needs at least %d (bound plus a %d-minute margin for setup, the arm before it, and cleanup), or GitHub cancels a stall before smoke.sh prints its named FAIL (#1457, #1741)",
					name, jn, sc, bound/60, job.TimeoutMinutes, (bound+stallBoundMarginSecs+59)/60, stallBoundMarginSecs/60))
			}
		}
	}
	return findings, checked
}

func TestEverySmokeStallBoundFiresBeforeItsJobTimeout(t *testing.T) {
	sh, err := os.ReadFile(filepath.Join("smoke", "smoke.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sh), smokeBoundFormula) {
		t.Fatalf("live/smoke/smoke.sh's scenario_bound_secs no longer computes %s; update smokeBoundSecs here to the new rule before trusting this guard", smokeBoundFormula)
	}
	cells := smokeClaimCells(t)
	total := 0
	for _, wf := range stallBoundWorkflows {
		src, err := os.ReadFile(wf)
		if err != nil {
			t.Fatal(err)
		}
		fs, n := stallBoundFindings(filepath.Base(wf), src, cells)
		for _, f := range fs {
			t.Error(f)
		}
		if n == 0 {
			t.Errorf("%s: no bounded scenario found; this guard is reading nothing there", wf)
		}
		total += n
	}
	t.Logf("checked %d scenario bound(s) against their job timeouts", total)
}

// TestStallBoundFindingsIsRedOnIssue1741sCase feeds the rule the case #1741
// found: a 20-minute claim (a 40-minute bound) in a 40-minute job.
func TestStallBoundFindingsIsRedOnIssue1741sCase(t *testing.T) {
	const wf = `
jobs:
  smoke:
    timeout-minutes: 40
    strategy:
      matrix:
        scenario:
          - k8s-records-in-the-cluster
    steps:
      - run: bash live/smoke/ci-run.sh ${{ matrix.scenario }} "$RUNNER_TEMP/smoke-logs/scenario.log"
`
	cells := map[string]smokeClaimCell{"k8s-records-in-the-cluster": {minutes: []int{20}}}
	got, n := stallBoundFindings("fixture.yml", []byte(wf), cells)
	if n != 1 || len(got) != 1 {
		t.Fatalf("want one finding over one scenario, got %d over %d: %v", len(got), n, got)
	}
	t.Logf("red, as it must be: %s", got[0])

	ok := strings.Replace(wf, "timeout-minutes: 40", "timeout-minutes: 50", 1)
	if got, _ := stallBoundFindings("fixture.yml", []byte(ok), cells); len(got) != 0 {
		t.Errorf("a 50-minute job over a 40-minute bound should pass: %v", got)
	}
	override := strings.Replace(wf, "    steps:", "    env:\n      SMOKE_TIMEOUT_SECS: \"3600\"\n    steps:", 1)
	override = strings.Replace(override, "timeout-minutes: 40", "timeout-minutes: 65", 1)
	if got, _ := stallBoundFindings("fixture.yml", []byte(override), cells); len(got) != 1 {
		t.Errorf("SMOKE_TIMEOUT_SECS=3600 in a 65-minute job should be one finding: %v", got)
	}
}
