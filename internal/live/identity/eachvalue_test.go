// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// #260. Every assertion here is on a RENDERED identity - ImportID and
// IdentityValues - and never on a predicate, because the defect this file
// guards against is not a refusal but a marker: try() catching an error this
// package introduced and writing its fallback literal into a cloud tag as a
// resource's identity. A test that only asked "did it refuse" would have
// passed while doing exactly that.

// eachValueInstances reports the rendered identity of every instance of one
// resource, keyed by instance address, plus the identities that came out
// CONCRETE. The count is returned separately from the keys deliberately: one
// defect's whole signature has been two instances where OpenTofu makes
// three, and a map comparison hides that when a key is duplicated.
func eachValueInstances(t *testing.T, dir, resource string) (all, everything []Resolution, concrete map[string]string) {
	t.Helper()

	cfg := loadConfigTree(t, filepath.Join("testdata", dir), nil)
	result, _ := Resolve(context.Background(), cfg)

	// A refused instance is not recorded at all, so an assertion about a
	// refusal cannot read the instance: it reads the WHOLE result instead,
	// which is what `everything` is for. A fallback literal that landed on
	// some other address is still a wrong marker.
	everything = result.All()

	concrete = map[string]string{}
	for _, r := range result.All() {
		if !strings.HasPrefix(r.Addr.String(), resource+"[") && r.Addr.String() != resource {
			continue
		}
		all = append(all, r)
		if r.Class == ClassConcrete {
			concrete[r.Addr.String()] = r.ImportID
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Addr.String() < all[j].Addr.String() })
	return all, everything, concrete
}

// TestEachValueLiteralSiblingResolves is the shape the issue is named for:
// an element with one data-source attribute among literal ones. The literal
// sibling is what the identity reads, and it is knowable.
func TestEachValueLiteralSiblingResolves(t *testing.T) {
	all, _, concrete := eachValueInstances(t, "shapeb-poison", "module.u.aws_iam_user.this")

	if len(all) != 2 {
		t.Fatalf("got %d instances, want 2: %v", len(all), addrsOf(all))
	}
	want := map[string]string{
		`module.u.aws_iam_user.this["alice"]`: "alice-user",
		`module.u.aws_iam_user.this["bob"]`:   "bob-user",
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

// TestEachValueDynamicSiblingStillRefuses is the mutation of the fixture
// above: same element, and the identity reads the DYNAMIC attribute instead
// of the literal one. Without this the test above proves only that something
// resolved, not that the selection picked the right attribute out.
func TestEachValueDynamicSiblingStillRefuses(t *testing.T) {
	all, everything, concrete := eachValueInstances(t, "shapeb-poison-dynamic", "module.u.aws_iam_user.this")

	if len(concrete) != 0 {
		t.Errorf("an identity resolved from a data-source read: %s", showValues(concrete))
	}
	assertNoFallback(t, everything)
	// And in particular it did not pick up the literal sibling's value, or
	// the instance key, which are the two values within reach.
	for _, r := range all {
		for _, wrong := range []string{"alice-user", "bob-user", "alice", "bob"} {
			if r.ImportID == wrong {
				t.Errorf("%s: import ID is %q, which is not what each.value.account names", r.Addr, wrong)
			}
		}
	}
}

// TestEachValueTryDoesNotWinOverAResolvableAttribute is the hazard, first
// direction. The attribute is present and resolvable, so the identity is the
// role's name - and "fallback" must appear nowhere.
func TestEachValueTryDoesNotWinOverAResolvableAttribute(t *testing.T) {
	all, _, concrete := eachValueInstances(t, "shapeb-tryref", "module.u.aws_iam_user.this")

	if len(all) != 1 {
		t.Fatalf("got %d instances, want 1: %v", len(all), addrsOf(all))
	}
	want := map[string]string{`module.u.aws_iam_user.this["alice"]`: "the-role"}
	if !reflect.DeepEqual(concrete, want) {
		t.Errorf("concrete import IDs are %s, want %s", showValues(concrete), showValues(want))
	}
	assertNoFallback(t, all)
}

// TestEachValueTryDoesNotWinOverAnUnresolvableAttribute is the hazard's
// sharp end: the attribute is PRESENT - a data-source read - so the language
// never reaches the fallback. This package cannot resolve it, so it refuses.
// Answering "fallback" here is the wrong-marker outcome the whole
// expression-shaped design exists to make impossible.
func TestEachValueTryDoesNotWinOverAnUnresolvableAttribute(t *testing.T) {
	_, everything, concrete := eachValueInstances(t, "shapeb-trydata", "module.u.aws_iam_user.this")

	if len(concrete) != 0 {
		t.Errorf("an identity resolved over a present-but-unresolvable attribute: %s", showValues(concrete))
	}
	assertNoFallback(t, everything)
}

// TestEachValueTryWinsOverAnAbsentAttribute is the other direction, and it
// is a false refusal today. The attribute is genuinely not in the element
// and the declared type is `any`, so stock OpenTofu takes the fallback and
// so must this.
func TestEachValueTryWinsOverAnAbsentAttribute(t *testing.T) {
	all, _, concrete := eachValueInstances(t, "shapeb-absent", "module.u.aws_iam_user.this")

	if len(all) != 1 {
		t.Fatalf("got %d instances, want 1: %v", len(all), addrsOf(all))
	}
	want := map[string]string{`module.u.aws_iam_user.this["alice"]`: "fallback"}
	if !reflect.DeepEqual(concrete, want) {
		t.Errorf("concrete import IDs are %s, want %s", showValues(concrete), showValues(want))
	}
	if got := all[0].IdentityValues["name"]; got != "fallback" {
		t.Errorf("identity attribute name = %q, want fallback", got)
	}
}

// TestEachValueAbsenceIsNotClaimedAfterADeclaredType is the third wrinkle.
// The caller writes no `name`, but the module's declared type supplies one
// through optional(), so the attribute IS present inside the module and the
// fallback is NOT what OpenTofu produces. The element expression is dropped
// at the conversion, so nothing here claims absence and the instance
// refuses.
//
// It is a refusal where OpenTofu resolves, which is debt rather than a
// defect: the identity would be "from-the-declared-type", and reading it
// needs the converted element read per attribute rather than per element.
// What must never happen is the third answer, "fallback".
func TestEachValueAbsenceIsNotClaimedAfterADeclaredType(t *testing.T) {
	_, everything, concrete := eachValueInstances(t, "shapeb-absent-typed", "module.u.aws_iam_user.this")

	if len(concrete) != 0 {
		t.Errorf("an identity resolved past a declared-type conversion this package did not read: %s", showValues(concrete))
	}
	assertNoFallback(t, everything)
}

func assertNoFallback(t *testing.T, all []Resolution) {
	t.Helper()
	for _, r := range all {
		if strings.Contains(r.ImportID, "fallback") {
			t.Errorf("%s: import ID is %q - try() fell back over an attribute the language does not fall back over", r.Addr, r.ImportID)
		}
		for name, v := range r.IdentityValues {
			if strings.Contains(v, "fallback") {
				t.Errorf("%s: identity attribute %s is %q - try() fell back over an attribute the language does not fall back over", r.Addr, name, v)
			}
		}
	}
}

func addrsOf(all []Resolution) []string {
	out := make([]string, 0, len(all))
	for _, r := range all {
		out = append(out, r.Addr.String())
	}
	return out
}
