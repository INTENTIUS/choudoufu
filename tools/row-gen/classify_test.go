// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestClassifyMapped_ServerAssigned pins rule 1: a primaryIdentifier that is
// wholly read-only classifies server-assigned regardless of arity - the
// marker path discovers such a type by listing, never by reconstructing an
// import string, so arity is not the composite problem here.
func TestClassifyMapped_ServerAssigned(t *testing.T) {
	e := registryEntry{
		TypeName:             "AWS::EC2::EIP",
		PrimaryIdentifier:    []string{"PublicIp", "AllocationId"},
		ReadOnlyProperties:   []string{"PublicIp", "AllocationId"},
		CreateOnlyProperties: nil,
	}
	e.Handlers.List = true

	p := classifyMapped("aws_eip", "AWS::EC2::EIP", e, nil, nil)
	if p.Bucket != bucketServerAssigned {
		t.Fatalf("bucket = %s, want %s", p.Bucket, bucketServerAssigned)
	}
}

// TestClassifyMapped_ClientNamed pins rule 2 with a confident argument
// source (the survey's identity schema).
func TestClassifyMapped_ClientNamed(t *testing.T) {
	e := registryEntry{
		TypeName:             "AWS::DynamoDB::Table",
		PrimaryIdentifier:    []string{"TableName"},
		ReadOnlyProperties:   []string{"Arn"},
		CreateOnlyProperties: []string{"TableName"},
	}
	e.Handlers.List = true

	survey := map[string]surveyEntry{
		"aws_dynamodb_table": {Type: "aws_dynamodb_table", Identity: &struct {
			RequiredForImport []string `json:"required_for_import"`
		}{RequiredForImport: []string{"name"}}},
	}

	p := classifyMapped("aws_dynamodb_table", "AWS::DynamoDB::Table", e, survey, nil)
	if p.Bucket != bucketClientNamed {
		t.Fatalf("bucket = %s, want %s", p.Bucket, bucketClientNamed)
	}
	if p.ArgName != "name" {
		t.Errorf("ArgName = %q, want %q", p.ArgName, "name")
	}
	if p.ArgSource != argSourceIdentitySchema {
		t.Errorf("ArgSource = %q, want %q", p.ArgSource, argSourceIdentitySchema)
	}
}

// TestClassifyMapped_ClientNamed_CarveSeedFallback pins the second
// preference: no survey identity schema, but the carve seed has an entry.
func TestClassifyMapped_ClientNamed_CarveSeedFallback(t *testing.T) {
	e := registryEntry{
		TypeName:             "AWS::ECS::Cluster",
		PrimaryIdentifier:    []string{"ClusterName"},
		ReadOnlyProperties:   []string{"Arn"},
		CreateOnlyProperties: []string{"ClusterName"},
	}
	e.Handlers.List = true

	p := classifyMapped("aws_ecs_cluster", "AWS::ECS::Cluster", e, nil, map[string]string{"aws_ecs_cluster": "name"})
	if p.Bucket != bucketClientNamed {
		t.Fatalf("bucket = %s, want %s", p.Bucket, bucketClientNamed)
	}
	if p.ArgName != "name" || p.ArgSource != argSourceCarveSeed {
		t.Errorf("got ArgName=%q ArgSource=%q, want name/%q", p.ArgName, p.ArgSource, argSourceCarveSeed)
	}
}

// TestClassifyMapped_GuessedIsEvidenceOnly pins the issue's explicit rule: a
// GUESSED argument name (neither the survey nor the carve seed has one)
// makes the row evidence-only, never a pastable client-named proposal.
func TestClassifyMapped_GuessedIsEvidenceOnly(t *testing.T) {
	e := registryEntry{
		TypeName:             "AWS::Foo::Bar",
		PrimaryIdentifier:    []string{"BarName"},
		ReadOnlyProperties:   []string{"Arn"},
		CreateOnlyProperties: []string{"BarName"},
	}
	e.Handlers.List = true

	p := classifyMapped("aws_foo_bar", "AWS::Foo::Bar", e, nil, nil)
	if p.Bucket != bucketEvidenceOnly {
		t.Fatalf("bucket = %s, want %s (GUESSED rows are evidence-only)", p.Bucket, bucketEvidenceOnly)
	}
	if p.ArgSource != argSourceGuessed {
		t.Errorf("ArgSource = %q, want %q", p.ArgSource, argSourceGuessed)
	}
	if p.ArgName != "bar_name" {
		t.Errorf("ArgName = %q, want %q", p.ArgName, "bar_name")
	}
}

// TestClassifyMapped_AWSRouteNeedsHandSeparator is the #39 trap the issue
// names explicitly: AWS::EC2::Route has a two-part primaryIdentifier
// (RouteTableId, CidrBlock) neither of which is read-only, so it must land
// in needs-hand-separator and never become a pastable row.
func TestClassifyMapped_AWSRouteNeedsHandSeparator(t *testing.T) {
	e := registryEntry{
		TypeName:             "AWS::EC2::Route",
		PrimaryIdentifier:    []string{"RouteTableId", "CidrBlock"},
		ReadOnlyProperties:   []string{"CidrBlock"},
		CreateOnlyProperties: []string{"RouteTableId", "DestinationCidrBlock", "DestinationIpv6CidrBlock", "DestinationPrefixListId"},
	}
	e.Handlers.List = true
	e.Handlers.ListRequiredInput = []string{"RouteTableId"}

	p := classifyMapped("aws_route", "AWS::EC2::Route", e, nil, nil)
	if p.Bucket != bucketNeedsHandSeparator {
		t.Fatalf("aws_route bucket = %s, want %s (the #39 trap)", p.Bucket, bucketNeedsHandSeparator)
	}
}

// TestClassifyMapped_AmbiguousShape pins the residual case: a singleton
// primaryIdentifier that is neither read-only nor create-only.
func TestClassifyMapped_AmbiguousShape(t *testing.T) {
	e := registryEntry{
		TypeName:             "AWS::Foo::Mutable",
		PrimaryIdentifier:    []string{"Name"},
		ReadOnlyProperties:   nil,
		CreateOnlyProperties: nil,
	}
	p := classifyMapped("aws_foo_mutable", "AWS::Foo::Mutable", e, nil, nil)
	if p.Bucket != bucketEvidenceOnly {
		t.Fatalf("bucket = %s, want %s", p.Bucket, bucketEvidenceOnly)
	}
}

// TestEnumerationStory pins rule 4's three-way split, mirroring
// tools/registry-gen's own Enumerability test.
func TestEnumerationStory(t *testing.T) {
	tests := []struct {
		name      string
		list      bool
		required  []string
		wantStory string
	}{
		{"not listable", false, nil, "not listable -> client-named only"},
		{"list-free", true, nil, "list-free"},
		{"parent-input", true, []string{"ParentId"}, "parent-input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			story, _ := enumerationStory(tt.list, tt.required)
			if story != tt.wantStory {
				t.Errorf("story = %q, want %q", story, tt.wantStory)
			}
		})
	}
}

// TestApplyImportGrammarDemotions_LambdaAliasShape reproduces the Lambda
// pilot's manual rejection (internal/live/identity/table.go's "Rejected"
// comment): row-gen classifies aws_lambda_alias server-assigned from the
// CFN registry's AliasArn (read-only there), but the provider's own Import
// docs show "function_name/alias_name" - an argument-composed ID, not an
// opaque one. Once live/import-grammar.json carries that evidence, the
// demotion pass must turn the proposal evidence-only and leave a note
// citing the doc's example, rather than a human catching it by hand every
// batch.
func TestApplyImportGrammarDemotions_LambdaAliasShape(t *testing.T) {
	proposals := []proposal{
		{TFType: "aws_lambda_alias", CFNType: "AWS::Lambda::Alias", Bucket: bucketServerAssigned},
	}
	composed := true
	sep := "/"
	grammar := map[string]importGrammarRow{
		"aws_lambda_alias": {
			TFType:              "aws_lambda_alias",
			ImportIDExample:     "example/production",
			ComposedOfArguments: &composed,
			Separator:           &sep,
		},
	}

	applyImportGrammarDemotions(proposals, grammar)

	p := proposals[0]
	if p.Bucket != bucketEvidenceOnly {
		t.Fatalf("aws_lambda_alias bucket = %s, want %s (demoted by import-docs evidence)", p.Bucket, bucketEvidenceOnly)
	}
	wantNote := "import docs show argument-composed ID: example/production"
	found := false
	for _, n := range p.Notes {
		if n == wantNote {
			found = true
		}
	}
	if !found {
		t.Errorf("Notes = %v, want it to include %q", p.Notes, wantNote)
	}
}

// TestApplyImportGrammarDemotions_LeavesOpaqueAlone is the negative
// control: a server-assigned proposal whose import-grammar row says
// composed_of_arguments is false (or the row is simply absent) must not be
// touched - the demotion is specifically for the argument-composed case,
// not a blanket "if we have any evidence, downgrade it" rule.
func TestApplyImportGrammarDemotions_LeavesOpaqueAlone(t *testing.T) {
	opaqueFalse := false
	proposals := []proposal{
		{TFType: "aws_kms_key", CFNType: "AWS::KMS::Key", Bucket: bucketServerAssigned},
		{TFType: "aws_no_grammar_row", CFNType: "AWS::Foo::Bar", Bucket: bucketServerAssigned},
		{TFType: "aws_already_client_named", CFNType: "AWS::Foo::Baz", Bucket: bucketClientNamed},
	}
	composed := true
	grammar := map[string]importGrammarRow{
		"aws_kms_key": {TFType: "aws_kms_key", ComposedOfArguments: &opaqueFalse},
		// aws_no_grammar_row deliberately absent.
		"aws_already_client_named": {TFType: "aws_already_client_named", ComposedOfArguments: &composed},
	}

	applyImportGrammarDemotions(proposals, grammar)

	if proposals[0].Bucket != bucketServerAssigned {
		t.Errorf("aws_kms_key bucket = %s, want unchanged %s (composed_of_arguments is false)", proposals[0].Bucket, bucketServerAssigned)
	}
	if proposals[1].Bucket != bucketServerAssigned {
		t.Errorf("aws_no_grammar_row bucket = %s, want unchanged %s (no import-grammar row at all)", proposals[1].Bucket, bucketServerAssigned)
	}
	if proposals[2].Bucket != bucketClientNamed {
		t.Errorf("aws_already_client_named bucket = %s, want unchanged %s (not a server-assigned proposal to begin with)", proposals[2].Bucket, bucketClientNamed)
	}
}

// TestClassifyFold pins the fold rule: a property-child always lands
// evidence-only, and notes a parent-derived proposal only when the fold
// parent's own TF type is itself proposed (server-assigned or
// client-named).
func TestClassifyFold(t *testing.T) {
	mapped := []proposal{
		{TFType: "aws_s3_bucket", CFNType: "AWS::S3::Bucket", Bucket: bucketClientNamed},
	}
	p := classifyFold("aws_s3_bucket_versioning", "AWS::S3::Bucket", mapped)
	if p.Bucket != bucketEvidenceOnly {
		t.Fatalf("fold bucket = %s, want %s", p.Bucket, bucketEvidenceOnly)
	}
	if !p.ParentKnown || p.ParentTFType != "aws_s3_bucket" {
		t.Fatalf("expected the fold parent resolved to aws_s3_bucket, got %+v", p)
	}
	if len(p.Notes) != 1 {
		t.Fatalf("expected exactly one note proposing parent-derived admission, got %v", p.Notes)
	}

	// A fold whose parent is not itself proposed (or not found at all)
	// gets a note saying so, not a parent-derived proposal.
	p2 := classifyFold("aws_orphan_child", "AWS::Nothing::Here", mapped)
	if p2.ParentKnown {
		t.Fatalf("expected no parent match for an unmapped fold_parent, got %+v", p2)
	}
}
