// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/providers"
)

// corpus-dynamodb-table-basic's own greenfield wall: a child reads a
// NON-identity attribute of a PARENT-DERIVED parent - one whose own
// identity is itself a formula, not yet ClassConcrete, ClassNeedsDiscovery
// or ClassRecordBacked, the three classes resolver.parentPart's deferrable
// check already covered. aws_dynamodb_table.this's `name` reads
// random_pet.suffix.id (a record-backed grandparent), which classifies the
// table ClassParentDerived; aws_dynamodb_resource_policy.this's own
// identity (`resource_arn`) then reads the table's `arn`, which is a real,
// Computed attribute of aws_dynamodb_table but not one of its
// IdentityAttrs (only "id" and "name" are - see table_generated.go).
//
// This used to end in "Not an identity attribute" outright, even though
// internal/live/projection's orderWork topologically sorts every
// ClassParentDerived resolution by its own Formula.Parents before
// builder.run's derived loop renders any of them - so a parent-derived
// table is always materialized (imported, then ReadResource) strictly
// before a child whose formula names it, the exact "whole object already
// in b.live" guarantee the concrete and needs-discovery cases already rest
// on, one phase later. See resolver.parentPart's own comment on this
// branch for the full argument.
func parentDerivedTestSchemas() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"random_pet": {
			args: map[string]string{"id": "comp", "length": "opt"},
		},
		"aws_dynamodb_table": {
			args: map[string]string{"name": "req", "arn": "comp"},
		},
		"aws_dynamodb_resource_policy": {
			args: map[string]string{"resource_arn": "req"},
		},
	})
}

// TestParentDerivedParentAttributeResolves is the fix from the outside: the
// resource policy resolves ClassParentDerived, with a ParentRef to the
// table's `arn` rather than a refusal, and the table it names is itself
// still ClassParentDerived (not concrete) at the moment this resolves -
// proving the deferral covers a parent that has not become concrete yet
// either.
func TestParentDerivedParentAttributeResolves(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "parent-derived-parent-attr"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: parentDerivedTestSchemas()})
	assertNoErrors(t, diags)

	table := resolutionAt(t, result, `aws_dynamodb_table.this`)
	if table.Class != ClassParentDerived {
		t.Fatalf("aws_dynamodb_table.this resolved %s (%s), want PARENT_DERIVED - this test is only meaningful if the parent itself is not concrete", table.Class, table.Reason)
	}

	policy := resolutionAt(t, result, `aws_dynamodb_resource_policy.this`)
	if policy.Class != ClassParentDerived {
		t.Fatalf("aws_dynamodb_resource_policy.this resolved %s (%s), want PARENT_DERIVED", policy.Class, policy.Reason)
	}
	parts := policy.Formula.Parts
	if len(parts) != 1 {
		t.Fatalf("aws_dynamodb_resource_policy.this has %d formula parts (%v), want exactly one parent reference", len(parts), policy.Formula)
	}
	if parts[0].Parent == nil {
		t.Fatalf("aws_dynamodb_resource_policy.this's formula part is not a parent reference: %v", parts[0])
	}
	if got, want := parts[0].Parent.Instance.String(), `aws_dynamodb_table.this`; got != want {
		t.Errorf("aws_dynamodb_resource_policy.this refers to parent %s, want %s", got, want)
	}
	if got, want := parts[0].Parent.Attr, "arn"; got != want {
		t.Errorf("aws_dynamodb_resource_policy.this reads attribute %q, want %q", got, want)
	}
}

// TestParentDerivedParentUnknownAttributeStillRefused is the boundary: the
// branch is a schema rule, not a licence to read any name off a
// parent-derived parent. An attribute the (fake) provider does not declare
// is nothing a real ReadResource could ever return either, so the refusal
// stands.
func TestParentDerivedParentUnknownAttributeStillRefused(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "parent-derived-parent-attr-unknown"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: parentDerivedTestSchemas()})
	if !diags.HasErrors() {
		t.Fatal("reading an attribute aws_dynamodb_table's schema does not declare was accepted")
	}
	if !hasDiag(diags, "Not an identity attribute", "aws_dynamodb_table's schema declares no string-valued \"no_such_attribute\"") {
		t.Errorf("wrong diagnostics:\n%s", renderDiags(diags))
	}
}

// TestParentDerivedParentAttributeNeedsSchemas: with no schemas supplied,
// nothing here can tell a real attribute from a typo, so the refusal is
// unchanged from before the fix - the same "needs schemas" boundary the
// concrete and record-backed branches already have.
func TestParentDerivedParentAttributeNeedsSchemas(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "parent-derived-parent-attr"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{})
	if !diags.HasErrors() {
		t.Fatal("a parent-derived parent's attribute was read with no provider schemas to confirm it exists")
	}
	if !hasDiag(diags, "Not an identity attribute", "no provider schemas were available to this run") {
		t.Errorf("wrong diagnostics:\n%s", renderDiags(diags))
	}
}
