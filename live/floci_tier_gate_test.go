// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #691, the third instance of the #591/#623 class: a test tier that
// exists but gates nothing. The staterecord SSM defect shipped through a
// conformance suite whose against-a-real-service run was gated on
// TF_FLOCI_TEST=1 and reachable only by a human typing `just test-floci` -
// no workflow set the variable, and the nightly scoped it to one package.
// While writing this guard a second gap surfaced: seven gated files live
// under tools/, which `make test-floci`'s ./internal/live/... scope never
// ran even when a human did type it.
//
// The roster is derived, never hand-listed: every package containing a
// flocitest.Gate call site. Two assertions close the class:
//
//   - every roster package is matched by a package pattern in the
//     Makefile's test-floci recipe, so the human-typed tier covers the
//     whole roster;
//   - at least one workflow under .github/workflows runs test-floci, so
//     the tier gates something automatically rather than depending on a
//     human remembering it exists.
func TestFlociTierCoversEveryGatedPackage(t *testing.T) {
	root := repoRoot(t)

	out, err := exec.Command("git", "-C", root, "grep", "-l", "flocitest.Gate", "--", "*_test.go").Output()
	if err != nil {
		t.Fatalf("enumerating flocitest.Gate call sites: %v", err)
	}
	pkgs := map[string]bool{}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f == "" {
			continue
		}
		pkgs[filepath.Dir(f)] = true
	}
	if len(pkgs) < 5 {
		t.Fatalf("found only %d gated packages; the roster extraction is broken rather than the tier being small", len(pkgs))
	}

	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	recipe := ""
	for _, block := range strings.Split(string(mk), "\n\n") {
		if strings.Contains(block, "test-floci:") && strings.Contains(block, "TF_FLOCI_TEST=1") {
			recipe = block
		}
	}
	if recipe == "" {
		t.Fatal("Makefile has no test-floci recipe setting TF_FLOCI_TEST=1; the tier's one entry point is gone")
	}
	var patterns []string
	for _, field := range strings.Fields(recipe) {
		if strings.HasPrefix(field, "./") && strings.HasSuffix(field, "/...") {
			patterns = append(patterns, strings.TrimSuffix(strings.TrimPrefix(field, "./"), "/..."))
		}
	}
	for pkg := range pkgs {
		covered := false
		for _, pat := range patterns {
			if pkg == pat || strings.HasPrefix(pkg, pat+"/") {
				covered = true
			}
		}
		if !covered {
			t.Errorf("package %s has flocitest.Gate tests but no test-floci pattern (%v) covers it; its against-a-real-service tier can never run", pkg, patterns)
		}
	}

	wfDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		t.Fatalf("reading %s: %v", wfDir, err)
	}
	automated := false
	for _, e := range entries {
		b, rerr := os.ReadFile(filepath.Join(wfDir, e.Name()))
		if rerr != nil {
			t.Fatalf("reading workflow %s: %v", e.Name(), rerr)
		}
		if strings.Contains(string(b), "test-floci") {
			automated = true
		}
	}
	if !automated {
		t.Error("no workflow under .github/workflows runs test-floci; the against-a-real-service tier gates nothing without a human remembering to type it (issue #691)")
	}
}
