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

	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/docsref"
)

// TestSpansAreCurrent is the drift guard, the same one tools/survey-gen has
// over its own spans in the same document.
//
// A generated document is only as good as the thing that fails when it is
// stale. Without this, a refusal added to any of the three registries would
// leave live/LIMITATIONS.md describing the previous set, and nothing would
// say so until somebody hit the new one.
func TestSpansAreCurrent(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	md, freq, measured := readForTest(t, root)
	catalog := check.AllRefusals()

	for _, span := range []struct {
		name, want string
	}{
		{spanTable, renderTable(catalog, freq, measured)},
		{spanEntries, renderEntries(catalog, freq, measured)},
	} {
		got, err := markers.Content(limitationsRel, md, span.name)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if got != span.want {
			t.Errorf("%s's %q span is stale; run `just limits` and commit the result.\n--- committed ---\n%s--- rendered ---\n%s",
				limitationsRel, span.name, got, span.want)
		}
	}

	// The whole-file check catches what the per-span one cannot: a marker
	// pair going missing or duplicated.
	out, err := markers.Replace(limitationsRel, md, spanTable, renderTable(catalog, freq, measured))
	if err != nil {
		t.Fatalf("rendering %s: %v", limitationsRel, err)
	}
	out, err = markers.Replace(limitationsRel, out, spanEntries, renderEntries(catalog, freq, measured))
	if err != nil {
		t.Fatalf("rendering %s: %v", limitationsRel, err)
	}
	if out != md {
		t.Errorf("%s differs from its rendered form; run `just limits` and commit the result", limitationsRel)
	}
}

// TestEveryGeneratedEntryHasContent guards the failure mode that shipped in
// the first draft of this generator: a heading with an empty **What.** under
// it.
//
// A lint rule's DocsRef can coincide with the heading this generator would
// write for it - "unadmitted-type" is both a rule name and a hand-written
// entry's heading - and lint carries no per-rule What, so the coincidence
// produced a second entry for that rule with nothing in it. [ownsEntry]
// excludes lint outright now; this is what would notice if that stopped
// being true.
func TestEveryGeneratedEntryHasContent(t *testing.T) {
	for _, r := range check.AllRefusals() {
		if !ownsEntry(r) {
			continue
		}
		if strings.TrimSpace(r.What) == "" {
			t.Errorf("%s/%s gets a generated entry with no What, which renders as a heading with nothing under it", r.Layer, r.ID)
		}
		if r.RaisedBy == "" {
			t.Errorf("%s/%s gets a generated entry naming no raising package", r.Layer, r.ID)
		}
	}
}

// TestGeneratedHeadingsMatchTheRefusalsThatCiteThem is the other half of
// internal/live/check's TestEveryRefusalDocsRefIsResolvable, checked here
// against the render rather than against the committed file: what this
// generator writes as a heading has to be exactly what the refusal's own
// DocsRef parses to.
//
// The two tests fail together in the ordinary case and separately in the
// interesting one - this fails if the derivation drifts, that one fails if
// the generator has not been run.
func TestGeneratedHeadingsMatchTheRefusalsThatCiteThem(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	_, freq, measured := readForTest(t, root)

	rendered := docsref.Headings(renderEntries(check.AllRefusals(), freq, measured))
	for _, r := range check.AllRefusals() {
		if !ownsEntry(r) {
			continue
		}
		ref, err := docsref.Parse(r.DocsRef)
		if err != nil {
			t.Errorf("%s/%s: %s", r.Layer, r.ID, err)
			continue
		}
		for _, heading := range ref.Headings {
			if !rendered[heading] {
				t.Errorf("%s/%s cites the heading %q, which this generator does not write", r.Layer, r.ID, heading)
			}
		}
	}
}

func readForTest(t *testing.T, root string) (md string, freq map[string]frequency, measured bool) {
	t.Helper()

	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(limitationsRel)))
	if err != nil {
		t.Fatalf("reading %s: %s", limitationsRel, err)
	}
	freq, measured, err = readCorpus(filepath.Join(root, corpusRel))
	if err != nil {
		t.Fatalf("reading %s: %s", corpusRel, err)
	}
	return string(src), freq, measured
}
