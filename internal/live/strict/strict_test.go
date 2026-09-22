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
	for _, v := range []Secrets{Store, Refuse, SSM} {
		if !SecretsValid(v) {
			t.Errorf("SecretsValid(%q) = false for a setting this package declares", v)
		}
	}
	for _, v := range []Secrets{"", "none", "Store", "STORE", "no", "SSM", "parameter_store"} {
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

	if got, want := SecretsNames(), `"refuse", "ssm", "store"`; got != want {
		t.Errorf("SecretsNames() = %s, want %s", got, want)
	}

	if !StoresSecrets(Store) {
		t.Error("StoresSecrets(Store) = false")
	}
	if StoresSecrets(Refuse) {
		t.Error("StoresSecrets(Refuse) = true")
	}
	// "ssm" keeps the value, so every layer asking "may this run keep
	// secret material at all" has to answer yes. Reading it as a second
	// "refuse" would make this setting refuse the random_password whose
	// value it exists to relocate - the estate would be running under a
	// refusal it never asked for, and the SSM write path would never be
	// reached to notice.
	if !StoresSecrets(SSM) {
		t.Error("StoresSecrets(SSM) = false; \"ssm\" keeps the value, it just keeps it somewhere else")
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

// TestSecretsInSSMAndStateCache pins the two predicates GitHub issue #1515
// adds beside [StoresSecrets], and it is written as a full truth table over
// the vocabulary plus the zero value because both of them are one-line
// functions whose defects are all off-by-one-setting.
func TestSecretsInSSMAndStateCache(t *testing.T) {
	for _, tc := range []struct {
		v           Secrets
		inSSM       bool
		noCache     bool
		storesAtAll bool
	}{
		{v: Store, inSSM: false, noCache: false, storesAtAll: true},
		{v: Refuse, inSSM: false, noCache: true, storesAtAll: false},
		{v: SSM, inSSM: true, noCache: true, storesAtAll: true},
		// The zero value is a caller that could not read a configuration.
		// Every predicate answers the conservative way for its own
		// question, and for the cache that is "leave it on": turning the
		// cache off for a caller holding nothing would change what every
		// run written before this setting existed does.
		{v: Secrets(""), inSSM: false, noCache: false, storesAtAll: false},
		{v: Secrets("none"), inSSM: false, noCache: false, storesAtAll: false},
	} {
		if got := SecretsInSSM(tc.v); got != tc.inSSM {
			t.Errorf("SecretsInSSM(%q) = %v, want %v", tc.v, got, tc.inSSM)
		}
		if got := NoStateCache(tc.v); got != tc.noCache {
			t.Errorf("NoStateCache(%q) = %v, want %v", tc.v, got, tc.noCache)
		}
		if got := StoresSecrets(tc.v); got != tc.storesAtAll {
			t.Errorf("StoresSecrets(%q) = %v, want %v", tc.v, got, tc.storesAtAll)
		}
	}

	// The relationship that is easy to lose: SecretsInSSM is a strict
	// subset of StoresSecrets, and NoStateCache is neither. A setting that
	// sent values to SSM while the layers above believed nothing was kept
	// would write a parameter no record ever references.
	for v := range secretsSettings {
		if SecretsInSSM(v) && !StoresSecrets(v) {
			t.Errorf("SecretsInSSM(%q) is true while StoresSecrets(%q) is false", v, v)
		}
	}
}
