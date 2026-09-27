// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The rest of the AWS claims' CI story (GitHub issue #1590, part of
// #1579). live/smoke/claims.json stated 23 proven, non-real-AWS AWS cells
// that no workflow ran: claims 1, 2, 3, 5-20, 40, 41, 42, 43. Claim 4 and
// 33-38 are real_aws and stay maintainer-run; claims 21-27 and 39 are
// Kubernetes-only and run in k8s-smoke.yml; claims 28-32 and 44 already run
// in bucket-smoke.yml and are not duplicated here. This file holds
// claims-smoke.yml's two matrices - smoke (every PR) and smoke-nightly (the
// scenarios over budget) - to the claim set that is left, so a claim added
// later lands with a matrix entry, a nightly-only entry, or a filed finding,
// never a silent "proven" nobody re-checks.
//
// Proving it red: add a proven, non-real-AWS AWS claim scenario with no
// matrix entry and no exclusion entry, move a scenario from one matrix to
// the other without moving its slug, or remove the BREAK step from either
// job; each fails a different check below.
const claimsSmokeWorkflow = "../.github/workflows/claims-smoke.yml"

// claimsSmokeExcluded is a proven, non-real-AWS AWS cell that measured
// false in CI while this file's guard was being built (#1590), filed as its
// own finding rather than folded into that issue or silently left out. An
// entry here must not appear in either matrix below; removing an entry
// without adding its scenario back to one of the two matrices is how its
// fix ships.
var claimsSmokeExcluded = map[string]string{
	// #1636: the BREAK control's own denied() check greps the whole apply
	// transcript, and matches an unrelated sweep warning that also
	// contains the string "AccessDenied" - a false positive, not a
	// regression in the tag boundary the claim proves.
	"the-tag-is-the-boundary": "#1636",
	// #1637: fails at the first apply, both arms, on #950's node-path
	// unmarked-apply refusal, which fires before any record can exist for
	// this claim's untaggable, record-recoverable resource to be exempted
	// by.
	"record-only-survives-cache-loss": "#1637",
}

// claimsSmokeNightlyOnly is a proven, non-real-AWS AWS cell measured over
// the 10-minute (both arms) budget the workflow's header states, so it runs
// in smoke-nightly rather than on every pull request. Moving a slug out of
// this map without moving it into the smoke job's matrix (or vice versa)
// fails TestClaimsSmokesRunInCIWithTheirControls.
var claimsSmokeNightlyOnly = map[string]bool{
	// Measured at 9m21s + 6m18s = 15m39s combined, against the pinned
	// floci emulator - three times the next heaviest candidate
	// (carve-by-retag, 5m26s).
	"no-secret-survives-in-what-the-tool-keeps": true,
}

// awsEmulatorClaimCells is every (claim, aws) cell that is proven, not
// real_aws, and not already covered by bucket-smoke.yml's own derivation
// (GitHub issue #1379) - the population claims-smoke.yml's two jobs
// together must equal, once claimsSmokeExcluded is set aside.
func awsEmulatorClaimCells(t *testing.T) []smokeScenarioCell {
	t.Helper()
	f := readSmokeClaims(t)
	inBucket := map[string]bool{}
	for _, name := range bucketSmokeScenarios(t) {
		inBucket[name] = true
	}
	var out []smokeScenarioCell
	for _, c := range smokeScenarioCells(f) {
		if c.Provider != "aws" {
			continue
		}
		if c.Cell.Status != "proven" || c.Cell.RealService {
			continue
		}
		if inBucket[c.Name] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// claimsSmokeJobMatrix extracts one job's `scenario:` matrix list from the
// workflow, the same way bucket_ci_test.go's own cut works: from
// "scenario:\n" up to the job's "steps:" line, so a name in a comment or a
// path filter is never mistaken for one the job runs.
func claimsSmokeJobMatrix(t *testing.T, wf, job string) []string {
	t.Helper()
	jobHeader := "\n  " + job + ":\n"
	start := strings.Index(wf, jobHeader)
	if start < 0 {
		t.Fatalf("%s has no top-level job %q", claimsSmokeWorkflow, job)
	}
	rest := wf[start+len(jobHeader):]
	// Bound the job's own body at the next top-level (two-space-indented)
	// key, so job "smoke"'s cut cannot run on into "smoke-nightly"'s body.
	if next := nextTopLevelJobKey.FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	_, matrix, found := strings.Cut(rest, "scenario:\n")
	if !found {
		t.Fatalf("job %q in %s has no `scenario:` matrix", job, claimsSmokeWorkflow)
	}
	if end := strings.Index(matrix, "    steps:"); end >= 0 {
		matrix = matrix[:end]
	}
	var names []string
	for _, m := range bucketMatrixEntry.FindAllStringSubmatch(matrix, -1) {
		names = append(names, m[1])
	}
	sort.Strings(names)
	return names
}

func TestClaimsSmokesRunInCIWithTheirControls(t *testing.T) {
	raw, err := os.ReadFile(claimsSmokeWorkflow)
	if err != nil {
		t.Fatalf("read %s: %v", claimsSmokeWorkflow, err)
	}
	wf := string(raw)

	cells := awsEmulatorClaimCells(t)
	if len(cells) < 15 {
		t.Fatalf("found %d proven, non-real-AWS AWS cells not already in bucket-smoke.yml; expected at least 15 (claims.json or bucketSmokeScenarios's derivation moved and this guard is looking in the wrong place)", len(cells))
	}

	var wantPR, wantNightly []string
	for _, c := range cells {
		if _, excluded := claimsSmokeExcluded[c.Name]; excluded {
			continue
		}
		if claimsSmokeNightlyOnly[c.Name] {
			wantNightly = append(wantNightly, c.Name)
		} else {
			wantPR = append(wantPR, c.Name)
		}
	}
	sort.Strings(wantPR)
	sort.Strings(wantNightly)

	inPR := claimsSmokeJobMatrix(t, wf, "smoke")
	inNightly := claimsSmokeJobMatrix(t, wf, "smoke-nightly")

	if strings.Join(wantPR, ",") != strings.Join(inPR, ",") {
		t.Errorf("claims-smoke.yml's smoke job matrix is %v; the proven AWS cells that belong there are %v. A claim no job runs is a claim whose \"proven\" cell is one laptop's word.", inPR, wantPR)
	}
	if strings.Join(wantNightly, ",") != strings.Join(inNightly, ",") {
		t.Errorf("claims-smoke.yml's smoke-nightly job matrix is %v; the over-budget AWS cells are %v.", inNightly, wantNightly)
	}

	// Every excluded finding must still be named in the file, so the
	// exclusion is documented rather than a scenario that just quietly
	// never appears anywhere.
	for name, issue := range claimsSmokeExcluded {
		found := false
		for _, c := range cells {
			if c.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("claimsSmokeExcluded names %q, which is no longer a proven, non-real-AWS AWS cell outside bucket-smoke.yml; remove the stale exclusion", name)
			continue
		}
		if !strings.Contains(wf, issue) {
			t.Errorf("claims-smoke.yml never mentions %s, the finding %q is excluded for; an exclusion with no citation is one nobody can follow up on", issue, name)
		}
		if strings.Contains(strings.Join(inPR, ","), name) || strings.Contains(strings.Join(inNightly, ","), name) {
			t.Errorf("%q is in claimsSmokeExcluded (%s) and also in a matrix; it must be in exactly one", name, issue)
		}
	}

	if strings.Count(wf, "BREAK: \"1\"") < 2 {
		t.Errorf("claims-smoke.yml should run a BREAK=1 control in both the smoke and smoke-nightly jobs; a scenario whose failure is never demonstrated is scenery")
	}
	// Through the wrapper that reads the verdict line (#1439), and
	// live/smoke_verdict_test.go holds every scenario-running step to it.
	if strings.Count(wf, "ci-run.sh ${{ matrix.scenario }}") < 4 {
		t.Errorf("claims-smoke.yml does not run live/smoke/ci-run.sh for each matrix entry in both jobs (scenario + BREAK, twice over)")
	}
	if strings.Count(wf, "fail-fast: false") < 2 {
		t.Errorf("claims-smoke.yml's matrices are fail-fast; one claim failing would hide the state of the others in the same job")
	}
}

// nextTopLevelJobKey matches the next two-space-indented "name:" key, which
// bounds one job's body inside the jobs: map.
var nextTopLevelJobKey = regexp.MustCompile(`\n  [a-zA-Z0-9_-]+:\n`)

// TestClaimsSmokeWorkflowWatchesWhatCanBreakIt: the trigger. A workflow
// that runs only on its own file, or only on demand, is one nobody sees
// fail - the same guard bucket-smoke.yml and k8s-smoke.yml already carry.
func TestClaimsSmokeWorkflowWatchesWhatCanBreakIt(t *testing.T) {
	raw, err := os.ReadFile(claimsSmokeWorkflow)
	if err != nil {
		t.Fatalf("read %s: %v", claimsSmokeWorkflow, err)
	}
	wf := string(raw)
	for _, path := range []string{
		".github/workflows/claims-smoke.yml",
		"cmd/**",
		"internal/**",
		"live/smoke/**",
		"go.mod",
		"go.sum",
	} {
		// Twice: once under pull_request and once under push, so a merge to
		// main is measured as well as the pull request that proposed it.
		if got := strings.Count(wf, `"`+path+`"`); got < 2 {
			t.Errorf("claims-smoke.yml names %q %d time(s) in its path filters, want it under both pull_request and push: a change to that tree can break these claims", path, got)
		}
	}
	if !strings.Contains(wf, "schedule:") || !strings.Contains(wf, "cron:") {
		t.Errorf("claims-smoke.yml has no schedule; the over-budget scenario in smoke-nightly would never run, and a repin of the emulator image would go unmeasured until the next pull request that happens to touch one of the paths above")
	}
	if !strings.Contains(wf, "if: github.event_name != 'schedule'") {
		t.Errorf("claims-smoke.yml's smoke job has no `if: github.event_name != 'schedule'`; a nightly run would re-run the PR-time matrix too, which is not what the 2026-09-26 ruling on #1590 asks for")
	}
	if !strings.Contains(wf, "if: github.event_name == 'schedule' || github.event_name == 'workflow_dispatch'") {
		t.Errorf("claims-smoke.yml's smoke-nightly job is not scoped to schedule/workflow_dispatch")
	}
}
