// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// osisLikeSurvey is the shape that produced the pinned-provider finding
// "offers aws_osis_pipeline.name as an identity source": the identity schema
// requires "name", and the resource schema spells that value pipeline_name,
// so survey-gen records "name" under not_resource_attributes.
func osisLikeSurvey() surveyEntry {
	return surveyEntry{
		Type: "aws_osis_pipeline",
		Path: "marker",
		Identity: &surveyIdentity{
			RequiredForImport:     []string{"name"},
			OptionalForImport:     []string{"account_id", "region"},
			NotResourceAttributes: []string{"account_id", "name"},
		},
	}
}

// TestMergeIdentityAttrsSkipsIdentityOnlyNames pins the one name the #197
// rule does not copy: an identity-schema attribute the resource schema has
// no attribute for. Before this rule the merge appended "name" here, and the
// table then offered aws_osis_pipeline.name as a reference target the
// provider does not serve (identity.VerifyTable, FindingAttributeNotInSchema,
// breaking, at the table's own pin).
func TestMergeIdentityAttrsSkipsIdentityOnlyNames(t *testing.T) {
	row := identity.TypeIdentity{Type: "aws_osis_pipeline", ServerAssigned: true, IdentityAttrs: []string{"pipeline_name"}}

	got := mergeIdentityAttrs(row, osisLikeSurvey())
	if want := []string{"pipeline_name"}; !reflect.DeepEqual(got.IdentityAttrs, want) {
		t.Fatalf("mergeIdentityAttrs copied an identity-schema name the resource schema does not carry: got %v, want %v", got.IdentityAttrs, want)
	}

	// The same schema with the vocabulary split removed still fills the
	// row, so the skip is exactly the not_resource_attributes fact and not
	// a wider retreat from the rule.
	agreeing := osisLikeSurvey()
	agreeing.Identity.NotResourceAttributes = []string{"account_id"}
	got = mergeIdentityAttrs(row, agreeing)
	if want := []string{"pipeline_name", "name"}; !reflect.DeepEqual(got.IdentityAttrs, want) {
		t.Fatalf("with no vocabulary split the rule should still append the schema's attribute: got %v, want %v", got.IdentityAttrs, want)
	}
}

// TestEmitRefusesAnIdentityOnlyNameInIdentityAttrs is the guard over the
// ratified side: a row that names the identity-schema spelling by hand does
// not reach the generated table, and the refusal names the row to correct.
func TestEmitRefusesAnIdentityOnlyNameInIdentityAttrs(t *testing.T) {
	survey := map[string]surveyEntry{"aws_osis_pipeline": osisLikeSurvey()}
	types := []string{"aws_osis_pipeline", "aws_other"}

	bad := map[string]identity.TypeIdentity{
		"aws_osis_pipeline": {Type: "aws_osis_pipeline", ServerAssigned: true, IdentityAttrs: []string{"name"}},
		"aws_other":         {Type: "aws_other", ServerAssigned: true, IdentityAttrs: []string{"id"}},
	}
	err := checkIdentityAttrsAreResourceAttrs(types, bad, survey)
	if err == nil {
		t.Fatal("a ratified IdentityAttrs naming the identity schema's spelling was emitted; want a refusal")
	}
	for _, want := range []string{"aws_osis_pipeline.name", ratifiedJSONRel, "not_resource_attributes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "aws_other") {
		t.Errorf("the refusal names a row the survey has no identity schema for:\n%s", err)
	}

	good := map[string]identity.TypeIdentity{
		"aws_osis_pipeline": {Type: "aws_osis_pipeline", ServerAssigned: true, IdentityAttrs: []string{"pipeline_name"}},
		"aws_other":         bad["aws_other"],
	}
	if err := checkIdentityAttrsAreResourceAttrs(types, good, survey); err != nil {
		t.Fatalf("the corrected row was refused: %v", err)
	}
}

// TestApplyResourceVocabularyDropsIdentityOnlyNames pins the proposal side:
// rule 9's correction to the identity schema's "name" is withdrawn when the
// survey says the resource has no such attribute, and nothing else moves.
func TestApplyResourceVocabularyDropsIdentityOnlyNames(t *testing.T) {
	proposals := []proposal{
		{TFType: "aws_osis_pipeline", Bucket: bucketServerAssigned, DerivedIdentityAttrs: []string{"name"}},
		{TFType: "aws_two_names", Bucket: bucketServerAssigned, DerivedIdentityAttrs: []string{"arn", "name"}},
		{TFType: "aws_unsurveyed", Bucket: bucketServerAssigned, DerivedIdentityAttrs: []string{"name"}},
		{TFType: "aws_agreeing", Bucket: bucketServerAssigned, DerivedIdentityAttrs: []string{"arn"}},
	}
	two := osisLikeSurvey()
	two.Type = "aws_two_names"
	survey := map[string]surveyEntry{
		"aws_osis_pipeline": osisLikeSurvey(),
		"aws_two_names":     two,
		"aws_agreeing":      {Type: "aws_agreeing", Identity: &surveyIdentity{RequiredForImport: []string{"arn"}, NotResourceAttributes: []string{"account_id"}}},
	}

	applyResourceVocabulary(proposals, survey)

	if got := proposals[0].DerivedIdentityAttrs; len(got) != 0 {
		t.Errorf("aws_osis_pipeline still claims %v; the survey says name is not a resource attribute", got)
	}
	if len(proposals[0].Notes) != 1 || !strings.Contains(proposals[0].Notes[0], `"name"`) {
		t.Errorf("the drop is not noted on the proposal: %v", proposals[0].Notes)
	}
	if got, want := proposals[1].DerivedIdentityAttrs, []string{"arn"}; !reflect.DeepEqual(got, want) {
		t.Errorf("aws_two_names: got %v, want %v", got, want)
	}
	if got, want := proposals[2].DerivedIdentityAttrs, []string{"name"}; !reflect.DeepEqual(got, want) || len(proposals[2].Notes) != 0 {
		t.Errorf("a type the survey does not cover was touched: %v %v", got, proposals[2].Notes)
	}
	if got, want := proposals[3].DerivedIdentityAttrs, []string{"arn"}; !reflect.DeepEqual(got, want) || len(proposals[3].Notes) != 0 {
		t.Errorf("an agreeing proposal was touched: %v %v", got, proposals[3].Notes)
	}
}
