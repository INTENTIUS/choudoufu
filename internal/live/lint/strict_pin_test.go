// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/strict"
)

// TestStrictPinRefusesWhatItPins is the loud half of GitHub issue #365's
// pinnable-from-environment acceptance criteria, test (a): with
// [strict.EnvPin] set, a configuration that explicitly relaxes a pinned
// toggle is refused by CheckContext, and the refusal names both the pin
// (the environment variable, and the value it forces) and the offending
// line (the toggle's own argument, at the range the decoder recorded for
// it).
//
// It reuses testdata/strict-secrets-store and
// testdata/strict-nosourcecreate-create rather than adding new fixtures:
// both already exist as the "the default/toggle written out by hand"
// clean-pass cases in TestCheck, which is test (c) - "pin unset: config
// governs, today's behavior" - for these same two files, so the pair
// together cover the pin-set and pin-unset side of the same construct
// without a third copy of it.
func TestStrictPinRefusesWhatItPins(t *testing.T) {
	for _, tc := range []struct {
		name          string
		dir           string
		rule          Rule
		toggleName    string
		writtenValue  string
		safeValue     string
		wantLine      int
		wantConstruct string
	}{
		{
			name:          "secrets = \"store\" while the pin forces \"refuse\"",
			dir:           "testdata/strict-secrets-store",
			rule:          RuleStrictSecrets,
			toggleName:    "secrets",
			writtenValue:  "store",
			safeValue:     "refuse",
			wantLine:      13,
			wantConstruct: `strict.secrets = "store"`,
		},
		{
			name:          "no_source_create = \"create\" while the pin forces \"refuse\"",
			dir:           "testdata/strict-nosourcecreate-create",
			rule:          RuleStrictNoSourceCreate,
			toggleName:    "no_source_create",
			writtenValue:  "create",
			safeValue:     "refuse",
			wantLine:      9,
			wantConstruct: `strict.no_source_create = "create"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadConfigDir(t, tc.dir)

			t.Run("pin unset: config governs, unchanged", func(t *testing.T) {
				issues := CheckContext(t.Context(), cfg)
				if len(issues) != 0 {
					t.Fatalf("CheckContext() with the pin unset = %v, want no issues", issues)
				}
			})

			t.Run("pin set: refused, naming both sides", func(t *testing.T) {
				t.Setenv(strict.EnvPin, "1")
				issues := CheckContext(t.Context(), cfg)

				var got []Issue
				for _, issue := range issues {
					if issue.Rule == tc.rule {
						got = append(got, issue)
					}
				}
				if len(got) != 1 {
					t.Fatalf("CheckContext() with the pin set reported %d %s issues, want exactly 1: %v", len(got), tc.rule, got)
				}
				issue := got[0]

				if issue.Construct != tc.wantConstruct {
					t.Errorf("Construct = %q, want %q", issue.Construct, tc.wantConstruct)
				}
				if got := issue.Subject.Start.Line; got != tc.wantLine {
					t.Errorf("Subject line = %d, want %d (the offending strict.%s line)", got, tc.wantLine, tc.toggleName)
				}
				for _, want := range []string{strict.EnvPin, tc.toggleName, `"` + tc.safeValue + `"`, `"` + tc.writtenValue + `"`} {
					if !strings.Contains(issue.Detail, want) {
						t.Errorf("Detail = %q, want it to contain %q", issue.Detail, want)
					}
				}
			})
		})
	}
}
