// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitForTest returns the git executable, skipping the test when the machine
// running it has none: these tests are about the branch carrier's behavior
// against a real repository, and without git there is no repository to be
// real against. The no-git behavior itself is covered by
// TestManager_snapshotBranchNoGitFallsBackToFile, which manufactures an
// empty PATH instead of requiring one.
func gitForTest(t *testing.T) string {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git executable on PATH")
	}
	return gitBin
}

// runGit runs one git command against dir and fails the test on error. The
// identity env matches gitPlumb's so that repos initialized here never
// depend on the machine's git config.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@localhost",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@localhost",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// initGitRepo initializes a repository with one commit on its default branch
// and one tracked file, so the tests can assert that HEAD, the index and the
// worktree all survive a snapshot write byte for byte.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("# module\n"), 0o644); err != nil {
		t.Fatalf("writing the tracked file: %s", err)
	}
	runGit(t, dir, "add", "main.tf")
	runGit(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

// TestWriteSnapshotBranch is the branch carrier's shape test: an
// apply-shaped write creates refs/heads/tofu-snapshots/<estate> holding the
// snapshot JSON as an orphan commit, a second write parents onto the first,
// and at no point do HEAD, the index or the worktree move.
//
// Reading the blob back out of the branch here is the same exemption the
// lifecycle snapshot test uses for the file: asserting what the writer wrote
// IS the verification of the no-reader contract, and a test doing so is not
// a code path that treats the snapshot as input to a decision.
func TestWriteSnapshotBranch(t *testing.T) {
	gitForTest(t)
	dir := initGitRepo(t)

	headBefore := runGit(t, dir, "rev-parse", "HEAD")
	indexBefore := runGit(t, dir, "write-tree")

	when := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	snap := buildSnapshot(testProjectionState(), "my-estate", when, nil)
	if err := writeSnapshotBranch(dir, "my-estate", snap); err != nil {
		t.Fatalf("writeSnapshotBranch: %s", err)
	}

	// The ref exists, and its one commit is an orphan: no parent from the
	// repository's own history or anywhere else.
	first := runGit(t, dir, "rev-parse", "refs/heads/tofu-snapshots/my-estate")
	if parents := runGit(t, dir, "log", "--format=%P", "-1", first); parents != "" {
		t.Errorf("the first snapshot commit has parents %q, want an orphan", parents)
	}

	// The commit's tree holds the snapshot JSON under the fixed name, and it
	// decodes into the shape the writer built.
	raw := runGit(t, dir, "cat-file", "-p", first+":"+snapshotBranchFileName)
	var got snapshot
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decoding the committed snapshot: %s\nraw: %s", err, raw)
	}
	if got.FormatVersion != snapshotFormatVersion {
		t.Errorf("formatVersion is %q, want %q", got.FormatVersion, snapshotFormatVersion)
	}
	if got.Estate != "my-estate" {
		t.Errorf("estate is %q, want %q", got.Estate, "my-estate")
	}
	if len(got.Resources) != 1 || got.Resources[0].Address != "aws_s3_bucket.data" {
		t.Errorf("resources are %#v, want the one projected bucket", got.Resources)
	}

	// HEAD, the index and the worktree are untouched.
	if headAfter := runGit(t, dir, "rev-parse", "HEAD"); headAfter != headBefore {
		t.Errorf("HEAD moved from %s to %s", headBefore, headAfter)
	}
	if indexAfter := runGit(t, dir, "write-tree"); indexAfter != indexBefore {
		t.Errorf("the index changed from tree %s to %s", indexBefore, indexAfter)
	}
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("the worktree is no longer clean:\n%s", status)
	}
	if _, err := os.Stat(filepath.Join(dir, snapshotBranchFileName)); !os.IsNotExist(err) {
		t.Errorf("a %s appeared in the worktree; the branch is the only carrier", snapshotBranchFileName)
	}

	// A second write parents onto the first, so history accumulates.
	second := buildSnapshot(testProjectionState(), "my-estate", when.Add(time.Minute), nil)
	if err := writeSnapshotBranch(dir, "my-estate", second); err != nil {
		t.Fatalf("second writeSnapshotBranch: %s", err)
	}
	tip := runGit(t, dir, "rev-parse", "refs/heads/tofu-snapshots/my-estate")
	if tip == first {
		t.Fatal("the second write did not move the branch tip")
	}
	if parent := runGit(t, dir, "rev-parse", "tofu-snapshots/my-estate~1"); parent != first {
		t.Errorf("the second commit parents onto %s, want the first write %s", parent, first)
	}
}

// TestWriteSnapshotBranch_notARepo: a module directory with no enclosing
// repository is the "unavailable" case, distinguishable by errors.Is so the
// manager can treat a configured snapshot_path as the designed fallback.
func TestWriteSnapshotBranch_notARepo(t *testing.T) {
	gitForTest(t)
	dir := t.TempDir()

	snap := buildSnapshot(testProjectionState(), "my-estate", time.Unix(0, 0), nil)
	err := writeSnapshotBranch(dir, "my-estate", snap)
	if err == nil {
		t.Fatal("writeSnapshotBranch succeeded outside a repository")
	}
	if !errors.Is(err, errSnapshotBranchUnavailable) {
		t.Errorf("the error is not errSnapshotBranchUnavailable: %s", err)
	}
	assertNoFilesUnder(t, dir)
}

// TestWriteSnapshotBranch_refusesCheckedOutBranch: a snapshot branch some
// worktree has checked out is refused rather than moved out from under it,
// and the refusal is a real failure rather than "unavailable" - the
// repository is right there, the operator just has to check something else
// out.
func TestWriteSnapshotBranch_refusesCheckedOutBranch(t *testing.T) {
	gitForTest(t)
	dir := initGitRepo(t)
	runGit(t, dir, "checkout", "-q", "-b", "tofu-snapshots/my-estate")

	snap := buildSnapshot(testProjectionState(), "my-estate", time.Unix(0, 0), nil)
	err := writeSnapshotBranch(dir, "my-estate", snap)
	if err == nil {
		t.Fatal("writeSnapshotBranch moved a checked-out branch")
	}
	if errors.Is(err, errSnapshotBranchUnavailable) {
		t.Errorf("a checked-out branch reported as unavailable, want a plain failure: %s", err)
	}
	if !strings.Contains(err.Error(), "checked out") {
		t.Errorf("the refusal does not say what it refused: %s", err)
	}
}

// TestManager_snapshotBranchWritten: the manager-level happy path. A Manager
// with only the branch carrier enabled writes the branch on PersistState,
// records no warning, and leaves no file behind anywhere in the module
// directory beyond git's own object store.
func TestManager_snapshotBranchWritten(t *testing.T) {
	gitForTest(t)
	dir := initGitRepo(t)

	m := NewManager()
	m.EnableSnapshotBranch(dir, "my-estate", fixedClock(time.Unix(0, 0)))
	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}

	if diags := m.SnapshotWarning(); len(diags) != 0 {
		t.Errorf("SnapshotWarning() after a successful branch write is %v, want none", diags)
	}
	tip := runGit(t, dir, "rev-parse", "refs/heads/tofu-snapshots/my-estate")
	raw := runGit(t, dir, "cat-file", "-p", tip+":"+snapshotBranchFileName)
	if !strings.Contains(raw, `"aws_s3_bucket.data"`) {
		t.Errorf("the committed snapshot does not list the projected bucket:\n%s", raw)
	}
	if status := runGit(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("the worktree is no longer clean:\n%s", status)
	}
}

// TestManager_snapshotBranchFallsBackToFile: with both carriers configured
// and no enclosing repository, the file carrier takes the write with no
// warning at all - that is the fallback the config surface promises, working
// as designed rather than failing.
func TestManager_snapshotBranchFallsBackToFile(t *testing.T) {
	gitForTest(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot-fallback.json")

	m := NewManager()
	m.EnableSnapshot(path, "my-estate", fixedClock(time.Unix(0, 0)))
	m.EnableSnapshotBranch(dir, "my-estate", fixedClock(time.Unix(0, 0)))
	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}

	if diags := m.SnapshotWarning(); len(diags) != 0 {
		t.Errorf("SnapshotWarning() for the designed fallback is %v, want none", diags)
	}
	snap := readSnapshot(t, path)
	if snap.Estate != "my-estate" {
		t.Errorf("the fallback file's estate is %q, want %q", snap.Estate, "my-estate")
	}
}

// TestManager_snapshotBranchNoGitFallsBackToFile: a machine with no git on
// PATH is the same "unavailable" case as no repository, so a configured file
// takes the write silently there too.
func TestManager_snapshotBranchNoGitFallsBackToFile(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot-fallback.json")

	m := NewManager()
	m.EnableSnapshot(path, "my-estate", fixedClock(time.Unix(0, 0)))
	m.EnableSnapshotBranch(dir, "my-estate", fixedClock(time.Unix(0, 0)))
	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}

	if diags := m.SnapshotWarning(); len(diags) != 0 {
		t.Errorf("SnapshotWarning() with no git on PATH but a file fallback is %v, want none", diags)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the fallback file was not written: %s", err)
	}
}

// TestManager_snapshotBranchUnavailableWarnsWithoutFallback: the branch
// carrier alone, outside any repository, writes nothing and warns - it never
// invents a file path the operator did not name - and PersistState still
// returns nil, because a failed snapshot can never fail an apply.
func TestManager_snapshotBranchUnavailableWarnsWithoutFallback(t *testing.T) {
	gitForTest(t)
	dir := t.TempDir()

	m := NewManager()
	m.EnableSnapshotBranch(dir, "my-estate", fixedClock(time.Unix(0, 0)))
	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState returned an error (rule 5: it must not): %s", err)
	}

	diags := m.SnapshotWarning()
	if len(diags) == 0 {
		t.Fatal("SnapshotWarning() is empty, want a warning about the missing repository")
	}
	if diags.HasErrors() {
		t.Errorf("the warning is error severity, want warning only: %s", diags.ErrWithWarnings())
	}
	if desc := diags[0].Description(); !strings.Contains(desc.Detail, "tofu-snapshots/my-estate") {
		t.Errorf("the warning does not name the branch: %+v", desc)
	}
	assertNoFilesUnder(t, dir)
}

// TestManager_snapshotBranchFailureFallsBackAndWarns: a repository that is
// present but refuses the write (here, the snapshot branch is checked out)
// is a real failure, so the configured file still takes the write AND the
// warning says the branch is not accumulating history.
func TestManager_snapshotBranchFailureFallsBackAndWarns(t *testing.T) {
	gitForTest(t)
	dir := initGitRepo(t)
	runGit(t, dir, "checkout", "-q", "-b", "tofu-snapshots/my-estate")
	path := filepath.Join(dir, "snapshot-fallback.json")

	m := NewManager()
	m.EnableSnapshot(path, "my-estate", fixedClock(time.Unix(0, 0)))
	m.EnableSnapshotBranch(dir, "my-estate", fixedClock(time.Unix(0, 0)))
	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState returned an error (rule 5: it must not): %s", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("the fallback file was not written: %s", err)
	}
	diags := m.SnapshotWarning()
	if len(diags) == 0 {
		t.Fatal("SnapshotWarning() is empty after a real branch failure, want a warning")
	}
	if diags.HasErrors() {
		t.Errorf("the warning is error severity, want warning only: %s", diags.ErrWithWarnings())
	}
	desc := diags[0].Description()
	if !strings.Contains(desc.Detail, path) {
		t.Errorf("the warning does not say the file carried the snapshot: %+v", desc)
	}
}
