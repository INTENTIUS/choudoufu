// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The bucket backend's CI story (GitHub issue #1379). Before
// .github/workflows/bucket-smoke.yml no job anywhere ran claims 28 to 32,
// and claim 28 was red from #1351 to #1369 with the claims table calling it
// proven the whole time. This file is what keeps that from happening again:
// the workflow's matrix is checked against the scenario directory rather
// than trusted, each entry runs its BREAK=1 control, and the workflow runs
// on the paths that can break these claims and nightly.
//
// Which scenarios belong is derived, not listed: a claim scenario that
// stands the emulator up (stack_up) and configures a record_store "s3" is a
// bucket claim that CI can run. The real-AWS ones of the same epic are
// excluded by that test on their own, because they start no emulator.
//
// Proving it red: add a scenario matching those two conditions with no
// matrix entry, drop the BREAK step, or remove one of the paths.

const bucketSmokeWorkflow = "../.github/workflows/bucket-smoke.yml"

// Anchored per line rather than written with a trailing \n: consecutive
// list entries share that newline, so a pattern that consumes it matches
// every other entry and reports half the matrix. The first draft did, and
// the check above caught it.
var bucketMatrixEntry = regexp.MustCompile(`(?m)^\s+- ([a-z0-9-]+)\s*$`)

// bucketSmokeScenarios is the set derived from the scenario directory.
func bucketSmokeScenarios(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("smoke", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("smoke", "scenarios", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if strings.Contains(body, `record_store "s3"`) && strings.Contains(body, "stack_up") {
			out = append(out, strings.TrimSuffix(e.Name(), ".sh"))
		}
	}
	sort.Strings(out)
	return out
}

func TestBucketSmokesRunInCIWithTheirControls(t *testing.T) {
	wf, err := os.ReadFile(bucketSmokeWorkflow)
	if err != nil {
		t.Fatalf("read %s: %v", bucketSmokeWorkflow, err)
	}
	onDisk := bucketSmokeScenarios(t)
	if len(onDisk) < 5 {
		t.Fatalf("found %d emulator record-store scenarios on disk, expected at least 5; this guard is looking in the wrong place", len(onDisk))
	}

	// The matrix block only, so a scenario named in a comment or in a path
	// filter is not mistaken for one the workflow runs.
	_, matrix, found := strings.Cut(string(wf), "scenario:\n")
	if !found {
		t.Fatalf("%s has no `scenario:` matrix", bucketSmokeWorkflow)
	}
	if end := strings.Index(matrix, "    steps:"); end >= 0 {
		matrix = matrix[:end]
	}
	var inMatrix []string
	for _, m := range bucketMatrixEntry.FindAllStringSubmatch(matrix, -1) {
		inMatrix = append(inMatrix, m[1])
	}
	sort.Strings(inMatrix)
	if strings.Join(onDisk, ",") != strings.Join(inMatrix, ",") {
		t.Errorf("bucket-smoke.yml's matrix is %v; the emulator record-store scenarios on disk are %v. A claim no job runs is a claim whose \"proven\" cell is one laptop's word, which is how claim 28 stayed red across several merges.", inMatrix, onDisk)
	}
	if !strings.Contains(string(wf), "BREAK: \"1\"") {
		t.Errorf("bucket-smoke.yml runs no BREAK=1 control; a scenario whose failure is never demonstrated is scenery")
	}
	if !strings.Contains(string(wf), "smoke.sh ${{ matrix.scenario }}") {
		t.Errorf("bucket-smoke.yml does not run live/smoke/smoke.sh for each matrix entry")
	}
}

// TestBucketSmokeWorkflowWatchesWhatCanBreakIt: the trigger. A workflow that
// runs only on its own file, or only on demand, is one nobody sees fail.
func TestBucketSmokeWorkflowWatchesWhatCanBreakIt(t *testing.T) {
	raw, err := os.ReadFile(bucketSmokeWorkflow)
	if err != nil {
		t.Fatalf("read %s: %v", bucketSmokeWorkflow, err)
	}
	wf := string(raw)
	for _, path := range []string{
		".github/workflows/bucket-smoke.yml",
		"live/smoke/**",
		"internal/live/staterecord/**",
		"internal/live/projection/**",
	} {
		// Twice: once under pull_request and once under push, so a merge to
		// main is measured as well as the pull request that proposed it.
		if got := strings.Count(wf, `"`+path+`"`); got < 2 {
			t.Errorf("bucket-smoke.yml names %q %d time(s) in its path filters, want it under both pull_request and push: a change to that tree can break these claims", path, got)
		}
	}
	if !strings.Contains(wf, "schedule:") || !strings.Contains(wf, "cron:") {
		t.Errorf("bucket-smoke.yml has no schedule; a repin of the emulator image changes nothing under the path filters and would go unmeasured until the next pull request that happens to touch one")
	}
	if !strings.Contains(wf, "fail-fast: false") {
		t.Errorf("bucket-smoke.yml's matrix is fail-fast; one claim failing would hide the state of the other four")
	}
}
