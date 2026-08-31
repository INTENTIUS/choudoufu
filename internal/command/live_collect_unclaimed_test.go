// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"
)

// TestCollectUnclaimedSettingDefaultsToAdoptionOnly is the whole mechanism
// rfc/20260830-stale-state-charter.md's ruling left to be designed, stated
// as a table: an ordinary plan does not ask the account-inventory question,
// and "choudoufu plan -adoption-only" - the command whose entire subject is
// that question - does.
func TestCollectUnclaimedSettingDefaultsToAdoptionOnly(t *testing.T) {
	for _, tc := range []struct {
		name         string
		env          string
		envSet       bool
		adoptionOnly bool
		want         bool
	}{
		{name: "ordinary plan", want: false},
		{name: "adoption-only", adoptionOnly: true, want: true},

		{name: "env 1 over an ordinary plan", env: "1", envSet: true, want: true},
		{name: "env true over an ordinary plan", env: "true", envSet: true, want: true},
		{name: "env on over an ordinary plan", env: "on", envSet: true, want: true},
		{name: "env yes over an ordinary plan", env: "yes", envSet: true, want: true},
		{name: "env TRUE is case-insensitive", env: "TRUE", envSet: true, want: true},

		{name: "env 0 over adoption-only", env: "0", envSet: true, adoptionOnly: true, want: false},
		{name: "env false over adoption-only", env: "false", envSet: true, adoptionOnly: true, want: false},
		{name: "env off over adoption-only", env: "off", envSet: true, adoptionOnly: true, want: false},
		{name: "env no over adoption-only", env: "no", envSet: true, adoptionOnly: true, want: false},

		{name: "whitespace is trimmed", env: "  1  ", envSet: true, want: true},
		{name: "empty env leaves the command's choice", env: "", envSet: true, adoptionOnly: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envSet {
				t.Setenv(collectUnclaimedEnvVar, tc.env)
			}
			got, diags := collectUnclaimedSetting(tc.adoptionOnly)
			if diags.HasErrors() {
				t.Fatalf("unexpected diagnostics: %s", diags.Err())
			}
			if got != tc.want {
				t.Errorf("collectUnclaimedSetting(adoptionOnly=%v) with %s=%q = %v, want %v",
					tc.adoptionOnly, collectUnclaimedEnvVar, tc.env, got, tc.want)
			}
		})
	}
}

// TestCollectUnclaimedSettingRefusesAValueItDoesNotUnderstand: an operator
// who wrote a setting meant to be running under it, and a run that silently
// ignored "TOFU_LIVE_COLLECT_UNCLAIMED=yes-please" and then reported
// "Foreign resources: nothing was swept" would read as an answer to a
// question it never asked. Same discipline as sweepParallelismSetting's own
// refusal.
func TestCollectUnclaimedSettingRefusesAValueItDoesNotUnderstand(t *testing.T) {
	t.Setenv(collectUnclaimedEnvVar, "sometimes")
	_, diags := collectUnclaimedSetting(false)
	if !diags.HasErrors() {
		t.Fatalf("%s=sometimes was accepted silently", collectUnclaimedEnvVar)
	}
	rendered := diags.Err().Error()
	for _, want := range []string{collectUnclaimedEnvVar, "sometimes", "-adoption-only"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the refusal does not mention %q, so it does not tell the operator what to write instead:\n%s", want, rendered)
		}
	}
}
