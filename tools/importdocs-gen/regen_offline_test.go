// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestArtifactMatchesOfflineRegeneration is the external-source check
// separatordrift_test.go and exclusive_test.go do not have.
//
// Both of those pin a hand-maintained count against live/import-grammar.json
// as it sits on disk today. That catches a scrape change that moves the
// count, but it cannot catch the shape issue #169 found in
// live/survey-full.json and this repo's own drift_test.go now documents by
// name: the artifact and the hand expectation can both go stale together,
// in lockstep, and nothing here would notice, because neither side is ever
// compared against anything the code would actually produce today.
//
// This test is that comparison. tools/importdocs-gen needs network only for
// a doc this machine has never fetched (main.go's own doc comment: "It
// needs network for the first fetch of each doc, or a warm cache"), and the
// cache under the OS user cache directory is complete for the pinned
// provider release - 1699 files, one per live/survey-full.json roster
// entry - so sweep can run for real, offline, and its output can be diffed
// against the committed artifact byte for byte.
//
// Where that cache is absent (a fresh checkout, most CI runners), this
// skips rather than reaching for the network itself: the fetcher passed in
// below fails loudly on any cache miss instead of dialing out, so a
// contributor who runs this with an incomplete cache gets a clear "which
// type is missing its cached doc" rather than a silent, slow network sweep
// or a false pass.
func TestArtifactMatchesOfflineRegeneration(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}

	cacheDir, err := defaultCacheDir()
	if err != nil {
		t.Fatalf("resolving the doc cache directory: %v", err)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) == 0 {
		t.Skipf("offline doc cache not present at %s (see tools/importdocs-gen/main.go's doc comment) - "+
			"this guard only runs where the cache is warm", cacheDir)
	}

	roster, err := loadRoster(filepath.Join(root, rosterJSONRel))
	if err != nil {
		t.Fatalf("loading the roster: %v", err)
	}

	noNetwork := func(_ context.Context, url string) (int, []byte, error) {
		return 0, nil, errors.New("network fetch attempted during the offline regeneration guard for " + url +
			" - the doc cache is missing an entry TestArtifactMatchesOfflineRegeneration expected to be warm; " +
			"populate it (see tools/importdocs-gen/main.go) rather than letting this test reach the network")
	}

	rows, docsFound, docsMissing, aliasRows, err := sweep(context.Background(), cacheDir, roster, noNetwork)
	if err != nil {
		t.Fatalf("offline sweep: %v", err)
	}
	art := buildArtifact(rows, len(roster), docsFound, docsMissing, aliasRows)
	got, err := art.marshal()
	if err != nil {
		t.Fatalf("marshaling the regenerated artifact: %v", err)
	}

	want, err := os.ReadFile(filepath.Join(root, importGrammarJSONRel)) //nolint:gosec // fixed path in the checkout
	if err != nil {
		t.Fatalf("reading the committed artifact: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("live/import-grammar.json does not match what today's importdocs-gen produces from the same "+
			"offline doc cache and the same live/survey-full.json roster - regenerate it (`just importdocs`, "+
			"or `go run ./tools/importdocs-gen`) and commit the diff, or explain in the commit why the "+
			"committed artifact should stay ahead of the code that builds it.\n"+
			"regenerated is %d bytes, committed is %d bytes", len(got), len(want))
	}
}
