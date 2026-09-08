// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// stampUnmarkedApplyRecordBackedFixture reuses
// internal/command/testdata/live-plan-stampgaps-unmarked-apply-950 - the
// exact fixture internal/command's own
// TestLivePlan_unmarkedApplyOfAMarkerOnlyResourceRefuses drives through a
// real plan - rather than a second copy under this package's own testdata:
// [TestIdentityGolden] sweeps every directory under internal/live and live
// (identitygolden_test.go's own identityGoldenRoots), and this repro's
// fixture must not add a new pinned row there. internal/command/testdata
// is outside both roots, so pointing Dir at it directly costs nothing.
const stampUnmarkedApplyRecordBackedFixture = "../../command/testdata/live-plan-stampgaps-unmarked-apply-950"

// stampUnmarkedApplyRecordBackedSchemas is
// live-plan-stampgaps-unmarked-apply-950/main.tf's aws_vpc, minus "tags" -
// a real provider schema shape (a type the hand-curated markerless table
// has never heard of, but with nowhere to write a marker anyway), the same
// caricature internal/command's own live_plan_test.go builds
// (statelessTestSchemasWithout) for the same fixture.
func stampUnmarkedApplyRecordBackedSchemas() map[string]providers.Schema {
	return map[string]providers.Schema{
		"aws_vpc": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":         {Type: cty.String, Computed: true},
				"cidr_block": {Type: cty.String, Optional: true},
				// Deliberately no "tags": that is the whole point.
			},
		}},
	}
}

// resolveStampUnmarkedApplyRecordBackedFixture loads
// [stampUnmarkedApplyRecordBackedFixture] and resolves it with
// [stampUnmarkedApplyRecordBackedSchemas], returning the real
// *identity.Result [NodeStampUnmarkedApply] needs. A real resolve rather
// than a hand-built Result: identity.Result's fields are unexported and
// resolution is where aws_vpc's ClassNeedsDiscovery classification and its
// DiscoveryCause actually come from.
func resolveStampUnmarkedApplyRecordBackedFixture(t *testing.T) (*configs.Config, *identity.Result) {
	t.Helper()

	schemas := stampUnmarkedApplyRecordBackedSchemas()
	report := Dir(t.Context(), stampUnmarkedApplyRecordBackedFixture, Context{Schemas: schemas})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}
	result, diags := identity.ResolveWith(t.Context(), report.Load.Config, identity.Context{Schemas: schemas})
	if diags.HasErrors() {
		t.Fatalf("resolving the fixture: %s", diags.Err())
	}
	if len(result.NeedsDiscovery()) != 1 {
		t.Fatalf("want exactly one needs-discovery instance, got %d: %v", len(result.NeedsDiscovery()), result.NeedsDiscovery())
	}
	return report.Load.Config, result
}

// hasSummary reports whether diags carries a diagnostic whose Summary is
// exactly summary.
func hasSummary(diags tfdiags.Diagnostics, summary string) bool {
	for _, d := range diags {
		if d.Description().Summary == summary {
			return true
		}
	}
	return false
}

// TestNodeStampUnmarkedApply_recordBackedInstanceIsExempt is GitHub issue
// #950's own regression guard on the exemption
// [statelessUnmarkedApplyGaps] (internal/command/live_plan.go) needs:
// #364's record store already holding an identity for a needs-discovery,
// untaggable instance must suppress this refusal, or the fix would refuse
// every estate #364 already made safe.
func TestNodeStampUnmarkedApply_recordBackedInstanceIsExempt(t *testing.T) {
	cfg, result := resolveStampUnmarkedApplyRecordBackedFixture(t)
	addr := result.NeedsDiscovery()[0].Addr.String()

	t.Run("not record-backed: refuses", func(t *testing.T) {
		diags := NodeStampUnmarkedApply(cfg, result, stampUnmarkedApplyRecordBackedSchemas(), "stampgaps-950", nil)
		if !diags.HasErrors() {
			t.Fatalf("want an error with no recordBacked entry; got none")
		}
		if !hasSummary(diags, stamp.SummaryUnmarkedApply) {
			t.Fatalf("want %q; got: %s", stamp.SummaryUnmarkedApply, diags.Err())
		}
	})

	t.Run("fully record-backed: exempt", func(t *testing.T) {
		diags := NodeStampUnmarkedApply(cfg, result, stampUnmarkedApplyRecordBackedSchemas(), "stampgaps-950", map[string]bool{addr: true})
		if diags.HasErrors() {
			t.Fatalf("%q fired on a fully record-backed instance; got: %s", stamp.SummaryUnmarkedApply, diags.Err())
		}
	})

	t.Run("a record for a different address does not exempt this one", func(t *testing.T) {
		diags := NodeStampUnmarkedApply(cfg, result, stampUnmarkedApplyRecordBackedSchemas(), "stampgaps-950", map[string]bool{"aws_vpc.someone_else": true})
		if !diags.HasErrors() {
			t.Fatalf("want an error; a record for a different address must not exempt %s", addr)
		}
		if !hasSummary(diags, stamp.SummaryUnmarkedApply) {
			t.Fatalf("want %q; got: %s", stamp.SummaryUnmarkedApply, diags.Err())
		}
	})
}
