// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestBucketsArtifactMatchesCommitted recomputes the bucket counts from the
// committed evidence artifacts and holds live/rowgen-buckets.json to them -
// the external-source drift pattern TestMismatchLedgerMatchesCommitted
// uses, so a classifier change that moves a bucket cannot leave the artifact
// (and the COVERAGE.md span rendered from it) silently stale.
func TestBucketsArtifactMatchesCommitted(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := loadProposals(root)
	if err != nil {
		t.Fatal(err)
	}
	want := bucketsFromProposals(proposals)

	raw, err := os.ReadFile(filepath.Join(root, bucketsJSONRel)) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v (run `go run ./tools/row-gen -emit` and commit it)", bucketsJSONRel, err)
	}
	var got bucketsArtifact
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parsing %s: %v", bucketsJSONRel, err)
	}
	if got != want {
		t.Errorf("%s is stale; run `go run ./tools/row-gen -emit` and commit it.\ncommitted: %+v\nrecomputed: %+v", bucketsJSONRel, got, want)
	}
}
