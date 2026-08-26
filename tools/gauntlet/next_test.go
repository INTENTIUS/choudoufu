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
// active stages first, name as the tiebreak, first non-pass active stage per
// estate, clear estates skipped, and two calls agree.
func TestNextIsDeterministicAndOrdered(t *testing.T) {
	active := ActiveStages()
	if len(active) < 2 {
		t.Skip("needs at least two active stages")
	}
	m := &Manifest{Estates: []Estate{
		{Name: "g-close", Source: "s", URL: "u", Pin: "p", Lane: "published-deployment", Set: SetGrowing},
		{Name: "c-far", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "c-close", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "c-done", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
	}}
	a := &Artifact{}
	a.Rebuild(m, "e")
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
	// c-close: everything passes except the last active stage.
	close := map[string]string{}
	for k, v := range allPass {
		close[k] = v
	}
	close[active[len(active)-1].ID] = VerdictFail
	set("c-close", close)
	set("g-close", close)
	// c-far: nothing passes.
	a.Rebuild(m, "e")

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
