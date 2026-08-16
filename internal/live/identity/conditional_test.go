// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"
)

// GitHub issue #196: 73 of the 118 corpus sites blocked by "Identity not
// resolvable from configuration" are `cond ? A : B` wrapping a value that
// already resolves today via [resolver.parentPart] once the wrapping is
// gone - resolveExpr only ever decomposed TemplateExpr, TemplateWrapExpr,
// ParenthesesExpr and a plain traversal, so a ConditionalExpr fell straight
// through to the generic "cannot be passed through functions or operators"
// refusal before the attribute was ever checked.
//
// [resolver.resolveConditional] fixes that by evaluating the condition
// through the same evalStatic/isSymbolic machinery every other identity
// argument uses, then recursing resolveExpr into whichever branch the
// condition selects - so the unselected branch is never consulted, and
// every existing boundary (parentPart's registered-IdentityAttrs check,
// siblingLiteralExpr's Computed-flag boundary) applies to the selected
// branch exactly as it would to a bare reference.

// TestConditionalResolvesSelectedBranch is the corpus shape itself:
// aws_route_table_association.route_table_id chooses between two
// aws_route_table.id references with a boolean variable. Both associations
// name an UNDECLARED route table on the branch they do NOT select, so a
// resolved formula naming only the selected table also proves the other
// branch's resource was never looked at - an undeclared-resource reference,
// if evaluated, does not fail silently.
func TestConditionalResolvesSelectedBranch(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "conditional-basic"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	trueBranch := resolutionAt(t, result, `aws_route_table_association.true_branch`)
	if trueBranch.Class != ClassParentDerived {
		t.Fatalf("true_branch resolved %s, want PARENT_DERIVED (both route tables are server-assigned)", trueBranch.Class)
	}
	if want := "${aws_subnet.this.id}/${aws_route_table.primary.id}"; trueBranch.Formula.String() != want {
		t.Errorf("true_branch formula is %q, want %q", trueBranch.Formula.String(), want)
	}

	falseBranch := resolutionAt(t, result, `aws_route_table_association.false_branch`)
	if falseBranch.Class != ClassParentDerived {
		t.Fatalf("false_branch resolved %s, want PARENT_DERIVED", falseBranch.Class)
	}
	if want := "${aws_subnet.this.id}/${aws_route_table.secondary.id}"; falseBranch.Formula.String() != want {
		t.Errorf("false_branch formula is %q, want %q", falseBranch.Formula.String(), want)
	}

	for _, d := range diags {
		if d.Description().Summary == "Reference to undeclared resource" {
			t.Fatalf("an undeclared-resource diagnostic reached the result - the unselected branch was consulted: %s", d.Description().Detail)
		}
	}
}

// TestConditionalConditionReferencingResourceRefuses is danger case 1: the
// CONDITION itself reads a managed resource's attribute, so which branch
// applies is not known until apply. Must still refuse - not resolve to
// whichever branch is written first.
func TestConditionalConditionReferencingResourceRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "conditional-condition-symbolic"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatalf("expected a refusal; the condition reads aws_subnet.flag.id")
	}
	if _, ok := result.Get(mustAddr(t, `aws_route_table_association.assoc`)); ok {
		t.Fatalf("assoc resolved; its route_table_id condition depends on a managed resource's apply-time value")
	}
	if !hasDiag(diags, "Identity not resolvable from configuration", "conditional expression") {
		t.Errorf("no \"Identity not resolvable from configuration\" diagnostic naming the conditional expression:\n%s", renderDiags(diags))
	}
}

// TestConditionalSelectedBranchComputedRefuses is danger case 2: the
// condition itself is static (so the conditional resolves as far as
// picking a branch), but the branch it selects reads a sibling's Computed
// attribute. Must refuse at the exact boundary siblingLiteralExpr already
// draws for a bare reference (#220) - not treat "reached through a
// conditional" as a reason to skip the schema check.
func TestConditionalSelectedBranchComputedRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "conditional-computed-boundary"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: siblingTestSchemas()})

	literal := resolutionAt(t, result, `aws_route53_record.reads_literal`)
	if literal.Class != ClassConcrete {
		t.Fatalf("reads_literal resolved %s, want CONCRETE; every component is a static literal", literal.Class)
	}
	if want := "Z1_hello_TXT"; literal.ImportID != want {
		t.Errorf("reads_literal resolved to %q, want %q", literal.ImportID, want)
	}

	if _, ok := result.Get(mustAddr(t, `aws_route53_record.reads_computed`)); ok {
		t.Fatalf("reads_computed resolved; computed_val is Computed and must stay refused even reached through a conditional")
	}
	if !hasDiag(diags, "Not an identity attribute", "computed_val") {
		t.Errorf("no \"Not an identity attribute\" diagnostic naming computed_val:\n%s", renderDiags(diags))
	}
}

// TestConditionalUnsetVariableConditionRefuses is danger case 4: the
// condition depends on a required root variable with no value. Must refuse
// rather than default to picking a branch - the same "error at use time"
// every other identity argument in this package already gives an unset
// required variable.
func TestConditionalUnsetVariableConditionRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "conditional-unset-var"), nil)

	result, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatalf("expected a refusal; var.unset_flag has no default and no supplied value")
	}
	if _, ok := result.Get(mustAddr(t, `aws_route_table_association.assoc`)); ok {
		t.Fatalf("assoc resolved; its route_table_id condition depends on an unset required variable")
	}
}
