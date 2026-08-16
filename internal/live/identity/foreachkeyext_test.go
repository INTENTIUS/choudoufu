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

// #189's slice 2: staticForEachKeys (localvalue.go, #178's key-set fix)
// stopped at "an object constructor, or merge() of several named locals",
// and its own doc said so explicitly - a for-comprehension, or a merge()
// reached through a splat rather than separate arguments, was left alone.
// The corpus shape (simpleinfra's team-members-datadog/users.tf) is both at
// once: a for-comprehension building the for_each object, fed by
// merge(list...) of several further for-comprehensions, each of whose VALUE
// clause reaches a managed resource's attribute while the KEY clause needs
// nothing but the loop's own key.

// TestForEachComprehensionKeyOnly is the positive case: the for_each source
// is a for-comprehension whose value clause reads a managed resource's
// attribute (aws_iam_role.team[k].name, standing in for the corpus's
// datadog_role.<x>.name), and whose key clause is the bare loop key. The
// key set must resolve without ever touching that resource attribute.
func TestForEachComprehensionKeyOnly(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-comprehension-keyonly"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`aws_iam_role.team["a"]`: `CONCRETE team-a`,
		`aws_iam_role.team["b"]`: `CONCRETE team-b`,
		`aws_iam_user.this["a"]`: `CONCRETE a`,
		`aws_iam_user.this["b"]`: `CONCRETE b`,
	})
}

// TestForEachComprehensionKeyNeedsValueRefuses is the first safety
// boundary: the key clause is "v.login", the for-comprehension's OWN value
// variable, not its key - so the key set genuinely is not knowable without
// the same resource-derived value data the fix exists to avoid needing.
// This must refuse exactly as evaluating the whole for_each expression
// already did, never fall back to treating the key clause as though it had
// been "k".
func TestForEachComprehensionKeyNeedsValueRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-comprehension-key-needs-value"), nil)

	_, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatal("expected a refusal: the key clause reads the for-comprehension's own value variable")
	}
	if !hasDiag(diags, "Unable to compute static value", "aws_iam_user.this for_each depends on local.merged") {
		t.Errorf("expected the ordinary for_each refusal to stand, unchanged; got:\n%s", renderDiags(diags))
	}
}

// TestForEachComprehensionSourceIsResourceRefuses is the second safety
// boundary: the for-comprehension's SOURCE collection is a managed
// resource directly (aws_iam_role.team), not a local or a variable, so its
// own key set is never reached through staticForEachKeys's local/var
// chase at all. This must refuse, not treat the resource's declared
// for_each keys as though they were free to read here.
func TestForEachComprehensionSourceIsResourceRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-comprehension-key-needs-resource-source"), nil)

	_, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatal("expected a refusal: the for-comprehension's source collection is a managed resource, not local/var")
	}
	if !hasDiag(diags, "Unable to compute static value", "aws_iam_user.this for_each depends on local.merged") {
		t.Errorf("expected the ordinary for_each refusal to stand, unchanged; got:\n%s", renderDiags(diags))
	}
}

// TestMergeSplatForEachKeys is merge(list...)'s own positive case: the
// splatted argument is a single list literal, itself built from two
// for-comprehensions whose value clauses each reach a different managed
// resource's attribute. The key set is the union of both, exactly as
// merge(a, b) already produced for two separate object arguments.
func TestMergeSplatForEachKeys(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-merge-splat"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`aws_iam_role.team["a"]`: `CONCRETE team-a`,
		`aws_iam_role.team["b"]`: `CONCRETE team-b`,
		`aws_iam_user.this["a"]`: `CONCRETE a`,
		`aws_iam_user.this["b"]`: `CONCRETE b`,
	})
}
