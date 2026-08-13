// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"io"
	"os/exec"
)

// prBranchVersion reads the provider version REGENERATE actually ran
// against - live/survey.json's committed provider_version, the same field
// DETECT compares (detect.go's pinnedProviderVersion). Branch/title naming
// reads this post-regeneration value rather than DETECT's "current"
// (registry.terraform.io's latest release): REGENERATE runs survey-gen
// against the still-pinned version (tools/survey-gen/main.go's
// providerVersion constant), which bumping requires its own reviewed
// source edit, so the branch name should say what was actually
// regenerated, not what DETECT merely noticed upstream.
func prBranchVersion(root string) (string, error) {
	return pinnedProviderVersion(root)
}

// prBranchName is pure so it's covered by pr_test.go without touching the
// filesystem.
func prBranchName(version string) string {
	return "pipeline/provider-" + version
}

func prTitle(version string) string {
	return fmt.Sprintf("admission-pipeline: regenerate artifacts (provider %s)", version)
}

// prCommitMessage never carries an attribution trailer - this repo's
// directive, honored the same way whether a human or this pipeline is the
// committer.
func prCommitMessage(version string) string {
	return fmt.Sprintf(
		"admission-pipeline: regenerate artifacts against provider %s\n\n"+
			"Automated regeneration from tools/admission-pipeline (issue #55).\n"+
			"See the PR body for the full drift/regeneration report.",
		version,
	)
}

// OpenPR commits REGENERATE's artifacts to a new pipeline/provider-<version>
// branch, pushes it to remote, and opens a PR with reportPath's contents as
// the body. Assumes main.go's -pr guard already refused a dirty tree before
// REGENERATE ran, and that REGENERATE actually ran (so there's something to
// commit).
func OpenPR(root, remote, reportPath string, log io.Writer) error {
	version, err := prBranchVersion(root)
	if err != nil {
		return fmt.Errorf("reading the regenerated provider version: %w", err)
	}
	branch := prBranchName(version)

	if err := gitCheckoutBranch(root, branch, log); err != nil {
		return fmt.Errorf("creating branch %s: %w", branch, err)
	}
	if err := gitCommitArtifacts(root, pipelineArtifactPaths, prCommitMessage(version), log); err != nil {
		return fmt.Errorf("committing regenerated artifacts: %w", err)
	}
	if err := gitPushBranch(root, remote, branch, log); err != nil {
		return fmt.Errorf("pushing %s to %s: %w", branch, remote, err)
	}
	return runGHPRCreate(root, prTitle(version), reportPath, log)
}

// runGHPRCreate shells out to `gh pr create`, the only step in this file
// that isn't exercised by pr_test.go - deliberately: the task calling for
// this pipeline says not to run -pr against the real repo, so tests cover
// the branch/commit/push plumbing against a local file:// remote and the
// report/body content directly, and leave gh itself untested here.
func runGHPRCreate(root, title, reportPath string, log io.Writer) error {
	cmd := exec.Command("gh", "pr", "create", "--title", title, "--body-file", reportPath)
	cmd.Dir = root
	cmd.Stdout = log
	cmd.Stderr = log
	return cmd.Run()
}
