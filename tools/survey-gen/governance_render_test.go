// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// governanceSweepFloor is the anti-tamper leg, in the spirit of
// derivation_guard_test.go's typeLiteralSweepFloor.
//
// Every figure this file checks can be made to agree by measuring less: skip
// a type, narrow the artifact read, treat an absent survey row as taggable,
// and the span and the test move together while the document's claim gets
// quietly weaker. This is the number of admitted AWS resource types the
// derivation must actually visit. It is not allowed to fall silently; if the
// admission table genuinely shrinks, lower it in the commit that shrank it.
const governanceSweepFloor = 800

// TestMarkersGovernanceSpansMatchTheArtifacts is the freshness guard.
//
// Both spans in live/MARKERS.md's "Granting an estate" section are rendered
// by this generator from committed artifacts, and the generator runs when a
// person runs it. Regenerating live/survey-full.json or ratifying a batch
// into the admission table without re-rendering leaves the document stating
// a governance scope the artifacts no longer support - and this section is
// precisely where an overstated scope costs something, because a reader
// writes an IAM policy from it.
func TestMarkersGovernanceSpansMatchTheArtifacts(t *testing.T) {
	root := repoRootFromTest(t)

	split, err := deriveGovernanceSplit(root)
	if err != nil {
		t.Fatalf("deriving the split: %v", err)
	}
	md := readMarkersMD(t, root)

	for _, span := range []struct {
		name string
		want string
	}{
		{spanGovernableCount, renderGovernableCount(split)},
		{spanGovernableGap, renderGovernableGap(split)},
	} {
		shipped, err := spanContent(markersMDRel, md, span.name)
		if err != nil {
			// The inline span's markers carry no newline, so spanContent's
			// block form misses it; fall back to a marker-pair read.
			var ok bool
			shipped, ok = inlineSpanContent(md, span.name)
			if !ok {
				t.Fatalf("%s no longer carries the %q span: %v.\n"+
					"It is generated content; if the section moved, the markers move with it.",
					markersMDRel, span.name, err)
			}
		}
		if strings.TrimSpace(shipped) != strings.TrimSpace(span.want) {
			t.Errorf("%s's %q span is stale.\n\nshipped:\n%s\n\nre-rendered:\n%s\n\n"+
				"Run `just survey-render` rather than editing the figure.",
				markersMDRel, span.name, shipped, span.want)
		}
	}
}

// TestGovernanceSplitAgreesWithAnIndependentRead defeats the shape where the
// span and its guard are the same derivation twice.
//
// TestMarkersGovernanceSpansMatchTheArtifacts would pass just as happily if
// untaggableAdmittedTypes returned nothing at all, because it re-renders
// through the same helper the generator used. So this leg recounts the
// untaggable-admitted set out of live/survey-full.json with its own decode
// and its own join, and compares the two.
//
// Bound, stated so nobody reads more into it: both readings share the
// committed artifact and the compiled admission table. This catches a broken
// helper, not a corrupted artifact. The artifact's own freshness is
// survey-gen's provider run and drift_test.go's business.
func TestGovernanceSplitAgreesWithAnIndependentRead(t *testing.T) {
	root := repoRootFromTest(t)

	raw, err := os.ReadFile(filepath.Join(root, surveyFullJSONRel))
	if err != nil {
		t.Fatalf("reading %s: %v", surveyFullJSONRel, err)
	}
	var full struct {
		Types []struct {
			Type    string `json:"type"`
			Signals struct {
				Taggable bool `json:"taggable"`
			} `json:"signals"`
		} `json:"types"`
	}
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatalf("decoding %s: %v", surveyFullJSONRel, err)
	}
	taggable := make(map[string]bool, len(full.Types))
	surveyed := make(map[string]bool, len(full.Types))
	for _, row := range full.Types {
		taggable[row.Type] = row.Signals.Taggable
		surveyed[row.Type] = true
	}

	// The join, done here rather than borrowed: an admitted type the AWS
	// survey has a row for, whose row says it carries no tags argument.
	// Types with no row are either the record-backed logical ones, which
	// belong to the null, time and random providers and are not AWS objects
	// at all, or the small nonAWSAdmittedUntaggable ledger
	// (untaggable_render.go, issue #326) naming the Kubernetes-provider
	// types this table has admitted with no AWS survey row to ever carry -
	// read here too, so this leg stays independent of the join it is
	// checking rather than independent of the evidence that join rests on.
	var wantUntaggable, wantTaggable []string
	for _, typeName := range identity.AdmittedTypes() {
		if !surveyed[typeName] {
			if nonAWSAdmittedUntaggable[typeName] {
				wantUntaggable = append(wantUntaggable, typeName)
			}
			continue
		}
		if taggable[typeName] {
			wantTaggable = append(wantTaggable, typeName)
			continue
		}
		wantUntaggable = append(wantUntaggable, typeName)
	}

	split, err := deriveGovernanceSplit(root)
	if err != nil {
		t.Fatalf("deriving the split: %v", err)
	}

	if got, want := strings.Join(split.Untaggable, ","), strings.Join(wantUntaggable, ","); got != want {
		t.Errorf("the untaggable-admitted set the span renders (%d types) disagrees with an independent read of %s (%d types).\n"+
			"One of the two joins is wrong, and the span is the one a reader turns into an IAM policy.",
			len(split.Untaggable), surveyFullJSONRel, len(wantUntaggable))
	}
	if got, want := strings.Join(split.Taggable, ","), strings.Join(wantTaggable, ","); got != want {
		t.Errorf("the taggable-admitted set the span renders (%d types) disagrees with an independent read of %s (%d types)",
			len(split.Taggable), surveyFullJSONRel, len(wantTaggable))
	}

	visited := len(wantUntaggable) + len(wantTaggable)
	if visited < governanceSweepFloor {
		t.Errorf("the derivation visited %d admitted AWS types, below the floor of %d.\n"+
			"Every figure in the section can be satisfied by measuring less, so this is the leg that is not allowed to move quietly.",
			visited, governanceSweepFloor)
	}
	if len(split.Untaggable) == 0 || len(split.Taggable) == 0 {
		t.Errorf("the split is degenerate: %d untaggable, %d taggable.\n"+
			"Either every admitted type can carry a marker or none can, and neither has ever been true here; the join is broken.",
			len(split.Untaggable), len(split.Taggable))
	}
}

// TestGovernanceSectionCoversBothHalves holds the claim the section exists to
// make.
//
// The across-estate grant conditions on tofu-estate and the within-estate one
// on tofu-address, and the gap is the same for both because the carrier is
// the same. The within-estate half is the finer claim and the one that had
// never been written down, so it is the one that would go first if this
// section were trimmed. Losing it would leave a section that reads as
// complete while covering half its subject.
//
// The two halves of the section are checked separately, and the split is the
// point. A first draft searched the whole section for
// "aws:ResourceTag/tofu-address" and survived its own mutation: deleting the
// key from the hand-written framing left the phrase present anyway, because
// the generated body mentions it in passing while making a different claim
// about continuation tags. So the hand-written region is checked with the
// generated span cut out of it, and the generated body is checked as the
// renderer's own output.
func TestGovernanceSectionCoversBothHalves(t *testing.T) {
	root := repoRootFromTest(t)
	md := readMarkersMD(t, root)

	const heading = "### What this grant cannot reach"
	i := strings.Index(md, heading)
	if i < 0 {
		t.Fatalf("%s no longer has a %q section.\n"+
			"The estate grant is published there with a worked policy; without it the document claims a governance model and names nothing it cannot govern.",
			markersMDRel, heading)
	}
	section := md[i:]
	if j := strings.Index(section[len(heading):], "\n## "); j >= 0 {
		section = section[:len(heading)+j]
	}

	// The hand-written framing: the section with its generated span removed.
	begin, end := spanMarkers(spanGovernableGap)
	handWritten := section
	if b := strings.Index(section, begin); b >= 0 {
		if e := strings.Index(section, end); e > b {
			handWritten = section[:b] + section[e+len(end):]
		}
	}
	if strings.Contains(handWritten, begin) || strings.Contains(handWritten, end) {
		t.Fatalf("could not cut the %q span out of the section; the marker pair is malformed", spanGovernableGap)
	}

	flat := normalizeWS(handWritten)
	for _, phrase := range []struct{ text, why string }{
		{"aws:ResourceTag/tofu-address",
			"the within-estate half. Granting a principal rights over one declared address means conditioning on this key, and an untaggable object carries it no more than it carries tofu-estate. It is the finer of the two claims and the one nobody had written down."},
		{"aws:ResourceTag/tofu-estate",
			"the across-estate half, which is the grant the worked policy above publishes"},
	} {
		if !strings.Contains(flat, phrase.text) {
			t.Errorf("the hand-written part of the %q section no longer mentions %q.\nThat sentence carried %s.", heading, phrase.text, phrase.why)
		}
	}

	// The generated body, checked as the renderer's own output rather than as
	// text in the document. The freshness guard pins document against
	// renderer, so editing the renderer and re-rendering satisfies it; only a
	// check on what the renderer produces catches a claim removed at source.
	split, err := deriveGovernanceSplit(root)
	if err != nil {
		t.Fatalf("deriving the split: %v", err)
	}
	body := normalizeWS(renderGovernableGap(split))
	for _, phrase := range []struct{ text, why string }{
		{"`tofu-estate` no more than it carries `tofu-address`",
			"the sentence that makes the gap one fact about both grants rather than two claims a reader has to join up"},
		{"markerless veto",
			"the population this figure is NOT: identity.MarkerlessTypes is untaggable and server-minted and is not admitted at all, so reading the two sets as one understates the gap by the whole of it"},
		{"within-estate half only",
			"the continuation-tag limit, which applies to a tofu-address condition and not to a tofu-estate one"},
	} {
		if !strings.Contains(body, phrase.text) {
			t.Errorf("renderGovernableGap no longer produces %q.\nThat sentence carried %s.", phrase.text, phrase.why)
		}
	}
}

// normalizeWS collapses every run of whitespace to one space, so a check on
// a phrase is not defeated by the line the paragraph happens to wrap on.
func normalizeWS(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestGovernanceClaimIsScopedWhereItIsMade checks the sentence the whole
// change is about.
//
// live/MARKERS.md used to open this section with the flat claim that IAM can
// condition on the marker directly "with no second permission model to keep
// in sync", and named no type that was untrue for. A reader who stops after
// the worked policy - which is most readers, since the policy is what they
// came for - never reaches the subsection below. So the scope has to be in
// the same sentence as the claim, and it has to be the derived figure rather
// than a hedge: a caveat with no number is what this section's SCP sibling
// already had, and #152 was filed because that was not good enough.
func TestGovernanceClaimIsScopedWhereItIsMade(t *testing.T) {
	md := readMarkersMD(t, repoRootFromTest(t))

	const claim = "no second permission model to keep in sync"

	// The paragraph the claim sits in, which is what a reader takes away.
	// Matched on normalized whitespace, so re-wrapping the paragraph is not
	// a way to make this pass.
	var paragraph string
	for _, p := range strings.Split(md, "\n\n") {
		if strings.Contains(normalizeWS(p), claim) {
			paragraph = p
			break
		}
	}
	if paragraph == "" {
		t.Fatalf("%s no longer makes the %q claim.\n"+
			"If it was withdrawn, this test goes with it - but it is TRUE for the overwhelming majority of admitted types and is the product's real strength, so check that it was scoped rather than deleted.",
			markersMDRel, claim)
	}

	begin, _ := inlineSpanMarkers(spanGovernableCount)
	if !strings.Contains(paragraph, begin) {
		t.Errorf("the paragraph making the %q claim no longer carries the %q span.\n"+
			"The claim is only true for the admitted types that have a tags argument, and the scope has to sit beside it rather than in a subsection further down.\n\nthe paragraph:\n%s",
			claim, spanGovernableCount, paragraph)
	}
	if !strings.Contains(paragraph, "cannot reach") {
		t.Errorf("the paragraph making the %q claim no longer points at the section naming what the grant cannot reach.\n\nthe paragraph:\n%s", claim, paragraph)
	}
}

// TestNoMarkerlessTypeIsAdmitted holds the sentence that separates the two
// populations.
//
// The span says none of identity.MarkerlessTypes is admitted, which is what
// makes the 221 the right figure and the 148 the wrong one. Nothing else in
// the tree checks it: row-gen derives the veto and the table in one run, so
// they agree by construction today, and a future admission route that reached
// past the veto would make this document's arithmetic wrong without any test
// noticing.
func TestNoMarkerlessTypeIsAdmitted(t *testing.T) {
	var both []string
	for _, typeName := range identity.AdmittedTypes() {
		if _, vetoed := identity.MarkerlessTypes[typeName]; vetoed {
			both = append(both, typeName)
		}
	}
	if len(both) != 0 {
		t.Errorf("%d types are both admitted and on identity.MarkerlessTypes: %v.\n"+
			"live/MARKERS.md's estate-grant section states that no markerless type is admitted, which is what makes the untaggable-admitted count the figure a reader needs. If a type can be both, the section's arithmetic is wrong.",
			len(both), both)
	}
}

// inlineSpanContent reads a span written with inline markers, which
// spanContent's block form cannot see.
func inlineSpanContent(md, name string) (string, bool) {
	begin, end := inlineSpanMarkers(name)
	i := strings.Index(md, begin)
	if i < 0 {
		return "", false
	}
	rest := md[i+len(begin):]
	j := strings.Index(rest, end)
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}

func readMarkersMD(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, markersMDRel))
	if err != nil {
		t.Fatalf("reading %s: %v", markersMDRel, err)
	}
	return string(raw)
}

// repoRootFromTest resolves the checkout root the same way the generator
// does, so a test and the tool it guards never disagree about where the
// artifacts are.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	return root
}
