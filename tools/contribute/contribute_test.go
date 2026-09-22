// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package contribute tests scripts/contribute.sh the way tools/ci-gate tests
// scripts/ci-gate.sh: shell out to the real script against a throwaway
// repository and assert on its real output.
//
// #1220: the script's "does this branch already exist" check read every
// non-zero exit from `git show-ref` as "no such branch", so a git that
// could not answer at all read as "the branch is free" and the script fell
// over one line later at `git worktree add` with no word about why.
package contribute

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// harness copies scripts/contribute.sh into <tmp>/repo/scripts so that the
// script's own ROOT resolution lands on a throwaway repository (and its
// worktree path, ROOT/../wt, stays inside the temp directory). Every tool
// the script insists on is stubbed on a prepended PATH; `git` is stubbed
// only when gitStub is non-empty.
func harness(t *testing.T, gitStub string) (root, bin string) {
	t.Helper()
	tmp := t.TempDir()
	root = filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "scripts", "contribute.sh"))
	if err != nil {
		t.Fatalf("reading scripts/contribute.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "contribute.sh"), src, 0o755); err != nil { //nolint:gosec // a test copy of a script
		t.Fatal(err)
	}
	realGit(t, root, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, root, "add", "README.md")
	realGit(t, root, "commit", "-qm", "fixture")

	bin = filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil { //nolint:gosec // a test fixture on a temp PATH
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"claude", "gh", "docker", "aws", "terraform"} {
		stub(tool, "exit 0\n")
	}
	// `go run ./tools/gauntlet next -json -n 1` is the one go invocation the
	// script makes before the branch check; CONTRIBUTE_UNIT then bypasses
	// parsing its output.
	stub("go", "echo '{\"id\":\"alpha/plan\"}'\n")
	if gitStub != "" {
		stub("git", gitStub)
	}
	return root, bin
}

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

// contribute runs the copied script with bin prepended to PATH and returns
// its combined output and exit code.
func contribute(t *testing.T, root, bin string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "contribute.sh"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CONTRIBUTE_UNIT=alpha/plan",
	)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running contribute.sh: %v\n%s", err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

const brokenGit = "echo \"fatal: You have not agreed to the Xcode license agreements.\" >&2\nexit 128\n"

func TestContributeStopsWhenGitCannotAnswerWhetherTheBranchExists(t *testing.T) {
	root, bin := harness(t, brokenGit)
	out, code := contribute(t, root, bin)
	// Exit 2 is the script's own refusal code. Exit 128 is git's, and
	// means the script fell over at `git worktree add` under set -e with
	// no diagnosis of its own.
	if code != 2 {
		t.Errorf("contribute.sh exited %d with a git that exits 128; want its own refusal (2), not git's exit propagated by set -e.\noutput: %s", code, out)
	}
	// The stub prints its message to stderr on every call, so the bare
	// string proves nothing; the script's OWN line has to carry it.
	var diagnosed bool
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "contribute:") && strings.Contains(line, "Xcode license") {
			diagnosed = true
		}
	}
	if !diagnosed {
		t.Errorf("contribute.sh has no line of its own quoting git's stderr.\noutput: %s", out)
	}
	if strings.Contains(out, "already exists") || strings.Contains(out, "contribute: unit") {
		t.Errorf("contribute.sh answered the branch question git could not answer.\noutput: %s", out)
	}
}

// TestContributeStillTakesAFreeBranch: with a real git, a branch that does
// not exist (show-ref exit 1) is the real "no", and the script proceeds to
// make the worktree.
func TestContributeStillTakesAFreeBranch(t *testing.T) {
	root, bin := harness(t, "")
	out, code := contribute(t, root, bin)
	if code != 0 {
		t.Fatalf("contribute.sh exited %d on a free branch.\noutput: %s", code, out)
	}
	if !strings.Contains(out, "contribute: unit alpha/plan") {
		t.Errorf("contribute.sh did not reach the worktree step for a free branch.\noutput: %s", out)
	}
}

// TestContributeStillRefusesAnExistingBranch: the real "yes" is unchanged.
func TestContributeStillRefusesAnExistingBranch(t *testing.T) {
	root, bin := harness(t, "")
	realGit(t, root, "branch", "gauntlet/alpha-plan")
	out, code := contribute(t, root, bin)
	if code == 0 {
		t.Fatalf("contribute.sh exited 0 with the branch already present.\noutput: %s", out)
	}
	if !strings.Contains(out, "already exists locally") {
		t.Errorf("contribute.sh did not name the existing branch.\noutput: %s", out)
	}
}
