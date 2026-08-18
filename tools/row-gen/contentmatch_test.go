// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"path/filepath"
	"testing"
)

// TestContentMatchRoster_TwoSourceJoin is a synthetic unit test over the
// join logic itself, independent of the real committed artifacts: it pins
// every shape the AND has to get right - both sources agreeing qualifies,
// either alone does not, a mismatched property leaf does not, and a fold
// row with no CFN type is skipped outright rather than panicking on an
// empty PropertyPath.
func TestContentMatchRoster_TwoSourceJoin(t *testing.T) {
	uniqueName := &struct {
		Path           []string `json:"path"`
		DeclaredUnique bool     `json:"declared_unique"`
	}{Path: []string{"FooConfig", "Name"}, DeclaredUnique: true}
	notUniqueName := &struct {
		Path           []string `json:"path"`
		DeclaredUnique bool     `json:"declared_unique"`
	}{Path: []string{"FooConfig", "Name"}, DeclaredUnique: false}

	tests := []struct {
		name        string
		proposals   []proposal
		grammar     map[string]importGrammarRow
		facts       map[string]schemaFactEntry
		wantTFTypes []string
	}{
		{
			name:      "both sources agree - qualifies",
			proposals: []proposal{{TFType: "aws_foo", CFNType: "AWS::Test::Foo"}},
			grammar: map[string]importGrammarRow{
				"aws_foo": {ArgumentReference: []argumentRefEntry{{Name: "name", DeclaredUnique: true}}},
			},
			facts:       map[string]schemaFactEntry{"AWS::Test::Foo": {TypeName: "AWS::Test::Foo", UniqueNameProperty: uniqueName}},
			wantTFTypes: []string{"aws_foo"},
		},
		{
			name:      "CFN says unique, provider docs do not - refused",
			proposals: []proposal{{TFType: "aws_foo", CFNType: "AWS::Test::Foo"}},
			grammar: map[string]importGrammarRow{
				"aws_foo": {ArgumentReference: []argumentRefEntry{{Name: "name", DeclaredUnique: false}}},
			},
			facts:       map[string]schemaFactEntry{"AWS::Test::Foo": {TypeName: "AWS::Test::Foo", UniqueNameProperty: uniqueName}},
			wantTFTypes: nil,
		},
		{
			name:      "provider docs say unique, CFN does not - refused (the OAC shape)",
			proposals: []proposal{{TFType: "aws_foo", CFNType: "AWS::Test::Foo"}},
			grammar: map[string]importGrammarRow{
				"aws_foo": {ArgumentReference: []argumentRefEntry{{Name: "name", DeclaredUnique: true}}},
			},
			facts:       map[string]schemaFactEntry{"AWS::Test::Foo": {TypeName: "AWS::Test::Foo", UniqueNameProperty: notUniqueName}},
			wantTFTypes: nil,
		},
		{
			name:      "neither source claims uniqueness - refused",
			proposals: []proposal{{TFType: "aws_foo", CFNType: "AWS::Test::Foo"}},
			grammar: map[string]importGrammarRow{
				"aws_foo": {ArgumentReference: []argumentRefEntry{{Name: "name", DeclaredUnique: false}}},
			},
			facts:       map[string]schemaFactEntry{"AWS::Test::Foo": {TypeName: "AWS::Test::Foo", UniqueNameProperty: notUniqueName}},
			wantTFTypes: nil,
		},
		{
			name:      "CFN's unique property leaf does not match any provider argument - refused",
			proposals: []proposal{{TFType: "aws_foo", CFNType: "AWS::Test::Foo"}},
			grammar: map[string]importGrammarRow{
				"aws_foo": {ArgumentReference: []argumentRefEntry{{Name: "title", DeclaredUnique: true}}},
			},
			facts:       map[string]schemaFactEntry{"AWS::Test::Foo": {TypeName: "AWS::Test::Foo", UniqueNameProperty: uniqueName}},
			wantTFTypes: nil,
		},
		{
			name:        "fold row with no CFN type - skipped, not a panic",
			proposals:   []proposal{{TFType: "aws_foo_child", CFNType: ""}},
			grammar:     map[string]importGrammarRow{},
			facts:       map[string]schemaFactEntry{},
			wantTFTypes: nil,
		},
		{
			name:        "CFN type absent from schema facts at all - refused, not an error",
			proposals:   []proposal{{TFType: "aws_foo", CFNType: "AWS::Test::Foo"}},
			grammar:     map[string]importGrammarRow{"aws_foo": {ArgumentReference: []argumentRefEntry{{Name: "name", DeclaredUnique: true}}}},
			facts:       map[string]schemaFactEntry{},
			wantTFTypes: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := contentMatchRoster(tc.proposals, tc.grammar, tc.facts)
			var gotTypes []string
			for _, r := range got {
				gotTypes = append(gotTypes, r.TFType)
			}
			if !equalStrings(gotTypes, tc.wantTFTypes) {
				t.Errorf("contentMatchRoster TF types = %v, want %v", gotTypes, tc.wantTFTypes)
			}
		})
	}
}

// TestContentMatchRoster_RealArtifacts is the issue's own evidence bar,
// against the real committed artifacts: aws_cloudfront_cache_policy and
// aws_cloudfront_origin_request_policy - the issue's two worked PROVEN
// examples - must qualify, and aws_cloudfront_origin_access_control - the
// issue's own worked NOT-proven negative case, which it explicitly asks to
// be kept as a permanent regression - must not. It also reports the
// measured reach: how many types the rule actually qualifies, not just the
// two named ones, since the issue is explicit that this must be measured
// rather than assumed.
func TestContentMatchRoster_RealArtifacts(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := loadProposals(root)
	if err != nil {
		t.Fatal(err)
	}
	grammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := loadSchemaFacts(filepath.Join(root, schemaFactsJSONRel))
	if err != nil {
		t.Fatal(err)
	}

	rows := contentMatchRoster(proposals, grammar, facts)

	byType := make(map[string]contentMatchRow, len(rows))
	for _, r := range rows {
		byType[r.TFType] = r
	}

	for _, want := range []struct {
		tfType   string
		cfnType  string
		argument string
		path     []string
	}{
		{"aws_cloudfront_cache_policy", "AWS::CloudFront::CachePolicy", "name", []string{"CachePolicyConfig", "Name"}},
		{"aws_cloudfront_origin_request_policy", "AWS::CloudFront::OriginRequestPolicy", "name", []string{"OriginRequestPolicyConfig", "Name"}},
	} {
		row, ok := byType[want.tfType]
		if !ok {
			t.Errorf("%s: expected to qualify (the issue's own PROVEN example), but contentMatchRoster refused it", want.tfType)
			continue
		}
		if row.CFNType != want.cfnType || row.Argument != want.argument || !equalStrings(row.PropertyPath, want.path) {
			t.Errorf("%s: got %+v, want CFNType=%s Argument=%s PropertyPath=%v", want.tfType, row, want.cfnType, want.argument, want.path)
		}
	}

	if _, ok := byType["aws_cloudfront_origin_access_control"]; ok {
		t.Error("aws_cloudfront_origin_access_control qualified - this is the issue's own permanent negative case " +
			"(the CFN description never says \"unique\", only \"a name to identify\"), and it must stay refused")
	}

	t.Logf("contentMatchRoster measured reach at the current artifacts: %d type(s) qualify: %v", len(rows), typeNamesOf(rows))
}

func typeNamesOf(rows []contentMatchRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.TFType
	}
	return out
}
