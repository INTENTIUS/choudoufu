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
