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

// TestUpstreamPageIsCurrent holds both outputs byte for byte to what the
// generator writes from the committed gaps.json and live/fork-surface.json:
// the site page's base span and live/NEW-RUNTIME.md. A gap row changed, or a
// sync that moved the base, without a regenerate fails here.
func TestUpstreamPageIsCurrent(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := loadForkSurface(root)
	if err != nil {
		t.Fatal(err)
	}
	const fix = "run `go run ./tools/upstreamdoc-gen` and commit the result"

	page, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(pageRel)))
	if err != nil {
		t.Fatalf("reading %s: %v", pageRel, err)
	}
	want, err := renderPage(fs, string(page))
	if err != nil {
		t.Fatalf("rendering %s: %v", pageRel, err)
	}
	if want != string(page) {
		got, _ := markers.Content(pageRel, string(page), spanBase)
		exp, _ := markers.Content(pageRel, want, spanBase)
		t.Errorf("%s's %q span is out of date.\n--- committed ---\n%s--- rendered ---\n%s%s", pageRel, spanBase, got, exp, fix)
	}

	doc, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(repoDocRel)))
	if err != nil {
		t.Fatalf("reading %s: %v (%s)", repoDocRel, err, fix)
	}
	if string(doc) != renderRepoDoc(fs, newruntime.Rows()) {
		t.Errorf("%s differs from its rendered form; %s", repoDocRel, fix)
	}
}

// TestRenderGapsNamesEveryRow proves live/NEW-RUNTIME.md is drawn from the
// whole table: every row id in gaps.json appears in it, so a check kind the
// renderer does not place cannot drop a row silently.
func TestRenderGapsNamesEveryRow(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(repoDocRel)))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	rows := newruntime.Rows()
	if len(rows) == 0 {
		t.Fatal("gaps.json has no rows; this test would check nothing")
	}
	for _, r := range rows {
		if !strings.Contains(body, "| `"+r.ID+"` |") {
			t.Errorf("%s does not name row %q (check %q)", repoDocRel, r.ID, r.Check)
		}
		for _, u := range r.Upstream {
			if !strings.Contains(body, "https://github.com/opentofu/opentofu/issues/"+u[1:]) {
				t.Errorf("%s: row %q's upstream %s is not linked to opentofu/opentofu", repoDocRel, r.ID, u)
			}
		}
	}
}
