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
		// The clean fixture is not clean overall - the setting itself is
		// refused as not implemented yet, which is
		// TestSecretsSSMIsRefusedAsUnimplemented's subject. What this
		// subtest holds is that THIS rule, which is only ever about the
		// pieces around the setting, says nothing when every piece is
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

// TestSecretsSSMIsRefusedAsUnimplemented is the honest half of this unit:
// the grammar is here, the write path is not, and a configuration that asks
// for the setting is told so rather than run under it.
//
// Accepting it silently is the failure this test exists to prevent, and it
// is worse than the marker_repair case the same mechanism was built for. A
// silently-ignored "never" leaves tags being written, which a plan shows. A
// silently-ignored "ssm" leaves every secret value written into the estate's
// records in clear while the configuration says they are in Parameter Store
// under the operator's own key, and nothing anywhere would look wrong: there
// would be no parameter to notice was missing.
//
// The two assertions that matter when the write path lands: the refusal
// names #1515 and the settings that DO work, so it is actionable, and it is
// refused as unimplemented rather than as a typo, so an author is not told
// their correctly spelled setting is a misspelling.
func TestSecretsSSMIsRefusedAsUnimplemented(t *testing.T) {
	if !strict.SecretsValid(strict.SSM) {
		t.Fatal(`strict.SecretsValid("ssm") = false, so the setting would be refused as a typo, which it is not`)
	}
	if strict.SecretsImplemented(strict.SSM) {
		t.Skip("the write path landed; this test is the one that has to change on purpose")
	}

	for _, dir := range []string{
		"testdata/strict-secrets-ssm-clean",
		"testdata/strict-secrets-ssm-no-block",
		"testdata/strict-secrets-ssm-no-key",
	} {
		t.Run(dir, func(t *testing.T) {
			var got []Issue
			for _, issue := range CheckContext(t.Context(), loadConfigDir(t, dir)) {
				if issue.Rule == RuleStrictSecrets {
					got = append(got, issue)
				}
			}
			if len(got) != 1 {
				t.Fatalf("CheckContext() reported %d %s issues, want exactly 1: %v", len(got), RuleStrictSecrets, got)
			}
			detail := got[0].Detail
			for _, want := range []string{"#1515", "does not implement yet", `"refuse", "store"`, "in clear"} {
				if !strings.Contains(detail, want) {
					t.Errorf("Detail does not contain %q; got %q", want, detail)
				}
			}
			// The typo message's own words. An author who spelled the
			// setting correctly must not be told they did not.
			if strings.Contains(detail, "is not a secrets setting") {
				t.Errorf("a correctly spelled setting was refused as a typo: %q", detail)
			}
		})
	}

	// The control: a setting this build does implement is not refused by
	// the same branch. Without it, a predicate that answered false for
	// everything would pass every assertion above.
	for _, issue := range CheckContext(t.Context(), loadConfigDir(t, "testdata/strict-secrets-store")) {
		if issue.Rule == RuleStrictSecrets {
			t.Errorf(`secrets = "store" was refused: %s`, issue.Detail)
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

	t.Run("pin unset: the refusal is about the mechanism, not the pin", func(t *testing.T) {
		if detail := detailWithPin(t); strings.Contains(detail, strict.EnvPin) {
			t.Errorf("the pin is named with the pin unset: %s", detail)
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
		// The pin's refusal replaces the unimplemented one rather than
		// joining it. An operator running under a pinned profile is being
		// told their configuration may not relax it, which is true today
		// and stays true the day the write path lands; leading with
		// "not implemented yet" would read as though the pin were the
		// thing that might be lifted.
		if strings.Contains(detail, "does not implement yet") {
			t.Errorf("the pin refusal and the unimplemented refusal were combined into one message: %s", detail)
		}
	})
}
