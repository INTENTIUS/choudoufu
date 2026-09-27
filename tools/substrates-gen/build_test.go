// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/substrate"
)

func testRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readCommitted(t *testing.T, root string) Artifact {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(OutputJSONRel))) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v (run `go run ./tools/substrates-gen` and commit it)", OutputJSONRel, err)
	}
	var art Artifact
	if err := json.Unmarshal(data, &art); err != nil {
		t.Fatalf("decoding %s: %v", OutputJSONRel, err)
	}
	return art
}

// TestArtifactMatchesCommitted is the staleness guard #1588 asks for,
// the same drift pattern tools/readiness-gen's TestArtifactMatchesCommitted
// and tools/row-gen's TestBucketsArtifactMatchesCommitted use: recompute
// from the same committed inputs and hold the artifact to it. Because
// Build() asks internal/live/substrate.All directly, this goes red both
// when live/substrates.json is hand-edited (the recomputed value differs
// from the edit) and when a Substrate method's answer changes without
// regenerating (the recomputed value differs from what is still committed).
func TestArtifactMatchesCommitted(t *testing.T) {
	root := testRepoRoot(t)
	want, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	got := readCommitted(t, root)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s is stale; run `go run ./tools/substrates-gen` and commit it", OutputJSONRel)
	}
}

// TestBuildIsDeterministic: two in-process Build() calls over the same tree
// must produce byte-identical JSON.
func TestBuildIsDeterministic(t *testing.T) {
	root := testRepoRoot(t)
	a, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	aj, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	bj, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(aj) != string(bj) {
		t.Errorf("two Build() runs over the same tree produced different output")
	}
}

// TestOneRowPerFamily is the Accept criterion's shape: the artifact has
// exactly one row per family in internal/live/substrate.All, no more and no
// fewer - so a GCP or Azure Substrate landing later (out of scope for
// #1588, ruled "not yet" on #1119/#1120) is exactly what would make this
// test's row count grow, rather than something silently missing here.
func TestOneRowPerFamily(t *testing.T) {
	root := testRepoRoot(t)
	art, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(art.Rows) != len(substrate.All) {
		t.Fatalf("got %d rows, substrate.All has %d families", len(art.Rows), len(substrate.All))
	}
	seen := map[string]bool{}
	for _, r := range art.Rows {
		if seen[r.Family] {
			t.Errorf("family %q appears twice", r.Family)
		}
		seen[r.Family] = true
	}
	for _, s := range substrate.All {
		if !seen[s.Name()] {
			t.Errorf("substrate.All names %q, which has no row", s.Name())
		}
	}
}

// TestGCPAndAzureRowsAreNotAdded pins the ruling on #1119/#1120 the issue
// body states ("GCP and Azure rows are not added"): the artifact carries
// exactly aws and kubernetes today.
func TestGCPAndAzureRowsAreNotAdded(t *testing.T) {
	root := testRepoRoot(t)
	art, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	var families []string
	for _, r := range art.Rows {
		families = append(families, r.Family)
		if r.Family == "gcp" || r.Family == "azure" || r.Family == "google" || r.Family == "azurerm" || r.Family == "azapi" {
			t.Errorf("row %q is a GCP or Azure row; #1588 rules those out until #1119/#1120", r.Family)
		}
	}
	if len(families) != 2 {
		t.Errorf("got families %v, want exactly aws and kubernetes", families)
	}
}

// TestEveryFamilyHasControllerHeldFacts, TestEveryFamilyHasFenceNotes and
// TestEveryFamilyHasHarnessFacts are the ledger-completeness guards on the
// three hand-kept lookups: every family in substrate.All must have an
// entry, so a third substrate does not silently read the zero value in any
// of them.
func TestEveryFamilyHasControllerHeldFacts(t *testing.T) {
	for _, s := range substrate.All {
		if _, ok := controllerHeldByFamily[s.Name()]; !ok {
			t.Errorf("controllerHeldByFamily has no entry for %q", s.Name())
		}
	}
	for name := range controllerHeldByFamily {
		if _, ok := substrate.ForProvider(name); !ok {
			t.Errorf("controllerHeldByFamily names %q, which substrate.ForProvider does not recognize; delete the entry", name)
		}
	}
}

func TestEveryFamilyHasFenceNotes(t *testing.T) {
	for _, s := range substrate.All {
		if _, ok := fenceByFamily[s.Name()]; !ok {
			t.Errorf("fenceByFamily has no entry for %q", s.Name())
		}
	}
	for name := range fenceByFamily {
		if _, ok := substrate.ForProvider(name); !ok {
			t.Errorf("fenceByFamily names %q, which substrate.ForProvider does not recognize; delete the entry", name)
		}
	}
}

func TestEveryFamilyHasHarnessFacts(t *testing.T) {
	for _, s := range substrate.All {
		if _, ok := harnessByFamily[s.Name()]; !ok {
			t.Errorf("harnessByFamily has no entry for %q", s.Name())
		}
	}
	for name := range harnessByFamily {
		if _, ok := substrate.ForProvider(name); !ok {
			t.Errorf("harnessByFamily names %q, which substrate.ForProvider does not recognize; delete the entry", name)
		}
	}
}

// TestUntaggableCountsAreConsistentWithReadiness cross-checks the
// untaggable tally against live/readiness.json's own counts, so a join
// mistake (wrong field, wrong slice) shows up as an arithmetic mismatch
// rather than a plausible-looking number nobody checked.
func TestUntaggableCountsAreConsistentWithReadiness(t *testing.T) {
	root := testRepoRoot(t)
	rf, err := loadReadinessFacts(root)
	if err != nil {
		t.Fatal(err)
	}
	art, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	byFamily := map[string]Row{}
	for _, r := range art.Rows {
		byFamily[r.Family] = r
	}

	wantAWS := 0
	for _, ty := range rf.awsTypes {
		if !ty.Facts.Taggable {
			wantAWS++
		}
	}
	if got := byFamily["aws"].Untaggable.Count; got != wantAWS {
		t.Errorf("aws untaggable count = %d, recomputing from live/readiness.json gives %d", got, wantAWS)
	}

	wantK8s := 0
	for _, ty := range rf.k8sTypes {
		if !ty.Facts.LabelSurface {
			wantK8s++
		}
	}
	if got := byFamily["kubernetes"].Untaggable.Count; got != wantK8s {
		t.Errorf("kubernetes untaggable count = %d, recomputing from live/readiness.json gives %d", got, wantK8s)
	}
}

// TestHarnessPinFilesAreReadFromDisk proves the pin field is not typed
// prose: it must equal what is on disk right now, byte for byte after
// trimming.
func TestHarnessPinFilesAreReadFromDisk(t *testing.T) {
	root := testRepoRoot(t)
	art, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range art.Rows {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(r.Harness.PinFile)))
		if err != nil {
			t.Fatalf("%s: reading %s: %v", r.Family, r.Harness.PinFile, err)
		}
		if r.Harness.Pin == "" {
			t.Errorf("%s: harness.pin is empty", r.Family)
		}
		if got, want := r.Harness.Pin, strings.TrimSpace(string(data)); got != want {
			t.Errorf("%s: harness.pin = %q, want the trimmed content of %s (%q)", r.Family, got, r.Harness.PinFile, want)
		}
	}
}
