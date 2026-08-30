// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// Issue #582's tier-vocabulary correction, held to the tree.
//
// Two populations answer to the name "record-carried" and they differ
// threefold: identity.MarkerlessTypes, the derived roster the admission
// rule vetoes, and live/readiness.json's record-carried tier, which
// tools/readiness-gen also fills by elimination from
// live/survey-full.json's path column. Issues #535 and #579 both quoted
// the second where the first was meant, and rfc/20260830's own
// "The populations" section exists because of it.
//
// The docs now state the split with both counts. Hand-typed counts in
// prose are exactly the thing this repository has watched go stale, so
// this test recomputes each one and then requires the prose to contain it.
// A provider bump that moves any of these fails here, naming the file to
// update, rather than leaving a page quietly wrong.
//
// Proving it red: change any expected count below, or delete a number
// from one of the documents named in readinessDocFigures, and this fails
// with the file and the figure it could not find.

// readinessCounts is the subset of live/readiness.json this test reads.
type readinessCounts struct {
	Types []struct {
		Type  string `json:"type"`
		Tier  string `json:"tier"`
		Facts struct {
			Taggable   bool `json:"taggable"`
			Markerless bool `json:"markerless"`
		} `json:"facts"`
	} `json:"types"`
}

// tierRecordCarried and tierExcludedByDesign are tools/readiness-gen's own
// tier names, spelled here rather than imported: that generator is a
// package main, so its consts are not reachable, and the names are
// published vocabulary (rfc/20260828-readiness-tiers.md).
const (
	tierRecordCarried    = "record-carried"
	tierExcludedByDesign = "excluded by design"
)

// readinessFigures are the counts the documents quote, recomputed rather
// than asserted against a literal, plus the one figure that comes from the
// Go symbol instead of the artifact.
type readinessFigures struct {
	total          int // every provider type readiness-gen classified
	markerlessFact int // facts.markerless == true
	recordCarried  int // tier == record-carried
	byElimination  int // record-carried and not markerless
	untaggable     int // facts.taggable == false
	markerlessMap  int // len(identity.MarkerlessTypes)
}

func computeReadinessFigures(t *testing.T) readinessFigures {
	t.Helper()

	raw, err := os.ReadFile("readiness.json")
	if err != nil {
		t.Fatalf("read live/readiness.json: %v", err)
	}
	var art readinessCounts
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("parse live/readiness.json: %v", err)
	}
	if len(art.Types) == 0 {
		t.Fatal("live/readiness.json carries no types; every count below would be vacuous")
	}

	f := readinessFigures{total: len(art.Types), markerlessMap: len(identity.MarkerlessTypes)}
	for _, row := range art.Types {
		if row.Facts.Markerless {
			f.markerlessFact++
		}
		if !row.Facts.Taggable {
			f.untaggable++
		}
		if row.Tier == tierRecordCarried {
			f.recordCarried++
			if !row.Facts.Markerless {
				f.byElimination++
			}
		}
	}
	return f
}

// TestReadinessVocabularyArithmeticHolds checks the two populations relate
// the way the docs say they do, before any document is read. If this
// fails, the prose is wrong in a way no wording fixes.
func TestReadinessVocabularyArithmeticHolds(t *testing.T) {
	f := computeReadinessFigures(t)

	if f.markerlessFact+f.byElimination != f.recordCarried {
		t.Errorf("record-carried tier does not decompose: markerless %d + by-elimination %d != %d",
			f.markerlessFact, f.byElimination, f.recordCarried)
	}

	// Every member of the Go roster must appear in the artifact, and any
	// member the artifact does not count as markerless must be there for
	// the one stated reason: tier D wins over the markerless branch in
	// tools/readiness-gen's classify.
	raw, err := os.ReadFile("readiness.json")
	if err != nil {
		t.Fatalf("read live/readiness.json: %v", err)
	}
	var art readinessCounts
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("parse live/readiness.json: %v", err)
	}
	tier := make(map[string]string, len(art.Types))
	markerless := make(map[string]bool, len(art.Types))
	for _, row := range art.Types {
		tier[row.Type] = row.Tier
		markerless[row.Type] = row.Facts.Markerless
	}
	for name := range identity.MarkerlessTypes {
		got, ok := tier[name]
		if !ok {
			t.Errorf("%s is in identity.MarkerlessTypes but absent from live/readiness.json", name)
			continue
		}
		if !markerless[name] && got != tierExcludedByDesign {
			t.Errorf("%s is in identity.MarkerlessTypes, is not counted markerless, and is tier %q rather than %q; "+
				"the docs explain the roster/tier gap as tier D alone, so that explanation is now incomplete",
				name, got, tierExcludedByDesign)
		}
	}
}

// readinessDocFigures names each document that quotes one of these counts
// and which counts it quotes. Derived from the prose, not from a guess:
// each entry was added alongside the sentence that carries the figure.
func readinessDocFigures(f readinessFigures) map[string][]int {
	return map[string][]int{
		"../site/content/docs/use/resource-tiers.md": {
			f.markerlessMap,  // identity.MarkerlessTypes
			f.markerlessFact, // of which, classified record-carried
			f.recordCarried,  // the tier
			f.byElimination,  // the remainder, by elimination
		},
		"../site/content/docs/model/identity.md": {
			f.untaggable,
			f.total,
		},
	}
}

// TestReadinessFiguresInDocsAreCurrent requires every document that quotes
// one of these counts to quote the current one.
func TestReadinessFiguresInDocsAreCurrent(t *testing.T) {
	f := computeReadinessFigures(t)

	for path, want := range readinessDocFigures(f) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		text := string(raw)
		for _, n := range want {
			if n == 0 {
				t.Errorf("%s: expected figure is zero, which would make this check vacuous", path)
				continue
			}
			re := regexp.MustCompile(fmt.Sprintf(`(^|[^0-9])%d([^0-9]|$)`, n))
			if !re.MatchString(text) {
				t.Errorf("%s does not quote %d.\n"+
					"That figure moved, and this document states it in prose. Recount from "+
					"live/readiness.json (fields tier and facts.markerless/facts.taggable) and "+
					"internal/live/identity/markerless_generated.go, then update the sentence "+
					"carrying the old number. Current figures: MarkerlessTypes=%d, "+
					"markerless-classified=%d, record-carried=%d, by-elimination=%d, "+
					"untaggable=%d, types=%d.",
					path, n,
					f.markerlessMap, f.markerlessFact, f.recordCarried,
					f.byElimination, f.untaggable, f.total)
			}
		}
	}
}
