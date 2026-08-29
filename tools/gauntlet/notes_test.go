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

// TestNotesReproducesV030ToV040 is #422's own acceptance test: the real
// board movement recorded in the committed snapshots, reproduced by the
// tool rather than asserted by hand. If either snapshot's schema changes
// again, this is the test that notices.
func TestNotesReproducesV030ToV040(t *testing.T) {
	root := testRoot(t)
	oldA, err := loadArtifactFile(filepath.Join(root, "live", "history", "v0.3.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	newA, err := loadArtifactFile(filepath.Join(root, "live", "history", "v0.4.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := RenderNotes(root, oldA, newA)

	for _, want := range []string{
		"## Board movement",
		"Core estates: 20/25 clear -> 25/25 clear (+5)",
		"All estates: 21/26 clear -> 26/26 clear (+5)",
		"## Newly cleared",
		"`corpus-alb-complete`",
		"`corpus-autoscaling-complete`",
		"`corpus-ecs-fargate`",
		"`corpus-eks-basic`",
		"`corpus-rds-complete-postgres`",
		"## Regressed",
		"## Emulator",
		"Repinned from `ghcr.io/lex00/floci@sha256:a9dc5342",
		"to `ghcr.io/lex00/floci@sha256:1c6450b8",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("notes output missing %q; full output:\n%s", want, out)
		}
	}
	// No estate may appear as both newly cleared and regressed, and no
	// estate that regressed should be silently absent: v0.3.0 -> v0.4.0
	// regressed nothing, so the Regressed section must say so explicitly.
	regressedSection := out[strings.Index(out, "## Regressed"):]
	if idx := strings.Index(regressedSection, "## Emulator"); idx >= 0 {
		regressedSection = regressedSection[:idx]
	}
	if !strings.Contains(regressedSection, "- none") {
		t.Errorf("expected an explicit \"none\" under Regressed, got:\n%s", regressedSection)
	}
}

func TestEstateMovementNewlyClearedAndRegressed(t *testing.T) {
	oldA := &Artifact{Estates: []EstateResult{
		{Name: "a", Clear: false},
		{Name: "b", Clear: true},
		{Name: "c", Clear: true},
		// d absent from old: added since, must not appear as movement.
	}}
	newA := &Artifact{Estates: []EstateResult{
		{Name: "a", Clear: true},  // newly cleared
		{Name: "b", Clear: false}, // regressed
		{Name: "c", Clear: true},  // unchanged
		{Name: "d", Clear: true},  // new estate, no prior verdict
	}}
	newlyCleared, regressed := estateMovement(oldA, newA)
	if len(newlyCleared) != 1 || newlyCleared[0] != "a" {
		t.Errorf("newlyCleared = %v, want [a]", newlyCleared)
	}
	if len(regressed) != 1 || regressed[0] != "b" {
		t.Errorf("regressed = %v, want [b]", regressed)
	}
}

func TestSignedDelta(t *testing.T) {
	cases := map[int]string{5: "+5", 0: "0", -3: "-3"}
	for in, want := range cases {
		if got := signedDelta(in); got != want {
			t.Errorf("signedDelta(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestNotesOmitsEmulatorSectionWhenUnchanged(t *testing.T) {
	oldA := &Artifact{Emulator: "same", Estates: []EstateResult{}}
	newA := &Artifact{Emulator: "same", Estates: []EstateResult{}}
	out := RenderNotes(t.TempDir(), oldA, newA)
	if strings.Contains(out, "## Emulator") {
		t.Errorf("expected no Emulator section when the pin is unchanged, got:\n%s", out)
	}
}

// TestReadinessSectionAbsentDegradesGracefully: as of #422, live/readiness.json
// does not exist anywhere in this checkout's history, and the Do note asks
// for this to degrade to nothing rather than error. Builds a real one-commit
// git repo with no readiness.json to prove the section is silently skipped
// rather than failing the whole render.
func TestReadinessSectionAbsentDegradesGracefully(t *testing.T) {
	root := initTestRepo(t)
	commit := commitTestFile(t, root, "README.md", "hello\n", "first")

	oldA := &Artifact{Estates: []EstateResult{{Name: "e", LastRun: &LastRun{Commit: commit, Date: "2026-01-01T00:00:00Z"}}}}
	newA := &Artifact{Estates: []EstateResult{{Name: "e", LastRun: &LastRun{Commit: commit, Date: "2026-01-02T00:00:00Z"}}}}

	if got := readinessSection(root, oldA, newA); got != "" {
		t.Errorf("readinessSection with no readiness.json in either commit = %q, want \"\"", got)
	}
	out := RenderNotes(root, oldA, newA)
	if strings.Contains(out, "## Readiness") {
		t.Errorf("RenderNotes must not print a Readiness section when the file is absent, got:\n%s", out)
	}
}

// TestReadinessSectionDiffsWhenPresent: the other half of the same
// requirement - once live/readiness.json exists at both commits, the section
// renders a real diff (added/removed/changed keys), schema-agnostic since
// nothing in this repository has defined the file's shape yet.
func TestReadinessSectionDiffsWhenPresent(t *testing.T) {
	root := initTestRepo(t)
	oldCommit := commitTestFile(t, root, "live/readiness.json", `{"a":1,"b":2}`+"\n", "old readiness")
	newCommit := commitTestFile(t, root, "live/readiness.json", `{"b":3,"c":4}`+"\n", "new readiness")

	oldA := &Artifact{Estates: []EstateResult{{Name: "e", LastRun: &LastRun{Commit: oldCommit, Date: "2026-01-01T00:00:00Z"}}}}
	newA := &Artifact{Estates: []EstateResult{{Name: "e", LastRun: &LastRun{Commit: newCommit, Date: "2026-01-02T00:00:00Z"}}}}

	section := readinessSection(root, oldA, newA)
	for _, want := range []string{
		"## Readiness",
		"`a` removed (was 1)",
		"`b`: 2 -> 3",
		"`c` added: 4",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("readiness section missing %q; got:\n%s", want, section)
		}
	}
}

func TestLatestCommit(t *testing.T) {
	a := &Artifact{Estates: []EstateResult{
		{Name: "x", LastRun: &LastRun{Commit: "early", Date: "2026-01-01T00:00:00Z"}},
		{Name: "y", LastRun: &LastRun{Commit: "late", Date: "2026-02-01T00:00:00Z"}},
		{Name: "z"}, // never run
	}}
	if got := latestCommit(a); got != "late" {
		t.Errorf("latestCommit = %q, want %q", got, "late")
	}
	if got := latestCommit(&Artifact{}); got != "" {
		t.Errorf("latestCommit of an empty artifact = %q, want \"\"", got)
	}
}

func TestCmdNotesRequiresTwoSnapshotPaths(t *testing.T) {
	if err := cmdNotes(t.TempDir(), nil); err == nil {
		t.Error("cmdNotes with no args should error")
	}
	if err := cmdNotes(t.TempDir(), []string{"one.json"}); err == nil {
		t.Error("cmdNotes with one arg should error")
	}
}

// initTestRepo makes a throwaway git repo isolated from the real checkout,
// so gitShowFile has something real to shell out to without touching this
// repository's own history.
func initTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	return root
}

// commitTestFile writes path (creating parent dirs) inside root, commits it,
// and returns the new commit's hash.
func commitTestFile(t *testing.T, root, path, content, message string) string {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("add", path)
	run("commit", "-q", "-m", message)
	return run("rev-parse", "HEAD")
}
