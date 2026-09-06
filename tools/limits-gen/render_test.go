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

	entries, err := renderEntries(catalog, freq, measured)
	if err != nil {
		t.Fatalf("rendering the %s span: %v", spanEntries, err)
	}
	roster, err := renderRoster(catalog, root)
	if err != nil {
		t.Fatalf("rendering the %s span: %v", spanRoster, err)
	}

	for _, span := range []struct {
		name, want string
	}{
		{spanTable, renderTable(catalog, freq, measured)},
		{spanEntries, entries},
		{spanRoster, roster},
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
	out, err = markers.Replace(limitationsRel, out, spanEntries, entries)
	if err != nil {
		t.Fatalf("rendering %s: %v", limitationsRel, err)
	}
	out, err = markers.Replace(limitationsRel, out, spanRoster, roster)
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
// produced a second entry for that rule with nothing in it.
// [hasGeneratedEntry] excludes lint outright now; this is what would notice
// if that stopped being true.
//
// Since #698 the emptiness itself is the render's business rather than this
// test's, so this asserts the render's own verdict: nothing missing today,
// and TestRenderRefusesARefusalWithNoDescription proves the verdict can go
// the other way.
func TestEveryGeneratedEntryHasContent(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	_, freq, measured := readForTest(t, root)
	if _, err := renderEntries(check.AllRefusals(), freq, measured); err != nil {
		t.Errorf("the entries span does not render: %v", err)
	}

	// The same statement made directly, so a failure says which refusal
	// rather than only that the render stopped at the first one.
	for _, r := range check.AllRefusals() {
		if !hasGeneratedEntry(r) {
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

// TestRenderRefusesARefusalWithNoDescription proves the render's own guard
// can fail, which is the only thing that makes its passing worth anything.
//
// The acceptance criterion #698 was opened with is "a refusal added without a
// doc string fails the render". Asserting that over the committed catalog
// proves only that today's catalog is complete; it would go on passing if the
// guard were deleted. So this feeds the renderer a catalog with the fault in
// it and requires the error.
func TestRenderRefusesARefusalWithNoDescription(t *testing.T) {
	catalog := check.AllRefusals()

	var sample check.Refusal
	for _, r := range catalog {
		if hasGeneratedEntry(r) {
			sample = r
			break
		}
	}
	if sample.ID == "" {
		t.Fatal("no refusal in the catalog gets a generated entry; this test has nothing to break")
	}

	for _, tc := range []struct {
		name  string
		spoil func(check.Refusal) check.Refusal
		want  string
	}{
		{"no What", func(r check.Refusal) check.Refusal { r.What = "   "; return r }, "has no What"},
		{"no RaisedBy", func(r check.Refusal) check.Refusal { r.RaisedBy = ""; return r }, "names no raising package"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spoiled := append([]check.Refusal(nil), catalog...)
			for i := range spoiled {
				if spoiled[i].Layer == sample.Layer && spoiled[i].ID == sample.ID {
					spoiled[i] = tc.spoil(spoiled[i])
				}
			}
			_, err := renderEntries(spoiled, nil, false)
			if err == nil {
				t.Fatalf("renderEntries accepted a catalog whose %s/%s %s; the render would write a heading with nothing under it",
					sample.Layer, sample.ID, tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("renderEntries failed for the wrong reason: %v", err)
			}
		})
	}
}

// TestRosterRefusesAnEntryWithNoFixture proves the roster's other half can
// fail: a lint rule documented at a heading in this document with no fixture
// directory to pin that entry's construct.
//
// The rename is the realistic fault. live/e2e/limits/<heading>/ and the
// "### <heading>" line are two spellings of one fact, and
// internal/live/lint's TestLimitationsDocCoversDirs holds the doc side; this
// holds the rule side, at render time.
func TestRosterRefusesAnEntryWithNoFixture(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	catalog := check.AllRefusals()
	spoiled := append([]check.Refusal(nil), catalog...)
	spoiled = append(spoiled, check.Refusal{
		Layer:    check.LayerLint,
		ID:       "no-such-rule",
		Title:    "A rule whose fixture directory was renamed",
		DocsRef:  `live/LIMITATIONS.md, "no-such-heading"`,
		RaisedBy: check.RaisedByLint,
	})
	if _, err := renderRoster(spoiled, root); err == nil {
		t.Fatal("renderRoster accepted a lint rule with no fixture directory; the roster would print a path that does not exist")
	}

	// And the summary half.
	spoiled = append([]check.Refusal(nil), catalog...)
	for i := range spoiled {
		if spoiled[i].RaisedBy == check.RaisedByLint {
			spoiled[i].Title = ""
			break
		}
	}
	if _, err := renderRoster(spoiled, root); err == nil {
		t.Fatal("renderRoster accepted a lint rule with no summary; its row would name the rule and say nothing")
	}
}

// TestVerifyCoverageRefusesAnUnwrittenEntry proves the third render-time
// guard: a refusal that cites a heading in this document which nobody wrote.
//
// This is the shape a lint rule added with no entry has, and the shape
// internal/live/check's TestEveryRefusalDocsRefIsResolvable catches over the
// committed file. Catching it in the render is what stops the broken
// document being written in the first place.
func TestVerifyCoverageRefusesAnUnwrittenEntry(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	md, _, _ := readForTest(t, root)

	if err := verifyCoverage(check.AllRefusals(), md); err != nil {
		t.Errorf("the committed %s does not cover its own catalog: %v", limitationsRel, err)
	}

	spoiled := append([]check.Refusal(nil), check.AllRefusals()...)
	spoiled = append(spoiled, check.Refusal{
		Layer:    check.LayerLint,
		ID:       "no-such-rule",
		Title:    "A rule nobody wrote an entry for",
		DocsRef:  `live/LIMITATIONS.md, "no-such-heading"`,
		RaisedBy: check.RaisedByLint,
	})
	if err := verifyCoverage(spoiled, md); err == nil {
		t.Fatal("verifyCoverage accepted a refusal citing a heading nobody wrote")
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

	entries, err := renderEntries(check.AllRefusals(), freq, measured)
	if err != nil {
		t.Fatalf("rendering the %s span: %v", spanEntries, err)
	}
	rendered := docsref.Headings(entries)
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
