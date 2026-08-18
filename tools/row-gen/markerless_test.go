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
		name         string
		taggable     bool
		contentMatch bool
		admitted     bool
		row          identity.TypeIdentity
		classified   bool
		documented   bool
		agree        bool
		named        bool
		want         bool
	}{
		{"admitted, untaggable, row says server-assigned", false, false, true, serverAssigned, false, false, false, false, true},
		{"unadmitted, untaggable, classifier says server-assigned", false, false, false, identity.TypeIdentity{}, true, false, false, false, true},
		{"unadmitted, untaggable, the docs name a server-minted segment", false, false, false, identity.TypeIdentity{}, false, true, false, false, true},

		{"taggable and server-assigned is the mechanism working", true, false, true, serverAssigned, true, true, false, false, false},
		{"untaggable but named from configuration needs no marker", false, false, true, clientNamed, false, false, false, false, false},
		{"untaggable with a conditional component is a different problem", false, false, true, ifAbsent, false, false, false, false, false},
		{"admitted: the ratified row overrules the classifier", false, false, true, clientNamed, true, false, false, false, false},
		{"admitted: the ratified row overrules the docs too", false, false, true, clientNamed, false, true, false, false, false},
		{"unadmitted and neither source says server-assigned", false, false, false, identity.TypeIdentity{}, false, false, false, false, false},

		// sourcesAgreeComposed (issue #274): CloudFormation's registry and
		// the provider's own import docs, read independently of the
		// classifier's bucket, both say the identity is argument-built.
		// That outranks a single-source verdict that produced the veto.
		{"unadmitted, untaggable, sources agree overrules the classifier's server-assigned call", false, false, false, identity.TypeIdentity{}, true, false, true, false, false},
		{"unadmitted, untaggable, sources agree overrules the docs' minted segment", false, false, false, identity.TypeIdentity{}, false, true, true, false, false},
		{"unadmitted, untaggable, sources agree and nothing else fired anyway", false, false, false, identity.TypeIdentity{}, false, false, true, false, false},
		{"admitted: the ratified row overrules sources agreeing too", false, false, true, serverAssigned, false, false, true, false, true},

		// boundByName (issue #272): the veto's premises all hold - the
		// identity is minted, the type is untaggable - and the type is
		// admitted anyway, because AWS refuses to issue the name the
		// configuration states twice, so a listing finds the object without
		// a marker. It is the only exception that spares a type the veto is
		// otherwise right about.
		{"unadmitted, untaggable, bound by name overrules the classifier's server-assigned call", false, false, false, identity.TypeIdentity{}, true, false, false, true, false},
		{"unadmitted, untaggable, bound by name overrules the docs' minted segment", false, false, false, identity.TypeIdentity{}, false, true, false, true, false},
		{"unadmitted, untaggable, bound by name and nothing else fired anyway", false, false, false, identity.TypeIdentity{}, false, false, false, true, false},
		// The order between the two exceptions is not observable and must
		// not be: both spare the type, and a case where they disagree does
		// not exist.
		{"unadmitted, untaggable, both exceptions fire", false, false, false, identity.TypeIdentity{}, true, true, true, true, false},
		// An ADMITTED row is untouched by boundByName. A ratified row is
		// what ships for its type, and uniqueNameRows refuses every type the
		// corpus carries - so a true here could only come from a caller that
		// had stopped believing that, and the veto must not follow it.
		{"admitted: the ratified row overrules bound-by-name too", false, false, true, serverAssigned, false, false, false, true, true},

		// Issue #272's other bypass: the same untaggable, admitted,
		// server-assigned shape as the very first case above, except the
		// type has cleared the two-source content-match proof - and that
		// alone flips the verdict, exactly the way taggable alone does.
		{"admitted, untaggable, server-assigned, but content-match qualified", false, true, true, serverAssigned, false, false, false, false, false},
		{"unadmitted, untaggable, classifier says server-assigned, but content-match qualified", false, true, false, identity.TypeIdentity{}, true, false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := markerless(tc.taggable, tc.contentMatch, tc.admitted, tc.row, tc.classified, tc.documented, tc.agree, tc.named); got != tc.want {
				t.Errorf("markerless(taggable=%v, contentMatch=%v, admitted=%v, row=%+v, classified=%v, documented=%v, sourcesAgreeComposed=%v, boundByName=%v) = %v, want %v",
					tc.taggable, tc.contentMatch, tc.admitted, tc.row, tc.classified, tc.documented, tc.agree, tc.named, got, tc.want)
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

	got := markerlessRoster(nil, survey, proposals, nil, nil, nil)
	want := []string{"aws_untaggable_sa"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("markerlessRoster = %v, want %v - a type outside live/survey-full.json must never be "+
			"vetoed, because its taggability signal is absent rather than false", got, want)
	}
}

// TestMarkerlessRosterSourcesAgreeSparesTheVeto isolates sourcesAgree's own
// wiring into markerlessRoster - registryComposedOfArguments plus
// docMintedSegment's negative, read off a proposal and a grammar row - from
// the ratified corpus entirely. Every type below is UNADMITTED (ratified is
// nil), so this is the state a type is in BEFORE a human writes a row for
// it: once a ratified composite row exists, [markerless]'s own admitted
// branch shields the type regardless of sourcesAgree, which is exactly why
// TestMarkerlessRosterTwoSourcesAgreement (over the real, now-ratified
// leads) cannot be the test that catches a broken sourcesAgree - mutating
// sourcesAgree to always return false left that test green. This one is
// what a broken sourcesAgree actually breaks: the population that has no
// ratified row yet to fall back on.
func TestMarkerlessRosterSourcesAgreeSparesTheVeto(t *testing.T) {
	survey := map[string]surveyEntry{
		"aws_test_agree_composite":          {Type: "aws_test_agree_composite"},          // untaggable (zero value)
		"aws_test_disagree_serverid":        {Type: "aws_test_disagree_serverid"},        // untaggable
		"aws_test_no_grammar_row":           {Type: "aws_test_no_grammar_row"},           // untaggable, no import-grammar row at all
		"aws_test_registry_alone_disagrees": {Type: "aws_test_registry_alone_disagrees"}, // untaggable
	}
	proposals := []proposal{
		// Registry: composite primaryIdentifier, no read-only part -
		// CloudFormation says supplied. Bucket server-assigned mimics
		// tryOpaqueOverride's real misfire.
		{TFType: "aws_test_agree_composite", Bucket: bucketServerAssigned,
			PrimaryIdentifier: []string{"PartOne", "PartTwo"}, ReadOnly: nil},
		// Registry: primaryIdentifier ⊆ readOnly - genuinely server-minted.
		// The doc's own SoleIDPart also says own-id: both sources agree
		// the OTHER way, so this must stay vetoed.
		{TFType: "aws_test_disagree_serverid", Bucket: bucketServerAssigned,
			PrimaryIdentifier: []string{"Id"}, ReadOnly: []string{"Id"}},
		{TFType: "aws_test_no_grammar_row", Bucket: bucketServerAssigned,
			PrimaryIdentifier: []string{"PartOne", "PartTwo"}, ReadOnly: nil},
		// Registry: primaryIdentifier ⊆ readOnly - server-minted - but the
		// doc's own structured evidence is silent (no IDParts, no
		// SoleIDPart), the way a page with no captured Import section
		// segments reads. The registry alone disagreeing is enough to
		// refuse agreement; the doc's silence must not be read as a second
		// vote for "supplied". This is the case that only fails if
		// registryComposedOfArguments itself is broken - the other three
		// cases here all still resolve correctly even with that function
		// stubbed to always return true, because each of them is also
		// gated by docMintedSegment or the missing grammar row.
		{TFType: "aws_test_registry_alone_disagrees", Bucket: bucketServerAssigned,
			PrimaryIdentifier: []string{"Id"}, ReadOnly: []string{"Id"}},
	}
	importGrammar := map[string]importGrammarRow{
		"aws_test_agree_composite": {
			TFType:                "aws_test_agree_composite",
			ArgumentNamesAnyDepth: []string{"part_one", "part_two"},
			// No IDParts, no SoleIDPart: the doc's structured evidence
			// names no server-provided segment.
		},
		"aws_test_disagree_serverid": {
			TFType:     "aws_test_disagree_serverid",
			SoleIDPart: &idPart{Token: "the resource's own ID", Source: idPartSourceOwnID},
		},
		// aws_test_no_grammar_row deliberately has no entry here.
		"aws_test_registry_alone_disagrees": {
			TFType: "aws_test_registry_alone_disagrees",
			// No IDParts, no SoleIDPart, same as the agree case - the
			// doc side alone cannot tell these two types apart.
		},
	}

	vetoed := setOf(markerlessRoster(nil, survey, proposals, importGrammar, nil, nil))

	if vetoed["aws_test_agree_composite"] {
		t.Error("aws_test_agree_composite: registry and docs agree the identity is argument-built, but the roster still vetoes it")
	}
	if !vetoed["aws_test_disagree_serverid"] {
		t.Error("aws_test_disagree_serverid: registry and docs BOTH say server-minted; this must stay vetoed")
	}
	if !vetoed["aws_test_no_grammar_row"] {
		t.Error("aws_test_no_grammar_row: no import-grammar row means only one source has an opinion at all; a lone source is not agreement, so this must stay vetoed")
	}
	if !vetoed["aws_test_registry_alone_disagrees"] {
		t.Error("aws_test_registry_alone_disagrees: the registry alone says server-minted and the doc is merely silent, not agreeing; this must stay vetoed")
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
	schemaFacts, err := loadSchemaFacts(filepath.Join(root, schemaFactsJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	contentMatch := contentMatchSet(contentMatchRoster(proposals, importGrammar, schemaFacts))
	vetoed := setOf(markerlessRoster(loadRatifiedForTest(t), survey, proposals, importGrammar, uniqueNameRows(loadRatifiedForTest(t), survey, proposals, importGrammar), contentMatch))

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

// TestMarkerlessRosterTwoSourcesAgreement pins issue #274's exception over
// the real committed inputs: CloudFormation's registry model and the
// provider's own import documentation, read independently of the
// classifier's bucket, agree the identity is built from configuration for
// three real types, and disagree for a fourth that must stay vetoed.
//
// aws_cognito_risk_configuration, aws_detective_member and
// aws_lambda_function_event_invoke_config are the agreement case: each has
// a composite CloudFormation primaryIdentifier with no read-only part, and
// the provider's Import section names no segment as the resource's own
// server-provided attribute - the classifier's bucketServerAssigned call for
// the first and third came from a documentation heuristic
// (tryOpaqueOverride) reading one import example that does not demonstrate
// every documented form, not from the registry's own rule 1.
//
// aws_route53_resolver_config is the disagreement case and the one this
// rule has to get right: the registry says ResourceId (supplied, not
// read-only) but the doc's own prose names the documented ID as "the Route
// 53 Resolver config ID" - the resource's own identifier, not an argument.
// The two sources disagree, so the ordinary veto must stand.
func TestMarkerlessRosterTwoSourcesAgreement(t *testing.T) {
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
	vetoed := setOf(markerlessRoster(loadRatifiedForTest(t), survey, proposals, importGrammar, uniqueNameRows(loadRatifiedForTest(t), survey, proposals, importGrammar), nil))

	for _, spared := range []string{
		"aws_cognito_risk_configuration",
		"aws_detective_member",
		"aws_lambda_function_event_invoke_config",
	} {
		if vetoed[spared] {
			t.Errorf("%s: registry and docs agree the identity is argument-built, but the roster still vetoes it", spared)
		}
	}

	const stillVetoed = "aws_route53_resolver_config"
	if !vetoed[stillVetoed] {
		t.Errorf("%s: the registry and the docs DISAGREE about this type's identity (registry says ResourceId, "+
			"the doc names the resource's own config ID) - it must stay vetoed, and it no longer is", stillVetoed)
	}
}
