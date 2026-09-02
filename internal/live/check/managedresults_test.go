// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// certResult is what a provider fills in during PlanResourceChange for the
// fixture's certificate: one validation option per domain, derived from
// domain_name and subject_alternative_names. tools/refusal-probe obtains this
// from the provider through projection.PlanInstances; the test supplies it
// directly, because what is under test here is the seam, not the provider.
func certResult() map[string]cty.Value {
	return map[string]cty.Value{
		"aws_acm_certificate.cert": cty.ObjectVal(map[string]cty.Value{
			"domain_name": cty.StringVal("example.com"),
			"domain_validation_options": cty.SetVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"domain_name":           cty.StringVal("example.com"),
				"resource_record_name":  cty.StringVal("_v.example.com."),
				"resource_record_type":  cty.StringVal("CNAME"),
				"resource_record_value": cty.StringVal("_w.acm-validations.aws."),
			})}),
		}),
	}
}

// TestAnalyzeWithoutManagedResultsStillRefuses is the control, and it runs
// first on purpose: it is what makes the next test mean anything. The seam
// must be the only reason the configuration stops being refused.
func TestAnalyzeWithoutManagedResultsStillRefuses(t *testing.T) {
	report := analyzeFixtureDir(t, "managed-result-foreach")

	if !report.Blocked() {
		t.Fatal("resolved with no managed results in hand; the for_each iterates an attribute " +
			"nothing in the configuration sets, so this must refuse")
	}
	if !hasFindingContaining(report, "for_each") {
		t.Fatalf("refused for some other reason: %+v", report.Findings)
	}
}

// TestAnalyzeForwardsManagedResultsToIdentity is the carrier. check.Analyze
// is deliberately provider-free - the caller holding a provider does the
// asking - so all this package owes is to pass the answers through. If it
// stops doing that, a whole family of configurations silently goes back to
// being refused for a value the caller had in hand.
func TestAnalyzeForwardsManagedResultsToIdentity(t *testing.T) {
	load := Load(context.Background(), filepath.Join("testdata", "managed-result-foreach"))
	if load.Config == nil {
		t.Fatalf("fixture did not load: %s", load.Diags.Error())
	}

	report := Analyze(context.Background(), load.Config, Context{ManagedResults: certResult()})

	if report.Blocked() {
		t.Fatalf("still refused with the certificate's planned values in hand: %+v", report.Findings)
	}
	// Assert on the rendered identity, never on the verdict alone: the
	// record's whole point is the marker it would write, and a pass that
	// resolved to the wrong string would satisfy Blocked() just as well.
	const want = "Z0423220__v.example.com._CNAME"
	var found bool
	for _, id := range report.Identities {
		if id.ImportID == want {
			found = true
		}
	}
	if !found {
		var got []string
		for _, id := range report.Identities {
			got = append(got, id.ImportID)
		}
		t.Errorf("no instance rendered %q; got %v", want, got)
	}
}

func hasFindingContaining(report Report, substr string) bool {
	for _, f := range report.Findings {
		if containsFold(f.ID, substr) {
			return true
		}
		for _, s := range f.Sites {
			if containsFold(s.Detail, substr) {
				return true
			}
		}
	}
	return false
}

func containsFold(hay, needle string) bool {
	return len(hay) >= len(needle) && (indexFold(hay, needle) >= 0)
}

func indexFold(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			a, b := hay[i+j], needle[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
