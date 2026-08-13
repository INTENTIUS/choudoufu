// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// admission-pipeline orchestrates issue #55's admission chain end to end:
// one command that notices when the pinned AWS provider or the pinned
// CloudFormation Registry schema moved, regenerates every derived artifact
// from source, verifies the result builds and tests green, writes a
// markdown report, and - with -pr - turns that into a pull request.
//
// Stages, each skippable by its own flag so a local loop can run a subset:
//
//	DETECT      compares the committed provider-version and registry-digest
//	            pins against what registry.terraform.io and the
//	            CloudFormation Registry currently serve (-skip-detect).
//	            With no drift and no -force, the run prints the drift
//	            report and exits 0 before touching anything else.
//	REGENERATE  runs survey-gen, registry-gen, mapping-gen, importdocs-gen
//	            and row-gen in dependency order, then survey-gen -render
//	            (-skip-regenerate).
//	VERIFY      `go build ./...` and the fast test tiers; a nonzero exit
//	            from either fails the run (-skip-verify).
//	REPORT      a generated markdown summary - pin states, artifact count
//	            deltas, row-gen's proposal counts, mapping-gen's former2
//	            contradictions and unclassifiable count - written under
//	            tmp/admission-pipeline/ (gitignored) and printed
//	            (-skip-report).
//
// Every *-gen tool this orchestrates is its own package main, so none of
// them is importable as a library; each stage shells out to `go run
// ./tools/<name>` instead (see regenerate.go's package doc for the one
// exception: DETECT's registry-digest check duplicates a few dozen lines of
// registry-gen's own zip/digest logic rather than shell out to a tool with
// no dry-run mode).
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/admission-pipeline                # detect; regenerate+verify+report only on drift
//	go run ./tools/admission-pipeline -force          # regenerate even with no drift
//	go run ./tools/admission-pipeline -pr             # ... and open a PR when there's something to review
//	go run ./tools/admission-pipeline -skip-verify -skip-report  # fast local DETECT+REGENERATE loop
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// repoRoot resolves the checkout's root from this file's own location, the
// same trick every other tools/*-gen's repoRoot uses (see
// tools/survey-gen/main.go's own copy of this function for why it's
// duplicated instead of shared: each tool is its own package main).
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/admission-pipeline/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	skipDetect := flag.Bool("skip-detect", false, "skip DETECT and proceed straight to REGENERATE (implies drift, same as -force)")
	skipRegenerate := flag.Bool("skip-regenerate", false, "skip REGENERATE (a DETECT-only, or DETECT+VERIFY, run)")
	skipVerify := flag.Bool("skip-verify", false, "skip VERIFY (go build/go test)")
	skipReport := flag.Bool("skip-report", false, "skip REPORT (no markdown summary written)")
	force := flag.Bool("force", false, "REGENERATE even when DETECT finds no drift")
	accept := flag.Bool("accept", false, "pass -accept through to survey-gen/importdocs-gen, stamping the regenerated pins' accepted date immediately instead of leaving that for PR approval (issue #37's accepted-date semantics; the default leaves ratification to the merge, per issue #55)")
	prMode := flag.Bool("pr", false, "commit the regenerated artifacts to a new pipeline/provider-<version> branch, push, and open a PR with the generated report as its body; refuses on a dirty working tree")
	remote := flag.String("remote", "origin", "git remote -pr pushes the branch to")
	flag.Parse()

	if err := run(*skipDetect, *skipRegenerate, *skipVerify, *skipReport, *force, *accept, *prMode, *remote); err != nil {
		fmt.Fprintf(os.Stderr, "admission-pipeline: %v\n", err)
		os.Exit(1)
	}
}

func run(skipDetect, skipRegenerate, skipVerify, skipReport, force, accept, prMode bool, remote string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	log := os.Stderr

	// The -pr guard runs before anything else touches the tree: refusing
	// after DETECT (a read-only stage) is equivalent, but refusing before
	// it fails fast instead of spending a round trip to two upstream
	// sources first.
	if prMode {
		dirty, err := gitDirty(root)
		if err != nil {
			return fmt.Errorf("checking whether the working tree is clean: %w", err)
		}
		if dirty {
			return fmt.Errorf("-pr refuses to run against a dirty working tree; commit or stash first")
		}
	}

	ctx := context.Background()

	var drift *DriftReport
	if !skipDetect {
		d, err := Detect(ctx, root)
		if err != nil {
			return fmt.Errorf("DETECT: %w", err)
		}
		drift = &d
		fmt.Fprint(log, drift.String())

		if !drift.Any() && !force {
			fmt.Fprintln(log, "admission-pipeline: no drift; exiting (pass -force to regenerate anyway)")
			return nil
		}
	}

	var regen *RegenerateResult
	if !skipRegenerate {
		regen, err = Regenerate(root, accept, log)
		if err != nil {
			return fmt.Errorf("REGENERATE: %w", err)
		}
	}

	if !skipVerify {
		if err := Verify(root, log); err != nil {
			return fmt.Errorf("VERIFY: %w", err)
		}
	}

	var reportPath string
	if !skipReport {
		reportPath, err = WriteReport(root, drift, regen, log)
		if err != nil {
			return fmt.Errorf("REPORT: %w", err)
		}
		fmt.Println(reportPath)
	}

	if prMode {
		if regen == nil {
			return fmt.Errorf("-pr needs REGENERATE to have run (drop -skip-regenerate)")
		}
		if reportPath == "" {
			return fmt.Errorf("-pr needs REPORT to have run, for the PR body (drop -skip-report)")
		}
		if err := OpenPR(root, remote, reportPath, log); err != nil {
			return fmt.Errorf("PR: %w", err)
		}
	}

	return nil
}
