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

// The key-set chase (#178, widened by #189 and #239) proves which instances a
// for_each produces without evaluating a single value, because a value is
// exactly where a managed resource's attribute is allowed to sit. It then
// threw the values away wholesale, so `for_each = { lit = "a-string", dyn =
// aws_x.y.attr }` refused each.value for BOTH keys - including the one whose
// value the resolver had just evaluated for itself.
//
// The tests below pin the per-key rule that replaced that: a key binds
// each.value when this resolver evaluated THAT key's own value expression,
// and stays unbound otherwise. Every assertion is on a rendered identity -
// Resolution.ImportID or the absence of an address from the result - rather
// than on whether some diagnostic fired, because a wrong binding produces a
// perfectly ordinary-looking marker and only the rendered value shows it.

// TestForEachKeyOnlyBindsOnlyProvenValues is the rule itself, on the shape
// that motivated it. See testdata/foreach-value-proven.
func TestForEachKeyOnlyBindsOnlyProvenValues(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-value-proven"), nil)
	result, _ := Resolve(context.Background(), cfg)

	res := resolutionAt(t, result, `aws_iam_user.team["alice"]`)
	if res.Class != ClassConcrete {
		t.Errorf(`aws_iam_user.team["alice"] resolved %s, want CONCRETE`, res.Class)
	}
	if res.ImportID != "alice-from-config" {
		t.Errorf(`aws_iam_user.team["alice"] import ID is %q, want %q`, res.ImportID, "alice-from-config")
	}
	if got := res.IdentityValues["name"]; got != "alice-from-config" {
		t.Errorf(`aws_iam_user.team["alice"] name is %q, want %q`, got, "alice-from-config")
	}

	// bob's value is aws_iam_group.admins.name. #260 widened this: the
	// element expression is now carried onto the expansion, so each.value
	// for bob reaches [resolver.parentPart] exactly as the direct reference
	// `name = aws_iam_group.admins.name` in a resource body always has, and
	// the group's own name is a literal, so the part collapses to it.
	//
	// That is not a fabrication and the assertion says so by pinning the
	// value: "admins" is the group's OWN name, which is what OpenTofu
	// really creates this user with. The three wrong answers this used to
	// guard against - the empty string, alice's value, and the instance key
	// - are each checked for by name below, because "resolved to something"
	// and "resolved to the right thing" are different claims.
	assertEachValueParent(t, result, `aws_iam_user.team["bob"]`, "admins")

	// The key set is unchanged by any of this: two keys, and neither of them
	// an unkeyed address. A value binding that leaked into key derivation
	// would show up here first.
	if _, ok := result.Get(mustAddr(t, `aws_iam_user.team`)); ok {
		t.Errorf("aws_iam_user.team resolved unkeyed; the block uses for_each")
	}
}

// TestForEachKeyOnlyProvenValueMutation removes the one obstacle
// TestForEachKeyOnlyBindsOnlyProvenValues says bob refuses for - the managed
// resource reference - and changes nothing else. Both instances then resolve,
// which is what makes "bob refuses because of aws_iam_group.admins.name" a
// claim about the cause rather than about the outcome.
func TestForEachKeyOnlyProvenValueMutation(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-value-proven-mutated"), nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	want := map[string]string{
		`aws_iam_user.team["alice"]`: "alice-from-config",
		`aws_iam_user.team["bob"]`:   "bob-from-config",
	}
	for addr, id := range want {
		res := resolutionAt(t, result, addr)
		if res.Class != ClassConcrete {
			t.Errorf("%s resolved %s, want CONCRETE", addr, res.Class)
			continue
		}
		if res.ImportID != id {
			t.Errorf("%s import ID is %q, want %q", addr, res.ImportID, id)
		}
	}
}

// TestForEachComprehensionBindsOnlyProvenValues is the same rule reached
// through a for-comprehension rather than a bare object: the corpus site in
// terraform-aws-modules/iam that this first cleared. See
// testdata/foreach-value-proven-comprehension.
//
// The comprehension's value clause is the bare loop variable, so what
// each.value binds to is whatever the SOURCE element proved - which is the
// seam between [resolver.forSourceElements] and [resolver.forExprElems], and
// the reason the two must agree on what "proven" means.
func TestForEachComprehensionBindsOnlyProvenValues(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-value-proven-comprehension"), nil)
	result, _ := Resolve(context.Background(), cfg)

	res := resolutionAt(t, result, `aws_iam_user.team["alice"]`)
	if res.ImportID != "alice-from-config" {
		t.Errorf(`aws_iam_user.team["alice"] import ID is %q, want %q`, res.ImportID, "alice-from-config")
	}
	assertEachValueParent(t, result, `aws_iam_user.team["bob"]`, "admins")
}

// TestForEachMergeValuePrecedence pins merge()'s own rule on the values, not
// only on the keys: the LATER argument wins.
//
// Nothing before this could tell first-wins from last-wins, because the key
// union is sorted before it becomes instance keys and both orders produce the
// same key set. A value can tell, and binding the overridden one would name a
// live object the configuration does not ask for.
func TestForEachMergeValuePrecedence(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-value-merge-precedence"), nil)
	result, _ := Resolve(context.Background(), cfg)

	res := resolutionAt(t, result, `aws_iam_user.team["shared"]`)
	if res.ImportID != "from-override" {
		t.Errorf(`aws_iam_user.team["shared"] import ID is %q, want %q (merge takes the later argument's value)`, res.ImportID, "from-override")
	}
	// #260: "dyn" resolves through its element EXPRESSION now, which also
	// makes this fixture pin merge's precedence on the expression half - the
	// union keeps the later argument's binding whichever half carries it.
	assertEachValueParent(t, result, `aws_iam_user.team["dyn"]`, "admins")
}

// assertEachValueParent pins an instance whose each.value is a managed
// resource's identity attribute (#260's B-ref/managed shape): it must
// resolve, and it must resolve to the PARENT's own value - not to the
// instance key, not to a sibling key's value, and not to nothing.
func assertEachValueParent(t *testing.T, result *Result, addr, want string) {
	t.Helper()
	res := resolutionAt(t, result, addr)
	if res.ImportID != want {
		t.Errorf("%s import ID is %q, want %q (the parent resource's own identity attribute)", addr, res.ImportID, want)
	}
	if got := res.IdentityValues["name"]; got != want {
		t.Errorf("%s identity attribute name is %q, want %q", addr, got, want)
	}
}

// TestForEachProvenValueRefusesMarked is the marks boundary. A sensitive
// value must not become each.value, because each.value reaches identity
// arguments and an identity becomes a tofu-address marker written to a cloud
// tag in plaintext.
//
// The assertion is that no resolution carries the secret ANYWHERE in its
// rendered identity, not merely that the instance is absent: a future change
// that resolved the instance under a partially-unmarked value would satisfy
// an absence check on the address alone.
func TestForEachProvenValueRefusesMarked(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-value-sensitive"), nil)
	result, _ := Resolve(context.Background(), cfg)

	for _, res := range result.All() {
		if strings.Contains(res.ImportID, "s3cr3t-name") {
			t.Errorf("%s import ID is %q, which carries the sensitive value", res.Addr, res.ImportID)
		}
		for attr, v := range res.IdentityValues {
			if strings.Contains(v, "s3cr3t-name") {
				t.Errorf("%s identity attribute %s is %q, which carries the sensitive value", res.Addr, attr, v)
			}
		}
	}
	if got, ok := result.Get(mustAddr(t, `aws_iam_user.team["alice"]`)); ok {
		t.Errorf(`aws_iam_user.team["alice"] resolved to import ID %q; its value is sensitive`, got.ImportID)
	}
	// carol is the control: an ordinary string in the same object, so the
	// refusal above is about the mark and not about the block.
	if res := resolutionAt(t, result, `aws_iam_user.team["carol"]`); res.ImportID != "carol-from-config" {
		t.Errorf(`aws_iam_user.team["carol"] import ID is %q, want %q`, res.ImportID, "carol-from-config")
	}
}

// TestForEachProvenValueRefusesImpure closes the bypass: an identity argument
// that calls uuid() is already refused, and routing the same call through the
// for_each source's value must not get it accepted by another door.
func TestForEachProvenValueRefusesImpure(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-value-impure"), nil)
	result, _ := Resolve(context.Background(), cfg)

	if got, ok := result.Get(mustAddr(t, `aws_iam_user.team["alice"]`)); ok {
		t.Errorf(`aws_iam_user.team["alice"] resolved to import ID %q; its value calls uuid()`, got.ImportID)
	}
	if res := resolutionAt(t, result, `aws_iam_user.team["carol"]`); res.ImportID != "carol-from-config" {
		t.Errorf(`aws_iam_user.team["carol"] import ID is %q, want %q`, res.ImportID, "carol-from-config")
	}
}

// TestForEachProvenValueCollisionStillFires is the audit shape that has
// caught three defects in this package: a fix that turns a warning into
// silence. Newly-bound values create newly-comparable identities, and two
// keys whose proven values are the same string are two blocks claiming one
// live user.
func TestForEachProvenValueCollisionStillFires(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-value-proven-collision"), nil)
	result, diags := Resolve(context.Background(), cfg)

	// Both really do resolve to the one identity - stated first, because a
	// collision diagnostic over instances that did not resolve would prove
	// nothing about this change.
	for _, key := range []string{"a", "b"} {
		res := resolutionAt(t, result, `aws_iam_user.team["`+key+`"]`)
		if res.ImportID != "shared-name" {
			t.Fatalf(`aws_iam_user.team[%q] import ID is %q, want %q`, key, res.ImportID, "shared-name")
		}
	}
	if !hasDiag(diags, "Two resources with the same identity", `both resolve to the identity "shared-name"`) {
		t.Fatalf("expected a collision diagnostic for the two instances named shared-name, got:\n%s", renderDiags(diags))
	}
}
