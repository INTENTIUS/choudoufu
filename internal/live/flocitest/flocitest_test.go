// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package flocitest

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cohorts"
)

// TestGenerateCohortsRendersTheWholeRoster is the mechanism test #48's union
// pin used to get from TestCohortDirsWalksEstatesDirectory: whatever the
// cohort consumers are handed must be every cohort, not a subset. The old
// version read live/e2e/estates with os.ReadDir, independently of CohortDirs.
// There is no committed directory to read any more (issue #699), so the
// independent side is now the roster itself: GenerateCohorts must render one
// tree per entry in internal/live/cohorts, in roster order, and each tree
// must actually hold configuration - a directory that exists but is empty
// would satisfy a path check and fail every caller.
//
// Gated: rendering runs `terraform init` and launches the provider plugin.
func TestGenerateCohortsRendersTheWholeRoster(t *testing.T) {
	Gate(t, "cohort rendering")
	RequireBinary(t, "go")
	RequireBinary(t, "terraform")

	want := cohorts.Names()
	if len(want) == 0 {
		t.Fatal("the cohort roster is empty; the union mechanism has nothing to prove")
	}

	got := GenerateCohorts(t)
	if len(got) != len(want) {
		t.Fatalf("GenerateCohorts returned %d trees, want the roster's %d:\ngot: %v", len(got), len(want), got)
	}
	for i, dir := range got {
		if base := filepath.Base(dir); base != want[i] {
			t.Errorf("GenerateCohorts[%d] is %s, want cohort %s", i, base, want[i])
		}
		matches, err := filepath.Glob(filepath.Join(dir, "*.tf"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 {
			t.Errorf("%s: rendered no .tf files", dir)
		}
	}
}

// TestEstatesHoldsNoConfiguration is the ratchet on the deletion. Issue #699
// took the rendered cohorts out of git because they were generator output
// that every working copy then filled with an ignored .terraform/; the
// .gitignore exception block that hid it is gone too, so a re-committed tree
// would come back with its install state visible rather than quietly.
//
// live/e2e/estates keeps the hand-written notes and nothing the loader would
// read. The check is over every configuration form internal/configs accepts,
// not ".tf" alone - the narrower filter is the one an audit already walked a
// resource-declaring iam.tf.json past in tools/estate-gen.
func TestEstatesHoldsNoConfiguration(t *testing.T) {
	root := filepath.Join(RepoRoot(t), "live", "e2e", "estates")
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == "estate.chdf.hcl" || strings.HasSuffix(name, ".tf") ||
			strings.HasSuffix(name, ".tf.json") || strings.HasSuffix(name, ".tofu") ||
			strings.HasSuffix(name, ".tofu.json") {
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Errorf("live/e2e/estates holds %d configuration file(s) again: %s.\n"+
			"Cohorts are rendered at run time (go run ./tools/estate-gen -all -out <dir>); this directory holds notes only - issue #699",
			len(found), strings.Join(found, ", "))
	}

	// And the notes themselves are still here: a deletion that took the
	// hand rulings with it would otherwise pass the check above.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	notes := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "README.md")); err == nil {
			notes++
		} else {
			t.Errorf("%s has no README.md; a cohort directory here is its notes", e.Name())
		}
	}
	if notes <= len(cohorts.Names()) {
		t.Errorf("found %d cohort notes files, want more than the roster's %d (the roster's cohorts plus at least the example)", notes, len(cohorts.Names()))
	}
}
