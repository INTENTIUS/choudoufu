// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"strings"
	"testing"

	residue "github.com/intentius/choudoufu/live"
)

// TestRefusalNamesDeprecatedServiceCohort, TestRefusalNamesUnmappedCohort,
// TestRefusalNamesRegistryLaggardCohort and
// TestRefusalNamesEmulatorBlockedCohort are the residue roster's second
// consumer (issue #49): one refusal-message test per cohort the refusal
// path can name. Each fixture's type is outside admittedTypesV0 on purpose,
// so CheckContext's unadmitted-type refusal fires, and each test holds the
// refusal's Detail to exactly the sentence live/residue.go's Lookup would
// produce for that type - not a hardcoded string, so a wording change to
// the cohort sentences moves both sides together instead of the test
// silently pinning stale prose.
//
// live/residue.go's CohortCFNOnly has no fifth test alongside these: no
// Terraform configuration can name a CloudFormation-only type, so that
// cohort never reaches a refusal at all. It is exercised on the doc side
// only, by tools/survey-gen's residue_test.go.
func TestRefusalNamesDeprecatedServiceCohort(t *testing.T) {
	assertRefusalNamesCohort(t, "testdata/residue-deprecated", "aws_pinpoint_app.app", residue.CohortDeprecated)
}

func TestRefusalNamesUnmappedCohort(t *testing.T) {
	assertRefusalNamesCohort(t, "testdata/residue-unmapped", "aws_cloudformation_type.example", residue.CohortUnmapped)
}

func TestRefusalNamesRegistryLaggardCohort(t *testing.T) {
	assertRefusalNamesCohort(t, "testdata/residue-laggard", "aws_codebuild_project.build", residue.CohortRegistryLaggard)
}

func TestRefusalNamesEmulatorBlockedCohort(t *testing.T) {
	assertRefusalNamesCohort(t, "testdata/residue-emulator", "aws_cloudfront_distribution.cdn", residue.CohortEmulatorBlocked)
}

// assertRefusalNamesCohort loads dir, runs CheckContext, and checks that
// the sole RuleUnadmittedType issue's Detail ends with exactly the
// sentence residue.Lookup produces for the fixture's resource type, and
// that Lookup itself reports the expected cohort.
func assertRefusalNamesCohort(t *testing.T, dir, construct string, wantCohort residue.Cohort) {
	t.Helper()

	resourceType := strings.SplitN(construct, ".", 2)[0]
	cohort, sentence, ok := residue.Lookup(resourceType)
	if !ok {
		t.Fatalf("residue.Lookup(%q) reports no cohort; the fixture no longer exercises %s", resourceType, wantCohort)
	}
	if cohort != wantCohort {
		t.Fatalf("residue.Lookup(%q) = %q, want %q; fixture and cohort data have drifted apart", resourceType, cohort, wantCohort)
	}

	cfg := loadConfigDir(t, dir)
	issues := CheckContext(t.Context(), cfg)
	if len(issues) != 1 || issues[0].Rule != RuleUnadmittedType {
		t.Fatalf("expected exactly one RuleUnadmittedType issue for %s, got %v", dir, issues)
	}
	if issues[0].Construct != construct {
		t.Errorf("issue named construct %q, want %q", issues[0].Construct, construct)
	}
	if !strings.HasSuffix(issues[0].Detail, sentence) {
		t.Errorf("refusal Detail does not end with the %s cohort sentence.\ngot:  %s\nwant suffix: %s", wantCohort, issues[0].Detail, sentence)
	}
}

// TestRefusalSilentForTypeInNoCohort holds the other half of issue #49's
// contract: a type in no exclusion cohort - the common case, since most
// unadmitted types are simply not wired yet rather than excluded by any
// rule - keeps exactly today's base refusal message, with nothing appended
// by [residue.Lookup]. aws_instance is the same example
// TestLimitationsDocAgainstSurvey pins as surveyed-but-unadmitted, and it
// is in no cohort: it has a real CFN counterpart with working handlers, in
// no deprecated service, and is not on the emulator-blocked roster.
func TestRefusalSilentForTypeInNoCohort(t *testing.T) {
	if _, _, ok := residue.Lookup("aws_instance"); ok {
		t.Fatal("aws_instance now resolves to a cohort; this test needs a type genuinely in none")
	}

	cfg := loadConfigDir(t, "testdata/unadmitted")
	issues := CheckContext(t.Context(), cfg)
	if len(issues) != 1 || issues[0].Rule != RuleUnadmittedType {
		t.Fatalf("expected exactly one RuleUnadmittedType issue, got %v", issues)
	}
	if !strings.HasSuffix(issues[0].Detail, "provider identity schemas") {
		t.Errorf("a type in no cohort should end with the base table sentence unchanged, got: %s", issues[0].Detail)
	}
}
