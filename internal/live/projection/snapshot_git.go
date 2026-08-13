// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// The branch carrier for the observational snapshot: one orphan branch per
// estate, named tofu-snapshots/<estate>, in the git repository enclosing the
// module directory. Each apply adds one commit whose tree holds the same
// [snapshot] JSON writeSnapshot would put in a file, under this single name
// at the tree root, so drift over time is "git log" and
// "git diff" on the branch instead of a single overwritten file.
//
// The write is plumbing only - hash-object, mktree, commit-tree, update-ref -
// so it never touches the worktree, the index, or HEAD: nothing is checked
// out, nothing is staged, and nothing is pushed. The branch starts orphaned
// (its first commit has no parent), and every later commit parents onto the
// branch's previous tip, so history accumulates without ever joining the
// repository's own history.
//
// This file is the writer; hint.go's ReadHintBranch is this branch's one
// sanctioned reader, exactly as it is for snapshot.go's file path - see
// TestSnapshot_noReader's restated invariant for what "sanctioned" is
// allowed to mean (a reduced, advisory Hint, never the branch's content
// treated as authority). Deleting the branch changes nothing about any run
// beyond costing a guided pass its hint; see decodeHint.
const (
	snapshotBranchNamespace = "refs/heads/tofu-snapshots/"
	snapshotBranchFileName  = "snapshot.json"
)

// errSnapshotBranchUnavailable marks the failures that mean the branch
// carrier does not exist in this environment at all - no git executable on
// PATH, or a module directory not inside a git repository - as opposed to a
// carrier that exists and failed. The manager uses the distinction to decide
// whether falling back to a configured snapshot_path is the designed quiet
// path or worth a warning; see [Manager.PersistState].
var errSnapshotBranchUnavailable = errors.New("the snapshot branch carrier is unavailable")

// writeSnapshotBranch encodes snap as indented JSON and commits it to the
// refs/heads/tofu-snapshots/<estate> branch of the git repository enclosing
// moduleDir, as described on the constants above.
//
// The ref update is compare-and-swap: update-ref is given the old tip this
// write parented onto (or "the ref must not exist" for the first commit), so
// a concurrent apply racing this one fails loudly instead of silently
// overwriting the other's commit - the loser's failure surfaces as the same
// warning every other snapshot write failure does. A branch that is currently
// checked out somewhere is refused for the same reason the plumbing was
// chosen: this writer does not move anybody's HEAD.
func writeSnapshotBranch(moduleDir, estate string, snap *snapshot) error {
	if estate == "" {
		return fmt.Errorf("the estate name is not settled, so there is no tofu-snapshots branch to name")
	}

	gitBin, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("%w: no git executable on PATH", errSnapshotBranchUnavailable)
	}
	if _, err := gitPlumb(gitBin, moduleDir, nil, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("%w: %s is not inside a git repository", errSnapshotBranchUnavailable, moduleDir)
	}

	ref := snapshotBranchNamespace + estate

	// Refuse to move a ref some worktree has checked out: updating it would
	// change what that worktree's HEAD resolves to, and never touching HEAD
	// is part of this writer's contract. Nobody has a reason to check out a
	// snapshot branch, so hitting this is an operator surprise worth a
	// warning rather than a silent ref move under their feet.
	if head, err := gitPlumb(gitBin, moduleDir, nil, "symbolic-ref", "--quiet", "HEAD"); err == nil && head == ref {
		return fmt.Errorf("the snapshot branch %s is currently checked out, and the snapshot writer does not move a checked-out branch", strings.TrimPrefix(ref, "refs/heads/"))
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the observational snapshot: %w", err)
	}
	data = append(data, '\n')

	blob, err := gitPlumb(gitBin, moduleDir, data, "hash-object", "-w", "--stdin")
	if err != nil {
		return fmt.Errorf("storing the observational snapshot blob: %w", err)
	}
	tree, err := gitPlumb(gitBin, moduleDir, []byte(fmt.Sprintf("100644 blob %s\t%s\n", blob, snapshotBranchFileName)), "mktree")
	if err != nil {
		return fmt.Errorf("building the observational snapshot tree: %w", err)
	}

	// The branch's previous tip, or "" when this is the first commit. An
	// error here is indistinguishable from "no such ref" by design:
	// rev-parse --verify --quiet exits nonzero for a missing ref, and a repo
	// broken enough to fail for another reason fails the update-ref below
	// with a real message.
	parent, _ := gitPlumb(gitBin, moduleDir, nil, "rev-parse", "--verify", "--quiet", ref)

	commitArgs := []string{"commit-tree", tree, "-m", fmt.Sprintf("live snapshot of estate %s at %s", estate, snap.WrittenAt)}
	if parent != "" {
		commitArgs = append(commitArgs, "-p", parent)
	}
	commit, err := gitPlumb(gitBin, moduleDir, nil, commitArgs...)
	if err != nil {
		return fmt.Errorf("committing the observational snapshot: %w", err)
	}

	// The CAS described on the function: parent is the tip this commit was
	// built on, and "" tells update-ref the ref must not exist yet.
	if _, err := gitPlumb(gitBin, moduleDir, nil, "update-ref", ref, commit, parent); err != nil {
		return fmt.Errorf("updating %s: %w", ref, err)
	}
	return nil
}

// gitPlumb runs one git plumbing command against the repository enclosing
// dir, with stdin as its input when non-nil, and returns its trimmed stdout.
//
// The identity environment is pinned so that commit-tree works in a
// repository (or on a build host) with no user.name configured: the snapshot
// committer is the tool, not the operator, and an observational cache should
// not fail to write because a CI image never ran git config. Plumbing
// commands run no hooks and do no signing, which is part of why the writer
// uses them rather than "git commit".
func gitPlumb(gitBin, dir string, stdin []byte, args ...string) (string, error) {
	cmd := exec.Command(gitBin, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=choudoufu",
		"GIT_AUTHOR_EMAIL=choudoufu@localhost",
		"GIT_COMMITTER_NAME=choudoufu",
		"GIT_COMMITTER_EMAIL=choudoufu@localhost",
	)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("git %s: %w", args[0], err)
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(out.String()), nil
}
