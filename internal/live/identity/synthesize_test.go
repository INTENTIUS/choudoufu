// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// fallbackSchemas are the four shapes the fallback has to tell apart, none of
// them in [DefaultTable]:
//
//   - aws_thing: identity attribute is a required argument. The schemas
//     settle it.
//   - aws_named_thing: identity attribute is Optional+Computed, so only a
//     configuration can say whether it is a name or a server-assigned ID.
//   - aws_child_thing: same as aws_thing, and its argument is written as a
//     reference to another resource, which is how a synthesized entry gets
//     read as a parent. It also marks account_id optional-for-import,
//     absent from its own block same as aws_thing does, so that
//     [isContextAttr]'s corroboration requirement has two independently
//     "authored" types to compare account_id against rather than one - the
//     same shape real provider schemas have (AWS marks account_id
//     optional-for-import on hundreds of types, never as a block argument
//     on any of them).
//   - aws_composite_thing: two required identity attributes, joined into an
//     import ID by a character no schema carries.
func fallbackSchemas() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"aws_thing": {
			args:     map[string]string{"name": "req", "id": "optcomp"},
			identity: map[string]string{"name": "req", "account_id": "opt", "region": "opt"},
		},
		"aws_named_thing": {
			args:     map[string]string{"label": "optcomp", "size": "opt", "id": "optcomp"},
			identity: map[string]string{"label": "req", "region": "opt"},
		},
		"aws_child_thing": {
			args:     map[string]string{"parent": "req", "id": "optcomp"},
			identity: map[string]string{"parent": "req", "account_id": "opt"},
		},
		"aws_composite_thing": {
			args:     map[string]string{"left": "req", "right": "req"},
			identity: map[string]string{"left": "req", "right": "req"},
		},
	})
}

// routeSchema is aws_route's own shape, stood up as a fake schema so
// SynthesizeTypeIdentity can be asked about aws_route directly: one
// required identity attribute, route_table_id, plus the three destination_*
// arguments the real AWS provider marks optional for import. Going through
// Resolve instead would find DefaultTable's hand row for aws_route before
// synthesis ever ran, which is why this schema exists rather than reusing
// [fallbackSchemas].
func routeSchema() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"aws_route": {
			args: map[string]string{
				"route_table_id":              "req",
				"destination_cidr_block":      "opt",
				"destination_ipv6_cidr_block": "opt",
				"destination_prefix_list_id":  "opt",
			},
			identity: map[string]string{
				"route_table_id":              "req",
				"destination_cidr_block":      "opt",
				"destination_ipv6_cidr_block": "opt",
				"destination_prefix_list_id":  "opt",
			},
		},
	})
}

// TestSynthesizeTypeIdentityRefusesRoute pins #39: a single required
// identity attribute is not a complete identity when the schema also marks
// something other than the context pair (account_id, region) optional for
// import. aws_route's route_table_id passes the old "one attribute" bar on
// its own, and a synthesized entry keyed on it alone would name the route
// table, not the route - every route in the table would resolve to the
// same identity. The hand row in DefaultTable covers this today; this test
// is what stops synthesis from covering it wrongly if that row is ever
// deleted.
func TestSynthesizeTypeIdentityRefusesRoute(t *testing.T) {
	signal := scanFixture(t, "schema-fallback-route")
	schemas := routeSchema()

	if entry, ok := SynthesizeTypeIdentity("aws_route", schemas, signal); ok {
		t.Fatalf("aws_route was synthesized from route_table_id alone: %#v", entry)
	}
}

func resolveFallback(t *testing.T, fixture string, schemas map[string]providers.Schema) (*Result, string) {
	t.Helper()

	cfg := loadConfig(t, filepath.Join("testdata", fixture), nil)
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	var errText string
	if diags.HasErrors() {
		errText = diags.Err().Error()
	}
	return result, errText
}

// TestSchemaFallbackResolves is the whole point of the fallback: three types
// with no hand-written row, resolved from the provider's schemas and this
// configuration alone.
func TestSchemaFallbackResolves(t *testing.T) {
	result, errText := resolveFallback(t, "schema-fallback", fallbackSchemas())
	if errText != "" {
		t.Fatalf("resolving with schemas produced errors: %s", errText)
	}

	assertClassifications(t, result, map[string]string{
		"aws_thing.one":       "CONCRETE alpha",
		"aws_named_thing.two": "CONCRETE beta",
		// The child read its parent's identity attribute, and the parent was
		// already concrete, so the child is concrete too rather than a
		// formula.
		"aws_child_thing.three": "CONCRETE alpha",
	})
}

// TestSchemaFallbackAbsentSchemasUnchanged pins the property the whole design
// rests on: a caller with no schemas gets exactly what it got before this
// existed.
func TestSchemaFallbackAbsentSchemasUnchanged(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "schema-fallback"), nil)

	_, withoutDiags := Resolve(context.Background(), cfg)
	if !withoutDiags.HasErrors() {
		t.Fatal("a type outside the table resolved with no schemas to justify it")
	}
	if got := withoutDiags.Err().Error(); !strings.Contains(got, "aws_thing") {
		t.Errorf("the refusal does not name the type it refused: %s", got)
	}
	// And the refusal says nothing about schemas, because none were offered.
	if got := withoutDiags.Err().Error(); strings.Contains(got, "identity schema") {
		t.Errorf("a run with no schemas explained itself in terms of schemas: %s", got)
	}
}

// TestSchemaFallbackTypeNotServed: schemas in hand, but not for this type.
func TestSchemaFallbackTypeNotServed(t *testing.T) {
	_, errText := resolveFallback(t, "unknown-type", fallbackSchemas())
	if errText == "" {
		t.Fatal("aws_appstream_directory_config resolved against schemas that do not describe it")
	}
	if !strings.Contains(errText, "serves no aws_appstream_directory_config") {
		t.Errorf("the refusal does not say the provider never served the type: %s", errText)
	}
}

// TestSchemaFallbackConfigDeclines is the aws_vpc answer: the schemas leave
// the question open and the configuration answers "the cloud names this".
func TestSchemaFallbackConfigDeclines(t *testing.T) {
	_, errText := resolveFallback(t, "schema-fallback-declined", fallbackSchemas())
	if errText == "" {
		t.Fatal("a type whose identity argument the configuration never sets was admitted anyway")
	}
	if !strings.Contains(errText, `requires "label"`) {
		t.Errorf("the refusal does not name the attribute nothing supplies: %s", errText)
	}
}

// TestSchemaFallbackAdmitsCompositeByIdentityObject is GitHub issue #105.
//
// A two-attribute identity used to be refused outright, because joining the
// values into the provider's legacy import-ID grammar needs a separator no
// schema carries. Terraform 1.12 resource identity is what makes that stop
// mattering: the projection imports by identity object when the schema
// allows one, and a type reaching synthesis has an identity schema by
// definition.
//
// So the type resolves, and the string it cannot build is absent rather than
// invented. The absence is the whole safety property - see
// TestSynthesizedCompositeHasNoImportID.
func TestSchemaFallbackAdmitsCompositeByIdentityObject(t *testing.T) {
	result, errText := resolveFallback(t, "schema-fallback-composite", fallbackSchemas())
	if errText != "" {
		t.Fatalf("a composite identity whose attributes the configuration all names was refused: %s", errText)
	}
	if result == nil || result.Len() == 0 {
		t.Fatal("nothing resolved")
	}
}

// TestSynthesizedCompositeHasNoImportID is the guard #105 asks for before
// the relaxation above is safe.
//
// Component values are concatenated with nothing between them, so a
// two-attribute composite would otherwise produce "leftright" - a string
// that is not empty, so every downstream guard that tests for emptiness
// passes it through, and a fallback from the identity object would import it
// against a real account with a TRACE log to say so.
func TestSynthesizedCompositeHasNoImportID(t *testing.T) {
	result, errText := resolveFallback(t, "schema-fallback-composite", fallbackSchemas())
	if errText != "" {
		t.Fatalf("resolution failed: %s", errText)
	}

	var checked int
	for _, res := range result.All() {
		if res.Addr.Resource.Resource.Type != "aws_composite_thing" {
			continue
		}
		checked++
		if res.Class != ClassConcrete {
			t.Errorf("%s classified %s, want %s", res.Addr, res.Class, ClassConcrete)
		}
		if res.ImportID != "" {
			t.Errorf("%s carries the import ID %q, which is its identity values run together with no separator between them", res.Addr, res.ImportID)
		}
		if len(res.IdentityValues) != 2 {
			t.Errorf("%s carries %d identity values, want both attributes: %v", res.Addr, len(res.IdentityValues), res.IdentityValues)
		}
	}
	if checked == 0 {
		t.Fatal("no aws_composite_thing instance resolved, so this asserted nothing")
	}
}

// TestSynthesizeTypeIdentityShape pins what a synthesized entry holds,
// especially what it declines to hold: "id" is not handed out as an identity
// source, because whether a type's id equals its import identity is exactly
// the inference no schema carries.
func TestSynthesizeTypeIdentityShape(t *testing.T) {
	signal := scanFixture(t, "schema-fallback")

	entry, ok := SynthesizeTypeIdentity("aws_thing", fallbackSchemas(), signal)
	if !ok {
		t.Fatal("aws_thing was not synthesized")
	}
	if !entry.Synthesized {
		t.Error("the entry does not mark itself synthesized")
	}
	if entry.Admits != AdmitSchema {
		t.Errorf("aws_thing was admitted as %q, want %q", entry.Admits, AdmitSchema)
	}
	if len(entry.Components) != 1 || len(entry.Components[0].Attrs) != 1 || entry.Components[0].Attrs[0] != "name" {
		t.Errorf("components are %#v, want one reading \"name\"", entry.Components)
	}
	if entry.Components[0].IdentityAttr != "name" {
		t.Errorf("the component supplies identity attribute %q, want \"name\"", entry.Components[0].IdentityAttr)
	}
	if len(entry.IdentityAttrs) != 1 || entry.IdentityAttrs[0] != "name" {
		t.Errorf("identity attributes are %v, want [name] and nothing else", entry.IdentityAttrs)
	}

	named, ok := SynthesizeTypeIdentity("aws_named_thing", fallbackSchemas(), signal)
	if !ok {
		t.Fatal("aws_named_thing was not synthesized from the configuration that names it")
	}
	if named.Admits != AdmitConfigSignal {
		t.Errorf("aws_named_thing was admitted as %q, want %q", named.Admits, AdmitConfigSignal)
	}

	composite, ok := SynthesizeTypeIdentity("aws_composite_thing", fallbackSchemas(), signal)
	if !ok {
		t.Fatal("a composite identity whose attributes the configuration all names was not synthesized (#105)")
	}
	if !composite.IdentityObjectOnly {
		t.Error("a composite entry does not mark itself identity-object-only, so classify would concatenate its values into an import ID with no separator")
	}
	if len(composite.IdentityAttrs) != 2 {
		t.Errorf("composite identity attributes are %v, want both", composite.IdentityAttrs)
	}
	if len(composite.Components) != len(composite.IdentityAttrs) {
		t.Errorf("composite has %d components for %d identity attributes; each attribute needs its own so IdentityValues carries them all", len(composite.Components), len(composite.IdentityAttrs))
	}
	for _, c := range composite.Components {
		if c.IdentityAttr == "" {
			t.Errorf("composite component %#v supplies no identity attribute, so its value would never reach IdentityValues", c)
		}
		if c.Literal != "" {
			t.Errorf("composite component %#v carries a literal, which is the separator this must not invent", c)
		}
	}
	if _, ok := SynthesizeTypeIdentity("aws_thing", nil, signal); ok {
		t.Error("something was synthesized with no schemas at all")
	}
}

// gcpShapedSchemas stands up two unrelated GCP-shaped types the way
// hashicorp/google actually serves them (verified against the provider
// directly, not guessed): a required attribute that names the resource, plus
// "project" optional-for-import and absent from the resource's own block -
// the GCP analogue of AWS's account_id. Two independently-authored types is
// what [isContextAttr]'s corroboration bar needs to tell "project" apart
// from a type-specific default; see #218 and isContextAttr's doc comment.
func gcpShapedSchemas() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"google_storage_bucket": {
			args:     map[string]string{"name": "req"},
			identity: map[string]string{"name": "req", "project": "opt"},
		},
		"google_bigquery_dataset": {
			args:     map[string]string{"dataset_id": "req"},
			identity: map[string]string{"dataset_id": "req", "project": "opt"},
		},
	})
}

// TestIsContextAttrCorroboratesAcrossTypes is #218: project is not a fixed
// name this rule knows in advance, it is context because a second,
// independently-authored type in the same provider's schemas treats it the
// same way - absent from that type's own block too. That is the derived
// signal replacing the old {account_id, region} literal pair.
func TestIsContextAttrCorroboratesAcrossTypes(t *testing.T) {
	schemas := gcpShapedSchemas()
	bucket := schemas["google_storage_bucket"]
	if !isContextAttr(schemas, bucket, "project") {
		t.Error("project was not recognized as context even though a second GCP type corroborates it")
	}

	entry, ok := SynthesizeTypeIdentity("google_storage_bucket", schemas, nil)
	if !ok {
		t.Fatal("google_storage_bucket was not synthesized even though its only optional-for-import attribute is corroborated context")
	}
	if len(entry.IdentityAttrs) != 1 || entry.IdentityAttrs[0] != "name" {
		t.Errorf("identity attributes are %v, want [name] - project must not be pulled into the identity", entry.IdentityAttrs)
	}
}

// TestIsContextAttrRefusesUncorroboratedName is aws_dynamodb_table_item's
// shape: range_key_value is Computed in that type's own block and optional
// for import, and no other type in the (tiny, fake) provider schema set
// marks the same name the same way. A single type's own schema cannot say
// whether an optional-for-import name is shared provider infrastructure or
// a value specific to that one type, so an uncorroborated name is refused -
// the safe direction, per #218's own instruction.
func TestIsContextAttrRefusesUncorroboratedName(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_dynamodb_table_item": {
			args:     map[string]string{"table_name": "req", "hash_key_value": "req", "range_key_value": "comp"},
			identity: map[string]string{"table_name": "req", "hash_key_value": "req", "range_key_value": "opt"},
		},
	})
	item := schemas["aws_dynamodb_table_item"]
	if isContextAttr(schemas, item, "range_key_value") {
		t.Error("range_key_value was treated as context with no other type in the schema set corroborating it")
	}
}

// TestIsContextAttrNestedBlockNeverContext is google_project_iam_member's
// shape: the identity schema marks "condition_title" optional standing next
// to three required attributes, but the resource block carries no
// "condition_title" attribute at all - "title" is a required argument
// inside a nested "condition" block instead. A member/role/project triple
// bound with two different conditions is two different IAM bindings, so
// dropping condition_title as mere context would resolve both to the same
// synthesized identity. Corroboration from a second type must not override
// this: the nested match is decisive on its own.
func TestIsContextAttrNestedBlockNeverContext(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		// A second type that ALSO marks condition_title optional and absent
		// from its own top-level block, so a rule that only checked
		// corroboration (and not the nested match) would wrongly admit it.
		"google_other_iam_member": {
			args:     map[string]string{"member": "req", "role": "req"},
			identity: map[string]string{"member": "req", "role": "req", "condition_title": "opt"},
		},
	})
	block := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"member":  {Type: cty.String, Required: true},
			"project": {Type: cty.String, Required: true},
			"role":    {Type: cty.String, Required: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"condition": {
				Block: configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"title":      {Type: cty.String, Required: true},
						"expression": {Type: cty.String, Required: true},
					},
				},
				Nesting: configschema.NestingList,
			},
		},
	}
	schema := providers.Schema{
		Block: block,
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"member":          {Type: cty.String, Required: true},
				"project":         {Type: cty.String, Required: true},
				"role":            {Type: cty.String, Required: true},
				"condition_title": {Type: cty.String, Optional: true},
			},
		},
	}
	schemas["google_project_iam_member"] = schema

	if isContextAttr(schemas, schema, "condition_title") {
		t.Error("condition_title was treated as context even though it flattens a required argument of a nested block")
	}
	if _, ok := SynthesizeTypeIdentity("google_project_iam_member", schemas, nil); ok {
		t.Error("google_project_iam_member was synthesized despite condition_title naming a real nested identity component")
	}
}

// TestIsContextAttrNameAndIDNeverContext pins the two literal exceptions
// [isContextAttr] carries: "name" and "id" are never context, even when
// Optional+Computed and corroborated by a second type, because a resource's
// own conventional label is exactly as unsafe to drop as its "id" already
// is (see [TypeIdentity.IdentityAttrs]'s doc comment on why "id" is never
// handed out). google_colab_runtime_template and
// google_cloud_quotas_quota_preference are both this shape in the real
// provider: name is Optional+Computed, and it is also the value that tells
// two instances apart.
func TestIsContextAttrNameAndIDNeverContext(t *testing.T) {
	schemas := fakeProviderSchemas(map[string]fakeType{
		"google_colab_runtime_template": {
			args:     map[string]string{"location": "req", "name": "optcomp", "id": "optcomp"},
			identity: map[string]string{"location": "req", "name": "opt"},
		},
		"google_cloud_quotas_quota_preference": {
			args:     map[string]string{"parent": "req", "name": "optcomp", "id": "optcomp"},
			identity: map[string]string{"parent": "req", "name": "opt"},
		},
	})
	template := schemas["google_colab_runtime_template"]
	if isContextAttr(schemas, template, "name") {
		t.Error("name was treated as context even though it is corroborated and the literal exclusion should have refused it first")
	}
	if _, ok := SynthesizeTypeIdentity("google_colab_runtime_template", schemas, nil); ok {
		t.Error("google_colab_runtime_template was synthesized with its own distinguishing name dropped as context")
	}
}

// TestSchemaFallbackNonIdentityAttrRefused: a synthesized entry hands out one
// attribute and refuses every other, id included.
func TestSchemaFallbackNonIdentityAttrRefused(t *testing.T) {
	schemas := fallbackSchemas()
	// Same fixture, with the child reading the parent's id instead of its
	// identity attribute.
	cfg := loadConfig(t, filepath.Join("testdata", "schema-fallback-wrong-attr"), nil)
	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	if !diags.HasErrors() {
		t.Fatal("a reference to a synthesized type's id resolved, which asserts an inference no schema carries")
	}
	if got := diags.Err().Error(); !strings.Contains(got, "not an identity attribute") &&
		!strings.Contains(got, "Not an identity attribute") {
		t.Errorf("the refusal is not the identity-attribute one: %s", got)
	}
}
