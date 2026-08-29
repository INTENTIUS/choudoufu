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

// TestSpansAreCurrent is the drift guard tools/limits-gen's render_test.go
// carries over live/LIMITATIONS.md, applied to this generator's own span.
//
// It renders straight from the already-committed live/tag-verbs.json
// artifact, not from a fresh botocore fetch: the artifact is what
// site/content/docs/use/reference.md's span is supposed to reflect, and
// that comparison needs no network to be meaningful. A regeneration that
// changes the artifact (tagverbs-gen's own `go run ./tools/tagverbs-gen`,
// which does need network) is what this test would then catch as a second,
// separate drift.
func TestSpansAreCurrent(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	doc, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(referenceMDRel)))
	if err != nil {
		t.Fatalf("reading %s: %s", referenceMDRel, err)
	}

	rows := readArtifactForTest(t, root)
	want := renderTagVerbTable(rows)

	got, err := markers.Content(referenceMDRel, string(doc), spanTagVerbs)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != want {
		t.Errorf("%s's %q span is stale; run `just tagverbs` and commit the result.\n--- committed ---\n%s--- rendered ---\n%s",
			referenceMDRel, spanTagVerbs, got, want)
	}

	wantTotal := renderTagVerbTotal(rows)
	gotTotal, err := markers.ContentInline(referenceMDRel, string(doc), spanTagVerbsTotal)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if gotTotal != wantTotal {
		t.Errorf("%s's %q span is stale (committed %q, rendered %q); run `just tagverbs` and commit the result",
			referenceMDRel, spanTagVerbsTotal, gotTotal, wantTotal)
	}

	// The whole-file check catches what the per-span ones cannot: a marker
	// pair going missing or duplicated, or the two spans' replacements
	// interfering with each other.
	out, err := applyTagVerbSpans(string(doc), rows)
	if err != nil {
		t.Fatalf("rendering %s: %v", referenceMDRel, err)
	}
	if out != string(doc) {
		t.Errorf("%s differs from its rendered form; run `just tagverbs` and commit the result", referenceMDRel)
	}
}

// readArtifactForTest loads live/tag-verbs.json's rows the way the render
// path receives them from a live sweep, without doing the sweep itself.
func readArtifactForTest(t *testing.T, root string) []Row {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tagVerbsJSONRel)))
	if err != nil {
		t.Fatalf("reading %s: %s", tagVerbsJSONRel, err)
	}
	var art Artifact
	if err := json.Unmarshal(data, &art); err != nil {
		t.Fatalf("parsing %s: %s", tagVerbsJSONRel, err)
	}
	return art.Rows
}
