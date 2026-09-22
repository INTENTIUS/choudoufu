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

	"github.com/intentius/choudoufu/internal/live/check"
)

// #1220: git has three answers - yes, no, and "I could not answer" - and
// this file's guards used to collapse the third into one of the first two.
// A `git rev-parse HEAD` that failed became Checkout == "", which SKIPS the
// drift guard rather than failing it; a `git status` that failed became a
// bare sha, which a reader believes is reproducible. Both are exercised
// here with a git that cannot answer anything, the shape that found them
// (#1149: a machine whose /usr/bin/git refused every call after an Xcode
// update).

// brokenGitOnPATH puts a `git` that always exits 128 at the front of PATH.
// PREPENDED, not substituted: a PATH holding only the stub would hide
// `bash` too, and a test that passes because the shell could not be found
// proves nothing.
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

// realGit runs the real git (looked up before any stub is installed).
func realGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// committedRoot is a real repository with one commit holding a manifest
// and one configuration directory, so that a sweep of it has a HEAD to
// record and a corpus to count.
func committedRoot(t *testing.T) (root, rel string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root = t.TempDir()
	rel = writeManifest(t, root, map[string]any{"glob": "here/*", "origin": "in-repo fixture"})
	writeConfig(t, filepath.Join(root, "here", "one"))
	realGit(t, root, "init", "-q")
	realGit(t, root, "add", "-A")
	realGit(t, root, "commit", "-qm", "fixture")
	return root, rel
}

// TestSweepStopsWhenGitCannotNameTheTree: a sweep whose git cannot answer
// `rev-parse HEAD` must refuse, quoting git, rather than record Commit ""
// and print a summary line indistinguishable from a real one.
func TestSweepStopsWhenGitCannotNameTheTree(t *testing.T) {
	root, rel := committedRoot(t)
	brokenGitOnPATH(t)

	r, err := sweep(sweepOptions{manifest: rel, root: root})
	if err == nil {
		t.Fatalf("sweep returned a run (commit %q) with a git that exits 128; want a refusal, not a run whose provenance is blank", r.Commit)
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("sweep error = %q; want it to quote git's own stderr so the reader is pointed at the toolchain", err)
	}
}

// TestSweepStillRecordsARealTree is the other half: the fix cannot be
// fail-everything. A real repository with a real HEAD still sweeps, and
// records that HEAD.
func TestSweepStillRecordsARealTree(t *testing.T) {
	root, rel := committedRoot(t)

	r, err := sweep(sweepOptions{manifest: rel, root: root})
	if err != nil {
		t.Fatalf("sweep of a real repository: %v", err)
	}
	if len(r.Commit) < 40 || strings.HasSuffix(r.Commit, "+dirty") {
		t.Errorf("commit = %q; want the clean 40-char HEAD of a freshly committed tree", r.Commit)
	}

	// And a tree that is not a git checkout at all is still unknown, not
	// wrong: corpus-fetch is free to stop leaving a .git behind.
	plain := t.TempDir()
	rel2 := writeManifest(t, plain, map[string]any{"glob": "here/*", "origin": "in-repo fixture"})
	writeConfig(t, filepath.Join(plain, "here", "one"))
	r2, err := sweep(sweepOptions{manifest: rel2, root: plain})
	if err != nil {
		t.Fatalf("sweep of a plain directory: %v", err)
	}
	if r2.Commit != "" {
		t.Errorf("commit = %q for a directory that is not a checkout; want \"\" (unknown)", r2.Commit)
	}
}

// statusBrokenGitOnPATH is a git that answers everything except `status`,
// which it refuses with exit 128 - the second tier-1 site: a failed status
// used to fall through to a bare sha, recording uncommitted work as clean.
func statusBrokenGitOnPATH(t *testing.T) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	body := "#!/bin/sh\n" +
		"case \" $* \" in *\" status \"*) echo \"fatal: You have not agreed to the Xcode license agreements.\" >&2; exit 128;; esac\n" +
		"exec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(body), 0o755); err != nil { //nolint:gosec // a test fixture on a temp PATH
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestTreeCommitDoesNotReadAFailedStatusAsClean(t *testing.T) {
	root, _ := committedRoot(t)
	// Make the tree genuinely dirty, so that a bare sha would be a lie.
	if err := os.WriteFile(filepath.Join(root, "here", "one", "main.tf"), []byte("# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	statusBrokenGitOnPATH(t)

	commit, err := treeCommit(root)
	if err == nil {
		t.Fatalf("treeCommit = %q with a git whose status exits 128; a dirty tree was recorded as %q", commit, commit)
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("error = %q; want git's own stderr in it", err)
	}
}

func TestTreeCommitStillMarksARealDirtyTree(t *testing.T) {
	root, _ := committedRoot(t)
	clean, err := treeCommit(root)
	if err != nil || len(clean) != 40 {
		t.Fatalf("treeCommit on a clean tree = (%q, %v); want a bare 40-char sha", clean, err)
	}
	if err := os.WriteFile(filepath.Join(root, "here", "one", "main.tf"), []byte("# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := treeCommit(root)
	if err != nil || dirty != clean+"+dirty" {
		t.Errorf("treeCommit on a dirty tree = (%q, %v); want %q", dirty, err, clean+"+dirty")
	}
}

// TestCorpusStateStopsWhenAFetchedCheckoutCannotBeRead is the first tier-1
// site: Checkout used to become "" and the drift guard, written to skip an
// empty Checkout, was silently disabled.
func TestCorpusStateStopsWhenAFetchedCheckoutCannotBeRead(t *testing.T) {
	root, _ := committedRoot(t)
	rel := writeManifest(t, root, map[string]any{
		"glob":   "here/*",
		"origin": "terraform-aws-modules",
		"fetch": map[string]any{
			"dir":    "here",
			"repo":   "https://example.invalid/thing",
			"commit": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		},
	})
	brokenGitOnPATH(t)

	m, err := check.ReadManifest(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	sources, problems, err := corpusState(root, m)
	if err == nil {
		t.Fatalf("corpusState = (%+v, %v, nil) with a git that exits 128; want an error, not a drift guard that did not run", sources, problems)
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("error = %q; want git's own stderr in it", err)
	}
}
