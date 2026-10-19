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
// Every diagram the docs site renders (site/src/assets/*.svg, imported by
// the pages that show it) has a copy under docs/diagrams/, which the repo's
// own markdown links (live/IDENTITY.md among them). Nothing writes either
// side; both are edited by hand. They drifted once: three of the five
// docs/diagrams copies stayed at b1290968bd while the site copies moved on,
// and diagram-values.svg ended up saying secret-generating resources are
// refused by default, the opposite of strict.DefaultSecrets
// (internal/live/strict/strict.go). The site copy is the source; edit it,
// then copy it over the docs one.
//
// It read site/assets/ until the Hugo site was retired (#2092). Its sibling
// TestLogoCopiesMatchRender went then too: the logo is rendered straight
// into site/public/ by scripts/brand/render.sh, and no copy of it is left
// to drift.
func TestDiagramCopiesMatchSite(t *testing.T) {
	root := repoRoot(t)
	sources, err := filepath.Glob(filepath.Join(root, "site", "src", "assets", "diagram-*.svg"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(sources)
	if len(sources) == 0 {
		t.Fatal("found no site/src/assets/diagram-*.svg; this guard is seeing nothing. If the site " +
			"stopped carrying diagrams on purpose, delete this test in the same change")
	}
	for _, src := range sources {
		name := filepath.Base(src)
		assertSameBytes(t, root, "site/src/assets/"+name, "docs/diagrams/"+name)
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
