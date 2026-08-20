// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

func sepPtr(s string) *string { return &s }

// TestClassifyCompositeImportNeedsBothSources is the rule's own boundary:
// a whole verdict requires the Attribute Reference's stated separator AND
// the Import section's independently-scraped one to be the same character.
//
// The third and fourth cases are the ones that matter. A page that states a
// composite with the wrong separator, and a page that says nothing at all,
// must land on the same side of the verdict - unproven - because the point
// of the roster is that a wrong identity recorded confidently is worse than
// a refusal.
func TestClassifyCompositeImportNeedsBothSources(t *testing.T) {
	for _, tc := range []struct {
		name        string
		row         importGrammarRow
		wantVerdict string
		wantReason  string
	}{
		{
			name: "both sources name the same separator",
			row: importGrammarRow{
				Separator:   sepPtr("/"),
				IDAttribute: &idAttributeDoc{StatedSeparator: "/", Description: "A and B separated by a slash (`/`)."},
			},
			wantVerdict: compositeVerdictWhole,
		},
		{
			name: "the page documents an id that states no composite",
			row: importGrammarRow{
				Separator:   sepPtr("/"),
				IDAttribute: &idAttributeDoc{Description: "The EMR Instance ID"},
			},
			wantVerdict: compositeVerdictUnproven,
			wantReason:  compositeReasonNotComposite,
		},
		{
			name: "the two sections name different separators",
			row: importGrammarRow{
				Separator:   sepPtr(","),
				IDAttribute: &idAttributeDoc{StatedSeparator: "/", Description: "A and B separated by a slash (`/`)."},
			},
			wantVerdict: compositeVerdictUnproven,
			wantReason:  compositeReasonSeparatorDiff,
		},
		{
			name:        "the page documents no id attribute at all",
			row:         importGrammarRow{Separator: sepPtr("/")},
			wantVerdict: compositeVerdictUnproven,
			wantReason:  compositeReasonNoIDBullet,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyCompositeImport("aws_example", tc.row)
			if got.Verdict != tc.wantVerdict {
				t.Errorf("verdict = %q, want %q", got.Verdict, tc.wantVerdict)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// TestCompositeImportRosterIsWhatTheRuleProduces holds the committed
// artifact to a fresh build from the committed inputs, the way every other
// generated artifact here is held.
//
// It also asserts the arithmetic the artifact's summary claims, because a
// summary is the part a reader quotes and the part nothing else checks.
func TestCompositeImportRosterIsWhatTheRuleProduces(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	grammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	fresh := buildCompositeImportArtifact(identity.MarkerlessTypes, survey, grammar)
	fresh.GeneratedBy = "tools/row-gen -composite-import"

	data, err := os.ReadFile(filepath.Join(root, compositeImportArtifactPath))
	if err != nil {
		t.Fatal(err)
	}
	var committed compositeImportArtifact
	if err := json.Unmarshal(data, &committed); err != nil {
		t.Fatal(err)
	}

	if committed.Summary != fresh.Summary {
		t.Errorf("%s's summary is %+v, the rule over the committed inputs produces %+v.\nRun `just composite-import`.",
			compositeImportArtifactPath, committed.Summary, fresh.Summary)
	}
	if len(committed.Types) != len(fresh.Types) {
		t.Fatalf("%s carries %d types, the rule produces %d. Run `just composite-import`.",
			compositeImportArtifactPath, len(committed.Types), len(fresh.Types))
	}
	for i := range fresh.Types {
		if committed.Types[i] != fresh.Types[i] {
			t.Errorf("%s entry %d is %+v, the rule produces %+v. Run `just composite-import`.",
				compositeImportArtifactPath, i, committed.Types[i], fresh.Types[i])
		}
	}

	s := fresh.Summary
	if s.CompositeImport != s.WireIdentitySchema+s.Residue {
		t.Errorf("summary does not add up: %d markerless types with a composite import, but %d with a wire identity schema plus %d residue",
			s.CompositeImport, s.WireIdentitySchema, s.Residue)
	}
	if s.Residue != s.Whole+s.Unproven {
		t.Errorf("summary does not add up: residue %d, but %d proven whole plus %d unproven", s.Residue, s.Whole, s.Unproven)
	}
	if s.Residue != len(fresh.Types) {
		t.Errorf("summary claims a residue of %d, the artifact carries %d entries", s.Residue, len(fresh.Types))
	}
	if s.Whole == 0 {
		t.Error("no type in the residue is proven whole, so the whole verdict is unreachable and this rule reports nothing; " +
			"either the evidence field stopped being populated (regenerate live/import-grammar.json) or the rule stopped firing")
	}
	if s.Unproven == 0 {
		t.Error("every type in the residue is proven whole, which is not what the evidence looked like when this was written; " +
			"check that the rule has not widened into accepting a stated composite without the Import section's corroboration")
	}
}

// TestCompositeImportRosterIsDisjointFromWhat329Answers guards the seam
// between the two mechanisms.
//
// #329's gate reads the provider's wire identity schema. This roster exists
// only for the types that gate cannot see. If a type ever appears in both
// populations, one of the two is answering a question the other already
// answered, and the roster would be a second, weaker opinion about a type
// the schema has already settled.
func TestCompositeImportRosterIsDisjointFromWhat329Answers(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	grammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	art := buildCompositeImportArtifact(identity.MarkerlessTypes, survey, grammar)
	for _, e := range art.Types {
		if entry, ok := survey[e.Type]; ok && entry.Identity != nil {
			t.Errorf("%s is in the roster and also carries a wire identity schema (%v); "+
				"identity.LocatedIdentityComponents reads that schema and this roster must not offer a second answer for the same type",
				e.Type, entry.Identity.RequiredForImport)
		}
		if _, ok := identity.MarkerlessTypes[e.Type]; !ok {
			t.Errorf("%s is in the roster but not in identity.MarkerlessTypes, so the record-located path never evaluates it", e.Type)
		}
		if _, ok := identity.LookupType(e.Type); ok {
			t.Errorf("%s is in the roster but has a ratified row, so it is admitted by its own path and is never record-located", e.Type)
		}
	}
}
