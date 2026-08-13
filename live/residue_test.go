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
		{"aws_ecr_repository", CohortEmulatorBlocked, true},
		{"aws_codebuild_project", CohortRegistryLaggard, true},
		{"aws_accessanalyzer_archive_rule", CohortUnmapped, true},
		{"aws_vpc", "", false},          // admitted, mapped, working handlers: in no cohort
		{"aws_s3_bucket", "", false},    // admitted, mapped, working handlers: in no cohort
		{"aws_no_such_type", "", false}, // not in mapping.json at all
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
