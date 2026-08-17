// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The plan is only as good as blockerAction, and a lookup table checked
// against nothing drifts the moment a refusal is renamed. Both directions are
// enforced here against live/corpus-refusals.json, which is produced by a
// different generator (tools/corpus-gen) from a different input than this
// package - so neither can quietly agree with the other.

// TestEveryFiredRefusalHasAnAction is the forwards leg: a refusal that
// actually blocked something in the last sweep and has no action is a blocker
// nobody has decided how to act on, and it would print as "?" in the plan.
func TestEveryFiredRefusalHasAnAction(t *testing.T) {
	fired := firedRefusals(t)
	if len(fired) < 10 {
		t.Fatalf("only %d refusals fired in the committed corpus artifact; the artifact is not being read properly and this check would pass vacuously", len(fired))
	}
	for _, id := range fired {
		if _, ok := blockerAction[id]; !ok {
			t.Errorf("refusal %q fired in live/corpus-refusals.json and blockerAction does not mention it.\n"+
				"Every blocker needs an action class, and the Reason is the deliverable: say WHERE the identity is, because that is what decides whether this is DERIVE, ADMIT, DEFER, RULE or PARITY.", id)
		}
	}
}

// TestEveryActionNamesARealRefusal is the backwards leg. An entry for a
// refusal the catalog no longer defines is dead weight that reads as
// coverage - the failure mode that has appeared three times in this
// repository under other names.
func TestEveryActionNamesARealRefusal(t *testing.T) {
	known := knownRefusalIDs(t)
	if len(known) < 50 {
		t.Fatalf("only %d refusal IDs in the committed corpus artifact; the read is broken and this check would pass vacuously", len(known))
	}
	for id := range blockerAction {
		if !known[id] {
			t.Errorf("blockerAction has an entry for %q, which is not a refusal ID the corpus artifact defines.\n"+
				"Either it was renamed - update the key - or it is gone, and the entry should go with it.", id)
		}
	}
}

// TestEveryActionCarriesAReason exists because an entry without one is a
// verdict with no argument, and the next reader cannot tell a considered
// classification from a guess.
func TestEveryActionCarriesAReason(t *testing.T) {
	for id, a := range blockerAction {
		switch a.Action {
		case ActionDerive, ActionAdmit, ActionDefer, ActionRule, ActionParity:
		default:
			t.Errorf("%q has action %q, which is not one of the five", id, a.Action)
		}
		if len(strings.Fields(a.Reason)) < 8 {
			t.Errorf("%q's reason is %d words: %q\nIt has to say where the identity is, not restate the refusal's title.", id, len(strings.Fields(a.Reason)), a.Reason)
		}
	}
}

// TestPlanIsOrderedAndStable pins the two properties an assignment rule needs:
// the first line is the estate with the fewest blockers, and two people
// planning from one sweep get the same first line.
func TestPlanIsOrderedAndStable(t *testing.T) {
	s := sweep{Entries: []struct {
		Name     string         `json:"name"`
		Origin   string         `json:"origin"`
		Blocked  bool           `json:"blocked"`
		Refusals map[string]int `json:"refusals"`
		Sites    int            `json:"sites"`
		Modules  int            `json:"unresolved_modules"`
	}{
		{Name: "z-two-blockers", Origin: ratePopulation, Blocked: true, Sites: 2,
			Refusals: map[string]int{"unadmitted-type": 1, "logical-resource": 1}},
		{Name: "a-one-blocker-many-sites", Origin: ratePopulation, Blocked: true, Sites: 90,
			Refusals: map[string]int{"unadmitted-type": 90}},
		{Name: "b-one-blocker-one-site", Origin: ratePopulation, Blocked: true, Sites: 1,
			Refusals: map[string]int{"unadmitted-type": 1}},
		{Name: "not-a-deployment", Origin: "in-repo fixture", Blocked: true, Sites: 1,
			Refusals: map[string]int{"unadmitted-type": 1}},
		{Name: "already-clean", Origin: ratePopulation, Blocked: false, Sites: 0},
	}}

	plan, unknown := buildPlan(s)
	if len(unknown) != 0 {
		t.Fatalf("fixture uses refusals with no action: %v", unknown)
	}
	got := []string{plan[0].Name, plan[1].Name, plan[2].Name}
	want := []string{"b-one-blocker-one-site", "a-one-blocker-many-sites", "z-two-blockers"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("plan[%d] = %q, want %q (fewest blockers first, then fewest sites)", i, got[i], want[i])
		}
	}
	if len(plan) != 3 {
		t.Errorf("plan holds %d estates, want 3: the fixture and the clean deployment must both be excluded", len(plan))
	}

	// Stability: the same sweep planned twice gives the same order. Map
	// iteration over Refusals is the thing that would break this.
	for range 20 {
		again, _ := buildPlan(s)
		for j := range again {
			if again[j].Name != plan[j].Name {
				t.Fatalf("plan is not stable across runs: position %d was %q, now %q", j, plan[j].Name, again[j].Name)
			}
		}
	}
}

// TestUnmappedRefusalIsReportedNotSwallowed proves the "?" path works, since
// a new refusal appearing is exactly when the plan is most misleading.
func TestUnmappedRefusalIsReportedNotSwallowed(t *testing.T) {
	s := sweep{Entries: []struct {
		Name     string         `json:"name"`
		Origin   string         `json:"origin"`
		Blocked  bool           `json:"blocked"`
		Refusals map[string]int `json:"refusals"`
		Sites    int            `json:"sites"`
		Modules  int            `json:"unresolved_modules"`
	}{
		{Name: "e", Origin: ratePopulation, Blocked: true, Sites: 1,
			Refusals: map[string]int{"a-refusal-nobody-classified": 1}},
	}}
	plan, unknown := buildPlan(s)
	if len(unknown) != 1 || unknown[0] != "a-refusal-nobody-classified" {
		t.Fatalf("unmapped refusal not reported, got %v", unknown)
	}
	if plan[0].Blockers[0].Action != "?" {
		t.Errorf("unmapped blocker rendered as %q, want \"?\"", plan[0].Blockers[0].Action)
	}
}

type corpusArtifact struct {
	Refusals []struct {
		ID      string `json:"id"`
		Configs int    `json:"configs"`
	} `json:"refusals"`
}

func readCorpusArtifact(t *testing.T) corpusArtifact {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, "live", "corpus-refusals.json")
		if b, err := os.ReadFile(p); err == nil {
			var a corpusArtifact
			if err := json.Unmarshal(b, &a); err != nil {
				t.Fatalf("parsing %s: %s", p, err)
			}
			return a
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("live/corpus-refusals.json not found above the package directory; this test guards a table against it, so its absence is a failure rather than a skip")
		}
		dir = parent
	}
}

func firedRefusals(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, r := range readCorpusArtifact(t).Refusals {
		if r.Configs > 0 {
			out = append(out, r.ID)
		}
	}
	return out
}

func knownRefusalIDs(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, r := range readCorpusArtifact(t).Refusals {
		out[r.ID] = true
	}
	return out
}
