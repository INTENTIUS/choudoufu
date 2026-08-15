// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// nestedSchema exercises one nested extraction per field, which is what
// issue #155 asks these tests to pin. Every shape here is one the real
// schemas use: an enum under definitions/properties (S3's SSEAlgorithm), an
// enum under items for an array-valued property, an enum inside a oneOf
// alternative, permissions on several handlers, and a root required list.
const nestedSchema = `{
  "typeName": "AWS::Test::Thing",
  "required": ["/properties/BucketName", "Mode"],
  "handlers": {
    "create": {"permissions": ["s3:CreateBucket", "s3:PutBucketTagging"]},
    "read":   {"permissions": ["s3:GetBucketTagging"]},
    "list":   {"permissions": ["s3:ListAllMyBuckets"]},
    "delete": {}
  },
  "properties": {
    "Mode":      {"type": "string", "enum": ["Disabled", "Enabled"]},
    "Tiers":     {"type": "array", "items": {"type": "string", "enum": ["hot", "cold"]}},
    "Choice":    {"oneOf": [{"enum": ["a", "b"]}, {"type": "string"}]},
    "Retention": {"type": "integer", "enum": [1, 7, 30]},
    "Plain":     {"type": "string"}
  },
  "definitions": {
    "ServerSideEncryptionByDefault": {
      "type": "object",
      "properties": {
        "SSEAlgorithm": {"type": "string", "enum": ["aws:kms", "AES256", "aws:kms:dsse"]}
      }
    }
  }
}`

func factOf(t *testing.T, raw string) SchemaFact {
	t.Helper()
	fact, unattributed, err := parseSchemaFacts([]byte(raw))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if unattributed != 0 {
		t.Errorf("%d enum(s) could not be attributed to a property; every enum in this fixture is "+
			"under a named properties or definitions key", unattributed)
	}
	return fact
}

func enumFor(f SchemaFact, property string) *SchemaEnum {
	for i := range f.Enums {
		if f.Enums[i].Property == property {
			return &f.Enums[i]
		}
	}
	return nil
}

// TestHandlerPermissionsAreExtractedPerOperation pins the field #143 needs:
// the actions each CRUDL operation declares, kept apart by operation rather
// than flattened, because a least-privilege policy for a read differs from
// one for a create.
func TestHandlerPermissionsAreExtractedPerOperation(t *testing.T) {
	f := factOf(t, nestedSchema)

	want := map[string][]string{
		"create": {"s3:CreateBucket", "s3:PutBucketTagging"},
		"read":   {"s3:GetBucketTagging"},
		"list":   {"s3:ListAllMyBuckets"},
	}
	if !reflect.DeepEqual(f.HandlerPermissions, want) {
		t.Errorf("HandlerPermissions = %v, want %v", f.HandlerPermissions, want)
	}
	if _, ok := f.HandlerPermissions["delete"]; ok {
		t.Error("a handler declaring no permissions was recorded with an empty list; " +
			"absent and empty are different claims and only one of them is true here")
	}
}

// TestRootRequiredIsExtractedAndStripped pins #136's second source. The
// paths are stripped the same way every other property path in these
// artifacts is, so a consumer joining against them does not have to know
// which ones happened to be written as JSON pointers.
func TestRootRequiredIsExtractedAndStripped(t *testing.T) {
	f := factOf(t, nestedSchema)
	want := []string{"BucketName", "Mode"}
	if !reflect.DeepEqual(f.Required, want) {
		t.Errorf("Required = %v, want %v", f.Required, want)
	}
}

// TestEnumsAreFoundWhereverTheyNest is the walk's own test. Each case is a
// shape a struct-field parser would miss, which is why extractEnums walks
// generically: an enum under a definition's properties, under items, and
// inside a oneOf alternative.
func TestEnumsAreFoundWhereverTheyNest(t *testing.T) {
	f := factOf(t, nestedSchema)

	for _, tc := range []struct {
		property string
		members  []string
		why      string
	}{
		{"SSEAlgorithm", []string{"aws:kms", "AES256", "aws:kms:dsse"}, "under definitions/<name>/properties"},
		{"Tiers", []string{"hot", "cold"}, "under items, for an array-valued property"},
		{"Choice", []string{"a", "b"}, "inside a oneOf alternative"},
		{"Mode", []string{"Disabled", "Enabled"}, "directly on a property"},
		{"Retention", []string{"1", "7", "30"}, "a numeric enum, rendered as strings"},
	} {
		got := enumFor(f, tc.property)
		if got == nil {
			t.Errorf("no enum extracted for %s (%s)", tc.property, tc.why)
			continue
		}
		if !reflect.DeepEqual(got.Members, tc.members) {
			t.Errorf("%s members = %v, want %v", tc.property, got.Members, tc.members)
		}
		if got.Pointer == "" {
			t.Errorf("%s carries no pointer, so an unattributed one could not be inspected", tc.property)
		}
	}

	if e := enumFor(f, "Plain"); e != nil {
		t.Errorf("a property with no enum produced one: %v", e)
	}
}

// TestEnumMemberOrderIsSchemaOrder pins the property the doc comment warns
// about, because a consumer will be tempted to take the first member.
//
// Sorting would make Mode read ["Disabled", "Enabled"] here and everywhere,
// which looks tidier and quietly changes which value "the first member"
// names. The schemas spell the same Status enum both ways, so order is
// reproducible and not meaningful, and this test exists so that stays true
// rather than becoming true by accident.
func TestEnumMemberOrderIsSchemaOrder(t *testing.T) {
	f := factOf(t, `{
	  "typeName": "AWS::Test::Ordered",
	  "properties": {
	    "Reversed": {"enum": ["Enabled", "Disabled"]}
	  }
	}`)
	got := enumFor(f, "Reversed")
	if got == nil {
		t.Fatal("no enum extracted")
	}
	if want := []string{"Enabled", "Disabled"}; !reflect.DeepEqual(got.Members, want) {
		t.Errorf("members = %v, want %v (schema order, not sorted)", got.Members, want)
	}
}

// TestObjectValuedEnumIsRejectedRatherThanFlattened. An enum whose members
// are objects is a shape this artifact cannot express, and inventing a
// string for it would put a value in the artifact that appears nowhere in
// the schema.
func TestObjectValuedEnumIsRejectedRatherThanFlattened(t *testing.T) {
	f := factOf(t, `{
	  "typeName": "AWS::Test::Objecty",
	  "properties": {
	    "Weird": {"enum": [{"a": 1}, {"b": 2}]}
	  }
	}`)
	if e := enumFor(f, "Weird"); e != nil {
		t.Errorf("an object-valued enum was extracted as %v; it has no string rendering", e.Members)
	}
}

// TestSchemaFactsAreDeterministic is issue #155's "re-running twice produces
// no diff", checked at the level that would produce the diff.
//
// Go map iteration is randomized, and this artifact walks three maps per
// schema - handlers, properties, definitions. Without the sorts, the same
// input produces a different byte stream on most runs, and the artifact
// churns on every regeneration for no reason.
func TestSchemaFactsAreDeterministic(t *testing.T) {
	schemas := map[string][]byte{
		"AWS::Test::Thing":   []byte(nestedSchema),
		"AWS::Test::Ordered": []byte(`{"typeName":"AWS::Test::Ordered","properties":{"A":{"enum":["x","y"]}}}`),
		"AWS::Test::Bare":    []byte(`{"typeName":"AWS::Test::Bare"}`),
	}

	first, err := buildSchemaFacts(SpecPin{}, schemas)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	firstData, err := first.marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 8; i++ {
		again, err := buildSchemaFacts(SpecPin{}, schemas)
		if err != nil {
			t.Fatalf("rebuild %d: %v", i, err)
		}
		againData, err := again.marshal()
		if err != nil {
			t.Fatalf("marshal %d: %v", i, err)
		}
		if !bytes.Equal(firstData, againData) {
			t.Fatalf("run %d produced a different artifact from run 0; a map is being walked unsorted", i)
		}
	}
}

// TestEveryTypeIsListedEvenWithNoFacts. A type carrying none of the three
// fields still gets a row, so the roster is the full set and absence reads
// as absence rather than as a type the extraction missed.
func TestEveryTypeIsListedEvenWithNoFacts(t *testing.T) {
	art, err := buildSchemaFacts(SpecPin{}, map[string][]byte{
		"AWS::Test::Bare": []byte(`{"typeName":"AWS::Test::Bare"}`),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(art.Types) != 1 || art.Types[0].TypeName != "AWS::Test::Bare" {
		t.Errorf("a type with no extracted facts was dropped: %v", art.Types)
	}
	if art.Counts.Types != 1 {
		t.Errorf("Counts.Types = %d, want 1", art.Counts.Types)
	}
}

// TestShippedSchemaFactsCountsMatchItsRows keeps the counts block honest
// against the artifact it summarises. The counts are the claim issue #155
// says to trust over the numbers in the issue, so they must be recomputable
// from the rows rather than accumulated separately and hoped over.
func TestShippedSchemaFactsCountsMatchItsRows(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skipf("repo root: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, schemaFactsRel))
	if err != nil {
		t.Skipf("%s not generated: %v", schemaFactsRel, err)
	}
	var art SchemaFacts
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("decoding %s: %v", schemaFactsRel, err)
	}

	var perms, acts, required, enums, members int
	for _, ty := range art.Types {
		if len(ty.HandlerPermissions) > 0 {
			perms++
			for _, a := range ty.HandlerPermissions {
				acts += len(a)
			}
		}
		if len(ty.Required) > 0 {
			required++
		}
		if len(ty.Enums) > 0 {
			enums++
			for _, e := range ty.Enums {
				members += len(e.Members)
			}
		}
	}

	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"types", art.Counts.Types, len(art.Types)},
		{"with_handler_permissions", art.Counts.WithHandlerPermissions, perms},
		{"handler_permission_actions", art.Counts.HandlerPermissionActs, acts},
		{"with_required", art.Counts.WithRequired, required},
		{"with_enums", art.Counts.WithEnums, enums},
		{"enum_members", art.Counts.EnumMembers, members},
	} {
		if c.got != c.want {
			t.Errorf("counts.%s = %d, but the rows say %d", c.name, c.got, c.want)
		}
	}
}
