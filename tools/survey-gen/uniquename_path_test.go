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
)

// TestSurveyFullClassifiesUniqueNameBoundTypes pins the four rows this
// classifier change exists for. internal/live/discovery/uniquename.go binds
// a live object for these types by comparing the configuration's declared
// name against the listed object's own name - the crossing
// tools/row-gen/uniquename.go computed to admit them into
// internal/live/identity's table (TypeIdentity.UniqueName). Before this
// change live/survey-full.json classified all four "enumerable, unbindable"
// and said "no discovery leg can bind what it returns", which was true of
// the generator and false of the code the moment the unique-name leg
// landed. This test is the artifact's half of that fix, held under test so
// a future classifier change cannot silently revert it.
func TestSurveyFullClassifiesUniqueNameBoundTypes(t *testing.T) {
	rows := surveyFullRowsByType(t)

	uniqueNameTypes := []string{
		"aws_cloudfront_cache_policy",
		"aws_cloudfront_origin_request_policy",
		"aws_cloudfront_response_headers_policy",
		"aws_route53_cidr_collection",
	}
	for _, typeName := range uniqueNameTypes {
		row, ok := rows[typeName]
		if !ok {
			t.Fatalf("live/survey-full.json has no row for %s", typeName)
		}
		if row.Path != pathUniqueName {
			t.Errorf("%s classifies %q, want %q - the unique-name discovery leg "+
				"(internal/live/discovery/uniquename.go) binds this type today, and the "+
				"survey has to say so rather than assert nothing can bind it",
				typeName, row.Path, pathUniqueName)
		}
		if row.Signals.Taggable {
			t.Errorf("%s is taggable in the survey's own signals; the unique-name path is only "+
				"for a type with no tags argument to write a marker into - it should be on the "+
				"marker path instead", typeName)
		}
		// The evidence sentence has to state what was actually checked: two
		// independent sources agreeing the name is unique, and that binding
		// reads that name rather than a tag - not the retired "no discovery
		// leg can bind what it returns" claim.
		if !strings.Contains(row.Evidence, "unique within the account and region") {
			t.Errorf("%s evidence %q does not say the name is documented unique within the account and region",
				typeName, row.Evidence)
		}
		if !strings.Contains(row.Evidence, "rather than by an ownership tag") {
			t.Errorf("%s evidence %q does not say binding is by name rather than by tag", typeName, row.Evidence)
		}
		if strings.Contains(row.Evidence, "no discovery leg can bind") {
			t.Errorf("%s evidence %q still carries the retired unbindable claim", typeName, row.Evidence)
		}
	}
}

// TestSurveyFullOriginAccessControlStaysUnbindable is the permanent negative
// case: aws_cloudfront_origin_access_control is untaggable, like the four
// unique-name types, but neither the provider's argument reference nor the
// CloudFormation registry schema documents its name as account-and-region
// unique (SURVEY.md's per-type table records why: it was retracted from the
// registry-ratified "list and match on name" route by the 2026-08-16
// markerless ruling precisely because AWS makes no such promise for OAC
// names). It must stay on enumerable, unbindable - a classifier change that
// starts crossing looser evidence for this type would be reading a
// guarantee AWS never made.
func TestSurveyFullOriginAccessControlStaysUnbindable(t *testing.T) {
	rows := surveyFullRowsByType(t)
	row, ok := rows["aws_cloudfront_origin_access_control"]
	if !ok {
		t.Fatal("live/survey-full.json has no row for aws_cloudfront_origin_access_control")
	}
	if row.Path != pathEnumerableUnbindable {
		t.Errorf("aws_cloudfront_origin_access_control classifies %q, want %q - its evidence does not "+
			"support unique naming and it is the permanent negative case for the unique-name path",
			row.Path, pathEnumerableUnbindable)
	}
	if strings.Contains(row.Evidence, "unique within the account and region") {
		t.Errorf("aws_cloudfront_origin_access_control evidence %q now claims a uniqueness guarantee; "+
			"review whether the unique-name branch has widened past its two-source crossing", row.Evidence)
	}
}

// surveyFullRowsByType reads live/survey-full.json, the same artifact
// TestSurveyArtifactsMatchTheirExpectations checks in drift_test.go, indexed
// by type name.
func surveyFullRowsByType(t *testing.T) map[string]Row {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, surveyFullJSONRel)) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v", surveyFullJSONRel, err)
	}
	var survey Survey
	if err := json.Unmarshal(data, &survey); err != nil {
		t.Fatalf("decoding %s: %v", surveyFullJSONRel, err)
	}
	out := make(map[string]Row, len(survey.Types))
	for _, row := range survey.Types {
		out[row.Type] = row
	}
	return out
}
