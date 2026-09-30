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

	"gopkg.in/yaml.v3"
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
// Revised 2026-09-29 by the maintainer: no smoke workflow runs on a pull
// request or a push any more. Every pull request that touched internal/**
// started about 40 smoke jobs against a 20-job concurrency cap, which queued
// every other check behind them, and the push-to-main runs were almost all
// cancelled by the next merge before finishing. The smokes now run nightly
// and on workflow_dispatch only; a branch that wants proof before merging
// dispatches them on itself. The rule below is that revision.
//
// This file checks every smoke workflow against the rule directly, rather
// than trusting each workflow's own header comment to stay in sync with the
// code: every smoke workflow or tier (k8s-smoke, bucket-smoke,
// claims-smoke, kind-tier) triggers on schedule and workflow_dispatch and
// nothing else, and is named in nightly-watch.yml's workflows list.
const (
	pathK8sSmokeWorkflow     = "../.github/workflows/k8s-smoke.yml"
	pathBucketSmokeWorkflow  = "../.github/workflows/bucket-smoke.yml"
	pathClaimsSmokeWorkflow  = "../.github/workflows/claims-smoke.yml"
	pathKindTierWorkflow     = "../.github/workflows/kind-tier.yml"
	pathNightlyWatchWorkflow = "../.github/workflows/nightly-watch.yml"
)

// everySmokeWorkflowOrTier is the full roster the trigger and watched-by
// checks below apply to.
var everySmokeWorkflowOrTier = []string{
	pathK8sSmokeWorkflow,
	pathBucketSmokeWorkflow,
	pathClaimsSmokeWorkflow,
	pathKindTierWorkflow,
}

// allowedSmokeTriggers is every event a smoke workflow may run on, and it
// must carry both: schedule so a repin or a drift is caught the night it
// lands, workflow_dispatch so a branch can be proved on demand.
var allowedSmokeTriggers = []string{"schedule", "workflow_dispatch"}

// workflowTriggers returns the event names under a workflow's top-level
// `on:`, read as YAML rather than searched as text, so a trigger named only
// in a comment, or a `paths:` flipped to `paths-ignore:`, cannot satisfy or
// dodge the rule.
func workflowTriggers(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	on, ok := doc["on"].(map[string]any)
	if !ok || len(on) == 0 {
		t.Fatalf("%s has no `on:` map; this guard is checking nothing", path)
	}
	var events []string
	for k := range on {
		events = append(events, k)
	}
	sort.Strings(events)
	return events
}

// TestSmokeWorkflowsRunOnlyNightlyAndOnDispatch: the rule as revised on
// 2026-09-29. Red: add a `pull_request:` (or `push:`) trigger back to any
// smoke workflow, or delete its schedule, and this names the file.
func TestSmokeWorkflowsRunOnlyNightlyAndOnDispatch(t *testing.T) {
	for _, path := range everySmokeWorkflowOrTier {
		events := workflowTriggers(t, path)
		if strings.Join(events, ",") != strings.Join(allowedSmokeTriggers, ",") {
			t.Errorf("%s triggers on %v, want exactly %v: smoke workflows run nightly and by hand, never on a pull request or a push (the smoke trigger rule as revised 2026-09-29, HANDOFF.md \"CI: smoke workflow triggers\")", path, events, allowedSmokeTriggers)
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
