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

// TestStrictSecretsSSMArrangement is GitHub issue #1515's configuration
// surface, measured on the one thing a lint rule can be measured on: which
// configurations it refuses and which it lets through.
//
// Every refusal fixture is testdata/strict-secrets-ssm-clean with exactly
// one thing removed or changed, and the clean case is a subtest here rather
// than a note, so a rule that fired on everything would fail on the control
// before it passed on the cases.
func TestStrictSecretsSSMArrangement(t *testing.T) {
	t.Run("the whole arrangement lints clean", func(t *testing.T) {
		issues := CheckContext(t.Context(), loadConfigDir(t, "testdata/strict-secrets-ssm-clean"))
		for _, issue := range issues {
			if issue.Rule == RuleStrictSecretsSSM {
				t.Errorf("a complete arrangement was refused: %s", issue.Detail)
			}
		}
	})

	for _, tc := range []struct {
		name      string
		dir       string
		construct string
		// wantDetail is what the refusal has to SAY, not merely that it
		// fired. Each entry is the fact an operator needs to act, and the
		// test is here because the four shapes share a rule: a detail
		// written for one of them appearing under another is a defect this
		// rule's own count cannot see.
		wantDetail []string
	}{
		{
			name:      "the setting with no ssm block",
			dir:       "testdata/strict-secrets-ssm-no-block",
			construct: `strict.secrets = "ssm"`,
			wantDetail: []string{
				"kms_key_id",
				"alias/aws/ssm",
				"ssm:GetParameter",
			},
		},
		{
			name:      "an ssm block with no kms_key_id",
			dir:       "testdata/strict-secrets-ssm-no-key",
			construct: "strict.ssm",
			wantDetail: []string{
				"kms_key_id",
				"alias/aws/ssm",
			},
		},
		{
			name:      "a declared record store that is not the bucket",
			dir:       "testdata/strict-secrets-ssm-local-store",
			construct: `strict.secrets = "ssm"`,
			wantDetail: []string{
				`record_store "s3"`,
				`record_store "local"`,
				"conditional write",
			},
		},
		{
			name:      "no record store at all",
			dir:       "testdata/strict-secrets-ssm-implied-store",
			construct: `strict.secrets = "ssm"`,
			wantDetail: []string{
				`record_store "s3"`,
				"implied local",
				"compare-and-swap",
			},
		},
		{
			name:      "an ssm block under a setting that would never read it",
			dir:       "testdata/strict-secrets-ssm-block-without-setting",
			construct: "strict.ssm",
			wantDetail: []string{
				`omitted, which means "store"`,
				"in clear",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []Issue
			for _, issue := range CheckContext(t.Context(), loadConfigDir(t, tc.dir)) {
				if issue.Rule == RuleStrictSecretsSSM {
					got = append(got, issue)
				}
			}
			if len(got) != 1 {
				t.Fatalf("CheckContext() reported %d %s issues, want exactly 1: %v", len(got), RuleStrictSecretsSSM, got)
			}
			issue := got[0]
			if issue.Construct != tc.construct {
				t.Errorf("Construct = %q, want %q", issue.Construct, tc.construct)
			}
			// A refusal with no source range leaves an operator to find
			// the line themselves, in a block whose every line looks like
			// every other.
			if issue.Subject.Filename == "" {
				t.Error("Subject is the zero range, so nothing points at the line to change")
			}
			for _, want := range tc.wantDetail {
				if !strings.Contains(issue.Detail, want) {
					t.Errorf("Detail does not contain %q; got %q", want, issue.Detail)
				}
			}
		})
	}
}

// TestStrictSecretsSSMIsNotATypo separates this rule from the one beside
// it. "ssm" is in the vocabulary now, so [RuleStrictSecrets] - whose whole
// content is "that spelling means nothing here" - must stay silent about
// it, and the refusals above must be the only thing a complete-but-for-one-
// thing arrangement gets. A vocabulary that had not been widened would
// refuse the clean fixture twice and neither refusal would be this rule's.
func TestStrictSecretsSSMIsNotATypo(t *testing.T) {
	if !strict.SecretsValid(strict.SSM) {
		t.Fatal(`strict.SecretsValid("ssm") = false, so the configuration surface below is refused as a typo before it is read`)
	}
	for _, dir := range []string{
		"testdata/strict-secrets-ssm-clean",
		"testdata/strict-secrets-ssm-no-block",
		"testdata/strict-secrets-ssm-no-key",
	} {
		for _, issue := range CheckContext(t.Context(), loadConfigDir(t, dir)) {
			if issue.Rule == RuleStrictSecrets {
				t.Errorf("%s: %s fired on a setting this schema defines: %s", dir, RuleStrictSecrets, issue.Detail)
			}
		}
	}
}

// TestStrictSecretsSSMIsRefusedUnderThePin is ruling 2's second half:
// CHOUDOUFU_STRICT_PIN pins this setting the way it pins the other two
// values. "ssm" keeps secret material, so it relaxes a pin that forces
// "refuse", and the refusal is [strict.PinRefusal]'s generic one - nothing
// about this setting needed a special case, which is the thing worth
// pinning by test.
//
// The clean fixture is used deliberately: a configuration with a perfect
// arrangement, refused for a reason that has nothing to do with the
// arrangement.
func TestStrictSecretsSSMIsRefusedUnderThePin(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/strict-secrets-ssm-clean")

	t.Run("pin unset: the configuration governs", func(t *testing.T) {
		for _, issue := range CheckContext(t.Context(), cfg) {
			if issue.Rule == RuleStrictSecrets {
				t.Errorf("refused with the pin unset: %s", issue.Detail)
			}
		}
	})

	t.Run("pin set: refused, naming both sides", func(t *testing.T) {
		t.Setenv(strict.EnvPin, "1")
		var got []Issue
		for _, issue := range CheckContext(t.Context(), cfg) {
			if issue.Rule == RuleStrictSecrets {
				got = append(got, issue)
			}
		}
		if len(got) != 1 {
			t.Fatalf("CheckContext() with the pin set reported %d %s issues, want exactly 1: %v", len(got), RuleStrictSecrets, got)
		}
		for _, want := range []string{strict.EnvPin, `secrets = "refuse"`, `secrets = "ssm"`} {
			if !strings.Contains(got[0].Detail, want) {
				t.Errorf("Detail does not contain %q; got %q", want, got[0].Detail)
			}
		}
	})
}
