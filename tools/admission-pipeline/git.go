// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// gitDirty reports whether root's working tree carries any uncommitted
// change - staged, unstaged, or untracked. Backs the -pr guard.
func gitDirty(root string) (bool, error) {
	out, err := gitOutput(root, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// gitShow returns ref's content for relPath (e.g. "HEAD:live/registry.json"
// via gitShow(root, "HEAD", "live/registry.json")). A path that doesn't
// exist at ref is a plain error the caller treats as "no before state" (a
// brand-new artifact).
func gitShow(root, ref, relPath string) ([]byte, error) {
	cmd := exec.Command("git", "show", ref+":"+relPath) //nolint:gosec // ref/relPath are internal, not attacker input
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// gitDiffNumstat returns added/removed line counts for each of paths,
// comparing the working tree against HEAD. A path with no diff (unchanged,
// or absent from the output entirely) is simply missing from the result
// map - callers treat a missing entry as {0, 0}.
func gitDiffNumstat(root string, paths []string) (map[string][2]int, error) {
	args := append([]string{"diff", "--numstat", "HEAD", "--"}, paths...)
	out, err := gitOutput(root, args...)
	if err != nil {
		return nil, err
	}

	result := map[string][2]int{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		add, _ := strconv.Atoi(fields[0]) // "-" for a binary file; Atoi leaves it 0
		rem, _ := strconv.Atoi(fields[1])
		result[fields[2]] = [2]int{add, rem}
	}
	return result, nil
}

func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // a fixed subcommand list, args are internal
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// gitCheckoutBranch creates and switches to a new branch.
func gitCheckoutBranch(root, branch string, log io.Writer) error {
	return runLogged(root, log, "git", "checkout", "-b", branch)
}

// gitCommitArtifacts stages exactly paths (not "-A" - REGENERATE's own
// artifact list, so an unrelated dirty file never rides along) and commits
// them with message. No attribution trailer is ever appended - the repo
// directive this pipeline (and every agent working in it) follows.
func gitCommitArtifacts(root string, paths []string, message string, log io.Writer) error {
	existing := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			existing = append(existing, p)
		}
	}
	if len(existing) == 0 {
		return fmt.Errorf("no artifact paths exist to commit")
	}
	addArgs := append([]string{"add", "--"}, existing...)
	if err := runLogged(root, log, "git", addArgs...); err != nil {
		return err
	}
	return runLogged(root, log, "git", "commit", "-m", message)
}

// gitPushBranch pushes branch to remote, setting the upstream.
func gitPushBranch(root, remote, branch string, log io.Writer) error {
	return runLogged(root, log, "git", "push", "-u", remote, branch)
}
