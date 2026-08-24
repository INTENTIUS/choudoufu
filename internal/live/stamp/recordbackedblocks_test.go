// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is corpus-alb-complete/test_plan's second #364 wall, the
// stamping-pass half: [Request.RecordBackedBlocks] is what
// [stamper.mustStamp] consults, alongside [identity.DiscoveryCause.
// BindsByName], to decide whether an unstamped marker-only resource is
// "lost to every future run" (an error, SummaryUnmarkedApply) or has
// another handle (a warning). Before this existed, an UNTAGGABLE type whose
// identity a migration had already recorded still escalated to the hard
// error, because the only two things this decision consulted were the
// discovery cause and nothing else - GitHub issue #364's own write half
// (liveimport, projection.LocatedRecordFrom's ratified-Components
// fallback) had nowhere to make its record MATTER to this pass.

// twoUntaggableSource is untaggableSource (discoverycause_test.go) plus a
// second, distinct untaggable block - the boundary a single-block fixture
// cannot prove: a record for ONE block must not exempt the OTHER.
const twoUntaggableSource = `
resource "aws_route_table_association" "app" {
  subnet_id      = "subnet-1"
  route_table_id = "rtb-1"
}

resource "aws_route_table_association" "other" {
  subnet_id      = "subnet-2"
  route_table_id = "rtb-2"
}
`

// TestRecordBackedBlocksDowngradesAnUnstampedMarkerOnlyResource is the fix
// itself. aws_route_table_association has NO tags argument at all -
// [markers.RefusedTagSurface] answers false for it, the same as every
// genuinely untaggable AWS type - so stamp.go's own untaggable branch is
// silent whenever [stamper.mustStamp] is false (its own comment: "a type
// with no tag surface at all is silent, as it always has been... hundreds
// of AWS resources are in this case"). RecordBackedBlocks is the SAME
// exemption identity.DiscoveryCause.BindsByName already gives mustStamp,
// so its own block goes silent too - not merely downgraded to a warning -
// while the untouched sibling block still escalates to the hard error.
func TestRecordBackedBlocksDowngradesAnUnstampedMarkerOnlyResource(t *testing.T) {
	cfg := loadSource(t, twoUntaggableSource)
	_, diags := Stamp(t.Context(), Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		NeedsDiscovery: map[string]identity.BlockDiscovery{
			"aws_route_table_association.app":   {Cause: identity.DiscoveryCauseUnspecified},
			"aws_route_table_association.other": {Cause: identity.DiscoveryCauseUnspecified},
		},
		RecordBackedBlocks: map[string]bool{
			"aws_route_table_association.app": true,
			// "other" is deliberately absent: it must still escalate.
		},
	})

	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want exactly 1 (app's block is fully exempt - silent, not merely "+
			"downgraded - and only other's error remains): %s", len(diags), diags.ErrWithWarnings())
	}
	d := diags[0]
	if !strings.Contains(d.Description().Detail, "aws_route_table_association.other") {
		t.Fatalf("the one remaining diagnostic does not name aws_route_table_association.other - the boundary "+
			"this test exists for (a record for ONE block must not exempt another) was not exercised: %s", d.Description().Detail)
	}
	if d.Severity() != tfdiags.Error {
		t.Errorf("other's severity = %v, want an error - it is NOT in RecordBackedBlocks, so this must "+
			"escalate exactly as it always has", d.Severity())
	}
	if strings.Contains(d.Description().Detail, "aws_route_table_association.app") {
		t.Errorf("the remaining diagnostic also names app, which is in RecordBackedBlocks and should not "+
			"appear in it at all: %s", d.Description().Detail)
	}
}

// TestNilRecordBackedBlocksChangesNothing is the flag-off byte-identical
// guarantee at this package's own boundary: a nil RecordBackedBlocks (every
// caller before this field existed, and every flag-off caller after it)
// must still escalate a marker-only resource exactly as before - a nil
// map's lookup is always false, so mustStamp's new condition is a no-op.
func TestNilRecordBackedBlocksChangesNothing(t *testing.T) {
	cfg := loadSource(t, untaggableSource)
	_, diags := Stamp(t.Context(), Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		NeedsDiscovery: map[string]identity.BlockDiscovery{
			"aws_route_table_association.app": {Cause: identity.DiscoveryCauseUnspecified},
		},
		// RecordBackedBlocks left nil.
	})
	if len(diags) != 1 || diags[0].Severity() != tfdiags.Error {
		t.Fatalf("got %s, want exactly one error diagnostic (unchanged, flag-off behaviour)", diags.ErrWithWarnings())
	}
}
