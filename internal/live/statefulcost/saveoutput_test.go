// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefulcost

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSaveOutputCreatesTheLabelsSubdirectory is the ungated half of
// saveOutput: the neighbour-cost arms label their runs "A-alone/<column>",
// and the one path that runs only for a non-empty plan must keep that plan's
// output rather than fail on a directory it never made (#1316).
func TestSaveOutputCreatesTheLabelsSubdirectory(t *testing.T) {
	dir := t.TempDir()
	prev := savedOutputDir
	savedOutputDir = dir
	t.Cleanup(func() { savedOutputDir = prev })

	path := saveOutput(t, "A-alone/choudoufu-live", 1, "Plan: 1 to import, 0 to add, 1 to change, 0 to destroy.\n")
	if want := filepath.Join(dir, "A-alone", "choudoufu-live_1.out"); path != want {
		t.Fatalf("saved to %s, want %s", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the saved output is not there: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("the saved output is empty")
	}
}
