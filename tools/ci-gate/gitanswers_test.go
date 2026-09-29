// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cigate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #1220: every `sha="$(git rev-parse HEAD)"` in ci-gate.sh was unchecked.
// Both sites happened to fail closed (INCOMPLETE GATE, STALE GATE), but the
// printed line read "HEAD is now " with nothing after it, and the
// script's own top-of-file check swallowed git's stderr into "not inside a
// git worktree" whatever the reason was.

const xcodeRefusal = "fatal: You have not agreed to the Xcode license agreements."

// stubGit writes a git on a fresh PATH entry. With onlyHEAD set, the stub
// refuses only `rev-parse HEAD` and hands every other invocation to the
// real git, so the script gets past its --show-toplevel check and the
// refusal lands on the site under test.
func stubGit(t *testing.T, onlyHEAD bool) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	body := "#!/bin/sh\n"
	if onlyHEAD {
		body += "if [ \"$1\" = rev-parse ] && [ \"$2\" = HEAD ]; then echo \"" + xcodeRefusal + "\" >&2; exit 128; fi\n" +
			"exec " + real + " \"$@\"\n"
	} else {
		body += "echo \"" + xcodeRefusal + "\" >&2\nexit 128\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(body), 0o755); err != nil { //nolint:gosec // a test fixture on a temp PATH
		t.Fatal(err)
	}
	return dir
}

// ciGateWithPATH is ciGate with a directory prepended to the child's PATH.
func ciGateWithPATH(t *testing.T, dir, pathDir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{scriptPath(t)}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("running ci-gate.sh %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	return string(out), code
}

func TestCheckRefusesWithGitsOwnWordsWhenHeadCannotBeRead(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")
	if out, code := ciGate(t, dir, "run", "--", "true"); code != 0 {
		t.Fatalf("seeding a green gate: %s", out)
	}

	out, code := ciGateWithPATH(t, dir, stubGit(t, true), "check")
	if code == 0 {
		t.Fatalf("check exited 0 with a git that cannot read HEAD.\noutput: %s", out)
	}
	if !strings.Contains(out, "Xcode license") {
		t.Errorf("check did not quote git's own stderr.\noutput: %s", out)
	}
	if strings.Contains(out, "STALE GATE") || strings.Contains(out, "GREEN") {
		t.Errorf("check gave a verdict about a HEAD it could not read.\noutput: %s", out)
	}
}

func TestRunAbortsBeforeRunningWhenHeadCannotBeRead(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")

	out, code := ciGateWithPATH(t, dir, stubGit(t, true), "run", "--", "touch", "ran")
	if code == 0 {
		t.Fatalf("run exited 0 with a git that cannot read HEAD.\noutput: %s", out)
	}
	if !strings.Contains(out, "Xcode license") {
		t.Errorf("run did not quote git's own stderr.\noutput: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
		t.Errorf("run started the gated command with no sha to stamp the result with")
	}
	if _, err := os.Stat(filepath.Join(dir, "ci.rc")); err == nil {
		t.Errorf("run wrote a ci.rc for a HEAD it could not name")
	}
}

func TestTopLevelCheckQuotesGitWhenItCannotAnswer(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")

	out, code := ciGateWithPATH(t, dir, stubGit(t, false), "check")
	if code == 0 {
		t.Fatalf("check exited 0 with a git that exits 128 on everything.\noutput: %s", out)
	}
	if !strings.Contains(out, "Xcode license") {
		t.Errorf("the script's opening check did not quote git's own stderr.\noutput: %s", out)
	}
}

// TestCheckStillPassesWithARealGit: the fix cannot be fail-everything.
func TestCheckStillPassesWithARealGit(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "f.txt", "one", "initial")
	if out, code := ciGate(t, dir, "run", "--", "true"); code != 0 {
		t.Fatalf("run: %s", out)
	}
	out, code := ciGate(t, dir, "check")
	if code != 0 || !strings.HasPrefix(out, "GREEN") {
		t.Fatalf("check = %d %q; want GREEN", code, out)
	}
}
