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

	var perms, acts, required, enums, members, withUniqueProp, declaredUnique int
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
		if ty.UniqueNameProperty != nil {
			withUniqueProp++
			if ty.UniqueNameProperty.DeclaredUnique {
				declaredUnique++
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
		{"with_unique_name_property", art.Counts.WithUniqueNameProperty, withUniqueProp},
		{"unique_name_property_declared_unique", art.Counts.UniqueNamePropertyDeclaredUnique, declaredUnique},
	} {
		if c.got != c.want {
			t.Errorf("counts.%s = %d, but the rows say %d", c.name, c.got, c.want)
		}
	}
}

// TestFindUniqueNameProperty pins issue #272's CFN-registry evidence source
// against the real schema shapes measured in the cached registry zip at
// v6.59.0 (see live/registry-schema-facts.json once regenerated). Every
// fixture below is the real properties/definitions shape for the named
// type, trimmed to what the function reads.
func TestFindUniqueNameProperty(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want *UniqueNameProperty
	}{
		{
			// AWS::CloudFront::CachePolicy, one of the issue's two worked
			// PROVEN examples: the whole mutable config, Name included, is
			// wrapped in one top-level object.
			name: "cache policy - wrapped config, proven unique",
			doc: `{
				"typeName": "AWS::CloudFront::CachePolicy",
				"properties": {
					"Id": {"type": "string"},
					"CachePolicyConfig": {"$ref": "#/definitions/CachePolicyConfig"}
				},
				"definitions": {
					"CachePolicyConfig": {
						"type": "object",
						"properties": {
							"Name": {"type": "string", "description": "A unique name to identify the cache policy."},
							"Comment": {"type": "string", "description": "A comment."}
						}
					}
				}
			}`,
			want: &UniqueNameProperty{Path: []string{"CachePolicyConfig", "Name"}, DeclaredUnique: true},
		},
		{
			// AWS::CloudFront::OriginAccessControl - the issue's own
			// worked NOT-proven negative case: same wrapped-config shape,
			// but the description never says "unique".
			name: "origin access control - wrapped config, not proven (permanent negative case)",
			doc: `{
				"typeName": "AWS::CloudFront::OriginAccessControl",
				"properties": {
					"Id": {"type": "string"},
					"OriginAccessControlConfig": {"$ref": "#/definitions/OriginAccessControlConfig"}
				},
				"definitions": {
					"OriginAccessControlConfig": {
						"type": "object",
						"properties": {
							"Name": {"type": "string", "description": "A name that identifies the origin access control."}
						}
					}
				}
			}`,
			want: &UniqueNameProperty{Path: []string{"OriginAccessControlConfig", "Name"}, DeclaredUnique: false},
		},
		{
			// AWS::EKS::Cluster's real shape: a plain top-level Name, no
			// wrapping at all.
			name: "top-level Name, proven unique",
			doc: `{
				"typeName": "AWS::EKS::Cluster",
				"properties": {
					"Name": {"type": "string", "description": "The unique name to give to your cluster."},
					"Arn": {"type": "string"}
				}
			}`,
			want: &UniqueNameProperty{Path: []string{"Name"}, DeclaredUnique: true},
		},
		{
			// AWS::ResilienceHub::App's real shape: the resource's OWN
			// top-level Name says nothing about uniqueness, and its only
			// "unique"-bearing Name lives on EventSubscriptions, an
			// array - so this must resolve on the top-level property, not
			// fall through to the array member.
			name: "top-level Name present alongside an unrelated array member's Name",
			doc: `{
				"typeName": "AWS::ResilienceHub::App",
				"properties": {
					"Name": {"type": "string", "description": "Name of the app."},
					"EventSubscriptions": {
						"type": "array",
						"items": {"$ref": "#/definitions/EventSubscription"}
					}
				},
				"definitions": {
					"EventSubscription": {
						"type": "object",
						"properties": {
							"Name": {"type": "string", "description": "Unique name to identify an event subscription."}
						}
					}
				}
			}`,
			want: &UniqueNameProperty{Path: []string{"Name"}, DeclaredUnique: false},
		},
		{
			// AWS::IoTSiteWise::AssetModel's real shape: no top-level
			// Name property at all, and the only "unique"-wording Name in
			// the whole schema lives inside AssetModelCompositeModels, an
			// array - one of the issue's own "12+ other resource types"
			// that the structural gate has to exclude.
			name: "Name only inside an array-wrapped definition - never a candidate",
			doc: `{
				"typeName": "AWS::IoTSiteWise::AssetModel",
				"properties": {
					"AssetModelId": {"type": "string"},
					"AssetModelCompositeModels": {
						"type": "array",
						"items": {"$ref": "#/definitions/AssetModelCompositeModel"}
					}
				},
				"definitions": {
					"AssetModelCompositeModel": {
						"type": "object",
						"properties": {
							"Name": {"type": "string", "description": "A unique, friendly name for the asset composite model."}
						}
					}
				}
			}`,
			want: nil,
		},
		{
			// A type with no Name property anywhere.
			name: "no Name property at all",
			doc: `{
				"typeName": "AWS::Test::Nameless",
				"properties": {
					"Id": {"type": "string"}
				}
			}`,
			want: nil,
		},
		{
			// A top-level property that IS a $ref to a definitions entry
			// with no Name of its own - must not fall through to some
			// other definition.
			name: "wrapped config with no Name inside it",
			doc: `{
				"typeName": "AWS::Test::NoNameInWrapper",
				"properties": {
					"Config": {"$ref": "#/definitions/Config"}
				},
				"definitions": {
					"Config": {
						"type": "object",
						"properties": {
							"Comment": {"type": "string", "description": "A unique comment, believe it or not."}
						}
					}
				}
			}`,
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findUniqueNameProperty([]byte(tc.doc))
			if err != nil {
				t.Fatalf("findUniqueNameProperty: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findUniqueNameProperty = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDeclaredUniqueText pins the negation-aware test's own behavior,
// independent of the structural walk above - the same discipline
// tools/importdocs-gen's TestArgumentReferenceEntries_DeclaredUnique holds
// itself to, over the registry's prose instead of the provider's.
func TestDeclaredUniqueText(t *testing.T) {
	tests := []struct {
		name string
		desc string
		want bool
	}{
		{"proven unique", "A unique name to identify the cache policy.", true},
		{"not proven", "A name that identifies the origin access control.", false},
		{"explicit denial, do not", "Alias names do not need to be unique.", false},
		{"explicit denial, does not", "This value does not need to be unique.", false},
		{"unrelated negation does not suppress a later positive", "Spaces are not allowed. The name must be unique.", true},
		{"empty description", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := declaredUniqueText(tc.desc); got != tc.want {
				t.Errorf("declaredUniqueText(%q) = %v, want %v", tc.desc, got, tc.want)
			}
		})
	}
}
