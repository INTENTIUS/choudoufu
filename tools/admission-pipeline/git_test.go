// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepo creates a git repo at dir with a single commit, config
// identity set so `git commit` never needs global config, and default
// branch main - the minimal fixture git.go's functions need.
func initTestRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "admission-pipeline-test@example.com")
	runGit(t, dir, "config", "user.name", "admission-pipeline-test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-q", "-m", "initial commit")
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return out.String()
}

func TestGitDirty(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)

	dirty, err := gitDirty(dir)
	if err != nil {
		t.Fatalf("gitDirty on a clean repo: %v", err)
	}
	if dirty {
		t.Error("gitDirty on a freshly committed repo = true, want false")
	}

	if err := os.WriteFile(filepath.Join(dir, "live-artifact.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = gitDirty(dir)
	if err != nil {
		t.Fatalf("gitDirty with an untracked file: %v", err)
	}
	if !dirty {
		t.Error("gitDirty with an untracked file = false, want true")
	}
}

func TestGitDiffNumstat(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)

	target := filepath.Join(dir, "live", "mapping.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"counts":{"types":1}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "live/mapping.json")
	runGit(t, dir, "commit", "-q", "-m", "add mapping.json")

	// Rewrite it - the working-tree-vs-HEAD diff gitDiffNumstat measures.
	if err := os.WriteFile(target, []byte(`{"counts":{"types":2}}`+"\nextra line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := gitDiffNumstat(dir, []string{"live/mapping.json"})
	if err != nil {
		t.Fatalf("gitDiffNumstat: %v", err)
	}
	stat, ok := stats["live/mapping.json"]
	if !ok {
		t.Fatalf("gitDiffNumstat: no entry for live/mapping.json, got %v", stats)
	}
	if stat[0] == 0 && stat[1] == 0 {
		t.Errorf("gitDiffNumstat for a changed file = %v, want nonzero lines added/removed", stat)
	}
}

func TestGitShow(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)

	target := filepath.Join(dir, "live", "registry.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	want := `{"pin":{"digest":"sha256:abc"}}` + "\n"
	if err := os.WriteFile(target, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "live/registry.json")
	runGit(t, dir, "commit", "-q", "-m", "add registry.json")

	got, err := gitShow(dir, "HEAD", "live/registry.json")
	if err != nil {
		t.Fatalf("gitShow: %v", err)
	}
	if string(got) != want {
		t.Errorf("gitShow = %q, want %q", got, want)
	}
}

func TestGitShow_MissingPath(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir)

	if _, err := gitShow(dir, "HEAD", "live/does-not-exist.json"); err == nil {
		t.Error("gitShow for a path absent at HEAD: want an error, got nil")
	}
}
