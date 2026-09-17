// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
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

// TestSaveScaleArtifactPublishesTheSiteCopy is #1187's prevention.
//
// live/gauntlet-scale.json has four producers - live-cert, scale-backfill,
// scale-import-slice and scale-patch-seconds - and its published copy used
// to be written by exactly one unrelated command, `gauntlet render`. After
// the scale-128 certification the two files disagreed by one record: the
// new one. Writing both from the same call is what makes that impossible to
// forget.
func TestSaveScaleArtifactPublishesTheSiteCopy(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	sa := &ScaleArtifact{Schema: ScaleRecordSchema}
	sa.UpsertScaleRecord(ScaleRecord{
		Schema:    ScaleRecordSchema,
		Estate:    "terralith-scale",
		Target:    "aws",
		Scale:     128,
		Commit:    "0123456789abcdef0123456789abcdef01234567",
		Resources: &ScaleResources{Total: 3705},
		Source:    "a test, not a run",
	})
	if err := SaveScaleArtifact(root, sa); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(root, ScaleRecordsPath))
	if err != nil {
		t.Fatal(err)
	}
	site, err := os.ReadFile(filepath.Join(root, SiteScalePath))
	if err != nil {
		t.Fatalf("%s was not written: %v - a scale record that is saved but never published is the #1187 defect", SiteScalePath, err)
	}
	if string(src) != string(site) {
		t.Errorf("%s and %s differ; the published copy is a byte-for-byte copy of the source by definition", ScaleRecordsPath, SiteScalePath)
	}
}

// TestStaleFilesCatchesAStaleSiteScaleCopy is #1187's catch: the guard that
// fires when the two files disagree, whatever produced the disagreement.
//
// Prevention and catch are both here on purpose. The first stops the one
// producer that was known to be missing the step; the second is what a
// future producer inherits, and is the half that generalises.
//
// It builds a self-consistent checkout in a temp directory rather than
// touching the real one, so the RED arm - dropping a record from the
// published copy only - can be exercised without ever writing to the tree
// under test.
func TestStaleFilesCatchesAStaleSiteScaleCopy(t *testing.T) {
	root := testRoot(t)
	tmp := t.TempDir()
	for _, rel := range []string{
		ManifestPath, ArtifactPath, BehaviorIndexPath, TypeIndexPath,
		OracleVersionsPin, ScaleRecordsPath, "live/floci-image",
	} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Skipf("%s is not readable in this checkout: %v", rel, err)
		}
		dst := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil { //nolint:gosec // a temp copy of a committed artifact
			t.Fatal(err)
		}
	}

	// Render once so every generated file in tmp is current; StaleFiles
	// must then find nothing, which is the control this test needs before
	// its RED arm means anything.
	m, a, err := loadAll(tmp)
	if err != nil {
		t.Fatal(err)
	}
	tt, err := LoadTypeIndexTotals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	scale, err := loadScaleRecordsBytes(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if scale == nil {
		t.Fatalf("%s is empty in this checkout, so there is nothing to publish and nothing to compare", ScaleRecordsPath)
	}
	if _, err := Render(tmp, m, a, tt, scale); err != nil {
		t.Fatal(err)
	}
	stale, err := StaleFiles(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) > 0 {
		t.Fatalf("a freshly rendered tree already reads stale (%s); the RED arm below would prove nothing", strings.Join(stale, ", "))
	}

	// RED arm: revert exactly one record from the PUBLISHED copy, leaving
	// the source artifact alone - the shape #1187 found by hand.
	sa, err := LoadScaleArtifact(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(sa.Records) < 2 {
		t.Skipf("%s holds %d record(s); this guard needs at least two to drop one", ScaleRecordsPath, len(sa.Records))
	}
	sa.Records = sa.Records[:len(sa.Records)-1]
	b, err := json.MarshalIndent(sa, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, filepath.FromSlash(SiteScalePath)), append(b, '\n'), 0o644); err != nil { //nolint:gosec // a temp copy of a committed artifact
		t.Fatal(err)
	}

	stale, err = StaleFiles(tmp)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rel := range stale {
		if rel == SiteScalePath {
			found = true
		}
	}
	if !found {
		t.Errorf("%s is short one record and `gauntlet check` did not name it (stale = %v) - a published scale point can go missing with nothing saying so", SiteScalePath, stale)
	}
}
