// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/cohorts"
)

// TestManifestReachesNoEstateGenCohort guards issue #940: the module pass
// writes .terraform/modules/modules.json into every directory the manifest
// resolves, and until #699 the manifest globbed live/e2e/estates/*, the
// committed estate-gen cohorts. Those trees now render into a temp dir at
// run time (flocitest.GenerateCohorts), so a manifest that reached that
// path again would recreate 31 untracked .terraform/ directories in every
// worktree, which is exactly the noise #699 removed.
//
// The check resolves the real manifest against a root where every cohort
// directory HOLDS a configuration, so a glob that reaches them cannot hide
// behind Resolve's empty-directory skip. Proven red by re-adding the
// pre-#699 glob {"glob": "live/e2e/estates/*", "origin": "in-repo fixture"}
// to live/corpus-manifest.json: the test then names every one of them.
func TestManifestReachesNoEstateGenCohort(t *testing.T) {
	manifest, err := check.ReadManifest(filepath.Join(repoRoot(t), "live", "corpus-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	names := append(cohorts.Names(), "any-other-name", "s3")
	for _, name := range names {
		dir := filepath.Join(root, "live", "e2e", "estates", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource \"aws_vpc\" \"main\" {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := manifest.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	var reached []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, "live/e2e/estates/") {
			reached = append(reached, entry.Name)
		}
	}
	if len(reached) > 0 {
		t.Fatalf("live/corpus-manifest.json resolves %d director(ies) under live/e2e/estates/, so `just corpus-fetch` would write .terraform/modules into each of them (#940, #699):\n  %s",
			len(reached), strings.Join(reached, "\n  "))
	}
}

// repoRoot walks up from the test's working directory to the checkout root,
// the directory that holds live/corpus-manifest.json.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "live", "corpus-manifest.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("live/corpus-manifest.json not found above the test directory")
		}
		dir = parent
	}
}
