// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// GAUNTLET.md states two counts in prose: how many stages a full estate runs,
// and how many per-estate sections a tier-1 gated stage would otherwise need.
// Both were literals in renderSpec ("fourteen", "26"), so TestRenderedDocsAreCurrent
// could not notice them going stale: adding a stage re-renders the headings
// and the literal side by side (#1249). They are now derived from Stages()
// and the manifest; these tests hold that, and hold the committed file to its
// own headings.

var (
	reSpecStageCount   = regexp.MustCompile(`a full estate's (\S+) stages`)
	reSpecEstateCount  = regexp.MustCompile(`rather than on (\S+) hand-written per-estate sections`)
	reSpecStageHeading = regexp.MustCompile(`(?m)^### \d+\. .*\(` + "`" + `[a-z0-9_]+` + "`" + `, `)
)

// specFigure pulls the single capture a pattern names out of a rendered spec,
// with line breaks folded to spaces so a wrapped phrase still matches.
func specFigure(t *testing.T, doc string, re *regexp.Regexp) string {
	t.Helper()
	flat := strings.Join(strings.Fields(doc), " ")
	all := re.FindAllStringSubmatch(flat, -1)
	if len(all) != 1 {
		t.Fatalf("expected exactly one %q in the spec, found %d; the sentence moved, so nothing was checked", re, len(all))
	}
	return all[0][1]
}

// TestSpecFiguresFollowTheirSources renders the spec with one more stage than
// the registry holds and the kind test manifest, and requires both figures to
// move with them. A literal in renderSpec cannot pass this.
func TestSpecFiguresFollowTheirSources(t *testing.T) {
	stagesSource = func() []Stage {
		out := registeredStages()
		extra := out[len(out)-1]
		extra.ID, extra.Order, extra.Title = "planted_extra", extra.Order+1, "Planted extra stage"
		return append(out, extra)
	}
	t.Cleanup(func() { stagesSource = registeredStages })

	m := kindManifest()
	a := &Artifact{}
	a.Rebuild(m, &BehaviorIndex{}, "img", OracleVersions{}, ProviderVersions{}, "", "")
	doc := renderSpec(m, a, TypeIndexTotals{})

	if got, want := specFigure(t, doc, reSpecStageCount), strconv.Itoa(len(Stages())); got != want {
		t.Errorf("the spec says %q for a full estate's stages; the registry it was rendered from holds %s", "a full estate's "+got+" stages", want)
	}
	if got, want := specFigure(t, doc, reSpecEstateCount), strconv.Itoa(len(m.Estates)); got != want {
		t.Errorf("the spec says %q per-estate sections; the manifest it was rendered from holds %s estates", got, want)
	}
}

// TestCommittedSpecFiguresAgreeWithItsHeadings reads only the committed
// live/GAUNTLET.md: the stated stage count must equal the number of numbered
// stage headings in the same file. TestRenderedDocsAreCurrent answers "does
// the file match the code"; this answers "does the file agree with itself",
// which a hand merge can break without touching the code.
func TestCommittedSpecFiguresAgreeWithItsHeadings(t *testing.T) {
	root := testRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "live", "GAUNTLET.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range checkSpecStageCount(t, string(src)) {
		t.Error(f)
	}
}

func checkSpecStageCount(t *testing.T, doc string) []string {
	t.Helper()
	headings := len(reSpecStageHeading.FindAllString(doc, -1))
	if headings == 0 {
		t.Fatalf("no numbered stage headings found in GAUNTLET.md; the heading shape changed, so nothing was counted")
	}
	stated := specFigure(t, doc, reSpecStageCount)
	if stated != strconv.Itoa(headings) {
		return []string{"GAUNTLET.md says \"a full estate's " + stated + " stages\" over " + strconv.Itoa(headings) + " numbered stage headings"}
	}
	return nil
}

// TestCommittedSpecStageCountCanFail is the red arm: one heading removed from
// a copy of the committed file must produce a finding.
func TestCommittedSpecStageCountCanFail(t *testing.T) {
	root := testRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "live", "GAUNTLET.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(src)
	loc := reSpecStageHeading.FindStringIndex(doc)
	if loc == nil {
		t.Fatal("no stage heading to remove")
	}
	mutated := doc[:loc[0]] + "#### demoted " + doc[loc[0]+len("### "):]
	findings := checkSpecStageCount(t, mutated)
	if len(findings) == 0 {
		t.Fatal("a spec with one stage heading fewer than its stated count passed; the check cannot fail")
	}
	t.Logf("red as required: %s", findings[0])
}
