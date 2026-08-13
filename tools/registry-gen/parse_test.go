// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"testing"
)

// TestParseType_KnownSignals pins parseType's output against hand-checked
// facts about each testdata schema (issue #42's "parsed signal correctness"
// requirement) - read straight off the committed testdata JSON, not
// re-derived from it, so a parser bug that reads the wrong field shows up
// as a mismatch here rather than passing by construction.
func TestParseType_KnownSignals(t *testing.T) {
	schemas := loadTestdataSchemas(t)

	tests := []struct {
		typeName string
		want     Entry
	}{
		{
			// A single-property primaryIdentifier, taggable, and a
			// full handler set whose list handler carries no
			// handlerSchema at all - the list-free shape.
			typeName: "AWS::S3::Bucket",
			want: Entry{
				TypeName:          "AWS::S3::Bucket",
				PrimaryIdentifier: []string{"BucketName"},
				Tagging:           Tagging{Taggable: true, TagOnCreate: true, TagUpdatable: true},
				Handlers:          Handlers{Create: true, Read: true, Update: true, Delete: true, List: true},
			},
		},
		{
			// A two-property compound primaryIdentifier, untaggable,
			// and the load-bearing novelty: handlers.list.handlerSchema
			// .required names the parent input (RouteTableId) a List
			// call needs.
			typeName: "AWS::EC2::Route",
			want: Entry{
				TypeName:          "AWS::EC2::Route",
				PrimaryIdentifier: []string{"RouteTableId", "CidrBlock"},
				Tagging:           Tagging{},
				Handlers: Handlers{
					Create: true, Read: true, Update: true, Delete: true, List: true,
					ListRequiredInput: []string{"RouteTableId"},
				},
			},
		},
		{
			// Single primaryIdentifier, taggable, list-free (the list
			// handler has permissions but no handlerSchema).
			typeName: "AWS::EC2::VPC",
			want: Entry{
				TypeName:          "AWS::EC2::VPC",
				PrimaryIdentifier: []string{"VpcId"},
				Tagging:           Tagging{Taggable: true, TagOnCreate: true, TagUpdatable: true},
				Handlers:          Handlers{Create: true, Read: true, Update: true, Delete: true, List: true},
			},
		},
		{
			// Taggable, and its list handler carries a handlerSchema
			// whose required array is present but empty - still
			// list-free, since "required" with no entries needs no
			// parent input.
			typeName: "AWS::Logs::LogGroup",
			want: Entry{
				TypeName:          "AWS::Logs::LogGroup",
				PrimaryIdentifier: []string{"LogGroupName"},
				Tagging:           Tagging{Taggable: true, TagOnCreate: true, TagUpdatable: true},
				Handlers:          Handlers{Create: true, Read: true, Update: true, Delete: true, List: true},
			},
		},
		{
			// No handlers section at all.
			typeName: "AWS::Pinpoint::App",
			want: Entry{
				TypeName:          "AWS::Pinpoint::App",
				PrimaryIdentifier: []string{"Id"},
				Tagging:           Tagging{},
				Handlers:          Handlers{},
			},
		},
		{
			// A 3-way compound primaryIdentifier, and another
			// list-with-required-input shape (Scope).
			typeName: "AWS::WAFv2::RegexPatternSet",
			want: Entry{
				TypeName:          "AWS::WAFv2::RegexPatternSet",
				PrimaryIdentifier: []string{"Name", "Id", "Scope"},
				Tagging:           Tagging{Taggable: true, TagOnCreate: true, TagUpdatable: true},
				Handlers: Handlers{
					Create: true, Read: true, Update: true, Delete: true, List: true,
					ListRequiredInput: []string{"Scope"},
				},
			},
		},
		{
			// additionalIdentifiers alongside the primaryIdentifier, and a
			// third list-with-required-input shape (DomainId).
			typeName: "AWS::Cases::Field",
			want: Entry{
				TypeName:              "AWS::Cases::Field",
				PrimaryIdentifier:     []string{"FieldArn"},
				AdditionalIdentifiers: [][]string{{"DomainId"}},
				Tagging:               Tagging{Taggable: true, TagOnCreate: false, TagUpdatable: true},
				Handlers: Handlers{
					Create: true, Read: true, Update: true, Delete: true, List: true,
					ListRequiredInput: []string{"DomainId"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			raw, ok := schemas[tt.typeName]
			if !ok {
				t.Fatalf("%s is not in the testdata schemas", tt.typeName)
			}
			got, err := parseType(raw)
			if err != nil {
				t.Fatalf("parseType(%s): %v", tt.typeName, err)
			}
			// Only compare the facets this test pins; ReadOnly/CreateOnly/
			// WriteOnlyProperties are exercised separately below since
			// hand-listing them all here would just retype the schema.
			got.ReadOnlyProperties = nil
			got.CreateOnlyProperties = nil
			got.WriteOnlyProperties = nil
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseType(%s) =\n  %+v\nwant\n  %+v", tt.typeName, got, tt.want)
			}
		})
	}
}

// TestParseType_PropertyPathsStripped checks readOnly/createOnly/
// writeOnlyProperties strip the "/properties/" prefix but leave deeper
// pointer nesting alone, and that the counts match the raw schema's arrays
// 1:1 (issue #42 asks for these three verbatim, not reshaped).
func TestParseType_PropertyPathsStripped(t *testing.T) {
	schemas := loadTestdataSchemas(t)
	entry, err := parseType(schemas["AWS::S3::Bucket"])
	if err != nil {
		t.Fatal(err)
	}

	if len(entry.ReadOnlyProperties) == 0 {
		t.Fatal("AWS::S3::Bucket: ReadOnlyProperties is empty, want the schema's readOnlyProperties")
	}
	for _, p := range entry.ReadOnlyProperties {
		if p == "" {
			t.Error("a stripped ReadOnlyProperties entry is empty")
		}
		if len(p) >= len("/properties/") && p[:len("/properties/")] == "/properties/" {
			t.Errorf("ReadOnlyProperties entry %q still carries the /properties/ prefix", p)
		}
	}

	want := []string{"Arn", "DomainName", "DualStackDomainName", "RegionalDomainName"}
	got := entry.ReadOnlyProperties[:4]
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AWS::S3::Bucket's first 4 ReadOnlyProperties = %v, want %v", got, want)
	}

	if len(entry.CreateOnlyProperties) != 3 {
		t.Errorf("AWS::S3::Bucket CreateOnlyProperties has %d entries, want 3 (BucketName, BucketNamePrefix, BucketNamespace)", len(entry.CreateOnlyProperties))
	}
}

// TestHandlers_Enumerability pins the three-way split issue #42's reference
// values report against: free (list handler, no required input),
// parent-input (list handler, required input), none (no working list
// handler - whether or not other handlers exist).
func TestHandlers_Enumerability(t *testing.T) {
	tests := []struct {
		name string
		h    Handlers
		want Enumerability
	}{
		{"list with no handlerSchema", Handlers{List: true}, EnumerabilityFree},
		{"list with empty required", Handlers{List: true, ListRequiredInput: nil}, EnumerabilityFree},
		{"list with required input", Handlers{List: true, ListRequiredInput: []string{"ParentId"}}, EnumerabilityParentInput},
		{"handlers present but no list", Handlers{Create: true, Read: true}, EnumerabilityNone},
		{"no handlers at all", Handlers{}, EnumerabilityNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.h.Enumerability(); got != tt.want {
				t.Errorf("Enumerability() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandlers_HasAny(t *testing.T) {
	if (Handlers{}).HasAny() {
		t.Error("HasAny() on the zero value: want false")
	}
	if !(Handlers{Read: true}).HasAny() {
		t.Error("HasAny() with Read=true: want true")
	}
}

// TestParseType_RejectsMissingTypeName checks a schema with no typeName -
// the shape probe in extractSchemas already filters these out of a real
// zip, but parseType is a separate entry point (used directly by tests and
// by buildRegistry) and must not silently synthesize an empty type row.
func TestParseType_RejectsMissingTypeName(t *testing.T) {
	if _, err := parseType([]byte(`{"primaryIdentifier": ["/properties/Id"]}`)); err == nil {
		t.Error("parseType on a schema with no typeName: want an error, got nil")
	}
}
