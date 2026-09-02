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
)

// #252 shape A. #189 slice 1 taught [resolver.namedDef] to thread a module
// CALL's own each.key/each.value into the argument expression it fetches
// from the caller - but namedDef is only reached for references
// [resolver.resolveExpr] decomposes down to a bare traversal. A string
// template is one of those, so #252's stated diagnosis ("any function call
// or template") is wrong by half: mutating the fixture to
// name = "u-${var.name}" resolves with this fix reverted. A FUNCTION CALL
// is not decomposed. The whole expression goes to
// [configs.StaticEvaluator] instead, whose var.* answer comes from the
// module call's variables closure frozen at load time, against a parent
// evaluator that carries no repetition data at all. The result was
//
//	Unable to use each.value in static context, which is required by
//	module.user:var.name
//
// which is the wording and the subject shape of every site in #252's shape
// A bucket. [resolver.enterModuleAt] now rebuilds that closure per module
// instance through [configs.ModuleCall.VariablesUsing], against the parent
// evaluator carrying THIS call instance's own repetition data - the same
// seam internal/live/dataread already uses for the data-lookup case (#212).

// TestModuleCallForEachVarWrapped is the positive case and the
// wrong-marker check together. All three resources sit in the same child
// module under the same for_each'd call and read the same var.name;
// direct and templated already resolved, and only aws_iam_user.wrapped
// puts a function call in the way. var.name is built from BOTH each.key
// and each.value.role, so if one call instance's resolution ever saw
// another's repetition data the pairing would break ("u-alice-reader")
// rather than the case refusing - and direct/templated hold the reading
// the fix must not change.
func TestModuleCallForEachVarWrapped(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-foreach-var-wrapped"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`module.user["alice"].aws_iam_user.direct`:    `CONCRETE alice-admin`,
		`module.user["alice"].aws_iam_user.templated`: `CONCRETE t-alice-admin`,
		`module.user["alice"].aws_iam_user.wrapped`:   `CONCRETE u-alice-admin`,
		`module.user["bob"].aws_iam_user.direct`:      `CONCRETE bob-reader`,
		`module.user["bob"].aws_iam_user.templated`:   `CONCRETE t-bob-reader`,
		`module.user["bob"].aws_iam_user.wrapped`:     `CONCRETE u-bob-reader`,
	})
}

// TestModuleCallForEachVarWrappedCollides is the guard against the fix
// buying its resolution by weakening the duplicate check. The child module
// discards var.name entirely and names its resource a constant, so both
// call instances resolve to ONE live identity;
// [resolver.checkCollisions] must still say so. Before the fix this
// configuration also failed, but for the wrong reason - the wrapped
// expression refused, and a resource that never resolves can never
// collide - so a test that only asserted "not clean" would have passed
// both before and after.
func TestModuleCallForEachVarWrappedCollides(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-foreach-var-collide"), nil)

	_, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatal("two call instances resolving to one identity must be reported")
	}
	var found bool
	for _, d := range diags {
		if strings.Contains(d.Description().Summary, "same identity") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a duplicate-identity diagnostic, got:\n%s", diags.Err())
	}
}
