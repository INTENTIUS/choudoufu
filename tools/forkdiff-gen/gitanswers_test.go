// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #1220: checkForkPointPresent used to read every non-zero exit from git
// as "the fork point is absent, run `git fetch upstream`". A git that
// cannot run at all sent the reader off to fetch a remote that would not
// have helped.

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

// emptyRepo is a real repository that does not hold the fork point.
func emptyRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func TestCheckForkPointDistinguishesACannotAnswerFromAnAbsentObject(t *testing.T) {
	dir := emptyRepo(t)
	brokenGitOnPATH(t)

	err := (&git{dir: dir}).checkForkPointPresent()
	if err == nil {
		t.Fatal("checkForkPointPresent returned nil with a git that exits 128")
	}
	if strings.Contains(err.Error(), "git fetch upstream") {
		t.Errorf("error = %q; it sends the reader to fetch upstream, but git could not answer at all", err)
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("error = %q; want git's own stderr in it", err)
	}
}

// TestCheckForkPointStillDirectsAFetchWhenTheObjectIsAbsent: the real
// "no" keeps its directive.
func TestCheckForkPointStillDirectsAFetchWhenTheObjectIsAbsent(t *testing.T) {
	dir := emptyRepo(t)
	err := (&git{dir: dir}).checkForkPointPresent()
	if err == nil {
		t.Fatal("checkForkPointPresent returned nil for a repository that does not hold the fork point")
	}
	if !strings.Contains(err.Error(), "git fetch upstream") {
		t.Errorf("error = %q; want the fetch directive for a genuinely absent object", err)
	}
}
