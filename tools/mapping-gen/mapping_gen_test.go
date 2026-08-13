// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// unexplainedNote is classifyRow's generic fallback note: a TF type that
// falls all the way through to via:none with no curated overlay entry
// behind it. The curated-68 pin below must see none of these - every
// non-mapped curated type has to carry an explicit fold or none entry in
// the overlay, not a silent auto-fallthrough.
const unexplainedNote = "no CFN counterpart found by name or curated overlay"

// TestCurated68Pin regenerates the join restricted to live/SURVEY.md's
// curated 68 and pins issue #43's own acceptance numbers: 59 mapped
// (28 by name, 31 by the overlay's aliases), 9 fold-or-none, and 0
// unexplained - every curated type the heuristic does not reach has an
// overlay entry that says why.
func TestCurated68Pin(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	curated, err := loadCuratedRoster(filepath.Join(root, curatedMDRel))
	if err != nil {
		t.Fatalf("loading the curated roster: %v", err)
	}
	if len(curated) != 68 {
		t.Fatalf("%s's per-type table has %d types, want 68", curatedMDRel, len(curated))
	}

	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}

	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}

	mapping, err := buildMapping(curated, cfnTypes, overlay)
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}

	foldOrNone := mapping.Counts.Fold + mapping.Counts.None
	var unexplained []string
	for _, row := range mapping.Rows {
		if row.Via == viaNone && row.Note != nil && *row.Note == unexplainedNote {
			unexplained = append(unexplained, row.TFType)
		}
	}

	if mapping.Counts.Mapped != 59 {
		t.Errorf("mapped = %d, want 59", mapping.Counts.Mapped)
	}
	if foldOrNone != 9 {
		t.Errorf("fold+none = %d, want 9", foldOrNone)
	}
	if len(unexplained) != 0 {
		t.Errorf("%d curated types fell through with no overlay explanation: %v", len(unexplained), unexplained)
	}
}

// TestOverlayTwoWayStaleness checks tools/mapping-gen/overlay.json against
// the current TF and CFN rosters in both directions, the way
// tools/survey-gen/survey_gen_test.go's exception table does: every entry
// must still name types that exist (drift: a type renamed or removed out
// from under a hand-written row), and every alias must still be needed (a
// no-cliff check, chant's aws-resources.test.ts:7-14 - an alias the name
// heuristic has since grown to derive on its own is a stale row that should
// be deleted, not carried forever).
func TestOverlayTwoWayStaleness(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	tfTypes, err := loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		t.Fatalf("loading the TF roster: %v", err)
	}
	tfSet := make(map[string]bool, len(tfTypes))
	for _, t := range tfTypes {
		tfSet[t] = true
	}

	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}
	cfnSet := make(map[string]bool, len(cfnTypes))
	for _, c := range cfnTypes {
		cfnSet[c] = true
	}

	index, err := buildNameIndex(cfnTypes)
	if err != nil {
		t.Fatalf("buildNameIndex: %v", err)
	}

	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}

	for tf, cfn := range overlay.Aliases {
		if !tfSet[tf] {
			t.Errorf("alias %s -> %s: %s is no longer in the TF roster (%s); remove or update this overlay entry", tf, cfn, tf, tfRosterRel)
		}
		if !cfnSet[cfn] {
			t.Errorf("alias %s -> %s: %s is no longer in the CFN roster (%s); remove or update this overlay entry", tf, cfn, cfn, cfnRosterRel)
		}
		if got, ok := index[tf]; ok && got == cfn {
			t.Errorf("alias %s -> %s is stale: the name heuristic now derives this pair on its own; delete the overlay entry", tf, cfn)
		}
	}
	for tf, parent := range overlay.Folds {
		if !tfSet[tf] {
			t.Errorf("fold %s -> %s: %s is no longer in the TF roster (%s); remove or update this overlay entry", tf, parent, tf, tfRosterRel)
		}
		if !cfnSet[parent] {
			t.Errorf("fold %s -> %s: %s is no longer in the CFN roster (%s); remove or update this overlay entry", tf, parent, parent, cfnRosterRel)
		}
	}
	for tf := range overlay.Nones {
		if !tfSet[tf] {
			t.Errorf("none entry for %s: %s is no longer in the TF roster (%s); remove or update this overlay entry", tf, tf, tfRosterRel)
		}
	}
}

// TestNoUnflaggedCollisions is the full-roster collision test: no two TF
// types resolve to the same CFN type unless the sharing is explicit -
// via:alias, a curated row someone deliberately wrote - rather than two
// independent via:name hits from the heuristic landing on the same CFN
// type, which the heuristic itself has no way to notice or resolve.
func TestNoUnflaggedCollisions(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	tfTypes, err := loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		t.Fatalf("loading the TF roster: %v", err)
	}
	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}
	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}

	mapping, err := buildMapping(tfTypes, cfnTypes, overlay)
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}

	byCFN := map[string][]Row{}
	for _, row := range mapping.Rows {
		if row.Via != viaName && row.Via != viaAlias {
			continue
		}
		byCFN[*row.CFNType] = append(byCFN[*row.CFNType], row)
	}

	for cfn, rows := range byCFN {
		if len(rows) < 2 {
			continue
		}
		var nameHits []string
		for _, r := range rows {
			if r.Via == viaName {
				nameHits = append(nameHits, r.TFType)
			}
		}
		if len(nameHits) > 1 {
			sort.Strings(nameHits)
			var all []string
			for _, r := range rows {
				all = append(all, r.TFType+" ("+r.Via+")")
			}
			sort.Strings(all)
			t.Errorf("%s is claimed by more than one unflagged name-heuristic hit (%v); every TF type past the first must be an explicit overlay alias. Full group: %v",
				cfn, nameHits, all)
		}
	}
}

// TestMappingJSONMatchesCommittedInputs regenerates live/mapping.json from
// the other committed inputs (live/survey-full.json, live/registry.json,
// the overlay) and diffs it against the committed artifact, the same
// pattern tools/survey-gen's TestSurveyJSONAgainstHandTable uses: it reads
// only committed files, so it needs no gate.
func TestMappingJSONMatchesCommittedInputs(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	tfTypes, err := loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		t.Fatalf("loading the TF roster: %v", err)
	}
	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		t.Fatalf("loading the CFN roster: %v", err)
	}
	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		t.Fatalf("loading the overlay: %v", err)
	}

	mapping, err := buildMapping(tfTypes, cfnTypes, overlay)
	if err != nil {
		t.Fatalf("buildMapping: %v", err)
	}
	got, err := mapping.marshal()
	if err != nil {
		t.Fatalf("marshaling the regenerated mapping: %v", err)
	}

	want, err := os.ReadFile(filepath.Join(root, mappingJSONRel))
	if err != nil {
		t.Fatalf("reading the committed %s: %v", mappingJSONRel, err)
	}
	if bytes.Equal(got, want) {
		return
	}

	var gotM, wantM Mapping
	if err := json.Unmarshal(got, &gotM); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantM); err != nil {
		t.Fatalf("decoding the committed %s: %v", mappingJSONRel, err)
	}
	wantRows := map[string]Row{}
	for _, r := range wantM.Rows {
		wantRows[r.TFType] = r
	}
	for _, g := range gotM.Rows {
		w, ok := wantRows[g.TFType]
		if !ok {
			t.Errorf("%s: regenerated but absent from the committed file", g.TFType)
			continue
		}
		delete(wantRows, g.TFType)
		gj, _ := json.Marshal(g)
		wj, _ := json.Marshal(w)
		if string(gj) != string(wj) {
			t.Errorf("%s drifted:\n  committed:   %s\n  regenerated: %s", g.TFType, wj, gj)
		}
	}
	for tfType := range wantRows {
		t.Errorf("%s: committed but no longer regenerated", tfType)
	}
	if gotM.Counts != wantM.Counts {
		t.Errorf("counts drifted: committed %+v, regenerated %+v", wantM.Counts, gotM.Counts)
	}
	t.Errorf("%s is stale; rerun `go run ./tools/mapping-gen` and review the diff", mappingJSONRel)
}
