// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestLintRefusalsCarryOwnershipClause is issue #1242's guard for this
// package's half: RuleOverlongAddress and RuleForEachKey - the latter on a
// resource's own for_each and on a module call's alike - say what an address
// is FOR through [markers.OwnershipClause] rather than in their own words.
// internal/live/identity's TestForEachKeyRefusalCarriesOwnershipClause is the
// same guard over the resolver's two branches, and carries the reasoning for
// why this is a positive assertion rather than a repo-wide phrase sweep.
//
// The third subtest is not decoration. #1242's whole shape is one sentence
// diverging across sites nobody was comparing, and the module-call path
// renders through the same reportBadForEachKeys as the resource path but with
// a different label, so it is the site a future edit is most likely to fork.
func TestLintRefusalsCarryOwnershipClause(t *testing.T) {
	// 1116 escaped characters against markers.MaxAddressLen's 1024: enough
	// to overrun the ceiling with room to spare if the budget ever widens
	// again, which it has once already (issue #71's continuation tags).
	long := strings.Repeat("k", 1100)

	cases := []struct {
		name  string
		rule  Rule
		body  string
		child bool
	}{
		{
			name: "overlong address",
			rule: RuleOverlongAddress,
			body: `resource "aws_subnet" "this" {
  for_each = toset(["` + long + `"])
}`,
		},
		{
			name: "for_each key on a resource",
			rule: RuleForEachKey,
			body: `resource "aws_subnet" "this" {
  for_each = toset(["50%full"])
}`,
		},
		{
			name: "for_each key on a module call",
			rule: RuleForEachKey,
			body: `module "wrapped" {
  source   = "./child"
  for_each = toset(["50%full"])
}`,
			child: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := "terraform {\n  live {\n    estate = \"clause-test\"\n  }\n}\n\n" + tc.body + "\n"
			if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
				t.Fatalf("writing fixture: %s", err)
			}
			if tc.child {
				childDir := filepath.Join(dir, "child")
				if err := os.Mkdir(childDir, 0o750); err != nil {
					t.Fatalf("creating child module dir: %s", err)
				}
				if err := os.WriteFile(filepath.Join(childDir, "main.tf"), []byte("resource \"aws_subnet\" \"c\" {}\n"), 0o600); err != nil {
					t.Fatalf("writing child fixture: %s", err)
				}
			}

			cfg := loadConfigDir(t, dir)
			var found int
			for _, iss := range CheckContext(context.Background(), cfg) {
				if iss.Rule != tc.rule {
					continue
				}
				found++
				if !strings.Contains(iss.Detail, markers.OwnershipClause) {
					t.Errorf("%s does not carry markers.OwnershipClause.\nwant to contain:\n%s\ngot:\n%s", tc.rule, markers.OwnershipClause, iss.Detail)
				}
				if strings.Contains(strings.ToLower(iss.Detail), "only record of ownership") {
					t.Errorf("%s still claims the marker is the only record of ownership (issue #1242); it is not, for a resource with nowhere to hang a tag:\n%s", tc.rule, iss.Detail)
				}
			}
			// A subtest that matched no issue would pass every assertion
			// above by not making one - the blind-scanner shape.
			if found != 1 {
				t.Fatalf("this fixture produced %d %s issues, want exactly 1; the assertions above measured nothing", found, tc.rule)
			}
		})
	}
}

// TestForEachKeyRefusalIsRungBlind is the measured half of #1242's ruling,
// and the reason [markers.OwnershipClause] has a second sentence.
//
// The premise the old wording rested on was that these rules only ever fire
// for a resource whose ownership really is carried by a marker. They do not.
// Neither checkForEachKeys nor checkOverlongAddresses filters
// mod.ManagedResources by type, so both fire for a type with nowhere to hang
// a tag, and for those the marker sentence's condition is not met at all:
//
//   - aws_acmpca_certificate is in identity.MarkerlessTypes - the provider
//     mints its identity and it has no tags argument - and with a
//     record_store declared it resolves ClassRecordLocated. No marker is ever
//     written for it.
//   - aws_iam_group_policy_attachment is admitted and untaggable: its
//     identity is {group}/{policy_arn}, re-derived from its own arguments on
//     every run, so it carries no marker either.
//
// Both are still refused, which is the behaviour this test pins: the ruling
// was to make the sentence true, not to narrow the rule. A change that made
// either of these stop refusing would turn a caught wedge into a silent one.
func TestForEachKeyRefusalIsRungBlind(t *testing.T) {
	for _, typ := range []string{"aws_acmpca_certificate", "aws_iam_group_policy_attachment"} {
		t.Run(typ, func(t *testing.T) {
			dir := t.TempDir()
			src := `terraform {
  live {
    estate = "rung-blind"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

resource "` + typ + `" "this" {
  for_each = toset(["50%full"])
}
`
			if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
				t.Fatalf("writing fixture: %s", err)
			}
			cfg := loadConfigDir(t, dir)
			var found bool
			for _, iss := range CheckContext(context.Background(), cfg) {
				if iss.Rule != RuleForEachKey {
					continue
				}
				found = true
				if !strings.Contains(iss.Detail, "The rule applies to every resource all the same") {
					t.Errorf("%s is refused by the for_each key rule but the diagnostic does not say why the rule reaches a resource with nowhere to hang a tag:\n%s", typ, iss.Detail)
				}
			}
			if !found {
				t.Fatalf("%s with a for_each key containing %q was not refused; the rule is meant to apply at the address, without consulting the resource's rung (issue #1242)", typ, "%")
			}
		})
	}
}
