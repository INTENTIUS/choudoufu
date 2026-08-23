// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"reflect"
	"testing"
)

// TestSchemaPrecedenceMatchesRowByValue is ruling 2's own acceptance bar
// (rfc/20260823-foundation-order-ruling.md, #387), asserted the way
// live-markers.md's own measurement discipline requires: never a predicate,
// always the rendered value. It resolves the same three fixtures twice -
// once with no schemas, so [resolver.lookupType] uses [DefaultTable]'s row
// exactly as it always has, and once with a fake provider schema built
// straight from each type's own real identity ([tests below quote the row
// each schema has to reproduce]) so [resolver.lookupType] instead takes the
// branch [schemaReproducesRow] gates - and requires the two resolutions to
// agree by value.
//
// aws_iam_role and aws_s3_bucket are single-attribute reproduced types:
// synthesizeTypeIdentity never sets IdentityObjectOnly for one attribute,
// so both routes classify the same way and ImportID must match exactly.
// aws_iam_role_policy_attachment is a reproduced two-attribute composite
// the row joins with "/" into one ImportID string; a synthesized entry for
// two attributes is always IdentityObjectOnly (identity.SynthesizeTypeIdentity's
// own doc comment - there is no schema-carried separator to join them
// with), so the SHAPE changes on purpose - the schema-derived ImportID is
// empty - while the VALUES it carries, IdentityValues, must still be the
// same {"role": ..., "policy_arn": ...} map the row's own resolution
// produces. That split is exactly [Resolution.IdentityValues]' own doc
// comment's worked example.
func TestSchemaPrecedenceMatchesRowByValue(t *testing.T) {
	cfg := loadConfig(t, "testdata/schema-precedence", nil)

	withoutSchemas, diags := ResolveWith(context.Background(), cfg, Context{})
	if diags.HasErrors() {
		t.Fatalf("resolving with no schemas: %s", diags.Err())
	}

	// Each fake schema's Block and IdentitySchema are read straight off
	// DefaultTable's own row for the type (dumped at the commit this test
	// was added): aws_iam_role and aws_s3_bucket each read one Required
	// argument (name, bucket); aws_iam_role_policy_attachment reads two
	// (role, policy_arn), joined by "/" in the row but not in any schema.
	// Real provider schemas mark these Optional+Computed rather than
	// Required, but derivableOne's AdmitSchema path only needs the
	// argument to be a real, present configuration attribute the schema
	// requires for import - Required is the simplest fixture-schema shape
	// that satisfies it, the same choice fallbackSchemas' own aws_thing
	// makes.
	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_iam_role": {
			args:     map[string]string{"name": "req"},
			identity: map[string]string{"name": "req"},
		},
		"aws_s3_bucket": {
			args:     map[string]string{"bucket": "req"},
			identity: map[string]string{"bucket": "req"},
		},
		"aws_iam_role_policy_attachment": {
			args:     map[string]string{"role": "req", "policy_arn": "req"},
			identity: map[string]string{"role": "req", "policy_arn": "req"},
		},
	})

	withSchemas, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	if diags.HasErrors() {
		t.Fatalf("resolving with schemas: %s", diags.Err())
	}

	tests := []struct {
		addr               string
		identityObjectOnly bool
	}{
		{addr: "aws_iam_role.example"},
		{addr: "aws_s3_bucket.example"},
		{addr: "aws_iam_role_policy_attachment.example", identityObjectOnly: true},
	}

	for _, tt := range tests {
		row, ok := withoutSchemas.Get(mustAddr(t, tt.addr))
		if !ok {
			t.Errorf("%s: did not resolve with no schemas at all", tt.addr)
			continue
		}
		schema, ok := withSchemas.Get(mustAddr(t, tt.addr))
		if !ok {
			t.Errorf("%s: did not resolve with schemas present", tt.addr)
			continue
		}

		if row.Class != ClassConcrete || schema.Class != ClassConcrete {
			t.Errorf("%s: class = %s (row) / %s (schema), want CONCRETE both ways", tt.addr, row.Class, schema.Class)
			continue
		}

		if !reflect.DeepEqual(row.IdentityValues, schema.IdentityValues) {
			t.Errorf("%s: IdentityValues differ by value:\n  row:    %#v\n  schema: %#v", tt.addr, row.IdentityValues, schema.IdentityValues)
		}

		switch {
		case tt.identityObjectOnly:
			if row.ImportID == "" {
				t.Errorf("%s: the row's own ImportID is empty; the fixture no longer exercises the joined-string shape this test needs", tt.addr)
			}
			if schema.ImportID != "" {
				t.Errorf("%s: schema-derived ImportID = %q, want empty - a synthesized two-attribute entry is always IdentityObjectOnly", tt.addr, schema.ImportID)
			}
		default:
			if row.ImportID != schema.ImportID {
				t.Errorf("%s: ImportID differs: row %q, schema %q", tt.addr, row.ImportID, schema.ImportID)
			}
			if row.ImportID == "" {
				t.Errorf("%s: both ImportIDs are empty; the fixture is not exercising a resolved identity at all", tt.addr)
			}
		}
	}
}

// TestSchemaPrecedenceKeepsRowWhenSchemaDisagrees is ruling 2's other half:
// the ledger keeps what the schema cannot say, so a candidate
// [schemaReproducesRow] refuses must keep resolving through the row even
// with real schemas present. aws_route is the canonical case
// (tools/row-gen/schemafirst.go classifies it "any-of": route_table_id is
// the only required identity attribute, but the ratified row also reads one
// of three alternative destination_* arguments no single required schema
// attribute names). The fake schema here deliberately only requires
// route_table_id - the shape a naive schema read would produce - and the
// resolution must still carry the destination, which only the row's own
// Components know to read.
func TestSchemaPrecedenceKeepsRowWhenSchemaDisagrees(t *testing.T) {
	cfg := loadConfig(t, "testdata/schema-precedence-disagree", nil)

	schemas := fakeProviderSchemas(map[string]fakeType{
		"aws_route": {
			args:     map[string]string{"route_table_id": "req", "destination_cidr_block": "req"},
			identity: map[string]string{"route_table_id": "req"},
		},
	})

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	if diags.HasErrors() {
		t.Fatalf("resolving with a disagreeing schema: %s", diags.Err())
	}

	res, ok := result.Get(mustAddr(t, "aws_route.example"))
	if !ok {
		t.Fatal("aws_route.example did not resolve at all")
	}
	want := "rtb-0123456789abcdef0_10.0.0.0/16"
	if res.ImportID != want {
		t.Errorf("ImportID = %q, want %q (the row's own composite - a route_table_id-only schema would have produced \"rtb-0123456789abcdef0\" alone, silently losing the destination)", res.ImportID, want)
	}
}
