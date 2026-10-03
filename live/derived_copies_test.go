// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestDiagramCopiesMatchSite is issue #1222's second guard.
//
// Every diagram under docs/diagrams/ is a copy of the one the docs site
// renders from site/assets/ (site/layouts/_markup/render-image.html resolves
// against site/assets first). Nothing writes either side; both are edited by
// hand. They drifted once: three of the five docs/diagrams copies stayed at
// b1290968bd while the site copies moved on, and diagram-values.svg ended up
// saying secret-generating resources are refused by default, the opposite of
// strict.DefaultSecrets (internal/live/strict/strict.go). The site copy is
// the source; edit it, then copy it over the docs one.
func TestDiagramCopiesMatchSite(t *testing.T) {
	root := repoRoot(t)
	copies, err := filepath.Glob(filepath.Join(root, "docs", "diagrams", "*.svg"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(copies)
	if len(copies) == 0 {
		t.Fatal("found no docs/diagrams/*.svg; this guard is seeing nothing. If the directory was " +
			"removed on purpose, delete this test in the same change")
	}
	for _, c := range copies {
		name := filepath.Base(c)
		assertSameBytes(t, root, "site/assets/"+name, "docs/diagrams/"+name)
	}
}

// TestLogoCopiesMatchRender is issue #1222's third guard.
//
// scripts/render-logo.sh renders the logo PNGs into docs/images/ and copies
// them on to the site. site/assets/ carries two of them, placed there by
// hand once; the script now copies them too, and this test holds the copies
// to the rendered originals whether or not the script was re-run.
func TestLogoCopiesMatchRender(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"choudoufu-hero.png", "choudoufu-inline-64.png"} {
		assertSameBytes(t, root, "docs/images/"+name, "site/assets/"+name)
	}
}

func assertSameBytes(t *testing.T, root, source, copy string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source)))
	if err != nil {
		t.Errorf("reading %s (the source of %s): %v", source, copy, err)
		return
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(copy)))
	if err != nil {
		t.Errorf("reading %s (a copy of %s): %v", copy, source, err)
		return
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from its source %s; copy the source over it (cp %s %s)", copy, source, source, copy)
	}
}
