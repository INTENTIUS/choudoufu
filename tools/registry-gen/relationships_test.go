// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestExtractRelationshipsShapes pins one extraction per shape the roster
// actually uses. The walk is generic on purpose (see relationships.go), so
// these are the evidence that it reaches each shape rather than a list of
// shapes it knows about.
func TestExtractRelationshipsShapes(t *testing.T) {
	cases := []struct {
		name     string
		schema   string
		want     []Relationship
		wantPtrs []string
	}{
		{
			name: "directly on a property",
			schema: `{"typeName":"AWS::X::Y","properties":{"VpcId":{
				"type":"string","relationshipRef":{"typeName":"AWS::EC2::VPC","propertyPath":"/properties/VpcId"}}}}`,
			want: []Relationship{{Property: "VpcId", TypeName: "AWS::EC2::VPC", PropertyPath: "VpcId"}},
		},
		{
			name: "inside an anyOf alternative",
			schema: `{"typeName":"AWS::X::Y","properties":{"Key":{"anyOf":[
				{"relationshipRef":{"typeName":"AWS::KMS::Key","propertyPath":"/properties/Arn"}},
				{"relationshipRef":{"typeName":"AWS::KMS::Key","propertyPath":"/properties/KeyId"}}]}}}`,
			want: []Relationship{
				{Property: "Key", TypeName: "AWS::KMS::Key", PropertyPath: "Arn"},
				{Property: "Key", TypeName: "AWS::KMS::Key", PropertyPath: "KeyId"},
			},
		},
		{
			name: "under items, for an array property",
			schema: `{"typeName":"AWS::X::Y","properties":{"SubnetIds":{"type":"array","items":{
				"relationshipRef":{"typeName":"AWS::EC2::Subnet","propertyPath":"/properties/SubnetId"}}}}}`,
			want: []Relationship{{Property: "SubnetIds", TypeName: "AWS::EC2::Subnet", PropertyPath: "SubnetId"}},
		},
		{
			name: "hanging off a named definition",
			schema: `{"typeName":"AWS::X::Y","definitions":{"VpcId":{
				"type":"string","relationshipRef":{"typeName":"AWS::EC2::VPC","propertyPath":"/properties/VpcId"}}}}`,
			want: []Relationship{{Property: "VpcId", TypeName: "AWS::EC2::VPC", PropertyPath: "VpcId"}},
		},
		{
			name: "a nested properties key wins over the definition enclosing it",
			schema: `{"typeName":"AWS::X::Y","definitions":{"EndpointConfiguration":{"properties":{"VpcEndpointIds":{
				"items":{"relationshipRef":{"typeName":"AWS::EC2::VPCEndpoint","propertyPath":"/properties/Id"}}}}}}}`,
			want: []Relationship{{Property: "VpcEndpointIds", TypeName: "AWS::EC2::VPCEndpoint", PropertyPath: "Id"}},
		},
		{
			name:   "an annotation missing half of itself is not a relationship",
			schema: `{"typeName":"AWS::X::Y","properties":{"A":{"relationshipRef":{"typeName":"AWS::EC2::VPC"}}}}`,
			want:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractRelationships([]byte(tc.schema))
			if err != nil {
				t.Fatalf("extractRelationships: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d relationships, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].Property != tc.want[i].Property ||
					got[i].TypeName != tc.want[i].TypeName ||
					got[i].PropertyPath != tc.want[i].PropertyPath {
					t.Errorf("relationship %d = %+v, want %+v", i, got[i], tc.want[i])
				}
				if got[i].Pointer == "" {
					t.Errorf("relationship %d carries no pointer; an unattributable one would be uninspectable", i)
				}
			}
		})
	}
}

// TestExtractRelationshipsIsDeterministic guards the artifact's stability:
// the walk iterates Go maps, so without the sort a regeneration would
// reorder rows and every re-run would diff.
func TestExtractRelationshipsIsDeterministic(t *testing.T) {
	schema := []byte(`{"typeName":"AWS::X::Y","properties":{
		"B":{"relationshipRef":{"typeName":"AWS::EC2::VPC","propertyPath":"/properties/VpcId"}},
		"A":{"relationshipRef":{"typeName":"AWS::EC2::Subnet","propertyPath":"/properties/SubnetId"}},
		"C":{"anyOf":[
			{"relationshipRef":{"typeName":"AWS::KMS::Key","propertyPath":"/properties/KeyId"}},
			{"relationshipRef":{"typeName":"AWS::KMS::Key","propertyPath":"/properties/Arn"}}]}}}`)

	first, err := extractRelationships(schema)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := extractRelationships(schema)
		if err != nil {
			t.Fatal(err)
		}
		for j := range first {
			if first[j] != again[j] {
				t.Fatalf("run %d differs at %d: %+v vs %+v", i, j, again[j], first[j])
			}
		}
	}
}
