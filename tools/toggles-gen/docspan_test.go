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

	"github.com/intentius/choudoufu/internal/live/strict"
)

// TestSpansAreCurrent is tools/tagverbs-gen/docspan_test.go's drift guard,
// applied to this generator's own span. Unlike that one, it needs no
// artifact read: [strict.Toggles] is the already-committed source this
// span is supposed to reflect, so the comparison below is a pure
// in-process render.
func TestSpansAreCurrent(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	doc, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(referenceMDRel)))
	if err != nil {
		t.Fatalf("reading %s: %s", referenceMDRel, err)
	}

	want := renderToggleTable(strict.Toggles)

	got, err := markers.Content(referenceMDRel, string(doc), spanStrictTable)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != want {
		t.Errorf("%s's %q span is stale; run `just toggles` and commit the result.\n--- committed ---\n%s--- rendered ---\n%s",
			referenceMDRel, spanStrictTable, got, want)
	}

	// The whole-file check catches what the per-span one cannot: the
	// marker pair itself going missing or duplicated.
	out, err := markers.Replace(referenceMDRel, string(doc), spanStrictTable, want)
	if err != nil {
		t.Fatalf("rendering %s: %v", referenceMDRel, err)
	}
	if out != string(doc) {
		t.Errorf("%s differs from its rendered form; run `just toggles` and commit the result", referenceMDRel)
	}
}

// TestRenderToggleTableNamesEveryToggle proves the table this generator
// writes is not a static string that happens to match [strict.Toggles]
// today: it renders every toggle currently registered, by Name, so a
// registry entry added without updating the table (or a row removed by
// hand from the doc) shows up here rather than only in the drift guard's
// whole-file diff. Made to fail on purpose while writing this test, by
// checking for a name not in [strict.Toggles]: it failed with a "does not
// mention" error, as expected.
func TestRenderToggleTableNamesEveryToggle(t *testing.T) {
	body := renderToggleTable(strict.Toggles)
	for _, tg := range strict.Toggles {
		if !strings.Contains(body, "`"+tg.Name+"`") {
			t.Errorf("rendered table does not mention toggle %q", tg.Name)
		}
		for _, v := range tg.Values {
			want := "`\"" + v + "\"`"
			if !strings.Contains(body, want) {
				t.Errorf("rendered table for %q does not contain declared value %s", tg.Name, want)
			}
		}
	}
	if strings.Contains(body, `"report"`) {
		t.Error(`rendered table mentions "report" - marker_repair's Values deliberately excludes it ` +
			`(see internal/live/strict/registry.go), and this table must not reintroduce it by drifting from Values`)
	}
}
