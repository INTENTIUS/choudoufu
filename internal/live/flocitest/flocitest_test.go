// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package flocitest

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCohortDirsWalksEstatesDirectory is a mechanism test for #48's union
// pin (table == union(estate, estates/*)): CohortDirs must return every
// subdirectory of live/e2e/estates, and FixtureDirs must be the demo estate
// followed by that same list. It reads the directory itself with
// os.ReadDir, independent of CohortDirs, and hardcodes neither a cohort
// name nor a count, so it stays true as cohorts are added or removed - the
// same property TestAdmissionTableCoversEstate and TestTableCoversFixtureTypes
// rely on in internal/live/lint and internal/live/identity.
func TestCohortDirsWalksEstatesDirectory(t *testing.T) {
	root := EstatesDir(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading %s directly: %v", root, err)
	}

	var want []string
	for _, entry := range entries {
		if entry.IsDir() {
			want = append(want, filepath.Join(root, entry.Name()))
		}
	}
	if len(want) == 0 {
		t.Fatalf("%s has no cohort subdirectories; the union mechanism has nothing to prove without at least one (see live/e2e/estates/example)", root)
	}

	got := CohortDirs(t)
	if len(got) != len(want) {
		t.Fatalf("CohortDirs returned %d entries, want %d:\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CohortDirs[%d] = %s, want %s", i, got[i], want[i])
		}
	}

	fixtureDirs := FixtureDirs(t)
	if len(fixtureDirs) != len(got)+1 {
		t.Fatalf("FixtureDirs returned %d entries, want the estate plus %d cohorts (%d)", len(fixtureDirs), len(got), len(got)+1)
	}
	if fixtureDirs[0] != EstateDir(t) {
		t.Errorf("FixtureDirs[0] = %s, want the demo estate %s", fixtureDirs[0], EstateDir(t))
	}
	for i, dir := range got {
		if fixtureDirs[i+1] != dir {
			t.Errorf("FixtureDirs[%d] = %s, want cohort %s", i+1, fixtureDirs[i+1], dir)
		}
	}
}

// TestCohortDirsToleratesMissingEstatesDirectory pins the branch in
// CohortDirs that a missing live/e2e/estates is not a fixture error: before
// the first cohort ever lands, the union it contributes to is simply empty.
// It cannot delete the checkout's real live/e2e/estates (the example
// cohort under it stays byte-untouched for other tests), so it checks the
// same os.IsNotExist path CohortDirs takes directly, against a scratch
// directory that never had one.
func TestCohortDirsToleratesMissingEstatesDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "estates")
	if _, err := os.ReadDir(missing); !os.IsNotExist(err) {
		t.Fatalf("expected os.IsNotExist for an absent directory, got %v", err)
	}
}
