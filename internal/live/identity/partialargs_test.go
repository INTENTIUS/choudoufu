// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// Every assertion in this file is on a RENDERED identity, never on
// Blocked() or on an instance count alone. The failure this change could
// cause is not a refusal: it is a marker naming a cloud object that is not
// the one the block owns, and a resolution that renders the wrong string
// raises nothing anywhere. See internal/live/check's TestIdentityGolden for
// the same discipline applied across every fixture at once.

// TestPartialModuleArgumentNamesItsInstances is the shape the change is for.
// The caller's `users` map is keyed alice and bob in the configuration
// itself; only a leaf under each key names a resource. Both instances exist,
// and each one's identity is the name its own block writes.
func TestPartialModuleArgumentNamesItsInstances(t *testing.T) {
	all, _, concrete := eachValueInstances(t, "modulearg-partial", "module.u.aws_iam_user.this")

	if len(all) != 2 {
		t.Fatalf("got %d instances, want 2: %v", len(all), addrsOf(all))
	}
	// The provider imports aws_iam_user by user name
	// (registry.terraform.io/hashicorp/aws, "Import": `terraform import
	// aws_iam_user.lb loadbalancer`), so the import ID is the name and
	// nothing else.
	want := map[string]string{
		`module.u.aws_iam_user.this["alice"]`: "user-alice",
		`module.u.aws_iam_user.this["bob"]`:   "user-bob",
	}
	if !reflect.DeepEqual(concrete, want) {
		t.Errorf("concrete import IDs are %s, want %s", showValues(concrete), showValues(want))
	}
	for _, r := range all {
		if got := r.IdentityValues["name"]; got != concrete[r.Addr.String()] {
			t.Errorf("%s: identity attribute name = %q, import ID = %q", r.Addr, got, r.ImportID)
		}
	}
}

// TestPartialModuleArgumentCountsItsElements is the count half. The caller
// writes a two-element list whose second element names a resource, so the
// LENGTH is in the configuration even though one element is not, and
// `count = length(var.groups) > 1 ? 1 : 0` is one instance.
func TestPartialModuleArgumentCountsItsElements(t *testing.T) {
	all, _, concrete := eachValueInstances(t, "modulearg-partial", "module.u.aws_iam_group.g")

	if len(all) != 1 {
		t.Fatalf("got %d instances, want 1: %v", len(all), addrsOf(all))
	}
	// aws_iam_group imports by group name.
	want := map[string]string{`module.u.aws_iam_group.g[0]`: "the-group"}
	if !reflect.DeepEqual(concrete, want) {
		t.Errorf("concrete import IDs are %s, want %s", showValues(concrete), showValues(want))
	}
}

// TestPartialModuleArgumentRefusesADynamicKey is the mutation, and the one
// that matters most. Move the unresolvable reference from a value to a KEY
// and nothing in the configuration says which instances exist. Naming the
// literal sibling alone would silently drop an instance; naming it and
// inventing the other would write a fabricated address into a cloud tag.
// Both are worse than the refusal this replaces, so the rebuild refuses the
// whole argument.
func TestPartialModuleArgumentRefusesADynamicKey(t *testing.T) {
	_, everything, concrete := eachValueInstances(t, "modulearg-partial-dynkey", "module.u.aws_iam_user.this")

	if len(concrete) != 0 {
		t.Errorf("an identity resolved over a for_each whose key set is not in the configuration: %s", showValues(concrete))
	}
	// And in particular the literal key did not come through on its own.
	for _, r := range everything {
		if r.ImportID == "user-bob" {
			t.Errorf("%s: import ID is %q - the literal half of an unprovable key set was used as if it were the whole", r.Addr, r.ImportID)
		}
	}
}

// TestRebuildConstructorSubstitutesOnlyValues drives the rebuild directly,
// so that the two rules that keep it honest are checked at the unit rather
// than inferred from a resolution: a value it cannot evaluate becomes an
// unknown, and a key it cannot evaluate refuses the whole constructor.
func TestRebuildConstructorSubstitutesOnlyValues(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "modulearg-partial"), nil)
	eval := cfg.Module.StaticEvaluator
	if eval == nil {
		t.Fatal("fixture has no static evaluator")
	}
	mc := cfg.Module.ModuleCalls["u"]
	if mc == nil {
		t.Fatal("fixture has no module call u")
	}
	attrs, _ := mc.Config.JustAttributes()

	users := attrs["users"]
	if users == nil {
		t.Fatal("fixture module call has no users argument")
	}
	val, ok := rebuildConstructor(context.Background(), eval.Pure(), users.Expr, rebuildIdent("var.users", users), nil)
	if !ok {
		t.Fatal("the users argument did not rebuild")
	}
	if !val.IsKnown() {
		t.Fatal("the rebuilt object is itself unknown, so it names no keys at all")
	}
	got := map[string]bool{}
	for it := val.ElementIterator(); it.Next(); {
		k, v := it.Element()
		got[k.AsString()] = true
		// The element itself rebuilds too - its own key, `role`, is
		// literal - so what has to be unknown is the LEAF, not the
		// element. IsWhollyKnown is the question; IsKnown would pass on
		// an object that had quietly resolved its whole interior.
		if v.IsWhollyKnown() {
			t.Errorf("element %q rebuilt to a wholly known value %#v; the leaf under it names a resource, so an unknown is the only honest answer", k.AsString(), v)
		}
	}
	if !reflect.DeepEqual(got, map[string]bool{"alice": true, "bob": true}) {
		t.Errorf("rebuilt keys are %v, want alice and bob", got)
	}

	// The key mutation, at the unit.
	dyn := loadConfigTree(t, filepath.Join("testdata", "modulearg-partial-dynkey"), nil)
	dynCall := dyn.Module.ModuleCalls["u"]
	dynAttrs, _ := dynCall.Config.JustAttributes()
	if _, ok := rebuildConstructor(context.Background(), dyn.Module.StaticEvaluator.Pure(), dynAttrs["users"].Expr, rebuildIdent("var.users", dynAttrs["users"]), nil); ok {
		t.Error("a constructor with an unevaluable KEY rebuilt; it must refuse, because the key is the address")
	}
}

// TestPartialModuleArgumentResolvesALiteralLeaf is the identity-ARGUMENT
// half (#323). modulearg-partial's two tests prove which instances exist;
// this proves what one of those instances' identity says, over the same
// poisoned argument. Both leaves the caller wrote out have to come through,
// through a real list(map(string)) type constraint and a template that
// joins them, so a conversion that quietly unified the element down to one
// unknown would fail here rather than pass with a shorter string.
func TestPartialModuleArgumentResolvesALiteralLeaf(t *testing.T) {
	all, _, concrete := eachValueInstances(t, "modulearg-partial-value", "module.u.aws_iam_user.literal")

	if len(all) != 1 {
		t.Fatalf("got %d instances, want 1: %v", len(all), addrsOf(all))
	}
	// aws_iam_user imports by user name, so the import ID is the name.
	want := map[string]string{`module.u.aws_iam_user.literal[0]`: "platform-alpha"}
	if !reflect.DeepEqual(concrete, want) {
		t.Errorf("concrete import IDs are %s, want %s", showValues(concrete), showValues(want))
	}
	for _, r := range all {
		if got := r.IdentityValues["name"]; got != concrete[r.Addr.String()] {
			t.Errorf("%s: identity attribute name = %q, import ID = %q", r.Addr, got, r.ImportID)
		}
	}
}

// TestPartialModuleArgumentStillRefusesTheDynamicLeaf is the mutation, and
// the half that has to hold for the half above to be worth having. The
// sibling resource reads the ONE leaf that is not in the configuration, off
// the very same rebuilt value, and must resolve to nothing at all.
//
// The named wrong answers are the two ways this could fabricate a marker.
// "unset" is lookup()'s own default, which is what a rebuild that DROPPED
// the refusing key instead of making it unknown would return - a perfectly
// plausible string, written to a cloud tag, naming an object nobody owns.
// "platform" and "alpha" are the literal siblings, which is what a rebuild
// that substituted at the wrong level would reach for.
func TestPartialModuleArgumentStillRefusesTheDynamicLeaf(t *testing.T) {
	_, everything, concrete := eachValueInstances(t, "modulearg-partial-value", "module.u.aws_iam_user.dynamic")

	if len(concrete) != 0 {
		t.Errorf("an identity resolved from a leaf the configuration does not state: %s", showValues(concrete))
	}
	for _, r := range everything {
		for _, wrong := range []string{"unset", "platform", "alpha", "platform-alpha"} {
			if r.ImportID == wrong && strings.Contains(r.Addr.String(), "aws_iam_user.dynamic") {
				t.Errorf("%s: import ID is %q, which is not what the granted leaf names", r.Addr, r.ImportID)
			}
		}
	}
}

func rebuildIdent(subject string, attr *hcl.Attribute) configs.StaticIdentifier {
	return configs.StaticIdentifier{Module: addrs.RootModule, Subject: subject, DeclRange: attr.Range}
}
