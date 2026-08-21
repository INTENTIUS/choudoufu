// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// TestEquivalentNullAgainstNonZero pins the guard that keeps [equivalent]
// from reaching its collection arms with a null value in hand.
//
// A legacy-SDK provider hands back null for a collection nobody set, and the
// zeroish allowance above the guard already forgives null-against-empty. The
// case that had no arm was null against a POPULATED collection: RawEquals is
// false, neither zeroish branch fires, the types match, and the switch then
// called LengthInt / ElementIterator / GetAttr on the null side - every one
// of which panics, surfacing to the operator as an OpenTofu crash report
// rather than a diagnostic.
//
// Measured, not hypothesised: `choudoufu live-import` run a second time
// against the already-stamped autoscaling-complete estate
// (live/e2e/corpus-autoscaling-complete) crashed in exactly this spot, on
// the ratify pass's driftedAttrs comparison. Re-running an import is a
// supported thing to do - "already stamped" is one of the outcomes the
// summary line counts - so the crash was reachable by ordinary use.
func TestEquivalentNullAgainstNonZero(t *testing.T) {
	populatedMap := cty.MapVal(map[string]cty.Value{"k": cty.StringVal("v")})
	populatedList := cty.ListVal([]cty.Value{cty.StringVal("v")})
	objTy := cty.Object(map[string]cty.Type{"a": cty.String})
	populatedObj := cty.ObjectVal(map[string]cty.Value{"a": cty.StringVal("v")})

	cases := []struct {
		name string
		a, b cty.Value
		want bool
	}{
		{"null map vs populated map", cty.NullVal(cty.Map(cty.String)), populatedMap, false},
		{"populated map vs null map", populatedMap, cty.NullVal(cty.Map(cty.String)), false},
		{"null list vs populated list", cty.NullVal(cty.List(cty.String)), populatedList, false},
		{"populated list vs null list", populatedList, cty.NullVal(cty.List(cty.String)), false},
		{"null object vs populated object", cty.NullVal(objTy), populatedObj, false},
		{"populated object vs null object", populatedObj, cty.NullVal(objTy), false},

		// The allowance the guard must not eat: null against the empty form
		// of the same type is still the same statement about the cloud.
		{"null map vs empty map", cty.NullVal(cty.Map(cty.String)), cty.MapValEmpty(cty.String), true},
		{"null list vs empty list", cty.NullVal(cty.List(cty.String)), cty.ListValEmpty(cty.String), true},
		{"null string vs empty string", cty.NullVal(cty.String), cty.StringVal(""), true},
		{"null vs null", cty.NullVal(cty.Map(cty.String)), cty.NullVal(cty.Map(cty.String)), true},

		// And the ordinary answers, so the guard is not silently returning
		// false for everything.
		{"same populated map", populatedMap, populatedMap, true},
		{"different populated maps", populatedMap, cty.MapVal(map[string]cty.Value{"k": cty.StringVal("w")}), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A panic here fails the test rather than taking the process
			// down with it, which is the whole point of the guard.
			got := equivalent(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("equivalent(%#v, %#v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestEquivalentNullNestedInsideObject is the shape the crash actually
// arrived in: the null was not the top-level value but an attribute reached
// by the object arm's own recursion, so a guard placed only at the call site
// would have missed it.
func TestEquivalentNullNestedInsideObject(t *testing.T) {
	ty := cty.Object(map[string]cty.Type{"tags": cty.Map(cty.String)})
	prior := cty.ObjectVal(map[string]cty.Value{"tags": cty.NullVal(cty.Map(cty.String))})
	live := cty.ObjectVal(map[string]cty.Value{
		"tags": cty.MapVal(map[string]cty.Value{"tofu-estate": cty.StringVal("e")}),
	})
	if !prior.Type().Equals(ty) || !live.Type().Equals(ty) {
		t.Fatalf("test setup built mismatched types: %s vs %s", prior.Type().GoString(), live.Type().GoString())
	}
	if equivalent(prior, live) {
		t.Fatal("an object whose tags map is null is not equivalent to one carrying a tag")
	}
}
