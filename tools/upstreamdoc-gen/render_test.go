// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/newruntime"
)

// TestUpstreamPageIsCurrent holds the committed page byte for byte to what
// the generator writes from the committed gaps.json and
// live/fork-surface.json. A gap row changed, or a sync that moved the base,
// without a regenerate fails here.
func TestUpstreamPageIsCurrent(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(pageRel)))
	if err != nil {
		t.Fatalf("reading %s: %v", pageRel, err)
	}
	want, err := renderPage(root, string(doc))
	if err != nil {
		t.Fatalf("rendering %s: %v", pageRel, err)
	}
	if want != string(doc) {
		for _, span := range []string{spanBase, spanGaps} {
			got, _ := markers.Content(pageRel, string(doc), span)
			exp, _ := markers.Content(pageRel, want, span)
			if got != exp {
				t.Errorf("%s's %q span is out of date with its source.\n--- committed ---\n%s--- rendered ---\n%s", pageRel, span, got, exp)
			}
		}
		t.Errorf("%s differs from its rendered form; run `go run ./tools/upstreamdoc-gen` and commit the result", pageRel)
	}
}

// TestRenderGapsNamesEveryRow proves the gaps span is drawn from the whole
// table: every row id in gaps.json appears in it, so a check kind the
// renderer does not place cannot drop a row silently.
func TestRenderGapsNamesEveryRow(t *testing.T) {
	body := renderGaps(newruntime.Rows())
	rows := newruntime.Rows()
	if len(rows) == 0 {
		t.Fatal("gaps.json has no rows; this test would check nothing")
	}
	for _, r := range rows {
		if !strings.Contains(body, "| `"+r.ID+"` |") {
			t.Errorf("the rendered gap tables do not name row %q (check %q)", r.ID, r.Check)
		}
		for _, u := range r.Upstream {
			if !strings.Contains(body, "https://github.com/opentofu/opentofu/issues/"+u[1:]) {
				t.Errorf("row %q: upstream %s is not linked to opentofu/opentofu", r.ID, u)
			}
		}
	}
}
