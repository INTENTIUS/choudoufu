// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file holds the guards for one family of defects: a tool that answers
// confidently when it could not answer at all (#1142, #1149), and a derived
// file one command writes and a different command publishes (#1187).
//
// Every test here is written from what the tool PROMISES, not from how it
// is implemented, and every one was proven red against the code as it stood
// before its fix - see the pull request for the red output, quoted.

// brokenGitOnPATH puts a `git` that always exits 128 at the front of PATH,
// reproducing the failure that started this family: a machine whose
// /usr/bin/git began refusing every invocation with "You have not agreed to
// the Xcode license agreements" after a background Xcode update.
//
// The stub directory is PREPENDED, never substituted for PATH, on purpose:
// a PATH holding only the stub would also hide `bash`, and a test that
// passes because the shell could not be found proves nothing about what it
// claims to prove.
func brokenGitOnPATH(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n" +
		"echo \"fatal: You have not agreed to the Xcode license agreements.\" >&2\n" +
		"exit 128\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(body), 0o755); err != nil { //nolint:gosec // a test fixture on a temp PATH
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestHeadCommitReturnsGitsOwnErrorRatherThanAnEmptyString is #1149's core.
//
// headCommit used to discard git's error and return "". Nothing downstream
// could tell that apart from a repository with no HEAD, so a broken
// toolchain surfaced as "built an invalid scale record: missing required
// field(s): commit" and the reader debugged the record builder. The error
// must exist, and it must carry git's own words.
func TestHeadCommitReturnsGitsOwnErrorRatherThanAnEmptyString(t *testing.T) {
	brokenGitOnPATH(t)
	got, err := headCommit(t.TempDir())
	if err == nil {
		t.Fatalf("headCommit returned (%q, nil) with a git that exits 128; want an error, not a blank commit", got)
	}
	if got != "" {
		t.Errorf("headCommit returned commit %q alongside its error; want no commit at all", got)
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("headCommit error = %q; want it to quote git's own stderr, so a reader is pointed at the toolchain and not at the record builder", err)
	}
}

// TestRunLiveCertRefusesBeforeSpendingWhenGitCannotAnswer is the other half
// of #1149, and the reason the provenance commit is resolved before the
// script starts rather than after it finishes.
//
// A live-cert is the most expensive run in this repo. Discovering that its
// provenance cannot be stamped is worth a `git rev-parse` beforehand and
// worth nothing at all afterwards: the hours are already spent, and what
// the old order produced was a live_cert row on disk with no matching scale
// row. The marker file is the load-bearing assertion - it proves the script
// never ran, not merely that the call returned an error.
func TestRunLiveCertRefusesBeforeSpendingWhenGitCannotAnswer(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "the-script-ran")
	script := filepath.Join(dir, "estate.sh")
	body := "#!/usr/bin/env bash\n" +
		"touch \"" + marker + "\"\n" +
		"printf 'GAUNTLET protocol=1\\n'\n" +
		"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=1\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // test fixture script, not a secret
		t.Fatal(err)
	}
	t.Setenv("LIVECERT_SCRIPT_OVERRIDE", script)
	brokenGitOnPATH(t)

	r, _, _, err := RunLiveCert("", "unused-estate-name", "floci", "us-east-1", 5, 30)
	if err == nil {
		t.Fatalf("RunLiveCert returned no error with a git that exits 128; want a refusal (result: %+v)", r)
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("RunLiveCert error = %q; want it to quote git's own stderr", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("the estate script RAN before RunLiveCert refused - the provenance commit is still being resolved after the spend, which is the whole of #1149")
	}
}

// TestArtifactAtRevisionRefusesWhenGitCannotAnswer is #1142's audit finding
// with the largest blast radius in this package.
//
// artifactAtRevision read any `git show <rev>:live/gauntlet.json` failure as
// "the file did not exist at that commit" and returned an empty artifact
// with a nil error. MergeArtifact calls it for base, ours and theirs, and
// writes the result to live/gauntlet.json, so one unreadable object - a
// blobless clone, a corrupt blob, a git that will not start - deletes every
// estate row that side measured and still prints `merged: N of M clear`.
// The absence has to be asked about separately from the failure.
func TestArtifactAtRevisionRefusesWhenGitCannotAnswer(t *testing.T) {
	brokenGitOnPATH(t)
	a, err := artifactAtRevision(t.TempDir(), "0123456789abcdef0123456789abcdef01234567")
	if err == nil {
		t.Fatalf("artifactAtRevision returned an artifact (%d estate row(s)) and no error while git exits 128; an empty artifact here silently deletes every row the revision recorded", len(a.Estates))
	}
	if !strings.Contains(err.Error(), "Xcode license") {
		t.Errorf("artifactAtRevision error = %q; want it to quote git's own stderr", err)
	}
}

// greenfieldVerdictFn lifts greenfield_pre_apply_verdict out of
// reference-k8s-cert-manager's run.sh, from the committed file and not a
// copy of it, so a test that drives the function drives the one the estate
// would run. The extraction is deliberately literal - the exact opening
// line through the next line that is a bare "}" - and fails loudly if the
// function is renamed or reshaped, because a silently empty extraction
// would make every assertion below vacuous.
func greenfieldVerdictFn(t *testing.T) string {
	t.Helper()
	root := testRoot(t)
	path := filepath.Join(root, "live", "e2e", "reference-k8s-cert-manager", "run.sh")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("%s is not readable in this checkout: %v", path, err)
	}
	script := string(b)
	// The fail branch has to call the function, or extracting it proves
	// nothing about what the stage reports.
	if !strings.Contains(script, `gauntlet_stage greenfield fail "$(greenfield_pre_apply_verdict "$G_PRE_RC" "${#G_TARGETS[@]}" <<< "$G_PRE")"`) {
		t.Fatalf("%s no longer composes the greenfield pre-apply fail verdict through greenfield_pre_apply_verdict; this test would be driving dead code", path)
	}
	lines := strings.Split(script, "\n")
	start := -1
	for i, ln := range lines {
		if ln == "greenfield_pre_apply_verdict() {" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s does not define greenfield_pre_apply_verdict() at column 0", path)
	}
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			return strings.Join(lines[start:i+1], "\n") + "\n"
		}
	}
	t.Fatalf("%s: greenfield_pre_apply_verdict has no closing brace at column 0", path)
	return ""
}

// driveGreenfieldVerdict runs the extracted function with the given exit
// code, -target count and pre-apply output, and returns the verdict
// sentence it composes.
func driveGreenfieldVerdict(t *testing.T, rc int, targets int, preApply string) string {
	t.Helper()
	dir := t.TempDir()
	outFile := filepath.Join(dir, "pre-apply.txt")
	if err := os.WriteFile(outFile, []byte(preApply), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("set -uo pipefail\n%s\ngreenfield_pre_apply_verdict %d %d < %q\n",
		greenfieldVerdictFn(t), rc, targets, outFile)
	out, err := runBash(script)
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	return string(out)
}

// unservedRefusal is what #1097's refusal looks like on the way out of the
// renderer: a summary line of its own, then a detail wrapped at the
// terminal width behind a "│ " gutter. The wrapping is the point - a
// matcher written against the unwrapped sentence sees nothing here.
const unservedRefusal = `╷
│ Error: Kubernetes kind not served by the cluster
│
│   on custom-resources.tf line 1, in resource "kubernetes_manifest" "clusterissuer":
│    1: resource "kubernetes_manifest" "clusterissuer" {
│
│ kubernetes_manifest.clusterissuer declares kind ClusterIssuer at
│ apiVersion cert-manager.io/v1, which the cluster does not serve, so the
│ provider has no schema to plan the block against and would refuse it at
│ plan time. Install the CustomResourceDefinition whose spec.group is
│ "cert-manager.io" and spec.names.kind is "ClusterIssuer", with version
│ "v1" served, or the aggregated API that serves it, and plan again.
╵
`

// somethingElseRefusing is the shape that exposed #1204: the pre-apply
// failed for a reason that is not #1097's refusal at all. Taken from the
// issue, which quotes "Cannot import for projection" six lines above the
// verdict in the #1176 log.
const somethingElseRefusing = `kubernetes_manifest.namespace: Creating...
╷
│ Error: Cannot import for projection
│
│   on cert-manager.tf line 12, in resource "kubernetes_manifest" "crd_certificates":
│   12: resource "kubernetes_manifest" "crd_certificates" {
│
│ The instance carries an estate marker but its prior state could not be
│ built, so the projection has nothing to import.
╵
`

// TestGreenfieldVerdictNamesOnlyTheRefusalItSaw is #1204.
//
// reference-k8s-cert-manager's greenfield stage has a fail branch for a
// pre-apply that will not run, and its verdict used to name one cause -
// #1097's "Kubernetes kind not served by the cluster" - whatever the
// output said. Midway through #1176 that refusal was gone and a different
// pass was refusing, and the branch printed
//
//	refused at exit 1 with 0 x "Kubernetes kind not served by the cluster" -
//	one for each of the three custom resources, which -target EXCLUDES from
//	this apply: none named
//
// A verdict whose stated cause has a count of zero behind it is worse than
// a bare failure: it sends the next reader to the wrong place, and it goes
// on saying the same thing however the behaviour changes, because the
// sentence never depended on the count.
//
// The branch does not fire in a passing run, so no gauntlet run exercises
// it. The function is lifted out of the committed script and driven here
// with both outputs directly. Written from what a verdict promises - that
// the cause it names is one it observed - and not from the branch's
// implementation.
func TestGreenfieldVerdictNamesOnlyTheRefusalItSaw(t *testing.T) {
	t.Run("a real sighting is reported as the cause, with the blocks it named", func(t *testing.T) {
		got := driveGreenfieldVerdict(t, 1, 47, unservedRefusal+unservedRefusal)
		for _, want := range []string{
			`2 x "Kubernetes kind not served by the cluster"`,
			"#1097",
			// Named from the wrapped detail: the gutter and the line
			// break between "at" and "apiVersion" must not hide it.
			"kubernetes_manifest.clusterissuer declares kind ClusterIssuer at apiVersion cert-manager.io/v1",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("a verdict over two real #1097 refusals does not contain %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "none named") || strings.Contains(got, "does not name") {
			t.Errorf("the refusal named its block and the verdict says otherwise:\n%s", got)
		}
	})

	t.Run("zero sightings reports the absence, not the cause", func(t *testing.T) {
		got := driveGreenfieldVerdict(t, 1, 47, somethingElseRefusing)
		// The defect itself: a count of zero spoken as an observation.
		if strings.Contains(got, `0 x "Kubernetes kind not served by the cluster"`) {
			t.Errorf("the verdict states a cause it counted zero of - this is #1204:\n%s", got)
		}
		if strings.Contains(got, "none named") {
			t.Errorf("the verdict lists the blocks a refusal named when no refusal fired:\n%s", got)
		}
		if !strings.Contains(got, "DID NOT APPEAR") {
			t.Errorf("the verdict does not say the expected refusal was absent:\n%s", got)
		}
		// And it points at what actually happened.
		if !strings.Contains(got, "Error: Cannot import for projection") {
			t.Errorf("the verdict does not quote the error the pre-apply actually printed:\n%s", got)
		}
		// Still a failure. The fix is to the explanation, never to the
		// verdict: a greenfield pre-apply that will not run has failed.
		if strings.Contains(got, "passes") && !strings.Contains(got, "cold_deploy passes") {
			t.Errorf("the absence arm reads as a pass:\n%s", got)
		}
	})

	t.Run("no Error: line at all is also not a sighting", func(t *testing.T) {
		got := driveGreenfieldVerdict(t, 137, 47, "kubernetes_manifest.namespace: Creating...\nkilled\n")
		if strings.Contains(got, `0 x "Kubernetes kind not served by the cluster"`) {
			t.Errorf("the verdict states a cause it counted zero of:\n%s", got)
		}
		if !strings.Contains(got, "no Error: line at all") {
			t.Errorf("the verdict does not say the pre-apply produced no diagnostic:\n%s", got)
		}
		if !strings.Contains(got, "exited 137") {
			t.Errorf("the verdict does not report the exit code it was handed:\n%s", got)
		}
	})

	t.Run("a sighting whose blocks it cannot parse says so instead of naming none", func(t *testing.T) {
		got := driveGreenfieldVerdict(t, 1, 47,
			"╷\n│ Error: Kubernetes kind not served by the cluster\n│\n│ wording this branch does not read\n╵\n")
		if !strings.Contains(got, `1 x "Kubernetes kind not served by the cluster"`) {
			t.Errorf("the refusal fired once and the verdict does not say so:\n%s", got)
		}
		if strings.Contains(got, "none named") {
			t.Errorf("%q is a zero count spoken as an observation, the same defect one level down:\n%s", "none named", got)
		}
		if !strings.Contains(got, "does not name") {
			t.Errorf("the verdict does not say the blocks were unreadable:\n%s", got)
		}
	})
}
