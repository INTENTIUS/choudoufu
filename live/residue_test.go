// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import "testing"

// TestLookupCohorts is this package's own direct coverage of [Lookup],
// independent of the two consumers (tools/survey-gen's doc render and
// internal/live/lint's refusal path) that exercise it indirectly. One type
// per cohort, plus the no-cohort control.
func TestLookupCohorts(t *testing.T) {
	tests := []struct {
		tfType string
		want   Cohort
		wantOK bool
	}{
		{"aws_pinpoint_app", CohortDeprecated, true},
		{"aws_db_instance", CohortEmulatorBlocked, true},
		{"aws_codebuild_project", CohortRegistryLaggard, true},
		// aws_cloudformation_type: issue #53 family sweep A left this one
		// unclassified on purpose (via:"none") - it manages a CFN Registry
		// type VERSION, but the Registry itself splits that concept across
		// four different types depending on the extension kind, so no
		// single cfn_type/fold_parent is correct without further work.
		{"aws_cloudformation_type", CohortUnmapped, true},
		// aws_waf_rule_group: via:"deprecated-service" in live/mapping.json
		// itself (issue #53's mechanical classifier), reaching
		// CohortDeprecated the same way aws_pinpoint_app above does (via
		// deprecatedPrefixFor, checked before the mapping row is even read) -
		// covering both paths to the identical cohort.
		{"aws_waf_rule_group", CohortDeprecated, true},
		// aws_vpc_peering_connection_accepter: issue #53's tf-only
		// mechanical classifier (an accepter, corroborated by no identity
		// schema in the provider).
		{"aws_vpc_peering_connection_accepter", CohortTFOnly, true},
		{"aws_vpc", "", false},            // admitted, mapped, working handlers: in no cohort
		{"aws_s3_bucket", "", false},      // admitted, mapped, working handlers: in no cohort
		{"aws_ecr_repository", "", false}, // registry-ratified batch #2 (#26): left EmulatorBlocked, now in no cohort
		{"aws_no_such_type", "", false},   // not in mapping.json at all
	}
	for _, tt := range tests {
		cohort, sentence, ok := Lookup(tt.tfType)
		if ok != tt.wantOK {
			t.Errorf("Lookup(%q) ok = %v, want %v (cohort %q)", tt.tfType, ok, tt.wantOK, cohort)
			continue
		}
		if !ok {
			continue
		}
		if cohort != tt.want {
			t.Errorf("Lookup(%q) cohort = %q, want %q", tt.tfType, cohort, tt.want)
		}
		if sentence == "" {
			t.Errorf("Lookup(%q) returned ok=true with an empty sentence", tt.tfType)
		}
	}
}

// TestDeprecatedTotalMatchesPerServiceSum guards DeprecatedTotal against
// silently drifting from the sum its own per-service counts produce.
func TestDeprecatedTotalMatchesPerServiceSum(t *testing.T) {
	sum := 0
	for _, d := range DeprecatedServices {
		sum += DeprecatedCount(d)
	}
	if got := DeprecatedTotal(); got != sum {
		t.Errorf("DeprecatedTotal() = %d, sum of DeprecatedCount over DeprecatedServices = %d", got, sum)
	}
}

// TestUnmappedGroupsSumToTotal guards UnmappedGroups against silently
// dropping or double-counting a row relative to its own reported total.
func TestUnmappedGroupsSumToTotal(t *testing.T) {
	groups, total := UnmappedGroups()
	sum := 0
	for _, g := range groups {
		sum += g.Count
	}
	if sum != total {
		t.Errorf("UnmappedGroups groups sum to %d, total reports %d", sum, total)
	}
}

// TestTFOnlyGroupsSumToTotal is TestUnmappedGroupsSumToTotal's tf-only
// twin (issue #53).
func TestTFOnlyGroupsSumToTotal(t *testing.T) {
	groups, total := TFOnlyGroups()
	sum := 0
	for _, g := range groups {
		sum += g.Count
	}
	if sum != total {
		t.Errorf("TFOnlyGroups groups sum to %d, total reports %d", sum, total)
	}
	if total == 0 {
		t.Error("TFOnlyGroups reports 0 total; issue #53's mechanical classifier should have placed at least one row")
	}
}

// TestCFNUnmodeledGroupsSumToTotal is TestUnmappedGroupsSumToTotal's
// cfn-unmodeled twin (issue #53). Unlike TFOnlyGroups, a 0 total is
// expected today - see overlay.json's cfn_unmodeled table for why.
func TestCFNUnmodeledGroupsSumToTotal(t *testing.T) {
	groups, total := CFNUnmodeledGroups()
	sum := 0
	for _, g := range groups {
		sum += g.Count
	}
	if sum != total {
		t.Errorf("CFNUnmodeledGroups groups sum to %d, total reports %d", sum, total)
	}
}

// TestRegistryLaggardTypesExcludeDeprecated guards the documented exclusion
// (LIMITATIONS.md's "Registry-laggard live services" section): no type
// under a DeprecatedServices prefix appears in RegistryLaggardTypes, so the
// two cohorts' counts never double-count the same type.
func TestRegistryLaggardTypesExcludeDeprecated(t *testing.T) {
	for _, l := range RegistryLaggardTypes() {
		if _, deprecated := deprecatedPrefixFor(l.TFType); deprecated {
			t.Errorf("RegistryLaggardTypes includes %s, which is also under a deprecated-service prefix", l.TFType)
		}
	}
}
