// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/providers"
)

// awsInstanceTaggableSchema is the minimal taggable schema
// [nodeStampMarkerConflicts]'s fixtures need: an "id" the provider assigns,
// two ordinary optional arguments, and a settable tags map -
// [markers.TagSurface]'s whole test.
func awsInstanceTaggableSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":            {Type: cty.String, Computed: true},
			"ami":           {Type: cty.String, Optional: true},
			"instance_type": {Type: cty.String, Optional: true},
			"tags":          {Type: cty.Map(cty.String), Optional: true},
		},
	}}
}

// This file's fixtures use [writeFixture] (declaredscan_test.go), a
// t.TempDir()-backed helper, rather than a committed testdata directory.
// TestIdentityGolden (identitygolden_test.go) sweeps and pins every
// configuration directory under internal/live and live; a committed
// fixture here would need that golden regenerated for two ADDED lines
// asserting nothing this test cares about (identity CLASS, not the
// ownership-marker tags this file's tests are actually about, which the
// golden does not render). t.TempDir() keeps these fixtures out of that
// sweep entirely.

// TestNodeStampMarkerConflict_hardcodedEstateDisagrees is GitHub issue
// #454's pin on [nodeStampMarkerConflicts]: a resource that hardcodes its
// own tofu-estate tag to a value this run would not write must still be
// refused, exactly as [stamp.Stamp]'s verify/verifyValue pair always
// refused it - now via [projection.NodeResolver.AdjustConfigValue]
// (GitHub issue #451) instead of a second, bespoke comparison.
//
// The fixture declares TWO resources with two DIFFERENT hardcoded
// tofu-estate values on purpose: [estateForStamp] (stamp.go, unchanged by
// this port) derives the estate to check against from the configuration's
// own declared tofu-estate tags when there is exactly one, which would
// make a single hardcoded value trivially agree with itself. Two
// disagreeing declared values fall through to the synthetic placeholder
// estate instead (declaredEstateNames returns more than one, so
// estateForStamp cannot pick either), and BOTH resources then hardcode a
// value that disagrees with it - the same shape a real user hits when
// their configuration already carries someone else's estate name by
// hand, or a stale one.
func TestNodeStampMarkerConflict_hardcodedEstateDisagrees(t *testing.T) {
	schemas := map[string]providers.Schema{
		"aws_instance": awsInstanceTaggableSchema(),
	}

	dir := writeFixture(t, `
resource "aws_instance" "a" {
  ami           = "ami-0123456789abcdef0"
  instance_type = "t3.micro"

  tags = {
    "tofu-estate" = "estate-a"
  }
}

resource "aws_instance" "b" {
  ami           = "ami-0123456789abcdef0"
  instance_type = "t3.micro"

  tags = {
    "tofu-estate" = "estate-b"
  }
}
`)
	report := Dir(t.Context(), dir, Context{Schemas: schemas})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	var got []string
	for _, f := range report.Findings {
		if f.Layer != LayerStamp || f.ID != stamp.SummaryMarkerConflict {
			continue
		}
		for _, site := range f.Sites {
			got = append(got, site.Detail)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d %q findings, want 2 (one per resource): %v\nall findings: %v", len(got), stamp.SummaryMarkerConflict, got, findingIDs(report))
	}
	for _, detail := range got {
		if !strings.Contains(detail, "tofu-estate") {
			t.Errorf("conflict detail does not mention tofu-estate: %s", detail)
		}
	}

	if got := ClassifyOnboarding(report.Readable(), refusalIDs(report.Findings)); got != OnboardingLanguageBlocked {
		t.Errorf("ClassifyOnboarding = %q, want %q: a hardcoded ownership-marker conflict is a hard stop, the same as it always was under stamp.Stamp", got, OnboardingLanguageBlocked)
	}
}

// TestNodeStampMarkerConflict_agreeingValueIsNotAConflict is the negative
// case: two resources that both hardcode the SAME tofu-estate value must
// not be flagged, whether that value happens to equal what estateForStamp
// derives (the ordinary case here, since a single declared value becomes
// the estate) or not. A false positive here would refuse configurations
// that already correctly declare their own markers by hand - exactly the
// migrated-estate population #454's ruling is about protecting.
func TestNodeStampMarkerConflict_agreeingValueIsNotAConflict(t *testing.T) {
	schemas := map[string]providers.Schema{
		"aws_instance": awsInstanceTaggableSchema(),
	}

	dir := writeFixture(t, `
resource "aws_instance" "a" {
  ami           = "ami-0123456789abcdef0"
  instance_type = "t3.micro"

  tags = {
    "tofu-estate" = "nodestamp-agree"
  }
}

resource "aws_instance" "b" {
  ami           = "ami-0123456789abcdef0"
  instance_type = "t3.micro"

  tags = {
    "tofu-estate" = "nodestamp-agree"
  }
}
`)
	report := Dir(t.Context(), dir, Context{Schemas: schemas})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	for _, f := range report.Findings {
		if f.Layer == LayerStamp {
			t.Errorf("stamp fired on a configuration whose hand-written markers agree with what this run would write: %s: %v", f.ID, f.Sites)
		}
	}
	for _, f := range report.Warnings {
		if f.Layer == LayerStamp {
			t.Errorf("stamp warned on a configuration whose hand-written markers agree with what this run would write: %s: %v", f.ID, f.Sites)
		}
	}
}
