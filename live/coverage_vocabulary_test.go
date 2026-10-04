// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"testing"
)

// This file is #696's guard: new code speaks readiness tiers only.
//
// One axis - what recovers an instance's identity - used to carry four
// vocabularies: lint's admission paths, HANDOFF's rungs, survey-gen's path
// taxonomy (live/survey-full.json's "path" field, rendered into
// live/SURVEY.md), and the readiness tiers A-D that tools/readiness-gen
// assigns (#417, #418). The tiers are the one that stays: every provider type
// carries exactly one, in live/readiness.json. The survey taxonomy was the
// one with code behind it, so it was the one that could spread.
//
// #1845 pinned where that taxonomy was spelled in hand-written Go (28
// literals in 6 files) and the retirement took the pin to zero: survey-gen
// stopped writing the path column and SURVEY.md was retired, readiness-gen
// derives destined tiers from the taggable and admission signals,
// row-gen's schema-first pass and evidence-gap families read admission and
// tiers, and the two values that only shared the spelling (row-gen's
// proposal bucket and identity.Naming's configuration claim) were renamed.
// So the census below is empty and this test now fails on any hand-written
// Go string literal that spells one of the path tokens.
//
// "Spelled" means a Go string literal that is exactly one of the path
// tokens. Comments are not counted: a pointer to where a fact used to live
// is documentation, not a consumer. "marker" is a path token too and is left
// out, because it is also the ordinary word for the thing the tiers are
// about.
//
// What is deliberately NOT counted is a reference to live/survey-full.json
// itself. That artifact now carries only the provider's own signals
// (taggable, list_resource, identity_schema, importable, the roster size)
// and identity.Report's schema-only admission verdict, which are external
// evidence several guards rely on precisely because no row-gen output
// writes them (internal/live/harness's burndown cites them that way).
// Those reads cannot move to live/readiness.json: readiness-gen is
// downstream of mapping-gen, row-gen and importdocs-gen (it reads
// mapping.json and the identity table they produce), so pointing them at
// readiness would make each generator's input depend on its own output.

// surveyPathTokens is survey-gen's path vocabulary (tools/survey-gen's
// classify.go), less "marker".
var surveyPathTokens = map[string]bool{
	"client-named":           true,
	"parent-derived":         true,
	"account-derived":        true,
	"unique-name":            true,
	"enumerable, unbindable": true,
	"moves to Ops":           true,
}

func surveyVocabularyLiteral(v string) bool {
	return surveyPathTokens[v]
}

// surveyVocabularySites is the census: 28 literals in 6 files when #1845
// landed, zero since the #696 retirement. It only ever shrinks, so it is
// empty for good; a file that needs one of these tokens back is a second
// classification of the identity axis, which is what #696 removed.
var surveyVocabularySites = map[string]int{}

func TestNoNewCodeSpeaksTheSurveyVocabulary(t *testing.T) {
	got := map[string]int{}
	files := 0
	forEachHandWrittenGoFile(t, func(rel string, f *ast.File, _ *token.FileSet) {
		files++
		ast.Inspect(f, func(n ast.Node) bool {
			bl, ok := n.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(bl.Value); err == nil && surveyVocabularyLiteral(v) {
				got[rel]++
			}
			return true
		})
	})
	if files < typeLiteralSweepFloor {
		t.Fatalf("walked %d hand-written Go files, below the %d floor derivation_guard_test.go holds; the walk is broken, not the tree clean", files, typeLiteralSweepFloor)
	}

	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	for k := range surveyVocabularySites {
		if _, ok := got[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, rel := range keys {
		have, want := got[rel], surveyVocabularySites[rel]
		switch {
		case want == 0:
			t.Errorf("%s spells survey-gen's path taxonomy in %d string literal(s) and is not in surveyVocabularySites.\n"+
				"New code speaks readiness tiers (live/readiness.json, tools/readiness-gen) - #696. Read the tier, "+
				"or the provider signal you actually need (survey-full.json's signals stay), instead of the survey path.", rel, have)
		case have > want:
			t.Errorf("%s spells the survey path taxonomy in %d string literal(s), up from the pinned %d. "+
				"The pin only shrinks (#696): read the readiness tier instead.", rel, have, want)
		case have < want:
			t.Errorf("%s spells the survey path taxonomy in %d string literal(s), down from the pinned %d. "+
				"Good - lower surveyVocabularySites[%q] to %d (or delete the entry at 0) so the allowance is not spent again.",
				rel, have, want, rel, have)
		}
	}
}
