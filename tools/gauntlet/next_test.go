// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNextIsDeterministicAndOrdered: core before growing, fewest remaining
// headline stages first, name as the tiebreak, first non-pass headline stage
// per estate, clear estates skipped, and two calls agree.
func TestNextIsDeterministicAndOrdered(t *testing.T) {
	active := HeadlineStages()
	if len(active) < 2 {
		t.Skip("needs at least two headline stages")
	}
	m := &Manifest{Estates: []Estate{
		{Name: "g-close", Source: "s", URL: "u", Pin: "p", Lane: "published-deployment", Set: SetGrowing},
		{Name: "c-far", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "c-close", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "c-done", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
	}}
	a := &Artifact{}
	a.Rebuild(m, "e", OracleVersions{})
	set := func(name string, verdicts map[string]string) {
		r, _ := a.Result(name)
		for k, v := range verdicts {
			r.Stages[k] = v
		}
		a.SetResult(r)
	}
	allPass := map[string]string{}
	for _, s := range active {
		allPass[s.ID] = VerdictPass
	}
	set("c-done", allPass)
	// c-close: everything passes except the last headline stage.
	close := map[string]string{}
	for k, v := range allPass {
		close[k] = v
	}
	close[active[len(active)-1].ID] = VerdictFail
	set("c-close", close)
	set("g-close", close)
	// c-far: nothing passes.
	a.Rebuild(m, "e", OracleVersions{})

	units := NextUnits(a, "all")
	got := []string{}
	for _, u := range units {
		got = append(got, u.ID)
	}
	want := []string{
		"c-close/" + active[len(active)-1].ID,
		"c-far/" + active[0].ID,
		"g-close/" + active[len(active)-1].ID,
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
	again := NextUnits(a, "all")
	for i := range units {
		if again[i].ID != units[i].ID {
			t.Fatal("NextUnits is not deterministic")
		}
	}
	core := NextUnits(a, "core")
	for _, u := range core {
		if u.Set != SetCore {
			t.Errorf("-set core returned %s", u.ID)
		}
	}
	if units[0].Remaining != 1 || units[1].Remaining != len(active) {
		t.Errorf("remaining counts %d, %d", units[0].Remaining, units[1].Remaining)
	}
}

// TestNextSurfacesStaleClearEstatesAsTrailingWork: a clear estate whose last
// recorded run used a since-superseded emulator pin is not confirmed
// against the CURRENT pin - #414's own shape, one layer down, is a repin
// silently invalidating evidence rather than enqueuing work to re-confirm
// it. NextUnits must turn that into a visible unit, deliberately ranked
// after every genuine fail/not_run unit: an estate already known broken
// outranks one merely unconfirmed. A clear estate whose last run DOES match
// the current pin must not appear at all - it is not work.
func TestNextSurfacesStaleClearEstatesAsTrailingWork(t *testing.T) {
	active := HeadlineStages()
	if len(active) == 0 {
		t.Skip("no headline stages")
	}
	m := &Manifest{Estates: []Estate{
		{Name: "c-fresh", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "c-stale", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "g-broken", Source: "s", URL: "u", Pin: "p", Lane: "published-deployment", Set: SetGrowing},
	}}
	a := &Artifact{}
	a.Rebuild(m, "new-pin", OracleVersions{})

	allPass := map[string]string{}
	for _, s := range active {
		allPass[s.ID] = VerdictPass
	}
	setStages := func(name string, verdicts map[string]string) {
		r, _ := a.Result(name)
		for k, v := range verdicts {
			r.Stages[k] = v
		}
		a.SetResult(r)
	}
	setStages("c-fresh", allPass)
	setStages("c-stale", allPass)
	// g-broken: left entirely not_run - real, current work.

	setLastRun := func(name, emulator string) {
		r, _ := a.Result(name)
		r.LastRun = &LastRun{Commit: "x", Date: "2020-01-01T00:00:00Z", Emulator: emulator, ExitCode: 0}
		a.SetResult(r)
	}
	setLastRun("c-fresh", "new-pin")
	setLastRun("c-stale", "old-pin")
	a.Rebuild(m, "new-pin", OracleVersions{})

	units := NextUnits(a, "all")
	var ids []string
	for _, u := range units {
		ids = append(ids, u.ID)
	}

	for _, id := range ids {
		if strings.HasPrefix(id, "c-fresh/") {
			t.Errorf("c-fresh is clear and its last_run.emulator matches the current pin; it must not appear as work, got %v", ids)
		}
	}
	if len(ids) == 0 || !strings.HasPrefix(ids[0], "g-broken/") {
		t.Fatalf("genuine failing/not-run work must rank before stale-but-clear work; got %v", ids)
	}
	last := ids[len(ids)-1]
	if last != "c-stale/stale_pin" {
		t.Errorf("last unit = %q, want %q (trailing stale-evidence work)", last, "c-stale/stale_pin")
	}
}

// TestWorkerBriefCitationsResolve: every repo path the worker and
// orchestrator briefs name exists, so an unattended agent never follows a
// dead reference.
func TestWorkerBriefCitationsResolve(t *testing.T) {
	root := testRoot(t)
	for _, brief := range []string{WorkerBriefPath, OrchestratorBriefPath} {
		t.Run(filepath.Base(brief), func(t *testing.T) { checkBriefCitations(t, root, brief) })
	}
}

func checkBriefCitations(t *testing.T, root, brief string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, brief))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile("`((?:\\.claude|live|tools|scripts|HANDOFF|\\.github)[A-Za-z0-9_./-]*)`")
	seen := 0
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		p := strings.TrimSuffix(m[1], "/")
		if strings.ContainsAny(p, "<>*") {
			continue
		}
		seen++
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s cites %s, which does not exist", brief, p)
		}
	}
	if seen < 6 {
		t.Fatalf("found only %d citations in the brief; the extraction is broken", seen)
	}
	musts := []string{"never merge", "[gauntlet:", "five-row", "not_run"}
	if brief == OrchestratorBriefPath {
		musts = []string{"stop and ask", "merge on green", "[gauntlet:", "render"}
	}
	for _, must := range musts {
		if !strings.Contains(strings.ToLower(string(b)), strings.ToLower(must)) {
			t.Errorf("%s no longer says %q", brief, must)
		}
	}
	for _, p := range []string{"scripts/contribute.sh", ".github/workflows/contribute.yml", ".github/workflows/automerge-artifact.yml"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s missing", p)
		}
	}
}
