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

// TestSecretsVocabulary pins the whole of GitHub issue #365 slice 3's
// vocabulary by value, including the two things that are easy to get right
// once and then lose: which setting is the default, and what the ZERO value
// answers.
func TestSecretsVocabulary(t *testing.T) {
	for _, v := range []Secrets{Store, Refuse} {
		if !SecretsValid(v) {
			t.Errorf("SecretsValid(%q) = false for a setting this package declares", v)
		}
	}
	for _, v := range []Secrets{"", "none", "Store", "STORE", "no"} {
		if SecretsValid(v) {
			t.Errorf("SecretsValid(%q) = true", v)
		}
	}

	// The default is "store", not "refuse", and this assertion is the whole
	// reversal slice 3 made: HANDOFF.md's "compatible out of the box" says
	// "secrets the configuration generates are stored there the way stock
	// stores them", and the principle is the toggle. Flipping this constant
	// back makes a configuration containing one random_password unrunnable
	// here and runnable on stock, which is HANDOFF's first difference row.
	if got, want := DefaultSecrets, Store; got != want {
		t.Fatalf("DefaultSecrets = %q, want %q", got, want)
	}
	if !SecretsValid(DefaultSecrets) {
		t.Fatal("DefaultSecrets is not in the vocabulary")
	}

	if got, want := SecretsNames(), `"refuse", "store"`; got != want {
		t.Errorf("SecretsNames() = %s, want %s", got, want)
	}

	if !StoresSecrets(Store) {
		t.Error("StoresSecrets(Store) = false")
	}
	if StoresSecrets(Refuse) {
		t.Error("StoresSecrets(Refuse) = true")
	}
	// The zero value answers FALSE while DefaultSecrets answers true, and
	// the two are different questions. A layer holding Secrets("") could not
	// read a configuration, and must not conclude the operator asked for
	// storage; a layer that CAN read one resolves an omitted argument to
	// DefaultSecrets first. See identity.SecretsFor.
	if StoresSecrets(Secrets("")) {
		t.Error("StoresSecrets of the zero value = true; a caller that could not read a configuration must not be told the operator asked to store secrets")
	}
}
