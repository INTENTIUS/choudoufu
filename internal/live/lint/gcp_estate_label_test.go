// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// gcpEstateConfig is a root with a live block naming estate and the given
// resources.
func gcpEstateConfig(estate, resources string) string {
	return `
terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
  live {
    estate = "` + estate + `"
  }
}
` + resources
}

const gcpEstateBucket = `
resource "google_storage_bucket" "b" {
  name     = "b"
  location = "EU"
}
`

// TestGCPEstateLabel pins the boundary: an estate of 63 characters is a
// legal label value and is admitted, one of 64 is refused on every google
// block. The plan's node stamp refuses the same value
// (projection.stampedGoogleLabels), so the two must agree on it, and both
// read [markers.ValidGoogleLabelValue].
func TestGCPEstateLabel(t *testing.T) {
	at63 := "e" + strings.Repeat("x", 62)
	at64 := at63 + "x"
	if !markers.ValidGoogleLabelValue(at63) || markers.ValidGoogleLabelValue(at64) {
		t.Fatalf("the fixture's boundary is not GCP's: 63 valid=%v, 64 valid=%v", markers.ValidGoogleLabelValue(at63), markers.ValidGoogleLabelValue(at64))
	}

	for _, tc := range []struct {
		name   string
		src    string
		refuse []string
	}{
		{name: "63 characters fit", src: gcpEstateConfig(at63, gcpEstateBucket)},
		{name: "64 characters do not", src: gcpEstateConfig(at64, gcpEstateBucket), refuse: []string{"google_storage_bucket.b"}},
		{name: "no google resource, nothing to stamp", src: gcpEstateConfig(at64, "")},
		{
			name: "every google block is named",
			src: gcpEstateConfig(at64, gcpEstateBucket+`
resource "google_pubsub_topic" "t" {
  name = "t"
}
`),
			refuse: []string{"google_pubsub_topic.t", "google_storage_bucket.b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadGCPFiles(t, map[string]string{"main.tf": tc.src})
			got := onlyRule(CheckContext(t.Context(), cfg), RuleGCPEstateLabel)
			constructs := constructsFor(got, RuleGCPEstateLabel)
			if strings.Join(constructs, ",") != strings.Join(tc.refuse, ",") {
				t.Fatalf("refused %v, want %v", constructs, tc.refuse)
			}
			for _, issue := range got {
				if !strings.Contains(issue.Detail, "is not a legal GCP label value") {
					t.Errorf("Detail does not say why:\n%s", issue.Detail)
				}
			}
			if len(got) > 0 && !HasErrors(got) {
				t.Errorf("%s must refuse: the plan refuses the same block", RuleGCPEstateLabel)
			}
		})
	}
}

// TestGCPEstateLabelReadsTheSurfaceWhenSchemasArePresent: with schemas, a
// type the stamp never labels is not refused. The instance template's
// labels propagate to its VMs, so the stamp never writes the estate there.
func TestGCPEstateLabelReadsTheSurfaceWhenSchemasArePresent(t *testing.T) {
	src := gcpEstateConfig("e"+strings.Repeat("x", 63), gcpEstateBucket+`
resource "google_compute_instance_template" "t" {
  name = "t"
}
`)
	labelled := func(description string) providers.Schema {
		return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"name":             {Type: cty.String, Required: true},
			"location":         {Type: cty.String, Optional: true},
			"labels":           {Type: cty.Map(cty.String), Optional: true, Description: description},
			"effective_labels": {Type: cty.Map(cty.String), Computed: true},
			"terraform_labels": {Type: cty.Map(cty.String), Computed: true},
		}}}
	}
	schemas := map[string]providers.Schema{
		"google_compute_instance_template": labelled("A set of key/value label pairs to assign to instances created from this template."),
		"google_storage_bucket":            labelled("A set of key/value label pairs to assign to the bucket."),
	}
	cfg := loadGCPFiles(t, map[string]string{"main.tf": src})
	got := constructsFor(onlyRule(CheckWith(t.Context(), cfg, Context{Schemas: schemas}), RuleGCPEstateLabel), RuleGCPEstateLabel)
	if strings.Join(got, ",") != "google_storage_bucket.b" {
		t.Fatalf("want exactly google_storage_bucket.b refused, got %v", got)
	}
}

// TestGCPEstateLabelHonoursTheTargetScope is TestLintHonoursTheTargetScope's
// three arms for this rule, on a fixture of its own: see
// TestEveryScopedRuleIsInTheFixture for why it is not in the shared one.
func TestGCPEstateLabelHonoursTheTargetScope(t *testing.T) {
	src := gcpEstateConfig("e"+strings.Repeat("x", 63), `
resource "google_storage_bucket" "named_targeted" {
  name     = "t"
  location = "EU"
}
resource "google_storage_bucket" "named_excluded" {
  name     = "e"
  location = "EU"
}
`)
	cfg := loadGCPFiles(t, map[string]string{"main.tf": src})
	for _, arm := range []struct {
		name  string
		scope identity.Scope
		want  []string
	}{
		{"no scope", nil, []string{"google_storage_bucket.named_excluded", "google_storage_bucket.named_targeted"}},
		{"keeps the targeted half", scopeKeepingTargeted(), []string{"google_storage_bucket.named_targeted"}},
		{"keeps nothing", scopeKeepingNothing(), nil},
	} {
		t.Run(arm.name, func(t *testing.T) {
			got := constructsFor(onlyRule(CheckWith(t.Context(), cfg, Context{Scope: arm.scope}), RuleGCPEstateLabel), RuleGCPEstateLabel)
			if strings.Join(got, ",") != strings.Join(arm.want, ",") {
				t.Errorf("got %v, want %v", got, arm.want)
			}
		})
	}
}
