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

// TestModuleForEachComprehensionEachValueAttr is the end-to-end pin for
// issue #315: builds on the fixture above (#308) by adding two SECOND
// arguments to the "task" module call - `label = each.value.label` and
// `owner = each.value.owner` - read from inside the SAME for-comprehension
// #308 already proves the key set of. Before #315's fix these refused
// wholesale - "Unable to use each.value.label in static context, which is
// required by module.task:var.label" - for every entry, even though
// "label" and "owner" are plain literals or declared defaults sitting
// beside the one genuinely unprovable attribute (fluent-bit's "image",
// which reaches a data source).
//
// "label" and "owner" exercise the two different sources #315's own
// resolveEntryAttrOrNull needs, beyond resolveEntryAttr's existing ones:
//
//   - "fluent-bit" never sets "label", so its value has to come from the
//     variable's declared `optional(string, "default-team")` default - an
//     EXPLICIT typeexpr default, [resolveEntryAttr]'s own declared-default
//     path already needed for #308's "create".
//   - NEITHER entry ever sets "owner", declared as a bare
//     `optional(string)` with NO explicit default at all. get_type.go's
//     own optional(T) handling never writes an entry into
//     typeexpr.Defaults.DefaultValues for this shape - only for
//     `optional(T, someDefault)` - so this is the one case that needs
//     [elementConstraintType] reading the DECLARED TYPE directly: the
//     correct answer is a properly-typed null, matching what
//     prepareFinalInputVariableValue's own conversion would have produced,
//     not a refusal.
//
// "app" sets label = "core" explicitly (owner is never set by anyone) and
// its own value happens to prove whole (unlike fluent-bit, nothing in its
// entry is unprovable) - the [resolveEntryAttr] path an unprivileged entry
// always takes.
//
// The resolved values flow into the child module's own resource name
// (wrapper/task/main.tf: name = "${var.name}-${var.label}-${coalesce(var.owner,
// "unset")}"), so a wrong or missing projection would either refuse
// (caught by assertNoErrors) or resolve to the wrong string (caught by the
// exact ImportID assertions) - never silently pass.
func TestModuleForEachComprehensionEachValueAttr(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-foreach-comprehension-each-value"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`module.wrapper.module.task["app"].aws_iam_user.this`:        `CONCRETE app-core-unset`,
		`module.wrapper.module.task["fluent-bit"].aws_iam_user.this`: `CONCRETE fluent-bit-default-team-unset`,
	})

	for _, r := range result.All() {
		if r.Addr.String() == `module.wrapper.module.task["disabled"].aws_iam_user.this` {
			t.Fatalf("the disabled entry (create = false) produced an instance: %s %q", r.Class, r.ImportID)
		}
	}
}
