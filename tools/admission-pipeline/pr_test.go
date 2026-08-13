// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPRBranchName(t *testing.T) {
	if got, want := prBranchName("6.59.0"), "pipeline/provider-6.59.0"; got != want {
		t.Errorf("prBranchName(%q) = %q, want %q", "6.59.0", got, want)
	}
}

func TestPRCommitMessage_NoAttributionTrailer(t *testing.T) {
	msg := prCommitMessage("6.59.0")
	if !strings.Contains(msg, "6.59.0") {
		t.Errorf("prCommitMessage: want it to name the version, got:\n%s", msg)
	}
	for _, bad := range []string{"Co-Authored-By", "Co-authored-by", "Generated with", "Claude"} {
		if strings.Contains(msg, bad) {
			t.Errorf("prCommitMessage contains %q; no attribution trailer is ever allowed (repo directive):\n%s", bad, msg)
		}
	}
}

func TestPRTitle(t *testing.T) {
	title := prTitle("6.59.0")
	if !strings.Contains(title, "6.59.0") {
		t.Errorf("prTitle = %q, want it to name the version", title)
	}
}

// TestPRGitFlow_LocalBareRemote exercises gitCheckoutBranch,
// gitCommitArtifacts and gitPushBranch end to end against a local file://
// clone remote (git clone --bare to a temp dir), per the task's own
// instruction not to run -pr against the real repo: this proves the
// branch/commit/push plumbing works without gh or a real GitHub remote.
// gh itself (runGHPRCreate) is deliberately not exercised here.
func TestPRGitFlow_LocalBareRemote(t *testing.T) {
	bareDir := t.TempDir()
	runGit(t, bareDir, "init", "-q", "--bare", "-b", "main")

	workDir := t.TempDir()
	runGit(t, workDir, "clone", "-q", bareDir, ".")
	runGit(t, workDir, "config", "user.email", "admission-pipeline-test@example.com")
	runGit(t, workDir, "config", "user.name", "admission-pipeline-test")

	// A bare clone of an empty bare repo has no commits yet; seed one so
	// there's a HEAD to branch from.
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, workDir, "add", "README.md")
	runGit(t, workDir, "commit", "-q", "-m", "initial commit")
	runGit(t, workDir, "push", "-q", "-u", "origin", "main")

	// Simulate REGENERATE having rewritten one artifact.
	artifactRel := filepath.Join("live", "mapping.json")
	artifactAbs := filepath.Join(workDir, artifactRel)
	if err := os.MkdirAll(filepath.Dir(artifactAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactAbs, []byte(`{"counts":{"types":1691}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	log := io.Discard
	branch := prBranchName("6.59.0")
	if err := gitCheckoutBranch(workDir, branch, log); err != nil {
		t.Fatalf("gitCheckoutBranch: %v", err)
	}
	if err := gitCommitArtifacts(workDir, []string{artifactRel}, prCommitMessage("6.59.0"), log); err != nil {
		t.Fatalf("gitCommitArtifacts: %v", err)
	}
	if err := gitPushBranch(workDir, "origin", branch, log); err != nil {
		t.Fatalf("gitPushBranch: %v", err)
	}

	// Assert the branch landed on the bare "remote" with the artifact
	// committed and no attribution trailer, by inspecting the bare repo
	// directly rather than trusting the local working copy.
	branches := runGit(t, bareDir, "branch", "--list", branch)
	if !strings.Contains(branches, branch) {
		t.Fatalf("branch %s not found on the bare remote: %q", branch, branches)
	}

	logOut := runGit(t, bareDir, "log", branch, "-1", "--pretty=%B")
	if !strings.Contains(logOut, "6.59.0") {
		t.Errorf("pushed commit message = %q, want it to mention 6.59.0", logOut)
	}
	if strings.Contains(logOut, "Co-Authored-By") {
		t.Errorf("pushed commit message carries a Co-Authored-By trailer: %q", logOut)
	}

	show := runGit(t, bareDir, "show", branch+":"+filepath.ToSlash(artifactRel))
	if !strings.Contains(show, "1691") {
		t.Errorf("pushed artifact content = %q, want it to carry the regenerated counts", show)
	}
}

func TestGitCommitArtifacts_NoExistingPaths(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)

	err := gitCommitArtifacts(dir, []string{"live/does-not-exist.json"}, "message", io.Discard)
	if err == nil {
		t.Error("gitCommitArtifacts with no existing artifact paths: want an error, got nil")
	}
}
