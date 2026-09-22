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
)

// #1220: `git grep` exits 1 for "no match" and 128 for "could not run".
// grepSurvivors used to read both as an empty match list, so a broken git
// classed every candidate line as lost in the merge - a verdict nobody
// measured.

// brokenGitOnPATH puts a `git` that always exits 128 at the front of PATH
// (prepended, so bash is still found and the test cannot pass for the
// wrong reason).
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

func TestGrepSurvivorsRefusesWhenGitCannotAnswer(t *testing.T) {
	dir := initRepo(t)
	writeT(t, dir, "a.txt", "alpha shared opening line\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "base")
	brokenGitOnPATH(t)

	r := &repo{dir: dir}
	found, err := grepSurvivors(r, "HEAD", map[string]bool{"alpha shared opening line": true})
	if err == nil {
		t.Fatalf("grepSurvivors returned %v with a git that exits 128; want an error, not \"no survivors\"", found)
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("error = %q; want git's own stderr in it", err)
	}
}

// TestGrepSurvivorsStillReadsNoMatchAsNoMatch: exit 1 from a real git is
// the real "no", and must stay a plain empty answer.
func TestGrepSurvivorsStillReadsNoMatchAsNoMatch(t *testing.T) {
	dir := initRepo(t)
	writeT(t, dir, "a.txt", "alpha shared opening line\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "base")

	r := &repo{dir: dir}
	found, err := grepSurvivors(r, "HEAD", map[string]bool{
		"alpha shared opening line":                 true,
		"this sentence appears nowhere in the tree": true,
	})
	if err != nil {
		t.Fatalf("grepSurvivors on a real repo: %v", err)
	}
	if !found["alpha shared opening line"] {
		t.Errorf("the committed line was not found: %v", found)
	}
	if found["this sentence appears nowhere in the tree"] {
		t.Errorf("an absent line was reported found: %v", found)
	}
}
