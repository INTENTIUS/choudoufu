// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The smoke trigger rule (issue #1592, HANDOFF.md "CI: smoke workflow
// triggers", part of #1579). Before this unit: k8s-smoke.yml had no
// `schedule:` at all, so a repin or a dependency drift that touched none of
// its path filters went unmeasured until the next matching pull request;
// nightly-watch.yml's `workflows:` list watched bucket-smoke and
// claims-smoke but not k8s-smoke, so even a scheduled k8s-smoke run (had
// one existed) would have failed in silence; and bucket-smoke.yml's paths
// (internal/live/staterecord/** and internal/live/projection/** only) let a
// pull request touching only cmd/ or the rest of internal/ skip it, unlike
// k8s-smoke.yml and claims-smoke.yml's wider cmd/**, internal/** paths -
// which is how claim 28 stayed red from #1351 to #1369 across several
// merges.
//
// This file checks every smoke workflow against the rule directly, rather
// than trusting each workflow's own header comment to stay in sync with the
// code: a smoke workflow with a pull_request trigger (k8s-smoke,
// bucket-smoke, claims-smoke) fires on the same common path set for both
// pull_request and push, and every smoke workflow or tier (adding
// kind-tier, which intentionally carries no pull_request/push trigger of
// its own - see HANDOFF.md) has workflow_dispatch, a nightly schedule, and
// a name in nightly-watch.yml's workflows list.
//
// Proving it red: on the tree before this unit, k8s-smoke.yml has no
// `schedule:`/`cron:` (TestEverySmokeWorkflowRunsNightly fails),
// nightly-watch.yml's list omits k8s-smoke
// (TestEverySmokeWorkflowIsWatchedByNightlyWatch fails), and
// bucket-smoke.yml's pull_request/push paths lack cmd/** and internal/**
// (TestSmokeWorkflowsTriggerOnTheCommonPaths fails).
const (
	pathK8sSmokeWorkflow     = "../.github/workflows/k8s-smoke.yml"
	pathBucketSmokeWorkflow  = "../.github/workflows/bucket-smoke.yml"
	pathClaimsSmokeWorkflow  = "../.github/workflows/claims-smoke.yml"
	pathKindTierWorkflow     = "../.github/workflows/kind-tier.yml"
	pathNightlyWatchWorkflow = "../.github/workflows/nightly-watch.yml"
)

// smokeWorkflowsWithPathTrigger is every smoke workflow the rule expects a
// pull_request/push path trigger from. kind-tier.yml is deliberately not
// here: standing up a kind cluster and running three test suites against it
// is over any pull-request-sized budget, so it runs on schedule only (see
// HANDOFF.md's "CI: smoke workflow triggers").
var smokeWorkflowsWithPathTrigger = []string{
	pathK8sSmokeWorkflow,
	pathBucketSmokeWorkflow,
	pathClaimsSmokeWorkflow,
}

// everySmokeWorkflowOrTier is the full roster the dispatch, nightly and
// watched-by checks below apply to.
var everySmokeWorkflowOrTier = []string{
	pathK8sSmokeWorkflow,
	pathBucketSmokeWorkflow,
	pathClaimsSmokeWorkflow,
	pathKindTierWorkflow,
}

// commonSmokeTriggerPaths is the path set every smoke workflow's
// pull_request and push blocks must both carry, beyond the workflow's own
// file: the code that can break any scenario it runs. A workflow may watch
// more (k8s-smoke.yml also watches live/kubernetes/** and others), never
// less.
var commonSmokeTriggerPaths = []string{"cmd/**", "internal/**", "live/smoke/**", "go.mod", "go.sum"}

func TestSmokeWorkflowsTriggerOnTheCommonPaths(t *testing.T) {
	for _, path := range smokeWorkflowsWithPathTrigger {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		wf := string(raw)
		want := append([]string{".github/workflows/" + filepath.Base(path)}, commonSmokeTriggerPaths...)
		for _, p := range want {
			// Twice: once under pull_request and once under push, so a merge
			// to main is measured as well as the pull request that proposed
			// it.
			if got := strings.Count(wf, `"`+p+`"`); got < 2 {
				t.Errorf("%s names %q %d time(s) in its path filters, want it under both pull_request and push (the smoke trigger rule, issue #1592): a change to that tree can break its scenarios", path, p, got)
			}
		}
	}
}

func TestEverySmokeWorkflowHasWorkflowDispatch(t *testing.T) {
	for _, path := range everySmokeWorkflowOrTier {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(raw), "workflow_dispatch") {
			t.Errorf("%s has no workflow_dispatch trigger; the smoke trigger rule (issue #1592) requires every smoke workflow to be runnable by hand or by an orchestrator, not only by a matching path change or a schedule", path)
		}
	}
}

func TestEverySmokeWorkflowRunsNightly(t *testing.T) {
	for _, path := range everySmokeWorkflowOrTier {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		wf := string(raw)
		if !strings.Contains(wf, "schedule:") || !strings.Contains(wf, "cron:") {
			t.Errorf("%s has no schedule/cron; the smoke trigger rule (issue #1592) requires every smoke workflow to run nightly, so a repin of a pinned image or a dependency drift is caught the day it happens rather than on the next pull request that happens to touch a watched path", path)
		}
	}
}

// smokeWorkflowNameLine reads a workflow's own top-level `name:`, the same
// way live/nightly_watch_test.go derives what nightly-watch.yml must name.
var smokeWorkflowNameLine = regexp.MustCompile(`(?m)^name:\s*(.+?)\s*$`)

// nightlyWatchWorkflowsList reads nightly-watch.yml's own
// `workflows: [...]` list under workflow_run.
var nightlyWatchWorkflowsList = regexp.MustCompile(`(?m)^\s*workflows:\s*\[([^\]]*)\]`)

func TestEverySmokeWorkflowIsWatchedByNightlyWatch(t *testing.T) {
	nightlyRaw, err := os.ReadFile(pathNightlyWatchWorkflow)
	if err != nil {
		t.Fatalf("read %s: %v", pathNightlyWatchWorkflow, err)
	}
	m := nightlyWatchWorkflowsList.FindStringSubmatch(string(nightlyRaw))
	if m == nil {
		t.Fatalf("%s has no `workflows: [...]` list under workflow_run", pathNightlyWatchWorkflow)
	}
	watched := map[string]bool{}
	for _, f := range strings.Split(m[1], ",") {
		if f = strings.Trim(strings.TrimSpace(f), `"'`); f != "" {
			watched[f] = true
		}
	}
	if len(watched) == 0 {
		t.Fatal("nightly-watch.yml's workflows list is empty; this guard is checking nothing")
	}

	for _, path := range everySmokeWorkflowOrTier {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		nm := smokeWorkflowNameLine.FindStringSubmatch(string(raw))
		if nm == nil {
			t.Fatalf("%s has no top-level name:, so nightly-watch.yml cannot name it", path)
		}
		name := strings.Trim(nm[1], `"'`)
		if !watched[name] {
			t.Errorf("nightly-watch.yml's workflows list %v does not name %q (from %s); a red scheduled run of it would die on the Actions run page rather than opening a nightly-red issue (#1316)", watchedWorkflowNames(watched), name, path)
		}
	}
}

// watchedWorkflowNames is a small helper for a readable failure message; it
// need not be sorted for correctness.
func watchedWorkflowNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
