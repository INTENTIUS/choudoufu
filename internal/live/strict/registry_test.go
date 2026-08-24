// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package strict

import "testing"

// TestTogglesDefaultsMatchConstants proves [Toggles]' Default field is not
// a second, independently-typed copy of the DefaultXxx constants that can
// drift from them: each entry's Default is rendered FROM its constant, and
// this test pins that against the constant directly rather than against
// the registry's own literal, so a future entry whose Default is hand-typed
// out of sync with its DefaultXxx constant fails here.
func TestTogglesDefaultsMatchConstants(t *testing.T) {
	want := map[string]string{
		"marker_repair":    string(DefaultMarkerRepair),
		"secrets":          string(DefaultSecrets),
		"no_source_create": string(DefaultNoSourceCreate),
	}
	seen := make(map[string]bool)
	for _, tg := range Toggles {
		seen[tg.Name] = true
		w, ok := want[tg.Name]
		if !ok {
			t.Errorf("Toggles has an entry named %q this test does not know about", tg.Name)
			continue
		}
		if tg.Default != w {
			t.Errorf("Toggles[%q].Default = %q, want %q (from the DefaultXxx constant)", tg.Name, tg.Default, w)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("Toggles has no entry named %q", name)
		}
	}
}

// TestToggleNamedAndPinnability pins which toggles this schema currently
// allows the environment to pin, and which it does not - a fact
// [PinRefusal] depends on to answer "" for marker_repair regardless of the
// pin, and one worth catching if a future edit flips it by accident.
func TestToggleNamedAndPinnability(t *testing.T) {
	for _, tc := range []struct {
		name         string
		wantOK       bool
		wantPinnable bool
	}{
		{"secrets", true, true},
		{"no_source_create", true, true},
		{"marker_repair", true, false},
		{"no_such_toggle", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg, ok := toggleNamed(tc.name)
			if ok != tc.wantOK {
				t.Fatalf("toggleNamed(%q) ok = %v, want %v", tc.name, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if tg.Pinnable != tc.wantPinnable {
				t.Errorf("toggleNamed(%q).Pinnable = %v, want %v", tc.name, tg.Pinnable, tc.wantPinnable)
			}
			if tg.Pinnable && tg.SafeValue == "" {
				t.Errorf("toggleNamed(%q) is Pinnable but SafeValue is empty", tc.name)
			}
			if !tg.Pinnable && tg.SafeValue != "" {
				t.Errorf("toggleNamed(%q) is not Pinnable but SafeValue = %q, want \"\"", tc.name, tg.SafeValue)
			}
			if tg.Doc == "" {
				t.Errorf("toggleNamed(%q).Doc is empty", tc.name)
			}
			if tg.Relaxes == "" {
				t.Errorf("toggleNamed(%q).Relaxes is empty", tc.name)
			}
		})
	}
}
