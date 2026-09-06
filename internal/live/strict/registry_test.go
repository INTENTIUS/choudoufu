// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package strict

import (
	"sort"
	"testing"
)

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
		"provider_change":  string(DefaultProviderChange),
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
		{"provider_change", true, true},
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

// TestToggleValuesAreRecognizedSpellings proves every spelling [Toggles]
// declares in a toggle's Values is one its own package-level Valid
// predicate actually recognizes - so this registry can never advertise a
// setting that is a typo the moment an operator writes it - and that
// Default is always among them, the way every other reader of this
// registry (checkLiveStrict's silent-default branch, the rendered doc
// table) assumes.
func TestToggleValuesAreRecognizedSpellings(t *testing.T) {
	valid := map[string]func(string) bool{
		"marker_repair":    func(v string) bool { return Valid(MarkerRepair(v)) },
		"secrets":          func(v string) bool { return SecretsValid(Secrets(v)) },
		"no_source_create": func(v string) bool { return NoSourceCreateValid(NoSourceCreate(v)) },
		"provider_change":  func(v string) bool { return ProviderChangeValid(ProviderChange(v)) },
	}
	for _, tg := range Toggles {
		isValid, ok := valid[tg.Name]
		if !ok {
			t.Fatalf("no Valid predicate registered in this test for toggle %q", tg.Name)
		}
		if len(tg.Values) == 0 {
			t.Errorf("Toggles[%q].Values is empty", tg.Name)
		}
		if tg.Meaning == "" {
			t.Errorf("Toggles[%q].Meaning is empty", tg.Name)
		}
		sawDefault := false
		for _, v := range tg.Values {
			if !isValid(v) {
				t.Errorf("Toggles[%q].Values contains %q, which this fork's own vocabulary does not recognize as a valid spelling", tg.Name, v)
			}
			if v == tg.Default {
				sawDefault = true
			}
		}
		if !sawDefault {
			t.Errorf("Toggles[%q].Values = %v does not contain Default %q", tg.Name, tg.Values, tg.Default)
		}
	}
}

// TestMarkerRepairValuesExcludesReport pins the 2026-08-24 audit finding by
// value: "report" is grammar [Valid] still recognizes (so a configuration
// that writes it gets [unimplementedRepairDetail]'s specific refusal, not
// a generic typo one), but it has no implementation path in this build -
// not even the conditional one "never" has through a markers "record"
// selection - so this registry does not declare it as a usable setting.
// If report mode gets a real mechanism, this is the test that has to
// change on purpose to admit it back into Values.
func TestMarkerRepairValuesExcludesReport(t *testing.T) {
	tg, ok := toggleNamed("marker_repair")
	if !ok {
		t.Fatal(`toggleNamed("marker_repair") not found`)
	}
	got := append([]string(nil), tg.Values...)
	sort.Strings(got)
	want := []string{string(Never), string(Repair)}
	if len(got) != len(want) {
		t.Fatalf("marker_repair Values = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("marker_repair Values = %v, want exactly %v", got, want)
		}
	}
	if !Valid(Report) {
		t.Error(`Valid(Report) = false, want true - "report" must still be recognized grammar, ` +
			`refused with a specific "not implemented" detail rather than a generic typo message`)
	}
	if Implemented(Report) || ImplementedWithSelection(Report) {
		t.Error(`strict.Report is implemented (with or without a selection) but marker_repair's Values still ` +
			`excludes it - the registry is now stale in the other direction and should declare it`)
	}
}
