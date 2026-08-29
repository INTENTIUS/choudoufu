// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func testRoot(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Skip("not in a git checkout")
	}
	return root
}

// gauntletEstateNames reads live/gauntlet.json's own estate list directly
// (rather than importing tools/gauntlet, a separate `package main` this
// tool cannot import, and rather than duplicating that list by hand, which
// would go stale the moment an estate is added or renamed) so this test
// holds estateSpecs and the committed artifact to the one place the board
// itself is defined.
func gauntletEstateNames(t *testing.T, root string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "live", "gauntlet.json"))
	if err != nil {
		t.Fatalf("reading live/gauntlet.json: %v", err)
	}
	var doc struct {
		Estates []struct {
			Name string `json:"name"`
		} `json:"estates"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing live/gauntlet.json: %v", err)
	}
	names := make([]string, 0, len(doc.Estates))
	for _, e := range doc.Estates {
		names = append(names, e.Name)
	}
	return names
}

// TestEveryEstateHasTypes is issue #435's own accept criterion, enforced in
// CI without needing .corpus populated (it reads the committed artifact,
// not a fresh scan): every estate live/gauntlet.json carries today has a
// row in live/estate-types.json with a non-empty Types list, estateSpecs
// carries exactly the same estate set (no drift either way), and the
// artifact's own Totals agree with what it actually holds.
func TestEveryEstateHasTypes(t *testing.T) {
	root := testRoot(t)

	board := gauntletEstateNames(t, root)
	boardSet := map[string]bool{}
	for _, n := range board {
		boardSet[n] = true
	}

	specSet := map[string]bool{}
	for _, s := range estateSpecs {
		if specSet[s.Name] {
			t.Errorf("estateSpecs lists %q more than once", s.Name)
		}
		specSet[s.Name] = true
		if !boardSet[s.Name] {
			t.Errorf("estateSpecs lists %q, which live/gauntlet.json does not carry; delete its entry in spec.go", s.Name)
		}
	}
	for _, n := range board {
		if !specSet[n] {
			t.Errorf("live/gauntlet.json carries %q but spec.go's estateSpecs has no entry for it; add one (see spec.go's doc comment) and regenerate", n)
		}
	}

	art, err := Read(root)
	if err != nil {
		t.Fatalf("reading %s: %v (run \"go run ./tools/estate-types\")", ArtifactPath, err)
	}

	artSet := map[string]bool{}
	distinct := map[string]bool{}
	for _, e := range art.Estates {
		artSet[e.Name] = true
		if len(e.Types) == 0 {
			t.Errorf("%s: estate %q has an empty Types list", ArtifactPath, e.Name)
		}
		if e.Count != len(e.Types) {
			t.Errorf("%s: estate %q Count=%d but Types has %d entries", ArtifactPath, e.Name, e.Count, len(e.Types))
		}
		if len(e.Sources) == 0 {
			t.Errorf("%s: estate %q has no Sources", ArtifactPath, e.Name)
		}
		for _, ty := range e.Types {
			distinct[ty] = true
		}
	}
	for _, n := range board {
		if !artSet[n] {
			t.Errorf("%s has no row for %q; run \"go run ./tools/estate-types\"", ArtifactPath, n)
		}
	}
	if art.Totals.Estates != len(art.Estates) {
		t.Errorf("%s: totals.estates=%d but the file has %d estate rows", ArtifactPath, art.Totals.Estates, len(art.Estates))
	}
	if art.Totals.DistinctTypes != len(distinct) {
		t.Errorf("%s: totals.distinct_types=%d but recounting the rows gives %d", ArtifactPath, art.Totals.DistinctTypes, len(distinct))
	}
}

// TestArtifactIsCurrent regenerates the artifact from .corpus and diffs it
// against the committed file, so a stale index is caught rather than
// trusted. Skipped when .corpus is not populated - internal/live/check's
// own sweep_test.go treats a missing .corpus the same way, for the same
// reason: a CI environment that never ran "just corpus-fetch" has nothing
// wrong with it, and this check has nothing to check.
func TestArtifactIsCurrent(t *testing.T) {
	root := testRoot(t)

	if info, err := os.Stat(filepath.Join(root, ".corpus")); err != nil || !info.IsDir() {
		t.Skip(".corpus is not populated; run \"just corpus-fetch\" to enable this check")
	}

	fresh, err := Generate(context.Background(), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	committed, err := Read(root)
	if err != nil {
		t.Fatalf("reading %s: %v", ArtifactPath, err)
	}
	if !reflect.DeepEqual(committed, fresh) {
		t.Errorf("%s is stale; run \"go run ./tools/estate-types\" and commit the result", ArtifactPath)
	}
}
