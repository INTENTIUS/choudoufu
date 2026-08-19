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

// TestModuleForEachComprehensionVarChase is the end-to-end pin for issue
// #308: a child module's for_each ranges over a for-comprehension whose
// SOURCE collection is a bare var.X reference, and that variable's actual
// object literal - with provably static keys - lives one module-call
// boundary up, at the CALLING module's own argument expression
// (testdata/module-foreach-comprehension-chase/main.tf's module "wrapper"
// call). The comprehension's filter reads one attribute of each entry's
// value (v.create) while a different, unrelated attribute (image) reaches
// a data source and must never be evaluated.
//
// "fluent-bit" never sets "create" in its own literal at all, so its value
// has to come from the variable's declared `optional(bool, true)` default
// (wrapper/main.tf) - #251's whole-value declared-type conversion answers
// the same question for a value that proves whole; this fixture is the one
// case that needs its per-attribute counterpart, because fluent-bit's
// entry does NOT prove whole (image blocks it). "app" sets create = true
// explicitly. "disabled" sets create = false and must produce no instance
// at all - proving the filter really excludes, not merely tolerates.
func TestModuleForEachComprehensionVarChase(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-foreach-comprehension-chase"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`module.wrapper.module.task["app"].aws_iam_user.this`:        `CONCRETE app`,
		`module.wrapper.module.task["fluent-bit"].aws_iam_user.this`: `CONCRETE fluent-bit`,
	})

	for _, r := range result.All() {
		if r.Addr.String() == `module.wrapper.module.task["disabled"].aws_iam_user.this` {
			t.Fatalf("the disabled entry (create = false) produced an instance: %s %q", r.Class, r.ImportID)
		}
	}
}
