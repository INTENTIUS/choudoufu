// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package strict

import "testing"

// TestDefaultIsTodaysBehavior pins the one property HANDOFF.md's "compatible
// out of the box" rests on at this layer: the default marker_repair setting
// is "repair", the behavior every configuration written before the strict
// block existed already gets, and it is implemented.
//
// A change to this constant is a change to what an existing estate does on
// upgrade, which is exactly the kind of thing that should have to be argued
// for in a diff rather than noticed afterwards.
func TestDefaultIsTodaysBehavior(t *testing.T) {
	if DefaultMarkerRepair != Repair {
		t.Errorf("DefaultMarkerRepair = %q, want %q", DefaultMarkerRepair, Repair)
	}
	if !Valid(DefaultMarkerRepair) {
		t.Errorf("the default %q is not in the vocabulary", DefaultMarkerRepair)
	}
	if !Implemented(DefaultMarkerRepair) {
		t.Errorf("the default %q is not implemented, so an omitted argument would be refused", DefaultMarkerRepair)
	}
}

// TestValidAndImplemented walks the whole vocabulary by value. Implemented
// must imply Valid, and the two non-default settings must be Valid and not
// Implemented - which is the state internal/live/lint refuses on, and the
// state the next slice of #365 changes.
func TestValidAndImplemented(t *testing.T) {
	for _, tc := range []struct {
		v           MarkerRepair
		valid       bool
		implemented bool
	}{
		{Repair, true, true},
		{Report, true, false},
		{Never, true, false},
		{MarkerRepair("sometimes"), false, false},
		{MarkerRepair(""), false, false},
		{MarkerRepair("Repair"), false, false},
	} {
		t.Run(string(tc.v), func(t *testing.T) {
			if got := Valid(tc.v); got != tc.valid {
				t.Errorf("Valid(%q) = %v, want %v", tc.v, got, tc.valid)
			}
			if got := Implemented(tc.v); got != tc.implemented {
				t.Errorf("Implemented(%q) = %v, want %v", tc.v, got, tc.implemented)
			}
			if tc.implemented && !tc.valid {
				t.Fatalf("%q is implemented but not valid, which cannot be a coherent state", tc.v)
			}
		})
	}
}

// TestNamesAreStable: both renderings are sorted, so a diagnostic quoting
// them does not vary between runs. Go's map iteration order is the reason
// this is a test rather than an assumption.
func TestNamesAreStable(t *testing.T) {
	if got, want := Names(), `"never", "repair", "report"`; got != want {
		t.Errorf("Names() = %s, want %s", got, want)
	}
	if got, want := ImplementedNames(), `"repair"`; got != want {
		t.Errorf("ImplementedNames() = %s, want %s", got, want)
	}
	for i := 0; i < 20; i++ {
		if Names() != Names() || ImplementedNames() != ImplementedNames() {
			t.Fatal("a rendering varied between two calls in the same process")
		}
	}
}
