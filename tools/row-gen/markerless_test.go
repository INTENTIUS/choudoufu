// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestMarkerlessRule covers the conjunction and, more importantly, the three
// shapes it must NOT fire on. Each of the three is a real population in the
// committed table, not a hypothetical: 442 admitted rows are server-assigned
// and taggable, 6 are untaggable with a ServerAssignedIfAbsent component
// that the author can clear by naming the object, and 6 are untaggable rows
// the classifier calls server-assigned while the ratified row does not.
func TestMarkerlessRule(t *testing.T) {
	serverAssigned := identity.TypeIdentity{Type: "x", ServerAssigned: true}
	clientNamed := identity.TypeIdentity{Type: "x"}
	ifAbsent := identity.TypeIdentity{Type: "x", Components: []identity.Component{
		{ServerAssignedIfAbsent: true, Attrs: []string{"name"}},
	}}

	for _, tc := range []struct {
		name       string
		taggable   bool
		admitted   bool
		row        identity.TypeIdentity
		classified bool
		documented bool
		want       bool
	}{
		{"admitted, untaggable, row says server-assigned", false, true, serverAssigned, false, false, true},
		{"unadmitted, untaggable, classifier says server-assigned", false, false, identity.TypeIdentity{}, true, false, true},
		{"unadmitted, untaggable, the docs name a server-minted segment", false, false, identity.TypeIdentity{}, false, true, true},

		{"taggable and server-assigned is the mechanism working", true, true, serverAssigned, true, true, false},
		{"untaggable but named from configuration needs no marker", false, true, clientNamed, false, false, false},
		{"untaggable with a conditional component is a different problem", false, true, ifAbsent, false, false, false},
		{"admitted: the ratified row overrules the classifier", false, true, clientNamed, true, false, false},
		{"admitted: the ratified row overrules the docs too", false, true, clientNamed, false, true, false},
		{"unadmitted and neither source says server-assigned", false, false, identity.TypeIdentity{}, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := markerless(tc.taggable, tc.admitted, tc.row, tc.classified, tc.documented); got != tc.want {
				t.Errorf("markerless(taggable=%v, admitted=%v, row=%+v, classified=%v, documented=%v) = %v, want %v",
					tc.taggable, tc.admitted, tc.row, tc.classified, tc.documented, got, tc.want)
			}
		})
	}
}

// TestMarkerlessRosterNeedsSurveyMembership is the silent-default guard. A
// surveyEntry's zero value decodes taggable as false, so a roster built over
// the union of this tool's inputs would veto every type the survey does not
// cover - on the absence of a signal rather than on a signal. The roster
// iterates the survey's own entries for exactly that reason, and this pins
// it: a type the classifier calls server-assigned and the survey has never
// heard of is not vetoed.
func TestMarkerlessRosterNeedsSurveyMembership(t *testing.T) {
	survey := map[string]surveyEntry{
		"aws_untaggable_sa": {Type: "aws_untaggable_sa"},
		"aws_taggable_sa":   {Type: "aws_taggable_sa", Signals: surveySignals{Taggable: true}},
	}
	proposals := []proposal{
		{TFType: "aws_untaggable_sa", Bucket: bucketServerAssigned},
		{TFType: "aws_taggable_sa", Bucket: bucketServerAssigned},
		{TFType: "aws_not_in_survey", Bucket: bucketServerAssigned},
	}

	got := markerlessRoster(survey, proposals, nil)
	want := []string{"aws_untaggable_sa"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("markerlessRoster = %v, want %v - a type outside live/survey-full.json must never be "+
			"vetoed, because its taggability signal is absent rather than false", got, want)
	}
}

// TestMarkerlessRosterSpares442ServerAssignedTaggableRows runs the rule over
// the real committed inputs and checks the population the brief for this
// change named as the thing that must not move: every admitted row that is
// server-assigned AND taggable. The marker is exactly what finds those, so a
// rule that reached one of them would break the mechanism it exists to
// protect.
func TestMarkerlessRosterSpares442ServerAssignedTaggableRows(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := loadProposals(root)
	if err != nil {
		t.Fatal(err)
	}
	importGrammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	vetoed := setOf(markerlessRoster(survey, proposals, importGrammar))

	var spared, caught int
	for _, typeName := range identity.AdmittedTypes() {
		entry, inSurvey := survey[typeName]
		if !inSurvey || !identity.DefaultTable[typeName].ServerAssigned || !entry.Signals.Taggable {
			continue
		}
		if vetoed[typeName] {
			caught++
			t.Errorf("%s is server-assigned and TAGGABLE and the rule vetoed it; the marker is what "+
				"finds this type, so the veto's premise does not hold for it", typeName)
			continue
		}
		spared++
	}
	if caught == 0 && spared == 0 {
		t.Fatal("no admitted row is both server-assigned and taggable; this test checked nothing, so " +
			"either the table or the survey signal changed shape")
	}
	t.Logf("%d admitted server-assigned taggable rows, none vetoed", spared)
}
