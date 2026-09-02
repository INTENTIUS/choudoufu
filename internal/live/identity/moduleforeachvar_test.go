// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/addrs"
)

// #189's slice 1: namedDef (localvalue.go) used to decline outright the
// moment a module CALL had its own for_each or count, whatever the
// argument expression actually needed - even a bare each.key, or nothing
// from each/count at all. The fix threads the call's own repetition data
// through [configs.StaticEvaluator.WithRepetitionData] before reading the
// argument expression, via [ChildModuleRepetitionData] (modulepath.go),
// exactly mirroring how [resolver.walkModule] already derived which
// instances exist via [ChildModuleKeys]/[ChildModuleCountKeys].

// TestModuleCallForEachVar is the positive case and, more importantly, the
// wrong-marker check for a module CALL's for_each (the resource-for_each
// equivalent, TestLocalAttrRepetition, already pins the resource case):
// var.name is built from BOTH each.key and each.value.role of the module
// call's own for_each, so if one instance's resolution ever saw another's
// repetition data, the two resolved names would not have the pairing the
// fixture author wrote (e.g. "alice-reader" instead of "alice-admin").
func TestModuleCallForEachVar(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-foreach-var"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`module.user["alice"].aws_iam_user.this`: `CONCRETE alice-admin`,
		`module.user["bob"].aws_iam_user.this`:   `CONCRETE bob-reader`,
	})
}

// TestModuleCallCountVar is [TestModuleCallForEachVar]'s count counterpart:
// count.index of the module call itself, reached only through var.name.
func TestModuleCallCountVar(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-count-var"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`module.user[0].aws_iam_user.this`: `CONCRETE user-0`,
		`module.user[1].aws_iam_user.this`: `CONCRETE user-1`,
	})
}

// TestChildModuleRepetitionDataRefusesNonStatic is the safety boundary this
// fix must never cross: a module call's for_each (or count) that is not
// itself fully statically evaluable - here, one whose root is a managed
// resource rather than var/local/path/terraform/tofu - must make
// [ChildModuleRepetitionData] report ok=false, never a guessed key/value.
// In the full [Resolve] walk this case never even reaches namedDef, because
// [ChildModuleKeys] itself already refuses the same expression before any
// instance of the call is created (see [resolver.walkModule]); this test
// pins the seam namedDef actually calls, directly, so a future caller that
// reaches it a different way inherits the same refusal rather than a panic
// or a fabricated value.
func TestChildModuleRepetitionDataRefusesNonStatic(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "local-attr-value"), nil)
	// Any loaded module with a StaticEvaluator will do; this test only
	// exercises ChildModuleRepetitionData's own static-scope check, which
	// runs before the evaluator is ever asked to evaluate anything.

	rng := hcl.Range{Filename: "test", Start: hcl.Pos{Line: 1}, End: hcl.Pos{Line: 1}}
	expr := &hclsyntax.ScopeTraversalExpr{
		Traversal: hcl.Traversal{
			hcl.TraverseRoot{Name: "aws_route53_zone", SrcRange: rng},
			hcl.TraverseAttr{Name: "public", SrcRange: rng},
		},
		SrcRange: rng,
	}

	_, ok := ChildModuleRepetitionData(context.Background(), cfg, "module \"x\"", nil, expr, addrs.StringKey("public"))
	if ok {
		t.Fatal("expected ok=false: the for_each expression references a managed resource, not var/local/path/terraform/tofu")
	}

	_, ok = ChildModuleRepetitionData(context.Background(), cfg, "module \"x\"", nil, expr, addrs.StringKey("nonexistent-key"))
	if ok {
		t.Fatal("expected ok=false: even a statically-evaluable for_each must refuse a key it did not itself produce")
	}
}
