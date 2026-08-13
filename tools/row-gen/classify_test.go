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
