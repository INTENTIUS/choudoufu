// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/dataread"
)

// These tests are #179 stage 1's live-check surface: the eligibility
// analysis runs (offline - Analyze makes no cloud calls, and neither does
// the analysis), an eligible site is re-homed as the data-read pass's own
// finding, and an ineligible one gets the class wording instead of the
// generic dynamic-value text.

func analyzeFixtureDir(t *testing.T, dir string) Report {
	t.Helper()
	report := Dir(context.Background(), filepath.Join("testdata", dir), Context{})
	if !report.Readable() {
		t.Fatalf("fixture %s did not load: %s", dir, report.Load.Diags.Error())
	}
	return report
}

// TestAnalyzeReportsEligibleDataReadAsItsOwnFinding: the site stops being a
// hard language refusal, appears under the data-read layer with the
// eligible-read ID, and the rung classifier puts the estate on the new
// rung.
func TestAnalyzeReportsEligibleDataReadAsItsOwnFinding(t *testing.T) {
	report := analyzeFixtureDir(t, "data-read-eligible")

	if len(report.Findings) != 1 {
		t.Fatalf("want exactly one finding, got %d: %+v", len(report.Findings), report.Findings)
	}
	f := report.Findings[0]
	if f.Layer != LayerDataread || f.ID != dataread.SummaryEligibleRead {
		t.Fatalf("finding is %s/%s, want %s/%s", f.Layer, f.ID, LayerDataread, dataread.SummaryEligibleRead)
	}
	if !f.Registered {
		t.Errorf("the eligible-read finding is not registered in the catalog")
	}
	if len(f.Sites) != 1 {
		t.Fatalf("want one site, got %d", len(f.Sites))
	}
	if detail := f.Sites[0].Detail; !strings.Contains(detail, "reading the data source from the provider before identity resolution") {
		t.Errorf("the site does not carry the eligible wording: %s", detail)
	}

	if got := ClassifyOnboarding(true, refusalIDs(report.Findings)); got != OnboardingDataReadEligible {
		t.Errorf("classified %q, want %q", got, OnboardingDataReadEligible)
	}
}

// TestAnalyzeReportsIneligibleDataReadWithClassWording: the generic
// dynamic-value text is replaced by the not-readable class sentence naming
// the managed dependency, and the estate stays language-blocked.
func TestAnalyzeReportsIneligibleDataReadWithClassWording(t *testing.T) {
	report := analyzeFixtureDir(t, "data-read-blocked")

	var found *Finding
	for i := range report.Findings {
		if report.Findings[i].Layer == LayerDataread && report.Findings[i].ID == dataread.SummaryNotReadable {
			found = &report.Findings[i]
		}
		if report.Findings[i].ID == "Dynamic value in static context" {
			for _, site := range report.Findings[i].Sites {
				if strings.Contains(site.Detail, "data.aws_route53_zone.of_instance") {
					t.Errorf("a classified data site kept the generic wording: %s", site.Detail)
				}
			}
		}
	}
	if found == nil {
		t.Fatalf("no %s/%s finding; findings: %+v", LayerDataread, dataread.SummaryNotReadable, report.Findings)
	}
	detail := found.Sites[0].Detail
	for _, part := range []string{"managed resource", "cannot be read before the plan"} {
		if !strings.Contains(detail, part) {
			t.Errorf("the class wording lacks %q: %s", part, detail)
		}
	}

	if got := ClassifyOnboarding(true, refusalIDs(report.Findings)); got != OnboardingLanguageBlocked {
		t.Errorf("classified %q, want %q", got, OnboardingLanguageBlocked)
	}
}
