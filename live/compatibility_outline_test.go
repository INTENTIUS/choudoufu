// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"reflect"
	"regexp"
	"testing"
)

// GitHub issue #1602, ruled 2026-09-26: root live/*.md docs are
// substrate-neutral, with a section per substrate where they differ, and
// live/kubernetes/ keeps only what is Kubernetes-only. The evidence for
// #1602 was that live/kubernetes/COMPATIBILITY.md was a third the length of
// live/COMPATIBILITY.md and dropped whole sections with no word that they
// did not apply.
//
// This pin holds the documents to the same level-2 outline: every ##
// heading in live/COMPATIBILITY.md must appear, in the same order, in
// live/kubernetes/COMPATIBILITY.md and live/azure/COMPATIBILITY.md, and
// vice versa. A section that does not apply to a substrate still gets the
// heading, with a line saying so - the heading disappearing silently is
// exactly what #1602 was filed over.
var compatH2Pattern = regexp.MustCompile(`(?m)^## (.+)$`)

func compatibilityHeadings(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	matches := compatH2Pattern.FindAllStringSubmatch(string(raw), -1)
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m[1]
	}
	return out
}

func TestCompatibilityDocsShareOutline(t *testing.T) {
	root := compatibilityHeadings(t, "COMPATIBILITY.md")
	if len(root) == 0 {
		t.Fatalf("COMPATIBILITY.md: no ## headings found; the heading pattern or the file moved")
	}

	// Every platform's copy (live/<platform>/COMPATIBILITY.md) is held to the
	// root outline: Kubernetes under #1602, Azure under #1120's docs unit
	// (live/ADDING-A-PLATFORM.md, requirement 15).
	for _, path := range []string{"kubernetes/COMPATIBILITY.md", "azure/COMPATIBILITY.md"} {
		got := compatibilityHeadings(t, path)
		if len(got) == 0 {
			t.Fatalf("%s: no ## headings found; the heading pattern or the file moved", path)
		}
		if !reflect.DeepEqual(root, got) {
			t.Errorf("live/COMPATIBILITY.md and live/%s have different ## outlines.\nCOMPATIBILITY.md: %v\n%s: %v",
				path, root, path, got)
		}
	}
}
