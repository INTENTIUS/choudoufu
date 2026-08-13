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
//     read as a parent.
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
			identity: map[string]string{"parent": "req"},
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
		t.Fatal("aws_customer_gateway resolved against schemas that do not describe it")
	}
	if !strings.Contains(errText, "serves no aws_customer_gateway") {
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

// TestSchemaFallbackRefusesComposite: no separator guessing. A two-attribute
// identity stays a table decision.
func TestSchemaFallbackRefusesComposite(t *testing.T) {
	_, errText := resolveFallback(t, "schema-fallback-composite", fallbackSchemas())
	if errText == "" {
		t.Fatal("a composite identity was synthesized, which means a separator was guessed")
	}
	if !strings.Contains(errText, "composite") {
		t.Errorf("the refusal does not say why a composite is different: %s", errText)
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

	if _, ok := SynthesizeTypeIdentity("aws_composite_thing", fallbackSchemas(), signal); ok {
		t.Error("a composite identity was synthesized")
	}
	if _, ok := SynthesizeTypeIdentity("aws_thing", nil, signal); ok {
		t.Error("something was synthesized with no schemas at all")
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
