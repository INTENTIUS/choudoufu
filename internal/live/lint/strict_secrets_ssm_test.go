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
	t.Run("a complete arrangement raises nothing about the arrangement", func(t *testing.T) {
		// Since the write path landed (GitHub issue #2393) the clean
		// fixture is clean overall: neither this rule nor strict-secrets
		// says anything about it. TestSecretsSSMIsAccepted holds the second
		// half; this subtest holds that THIS rule, which is only ever about
		// the pieces around the setting, is silent when every piece is
		// there.
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
		// test is here because the five shapes share a rule: a detail
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
			name:      "an AWS managed key by its alias",
			dir:       "testdata/strict-secrets-ssm-aws-managed-key",
			construct: "strict.ssm.kms_key_id",
			wantDetail: []string{
				`kms_key_id "alias/aws/ssm" is an AWS managed key by its alias`,
				"customer managed key",
				"allow_insecure does not cover this",
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

// TestSecretsSSMIsAccepted is the lift GitHub issue #2393 made: a complete
// arrangement under secrets = "ssm" draws no strict-secrets refusal, and the
// five structural refusals still draw exactly the one strict-secrets-ssm
// issue each (TestStrictSecretsSSMArrangement). It replaced
// TestSecretsSSMIsRefusedAsUnimplemented, which held the refusal while
// nothing wrote a parameter.
//
// The setting stays opt-in: a strict block that omits secrets is not under
// "ssm", and an ssm block without the setting is still refused as the
// reverse mistake.
func TestSecretsSSMIsAccepted(t *testing.T) {
	if !strict.SecretsImplemented(strict.SSM) {
		t.Fatalf("strict.SecretsImplemented(%q) = false, so the setting is still refused", strict.SSM)
	}
	if strict.DefaultSecrets == strict.SSM {
		t.Fatalf("the default secrets setting is %q; it has to be written to be in force", strict.SSM)
	}

	for _, issue := range CheckContext(t.Context(), loadConfigDir(t, "testdata/strict-secrets-ssm-clean")) {
		if issue.Rule == RuleStrictSecrets || issue.Rule == RuleStrictSecretsSSM {
			t.Errorf("a complete secrets = \"ssm\" arrangement was refused: %s: %s", issue.Rule, issue.Detail)
		}
	}

	// The two incomplete arrangements draw the arrangement's refusal and
	// not a strict-secrets one: the setting is accepted, the shape is not.
	for _, dir := range []string{
		"testdata/strict-secrets-ssm-no-block",
		"testdata/strict-secrets-ssm-no-key",
	} {
		t.Run(dir, func(t *testing.T) {
			var arrangement int
			for _, issue := range CheckContext(t.Context(), loadConfigDir(t, dir)) {
				switch issue.Rule {
				case RuleStrictSecrets:
					t.Errorf("strict-secrets fired on a correctly spelled, implemented setting: %s", issue.Detail)
				case RuleStrictSecretsSSM:
					arrangement++
				}
			}
			if arrangement != 1 {
				t.Errorf("got %d %s issues, want 1", arrangement, RuleStrictSecretsSSM)
			}
		})
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

	detailWithPin := func(t *testing.T) string {
		t.Helper()
		var got []Issue
		for _, issue := range CheckContext(t.Context(), cfg) {
			if issue.Rule == RuleStrictSecrets {
				got = append(got, issue)
			}
		}
		if len(got) != 1 {
			t.Fatalf("CheckContext() reported %d %s issues, want exactly 1: %v", len(got), RuleStrictSecrets, got)
		}
		return got[0].Detail
	}

	t.Run("pin unset: accepted", func(t *testing.T) {
		for _, issue := range CheckContext(t.Context(), cfg) {
			if issue.Rule == RuleStrictSecrets {
				t.Errorf("refused with the pin unset: %s", issue.Detail)
			}
		}
	})

	t.Run("pin set: refused, naming both sides", func(t *testing.T) {
		t.Setenv(strict.EnvPin, "1")
		detail := detailWithPin(t)
		for _, want := range []string{strict.EnvPin, `secrets = "refuse"`, `secrets = "ssm"`} {
			if !strings.Contains(detail, want) {
				t.Errorf("Detail does not contain %q; got %q", want, detail)
			}
		}
		if strings.Contains(detail, "does not implement") {
			t.Errorf("the pin refusal reads as an unimplemented setting: %s", detail)
		}
	})
}
