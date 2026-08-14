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

// gitT runs git in dir with a fixed identity, failing the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=merge-guard-test",
		"GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=merge-guard-test",
		"GIT_COMMITTER_EMAIL=t@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeT(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-b", "main")
	return dir
}

func scanT(t *testing.T, dir string) *result {
	t.Helper()
	res, err := runScan(options{repoDir: dir, ref: "HEAD", minLen: 12})
	if err != nil {
		t.Fatalf("runScan: %v", err)
	}
	return res
}

// TestDetectsSilentLoss builds a merge that discards the side branch's own
// new content (git merge -s ours: no conflict, no trace) and asserts the
// detector reports the dropped line against that merge.
func TestDetectsSilentLoss(t *testing.T) {
	dir := initRepo(t)
	writeT(t, dir, "a.txt", "alpha shared opening line\nbeta shared closing line\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "base")

	gitT(t, dir, "checkout", "-b", "feature")
	writeT(t, dir, "a.txt", "alpha shared opening line\nthe feature branch contributed this distinctive sentence\nbeta shared closing line\n")
	gitT(t, dir, "commit", "-am", "feature work")

	gitT(t, dir, "checkout", "main")
	writeT(t, dir, "b.txt", "main side does something unrelated here\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "main work")
	gitT(t, dir, "merge", "-s", "ours", "feature", "-m", "merge feature, silently dropping it")

	res := scanT(t, dir)
	if len(res.Findings) != 1 {
		t.Fatalf("want exactly 1 finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	f := res.Findings[0]
	if f.File != "a.txt" {
		t.Errorf("finding file = %q, want a.txt", f.File)
	}
	mergeSha := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD"))
	if f.Merge != mergeSha {
		t.Errorf("finding merge = %s, want the merge commit %s", f.Merge, mergeSha)
	}
	want := "the feature branch contributed this distinctive sentence"
	if len(f.Lines) != 1 || f.Lines[0] != want {
		t.Errorf("finding lines = %q, want [%q]", f.Lines, want)
	}
}

// TestCleanMergeIsQuiet asserts a conflict-free merge that keeps both
// sides' work produces no findings.
func TestCleanMergeIsQuiet(t *testing.T) {
	dir := initRepo(t)
	writeT(t, dir, "a.txt", "alpha shared opening line\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "base")

	gitT(t, dir, "checkout", "-b", "feature")
	writeT(t, dir, "c.txt", "feature adds a brand new file with real content\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "feature work")

	gitT(t, dir, "checkout", "main")
	writeT(t, dir, "b.txt", "main side does something unrelated here\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "main work")
	gitT(t, dir, "merge", "feature", "-m", "clean merge")

	res := scanT(t, dir)
	if len(res.Findings) != 0 {
		t.Fatalf("want no findings on a clean merge, got %+v", res.Findings)
	}
	if res.Stats.MergesScanned != 1 {
		t.Errorf("merges scanned = %d, want 1", res.Stats.MergesScanned)
	}
}

// TestInformedDeletionFiltered: the other side added the same line itself
// and then removed it before the merge. It saw the content; dropping it was
// a decision, and the detector must stay quiet.
func TestInformedDeletionFiltered(t *testing.T) {
	dir := initRepo(t)
	writeT(t, dir, "a.txt", "alpha shared opening line\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "base")

	const contested = "a contested sentence both sides wrote independently"

	gitT(t, dir, "checkout", "-b", "feature")
	writeT(t, dir, "a.txt", "alpha shared opening line\n"+contested+"\n")
	gitT(t, dir, "commit", "-am", "feature adds the contested line")

	gitT(t, dir, "checkout", "main")
	writeT(t, dir, "a.txt", "alpha shared opening line\n"+contested+"\n")
	gitT(t, dir, "commit", "-am", "main adds the same line")
	writeT(t, dir, "a.txt", "alpha shared opening line\n")
	gitT(t, dir, "commit", "-am", "main thinks better of it")
	gitT(t, dir, "merge", "-s", "ours", "feature", "-m", "merge keeps main's deletion")

	res := scanT(t, dir)
	if len(res.Findings) != 0 {
		t.Fatalf("want informed deletion filtered out, got %+v", res.Findings)
	}
	if res.Stats.InformedDropped == 0 {
		t.Errorf("expected the informed filter to fire, stats: %+v", res.Stats)
	}
}

// TestMovedContentIsQuiet: the merge keeps the parent's contribution but in
// a different file; set membership must count that as survival.
func TestMovedContentIsQuiet(t *testing.T) {
	dir := initRepo(t)
	writeT(t, dir, "a.txt", "alpha shared opening line\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "base")

	const moved = "a paragraph that will migrate to another file"

	gitT(t, dir, "checkout", "-b", "feature")
	writeT(t, dir, "a.txt", "alpha shared opening line\n"+moved+"\n")
	gitT(t, dir, "commit", "-am", "feature work")

	gitT(t, dir, "checkout", "main")
	writeT(t, dir, "b.txt", "main side does something unrelated here\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "main work")
	// Evil-ish merge: take feature's sentence but relocate it.
	gitT(t, dir, "merge", "--no-commit", "--no-ff", "feature")
	writeT(t, dir, "a.txt", "alpha shared opening line\n")
	writeT(t, dir, "d.txt", moved+"\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "merge, relocating the paragraph")

	res := scanT(t, dir)
	if len(res.Findings) != 0 {
		t.Fatalf("want moved content counted as survival, got %+v", res.Findings)
	}
}
