// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #73's record-backed class had no way out of itself: a
// resource whose identity read one of its attributes was refused "Not an
// identity attribute", because [Resolution.attrParts] answers only for
// ClassConcrete and ClassParentDerived and a RecordBacked row carries no
// [TypeIdentity.IdentityAttrs] for hasIdentityAttr to match either. Neither
// of those is a claim that the value is unknowable - the record store holds
// the parent's whole object, and internal/live/projection materializes
// every record-backed resolution before it renders any formula. See
// [resolver.parentPart]'s record-backed branch and
// [resolver.stringAttrInSchema].
//
// The rule is keyed on the class and settled by the provider's schema, so
// it covers all ten of DefaultTable's RecordBacked rows and any row row-gen
// marks later. TestRecordBackedRuleCoversEveryRecordBackedRow below is the
// part that keeps that claim honest rather than testing three types by hand.

// recordBackedTestSchemas is a caricature of the real hashicorp/null,
// hashicorp/random and builtin terraform schemas. The child type in the
// fixtures, aws_cloudwatch_log_group, deliberately gets no entry: it is a
// ratified DefaultTable row and resolves from the table alone, so these
// schemas describe only the parents whose attributes are being read.
//
// terraform_data's "output" is cty.DynamicPseudoType on purpose: that is
// what the real builtin provider declares, and it is the one attribute
// shape where the guard admits a read whose value could still turn out not
// to be a string. Doing so is deliberate - see the test named for it.
func recordBackedTestSchemas() map[string]providers.Schema {
	s := fakeProviderSchemas(map[string]fakeType{
		"random_pet": {
			args: map[string]string{"id": "comp", "length": "opt", "prefix": "opt"},
		},
		"null_resource": {
			args: map[string]string{"id": "comp"},
		},
		"terraform_data": {},
	})

	// fakeProviderSchemas types every argument cty.String; these two are
	// the real shapes, and the point of the test.
	s["null_resource"].Block.Attributes["triggers"] = &configschema.Attribute{
		Type: cty.Map(cty.String), Optional: true,
	}
	s["terraform_data"].Block.Attributes["input"] = &configschema.Attribute{
		Type: cty.DynamicPseudoType, Optional: true,
	}
	s["terraform_data"].Block.Attributes["output"] = &configschema.Attribute{
		Type: cty.DynamicPseudoType, Computed: true,
	}
	s["terraform_data"].Block.Attributes["id"] = &configschema.Attribute{
		Type: cty.String, Computed: true,
	}
	return s
}

// TestRecordBackedParentAttributeResolves is the whole of the fix from the
// outside: four children reading three different record-backed parents all
// come back parent-derived, with a ParentRef to the attribute they named
// rather than a refusal.
func TestRecordBackedParentAttributeResolves(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "record-backed-parent-attr"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: recordBackedTestSchemas()})
	assertNoErrors(t, diags)

	cases := []struct {
		child      string
		wantParent string
		wantAttr   string
		wantPrefix string
	}{
		{`aws_cloudwatch_log_group.from_pet`, `random_pet.suffix`, "id", "svc-"},
		{`aws_cloudwatch_log_group.from_data`, `terraform_data.seed`, "output", "seeded-"},
		{`aws_cloudwatch_log_group.from_null`, `null_resource.gate`, "id", "gated-"},
	}
	for _, tc := range cases {
		res := resolutionAt(t, result, tc.child)
		if res.Class != ClassParentDerived {
			t.Errorf("%s resolved %s (%s), want PARENT_DERIVED", tc.child, res.Class, res.Reason)
			continue
		}
		parts := res.Formula.Parts
		if len(parts) != 2 {
			t.Errorf("%s has %d formula parts (%v), want a literal and a parent reference", tc.child, len(parts), res.Formula)
			continue
		}
		if parts[0].Literal != tc.wantPrefix {
			t.Errorf("%s's first part is %q, want %q", tc.child, parts[0].Literal, tc.wantPrefix)
		}
		if parts[1].Parent == nil {
			t.Errorf("%s's second part is not a parent reference: %v", tc.child, parts[1])
			continue
		}
		if got := parts[1].Parent.Instance.String(); got != tc.wantParent {
			t.Errorf("%s refers to parent %s, want %s", tc.child, got, tc.wantParent)
		}
		if got := parts[1].Parent.Attr; got != tc.wantAttr {
			t.Errorf("%s reads attribute %q, want %q", tc.child, got, tc.wantAttr)
		}
	}

	// Two record-backed parents in one identity: the branch has to compose
	// with itself, not merely supply a whole identity on its own.
	both := resolutionAt(t, result, `aws_cloudwatch_log_group.from_both`)
	if both.Class != ClassParentDerived {
		t.Fatalf("aws_cloudwatch_log_group.from_both resolved %s (%s), want PARENT_DERIVED", both.Class, both.Reason)
	}
	if n := len(both.Formula.Parents); n != 2 {
		t.Errorf("aws_cloudwatch_log_group.from_both names %d parents (%v), want 2", n, both.Formula.Parents)
	}
}

// TestRecordBackedParentUnknownAttributeStillRefused is the boundary. The
// branch is a schema rule, not a licence to read any name off a
// record-backed parent: an attribute the provider does not declare is
// nothing the record store could ever hold, so the refusal stands and says
// why in this class's own terms.
func TestRecordBackedParentUnknownAttributeStillRefused(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "record-backed-parent-attr-unknown"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: recordBackedTestSchemas()})
	if !diags.HasErrors() {
		t.Fatal("reading an attribute random_pet's schema does not declare was accepted")
	}
	if !hasDiag(diags, "Not an identity attribute", "its schema declares no string-valued \"no_such_attribute\"") {
		t.Errorf("wrong diagnostics:\n%s", renderDiags(diags))
	}
}

// TestRecordBackedParentAttributeNeedsSchemas: with no schemas supplied,
// nothing here can tell a real attribute from a typo, so the refusal is
// unchanged from before the fix. This is what keeps the branch a rule
// rather than a name list - it has exactly one source of truth, and
// without it there is no rule to apply.
func TestRecordBackedParentAttributeNeedsSchemas(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "record-backed-parent-attr"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{})
	if !diags.HasErrors() {
		t.Fatal("a record-backed parent's attribute was read with no provider schemas to confirm it exists")
	}
	if !hasDiag(diags, "Not an identity attribute", "no provider schemas were available to this run") {
		t.Errorf("wrong diagnostics:\n%s", renderDiags(diags))
	}
}

// TestRecordBackedRuleCoversEveryRecordBackedRow is the claim that this is
// one rule rather than three worked examples. It walks every row in
// [DefaultTable] that carries RecordBacked and asserts the guard admits a
// plain string attribute of it, with no per-type branch anywhere in the
// derivation. A row row-gen marks RecordBacked in future is covered the
// moment it appears here.
func TestRecordBackedRuleCoversEveryRecordBackedRow(t *testing.T) {
	var rows []string
	for name, entry := range DefaultTable {
		if entry.RecordBacked {
			rows = append(rows, name)
		}
	}
	if len(rows) == 0 {
		t.Fatal("no RecordBacked rows in DefaultTable; this test can see nothing")
	}

	schemas := make(map[string]providers.Schema, len(rows))
	for _, name := range rows {
		schemas[name] = providers.Schema{Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true},
			},
		}}
	}
	r := &resolver{schemas: schemas}

	for _, name := range rows {
		if !r.stringAttrInSchema(name, "id") {
			t.Errorf("%s is RecordBacked but the guard refuses to read its id", name)
		}
		if r.stringAttrInSchema(name, "nope") {
			t.Errorf("%s admitted an attribute its schema does not declare", name)
		}
	}
	t.Logf("the rule covers %d RecordBacked rows: %v", len(rows), rows)
}

// TestStringAttrInSchemaConversions pins which attribute types the guard
// admits, because that is the whole of what it decides.
//
// DynamicPseudoType is admitted deliberately, and it is the one entry here
// that can be wrong at render time: terraform_data's "output" is dynamic,
// it is the most useful attribute the class has, and a value that turns out
// not to be a string fails loudly downstream rather than silently -
// internal/live/projection's builder.renderFormula raises "Cannot read a
// parent's identity from the projection" and omits the child. Refusing
// every dynamic attribute here would trade that named failure for refusing
// terraform_data outright.
func TestStringAttrInSchemaConversions(t *testing.T) {
	cases := []struct {
		name string
		ty   cty.Type
		want bool
	}{
		{"string", cty.String, true},
		{"number", cty.Number, true},
		{"bool", cty.Bool, true},
		{"dynamic", cty.DynamicPseudoType, true},
		{"list of string", cty.List(cty.String), false},
		{"map of string", cty.Map(cty.String), false},
		{"object", cty.Object(map[string]cty.Type{"a": cty.String}), false},
	}
	for _, tc := range cases {
		r := &resolver{schemas: map[string]providers.Schema{
			"null_resource": {Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{"attr": {Type: tc.ty, Computed: true}},
			}},
		}}
		if got := r.stringAttrInSchema("null_resource", "attr"); got != tc.want {
			t.Errorf("a %s attribute: guard returned %v, want %v", tc.name, got, tc.want)
		}
	}

	// A nested block is not an attribute, whatever it is called.
	r := &resolver{schemas: map[string]providers.Schema{
		"null_resource": {Block: &configschema.Block{
			BlockTypes: map[string]*configschema.NestedBlock{
				"attr": {Nesting: configschema.NestingSingle},
			},
		}},
	}}
	if r.stringAttrInSchema("null_resource", "attr") {
		t.Error("a nested block was admitted as a readable attribute")
	}
}
