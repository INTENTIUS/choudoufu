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
)

// GitHub issue #336. corpus-lambda-simple's five remaining test_plan
// diagnostics all traced to one expression in the estate's own main.tf,
//
//	function_name = "${random_pet.this.id}-lambda-simple"
//
// and the issue read that as an identity resolver declining to read the
// record store. It is not: [resolver.parentPart]'s record-backed branch
// already reads it, and [resolver.namedLeaf] already carries the resulting
// formula across a module-call argument, which is why
// aws_lambda_function.this - whose function_name is a bare var.function_name
// - resolved before any of this landed.
//
// What refused was coalesce(). terraform-aws-modules/terraform-aws-lambda
// names its IAM role `coalesce(var.role_name, var.function_name, "*")`, its
// inline policy `coalesce(var.policy_name, local.role_name, "*")` and its
// log group `coalesce(var.logging_log_group, "/aws/lambda/...")`, and every
// argument of all three is a var or a local - so [resolver.isSymbolic] saw
// no managed resource in them, sent them to whole-expression evaluation,
// and had nothing structural left to try when that failed.
//
// These tests assert the rendered identity, never a boolean: a selection
// rule that picked the wrong argument would produce a perfectly
// well-formed marker naming the wrong live object.

// TestCoalesceSelectsThroughToTheRecordBackedParent is the whole of the fix
// from the outside. Five children, five different coalesce shapes, all of
// them resolving to a formula over the one record-backed parent two module
// boundaries away.
func TestCoalesceSelectsThroughToTheRecordBackedParent(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "coalesce-selection"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: recordBackedTestSchemas()})
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		// The plain case: a null argument skipped, then the module input
		// carrying the record-backed formula.
		"module.child.aws_iam_group.plain": "PARENT_DERIVED ${random_pet.suffix.id}-svc-plain",
		// The empty string is skipped exactly as null is.
		"module.child.aws_iam_group.blank_skipped": "PARENT_DERIVED ${random_pet.suffix.id}-svc-blank",
		// Through a conditional and two locals - the estate's own chain.
		"module.child.aws_iam_group.through_local": "PARENT_DERIVED ${random_pet.suffix.id}-svc",
		// A selected argument that is a template with its own prefix.
		"module.child.aws_iam_group.prefixed": "PARENT_DERIVED /aws/lambda/${random_pet.suffix.id}-svc",
		// A literal earlier in the chain wins and the parent is never read,
		// so this one is CONCRETE rather than a formula.
		"module.child.aws_iam_group.literal_wins": "CONCRETE literal-name",
	})
}

// TestCoalesceRefusesWhatItCannotDecide is the boundary, and it is the half
// that matters. Every case here USED to refuse for a different reason and
// must still refuse: a rule that fell through to the next argument whenever
// it could not decide the current one would build a marker out of the wrong
// expression, silently, on configurations that plan perfectly well under
// stock OpenTofu.
func TestCoalesceRefusesWhatItCannotDecide(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "coalesce-undecidable"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: recordBackedTestSchemas()})
	if !diags.HasErrors() {
		t.Fatal("every child in this fixture must refuse; none did")
	}

	for _, addr := range []string{
		// The selected argument is a bare parent reference with no literal
		// anywhere in it, so nothing here proves it non-empty - and an
		// empty one is exactly what coalesce() would have skipped.
		"module.child.aws_iam_group.bare_parent_ref",
		// An argument BEFORE the record-backed one that cannot be proved
		// empty. Selecting the later one would be a fall-through past a
		// value that may well be set.
		"module.child.aws_iam_group.undecidable_first",
		// A sensitive argument's contents are not readable here, so
		// whether coalesce() skips it is not decidable either.
		"module.child.aws_iam_group.sensitive_first",
	} {
		if res, ok := result.Get(mustAddr(t, addr)); ok {
			rendered := res.ImportID
			if res.Formula != nil {
				rendered = res.Formula.String()
			}
			t.Errorf("%s resolved to %s %q; it must refuse", addr, res.Class, rendered)
		}
	}
}

// TestCoalesceValueArmVerdicts pins the value half of the rule directly,
// because the fixtures above can only reach the shapes HCL lets a
// configuration write. Null and the empty string skip; a numeric zero does
// NOT, because coalesce()'s own empty-string rule is guarded on the call's
// return type being a string (internal/lang/funcs.CoalesceFunc); unknown
// and marked decide nothing at all.
func TestCoalesceValueArmVerdicts(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  cty.Value
		want coalesceArm
	}{
		{"null string", cty.NullVal(cty.String), coalesceSkipped},
		{"empty string", cty.StringVal(""), coalesceSkipped},
		{"non-empty string", cty.StringVal("x"), coalesceSelected},
		{"zero number", cty.NumberIntVal(0), coalesceSelected},
		{"unknown string", cty.UnknownVal(cty.String), coalesceUndecidable},
		{"marked string", cty.StringVal("x").Mark("sensitive"), coalesceUndecidable},
		{"nil value", cty.NilVal, coalesceUndecidable},
	} {
		if got := coalesceValueArm(tc.val); got != tc.want {
			t.Errorf("%s: got verdict %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestPartsProvablyNonEmpty pins the other half: what counts as a proof
// that a symbolic argument's rendering cannot be the empty string.
func TestPartsProvablyNonEmpty(t *testing.T) {
	parent := &ParentRef{Attr: "id"}
	for _, tc := range []struct {
		name  string
		parts []Part
		want  bool
	}{
		{"nothing at all", nil, false},
		{"one parent reference", []Part{{Parent: parent}}, false},
		{"two parent references", []Part{{Parent: parent}, {Parent: parent}}, false},
		{"an empty literal beside a parent", []Part{{Literal: ""}, {Parent: parent}}, false},
		{"a literal suffix", []Part{{Parent: parent}, {Literal: "-svc"}}, true},
		{"a literal prefix", []Part{{Literal: "/aws/lambda/"}, {Parent: parent}}, true},
		{"a bare literal", []Part{{Literal: "x"}}, true},
	} {
		if got := partsProvablyNonEmpty(tc.parts); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
