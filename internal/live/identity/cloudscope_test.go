// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"strings"
	"testing"
)

// TestCloudScopeRegionArgument covers issue #200: two resources with the
// same import identity string are only a genuine collision when they also
// target the same account and region. A resource-level `region` argument
// (govuk-infrastructure's chat estate: two aws_cloudwatch_log_group blocks
// both named "/aws/bedrock", one region = "eu-west-1" and one
// region = "eu-west-2") must resolve cleanly, not refuse as "Two resources
// with the same identity" - but a genuine same-name, same-region pair must
// still refuse.
func TestCloudScopeRegionArgument(t *testing.T) {
	cfg := loadConfig(t, "testdata/cloud-scope-region", nil)
	result, diags := Resolve(context.Background(), cfg)

	dublin := resolutionAt(t, result, "aws_cloudwatch_log_group.dublin")
	london := resolutionAt(t, result, "aws_cloudwatch_log_group.london")
	if dublin.Class != ClassConcrete || london.Class != ClassConcrete {
		t.Fatalf("dublin=%s london=%s; both have a literal name and should resolve", dublin.Class, london.Class)
	}
	if dublin.ImportID != london.ImportID {
		t.Fatalf("dublin=%q london=%q; both should carry the same bare import ID - only the region tells them apart", dublin.ImportID, london.ImportID)
	}

	foundLondonCollision := false
	foundDublinAgainCollision := false
	for _, d := range diags {
		if d.Description().Summary != "Two resources with the same identity" {
			continue
		}
		detail := d.Description().Detail
		if strings.Contains(detail, "london") {
			foundLondonCollision = true
		}
		if strings.Contains(detail, "dublin_again") {
			foundDublinAgainCollision = true
		}
	}
	if foundLondonCollision {
		t.Fatalf("dublin and london both refused as a duplicate identity, but they target different regions: %v", diags)
	}
	if !foundDublinAgainCollision {
		t.Fatalf("expected a %q diagnostic naming dublin_again (same name, same region as dublin), got: %v", "Two resources with the same identity", diags)
	}
}

// TestCloudScopeRegionInheritedFromProvider covers issue #217: a
// [resolver.resourceCloudScope] regression introduced while fixing #200. The
// region component came only from a literal `region` argument on the
// resource body, and [providerscope.ResolveResource]'s own address string
// never encodes region - so two resources with the same identity, the same
// account and the same EFFECTIVE region collided before #200's own commit
// but stopped colliding after it, the moment one of them spelled the region
// out explicitly and the other inherited the identical region from the
// enclosing `provider "aws"` block. "explicit" and "inherited" below are
// exactly that pair; "elsewhere" inherits the same provider block but names
// a different log group, so it must not collide with either.
func TestCloudScopeRegionInheritedFromProvider(t *testing.T) {
	cfg := loadConfig(t, "testdata/cloud-scope-region-inherited", nil)
	result, diags := Resolve(context.Background(), cfg)

	explicit := resolutionAt(t, result, "aws_cloudwatch_log_group.explicit")
	inherited := resolutionAt(t, result, "aws_cloudwatch_log_group.inherited")
	if explicit.Class != ClassConcrete || inherited.Class != ClassConcrete {
		t.Fatalf("explicit=%s inherited=%s; both have a literal name and should resolve", explicit.Class, inherited.Class)
	}
	if explicit.ImportID != inherited.ImportID {
		t.Fatalf("explicit=%q inherited=%q; both should carry the same bare import ID", explicit.ImportID, inherited.ImportID)
	}

	var foundCollision, foundElsewhere bool
	for _, d := range diags {
		if d.Description().Summary != "Two resources with the same identity" {
			continue
		}
		detail := d.Description().Detail
		if strings.Contains(detail, "explicit") && strings.Contains(detail, "inherited") {
			foundCollision = true
		}
		if strings.Contains(detail, "elsewhere") {
			foundElsewhere = true
		}
	}
	if !foundCollision {
		t.Fatalf("expected a %q diagnostic between explicit (region stated) and inherited (region inherited from the same provider block, same effective eu-west-1) - #217, got: %v", "Two resources with the same identity", diags)
	}
	if foundElsewhere {
		t.Fatalf("elsewhere collided with something, but it names a different log group (/aws/other, not /aws/bedrock): %v", diags)
	}
}

// TestCloudScopeUnknownRegionCollides is #217's own stated safety
// direction, one step past the asymmetric-spelling case above: "known"
// states an explicit region; "unknown" states none, and neither does its
// provider block, so this run has no way to determine unknown's effective
// region at all - not merely a different one from known's. An unknown
// region must act as a wildcard against a known one, the same way it
// already has to act as a wildcard against another unknown one (the
// ordinary single-region estate, where cloudScopeKey.region is never
// determined for anything and the check reduces to bare Type+identity) -
// never as evidence the two resources live in different places.
func TestCloudScopeUnknownRegionCollides(t *testing.T) {
	cfg := loadConfig(t, "testdata/cloud-scope-region-unknown", nil)
	result, diags := Resolve(context.Background(), cfg)

	known := resolutionAt(t, result, "aws_cloudwatch_log_group.known")
	unknown := resolutionAt(t, result, "aws_cloudwatch_log_group.unknown")
	if known.Class != ClassConcrete || unknown.Class != ClassConcrete {
		t.Fatalf("known=%s unknown=%s; both have a literal name and should resolve", known.Class, unknown.Class)
	}

	var found bool
	for _, d := range diags {
		if d.Description().Summary != "Two resources with the same identity" {
			continue
		}
		detail := d.Description().Detail
		// ".known" and ".unknown" (with the leading dot) are the
		// resource-address spellings, chosen deliberately over the bare
		// "known"/"unknown" because "unknown" itself contains "known" as a
		// substring ("un-known") - a bare check would pass on either
		// diagnostic naming "unknown" alone.
		if strings.Contains(detail, ".known") && strings.Contains(detail, ".unknown") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a %q diagnostic between known (region=us-east-1) and unknown (no determinable region) - #217's own safety direction, got: %v", "Two resources with the same identity", diags)
	}
}
