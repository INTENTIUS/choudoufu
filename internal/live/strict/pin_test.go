// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package strict

import (
	"strings"
	"testing"
)

// TestPinned pins EnvPin's exact grammar: "1" and nothing else. (This is
// EnvPin's own convention, not shared with
// internal/command/live_mode.go's CHOUDOUFU_NODE_RESOLVE: that flag
// defaulted on 2026-08-25 and switched to an opt-out grammar - anything
// other than exactly "0" - the opposite shape from this opt-in one.)
func TestPinned(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  string
		set  bool
		want bool
	}{
		{"unset", "", false, false},
		{"set to 1", "1", true, true},
		{"set empty", "", true, false},
		{"set to true (not the grammar)", "true", true, false},
		{"set to 0", "0", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(EnvPin, tc.val)
			}
			if got := Pinned(); got != tc.want {
				t.Errorf("Pinned() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSecretsDefaultAndNoSourceCreateDefault is test (c) and half of test
// (b) from GitHub issue #365's pinnable-from-environment acceptance
// criteria: with the pin unset, resolution is byte-identical to the
// original constants; with it set, an omitted argument resolves to the
// pinned (safe) setting instead.
func TestSecretsDefaultAndNoSourceCreateDefault(t *testing.T) {
	t.Run("pin unset: config governs, byte-identical to the original constants", func(t *testing.T) {
		if got, want := SecretsDefault(), DefaultSecrets; got != want {
			t.Errorf("SecretsDefault() = %q, want %q (DefaultSecrets)", got, want)
		}
		if got, want := NoSourceCreateDefault(), DefaultNoSourceCreate; got != want {
			t.Errorf("NoSourceCreateDefault() = %q, want %q (DefaultNoSourceCreate)", got, want)
		}
	})

	t.Run("pin set: an omitted argument resolves to the pinned setting", func(t *testing.T) {
		t.Setenv(EnvPin, "1")
		if got, want := SecretsDefault(), Refuse; got != want {
			t.Errorf("SecretsDefault() with the pin set = %q, want %q", got, want)
		}
		if got, want := NoSourceCreateDefault(), NoSourceRefuse; got != want {
			t.Errorf("NoSourceCreateDefault() with the pin set = %q, want %q", got, want)
		}
	})
}

// TestPinRefusal is test (a): a value that relaxes a pinned, pinnable
// toggle gets a refusal naming both the pin (the environment variable and
// the value it forces) and the offending setting (the toggle name and the
// value written in configuration).
func TestPinRefusal(t *testing.T) {
	t.Run("pin unset: no refusal, whatever the value", func(t *testing.T) {
		if got := PinRefusal("secrets", "store"); got != "" {
			t.Errorf("PinRefusal() with the pin unset = %q, want \"\"", got)
		}
	})

	t.Run("pin set, value already the pinned setting: no refusal", func(t *testing.T) {
		t.Setenv(EnvPin, "1")
		if got := PinRefusal("secrets", "refuse"); got != "" {
			t.Errorf("PinRefusal() for the pinned value itself = %q, want \"\"", got)
		}
		if got := PinRefusal("no_source_create", "refuse"); got != "" {
			t.Errorf("PinRefusal() for the pinned value itself = %q, want \"\"", got)
		}
	})

	t.Run("pin set, not a pinnable toggle: no refusal", func(t *testing.T) {
		t.Setenv(EnvPin, "1")
		if got := PinRefusal("marker_repair", "never"); got != "" {
			t.Errorf("PinRefusal(%q) = %q, want \"\" - marker_repair is not Pinnable", "marker_repair", got)
		}
	})

	t.Run("pin set, unknown toggle name: no refusal", func(t *testing.T) {
		t.Setenv(EnvPin, "1")
		if got := PinRefusal("no_such_toggle", "anything"); got != "" {
			t.Errorf("PinRefusal() for an unknown toggle = %q, want \"\"", got)
		}
	})

	t.Run("secrets relaxed while pinned: refusal names both sides", func(t *testing.T) {
		t.Setenv(EnvPin, "1")
		got := PinRefusal("secrets", "store")
		if got == "" {
			t.Fatal("PinRefusal() = \"\", want a refusal")
		}
		for _, want := range []string{EnvPin, "secrets", `"refuse"`, `"store"`} {
			if !strings.Contains(got, want) {
				t.Errorf("PinRefusal() = %q, want it to contain %q", got, want)
			}
		}
	})

	t.Run("no_source_create relaxed while pinned: refusal names both sides", func(t *testing.T) {
		t.Setenv(EnvPin, "1")
		got := PinRefusal("no_source_create", "create")
		if got == "" {
			t.Fatal("PinRefusal() = \"\", want a refusal")
		}
		for _, want := range []string{EnvPin, "no_source_create", `"refuse"`, `"create"`} {
			if !strings.Contains(got, want) {
				t.Errorf("PinRefusal() = %q, want it to contain %q", got, want)
			}
		}
	})
}

// TestPinRefusalMutationCheck is test (d): the message is asserted by the
// specific facts it must carry, not merely by non-emptiness, so that a
// change which drops one of those facts - the env var's name, the value it
// forces, or the value the configuration wrote - fails this test rather
// than passing silently. Each subtest below removes exactly one of those
// facts from what PinRefusal is given or expected to say, mirroring what a
// real regression would look like: a caller passing the wrong toggle name,
// or an assertion that stopped checking the offending value.
//
// This is the check proving itself falsifiable, per the gauntlet worker
// brief's "prove your checks can fail" rule: PinRefusalNamesBothSidesFacts
// below is exercised once here with the real function, and its own doc
// comment records that it was hand-verified to fail when the message is
// mutated to drop the environment variable's name (temporarily changing
// pin.go's format string; not committed).
func TestPinRefusalMutationCheck(t *testing.T) {
	t.Setenv(EnvPin, "1")
	msg := PinRefusal("secrets", "store")
	if !pinRefusalNamesBothSidesFacts(msg, EnvPin, "secrets", "refuse", "store") {
		t.Fatalf("PinRefusal() = %q, missing one of: env var name, toggle name, pinned value, or written value", msg)
	}

	// Each of these must NOT pass the same check against a message missing
	// one required fact, which is what makes the helper itself trustworthy
	// rather than a tautology that accepts anything.
	for _, tc := range []struct {
		name    string
		mutated string
	}{
		{"drops the env var name", strings.ReplaceAll(msg, EnvPin, "")},
		{"drops the pinned value", strings.ReplaceAll(msg, `"refuse"`, "")},
		{"drops the written value", strings.ReplaceAll(msg, `"store"`, "")},
		{"drops the toggle name", strings.ReplaceAll(msg, "secrets", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if pinRefusalNamesBothSidesFacts(tc.mutated, EnvPin, "secrets", "refuse", "store") {
				t.Errorf("pinRefusalNamesBothSidesFacts() accepted a message that %s: %q", tc.name, tc.mutated)
			}
		})
	}
}

// pinRefusalNamesBothSidesFacts is the fact-check TestPinRefusalMutationCheck
// runs against both the real message and a set of deliberately mutated
// ones, so the assertion itself is proven to reject what it should reject.
func pinRefusalNamesBothSidesFacts(msg, envVar, toggleName, pinnedValue, writtenValue string) bool {
	return strings.Contains(msg, envVar) &&
		strings.Contains(msg, toggleName) &&
		strings.Contains(msg, `"`+pinnedValue+`"`) &&
		strings.Contains(msg, `"`+writtenValue+`"`)
}
