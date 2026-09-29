// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1220: gitShow used to return exec's bare "exit status 128" for every
// failure, and artifactCounts read every failure as "brand-new artifact".
// A broken git therefore made the admission report show the whole artifact
// as newly appearing.

func brokenGitOnPATH(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n" +
		"echo \"fatal: You have not agreed to the Xcode license agreements.\" >&2\n" +
		"exit 128\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(body), 0o755); err != nil { //nolint:gosec // a test fixture on a temp PATH
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGitShowCarriesGitsOwnWordsWhenItCannotAnswer(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)
	brokenGitOnPATH(t)

	_, err := gitShow(dir, "HEAD", "README.md")
	if err == nil {
		t.Fatal("gitShow returned nil with a git that exits 128")
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("error = %q; want git's own stderr in it, not a bare exit status", err)
	}
}

// TestArtifactCountsRefusesRatherThanReportingABrandNewArtifact is the
// tier-2 half: the report must stop, not show every artifact as newly
// appearing.
func TestArtifactCountsRefusesRatherThanReportingABrandNewArtifact(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)
	brokenGitOnPATH(t)

	counts, err := artifactCounts(dir, "HEAD", "README.md")
	if err == nil {
		t.Fatalf("artifactCounts = %v, nil with a git that exits 128; want an error, not \"no before state\"", counts)
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("error = %q; want git's own stderr in it", err)
	}
}

// TestArtifactCountsStillReadsAnAbsentPathAsAbsent: the real "no" (the
// path is not at HEAD, or not on disk) is still a nil map and no error.
func TestArtifactCountsStillReadsAnAbsentPathAsAbsent(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)

	for _, ref := range []string{"HEAD", ""} {
		counts, err := artifactCounts(dir, ref, "live/does-not-exist.json")
		if err != nil {
			t.Errorf("artifactCounts(ref=%q) for an absent path: %v; want nil, nil", ref, err)
		}
		if counts != nil {
			t.Errorf("artifactCounts(ref=%q) for an absent path = %v; want nil", ref, counts)
		}
	}
	_, err := gitShow(dir, "HEAD", "live/does-not-exist.json")
	if !errors.Is(err, errNotAtRef) {
		t.Errorf("gitShow for an absent path = %v; want errNotAtRef", err)
	}
}
