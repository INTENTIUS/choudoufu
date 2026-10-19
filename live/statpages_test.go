// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/tools/statpages"
)

// TestGeneratedStatPagesAreCurrent is #2092's freshness guard, in the shape
// of tools/gauntlet's TestRenderedDocsAreCurrent: every generated span of
// every stat page under live/ (BOARD, READINESS, CLAIMS, IAM-REFERENCE,
// FORK-SURFACE, SUBSTRATES) is re-rendered in memory from the committed
// inputs, and the result must equal the committed page byte for byte.
//
// A hand edit inside a span, an input that moved without a re-render, and
// a page whose markers were damaged all fail here, naming the page and the
// command that fixes it. The text outside the markers is hand-written and
// is carried through unchanged, so editing it never fails this test.
func TestGeneratedStatPagesAreCurrent(t *testing.T) {
	root := repoRoot(t)
	pages := statpages.Pages()
	if len(pages) == 0 {
		t.Fatal("statpages.Pages() is empty; this guard is checking nothing")
	}
	for _, p := range pages {
		want, err := statpages.Render(root, p)
		if err != nil {
			t.Errorf("%s: %v", p.Path, err)
			continue
		}
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p.Path)))
		if err != nil {
			t.Errorf("reading %s: %v", p.Path, err)
			continue
		}
		if string(got) != want {
			fix := "`" + p.Command + "`"
			if p.Command != statpages.RefreshCommand {
				fix += " (or `" + statpages.RefreshCommand + "`, which refreshes every stat page)"
			}
			t.Errorf("%s is stale against %s; run %s. First difference at line %d.",
				p.Path, strings.Join(p.Inputs, ", "), fix, firstDiffLine(string(got), want))
		}
	}
}

func firstDiffLine(a, b string) int {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) && i < len(bl); i++ {
		if al[i] != bl[i] {
			return i + 1
		}
	}
	if len(al) < len(bl) {
		return len(al) + 1
	}
	return len(bl) + 1
}
