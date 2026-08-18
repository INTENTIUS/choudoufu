// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestStampGate_UnknownSchemaIsNotRefused is GitHub issue #230's repro. AWS's
// schema failed to load while random's succeeded (a routine shape - 33 of
// 250 corpus entries show one provider failing while another succeeds).
// aws_instance's ClassNeedsDiscovery classification comes from
// table_generated.go, not from AWS's schema being present, so this run
// cannot tell whether aws_instance is taggable - it is unknown, not refused.
//
// Before the fix, this fabricated a hard "Unmarked apply of a marker-only
// resource" error on aws_instance.web because the old gate
// (len(actx.Schemas) > 0) went true the moment random_id's schema loaded,
// and stamp.Stamp's SkipNoSchema path escalated to an error because
// mustStamp still read true for aws_instance.
func TestStampGate_UnknownSchemaIsNotRefused(t *testing.T) {
	schemas := map[string]providers.Schema{
		"random_id": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"byte_length": {Type: cty.Number, Required: true},
				"id":          {Type: cty.String, Computed: true},
				"hex":         {Type: cty.String, Computed: true},
			},
		}},
		// aws_instance's own schema is deliberately absent: AWS's schema
		// acquisition is the thing that failed in this shape.
	}

	report := Dir(t.Context(), filepath.Join("testdata", "stamp-schema-gate"), Context{Schemas: schemas})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}
	if !report.Schemas {
		t.Fatal("report.Schemas is false; the repro needs at least one schema present (random_id's), matching issue #230's shape")
	}

	for _, f := range report.Findings {
		if f.Layer == LayerStamp && f.ID == stamp.SummaryUnmarkedApply {
			t.Fatalf("stamp fabricated a refusal on aws_instance.web with its schema unavailable: %v", f)
		}
	}
	for _, f := range report.Warnings {
		if f.Layer == LayerStamp && f.ID == stamp.SummaryUnmarkedApply {
			t.Fatalf("stamp fabricated a refusal (as a warning) on aws_instance.web with its schema unavailable: %v", f)
		}
	}

	// And the other way to get this wrong: reporting nothing at all. The
	// instrument's whole claim is that it says what it checked, so a
	// resource it could not check has to appear as a warning rather than
	// vanish into a pass.
	assertStampUnknownWarning(t, report, "aws_instance")
}

// assertStampUnknownWarning finds the "taggability unknown" warning stamping
// raises for a needs-discovery resource whose type schema this run has not
// got. See [stamp.SkipReason.Unknown].
func assertStampUnknownWarning(t *testing.T, report Report, typeName string) {
	t.Helper()

	for _, f := range report.Warnings {
		if f.Layer != LayerStamp || f.ID != stamp.SummaryNotStamped {
			continue
		}
		for _, site := range f.Sites {
			if strings.Contains(site.Detail, "The schema of "+typeName+" is not available") {
				return
			}
		}
	}
	t.Errorf("no stamp %q warning names %s's unreadable schema; unknown became silence: %v", stamp.SummaryNotStamped, typeName, report.Warnings)
}

// TestStampGate_NoSchemasAtAllIsNotRefused is the zero-schema case the old
// gate (len(actx.Schemas) > 0) handled by never calling stamp.Stamp at all.
// The gate is gone in both its forms now - [stamp.SkipReason.Unknown] holds
// the invariant inside stamp.Stamp instead - so this pins that a run with no
// schemas whatsoever still produces no fabricated stamp refusal, and still
// says out loud what it could not check.
func TestStampGate_NoSchemasAtAllIsNotRefused(t *testing.T) {
	report := Dir(t.Context(), filepath.Join("testdata", "stamp-schema-gate"), Context{})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}
	if report.Schemas {
		t.Fatal("report.Schemas is true with no schemas given")
	}

	for _, f := range report.Findings {
		if f.Layer == LayerStamp {
			t.Fatalf("stamp produced a finding with no schemas at all: %v", f)
		}
	}
	assertStampUnknownWarning(t, report, "aws_instance")
}

// TestStampGate_GenuinelyUntaggableTypeStillRefuses is the mirror check the
// #230 fix must not break: aws_cloudfront_origin_access_control is
// server-assigned and its real provider schema has no tags/tags_all
// argument at all - there is nowhere to carry a marker, and no static
// identity either, so an unmarked apply of it genuinely creates a resource
// this configuration can never find again. Gating per type must not
// silently swallow a real "untaggable" classification along with the
// "unknown" one issue #230 is about.
//
// This test used aws_cloudfront_cache_policy until issue #272: that type
// cleared #272's two-source content-match proof (its own "name" argument
// is proven unique by both the provider's docs and the CFN registry) and
// is no longer in MarkerlessTypes, so it stopped being an example of a
// type with NO escape from "untaggable, server-assigned, unreachable
// again". aws_cloudfront_origin_access_control is the issue's own worked
// NOT-proven negative case - its "name" carries no uniqueness claim from
// either source - so it stays vetoed and stays a valid example here.
//
// The refusal is the same; the LAYER moved, and this test is the record of
// why. Until #249 the block was admitted, resolved to ClassNeedsDiscovery,
// and refused at apply-planning time by internal/live/stamp's
// SummaryUnmarkedApply - which is a refusal delivered after admission has
// already said yes. The markerless retraction takes the row out of the
// admission table and internal/live/lint's RuleMarkerlessType refuses it up
// front, before resolution runs at all. That is the same fact stated one
// layer earlier, and it is stated to an author reading a findings list
// rather than to an operator watching an apply stop.
//
// What this test asserts, then, is that the refusal did not evaporate in the
// move. Three things have to hold together, and any one of them alone would
// pass while the mechanism was broken:
//
//   - the type is refused, at LayerLint with RuleMarkerlessType;
//   - the refusal is BLOCKING - ClassifyOnboarding puts the configuration on
//     language-blocked. A non-blocking finding here would be the failure the
//     retraction was held back for: the rows would leave the table, the
//     estates would climb the ladder, and nothing would have become
//     applyable;
//   - the refusal carries the same consequence sentence stamp used, so the
//     one fact keeps the one wording (#111).
//
// Stamp must NOT also fire. Not because a second refusal would be wrong, but
// because it cannot happen: with no table row, resolution never classifies
// the block and stamp is never asked. A stamp finding here would mean
// something re-admitted the type behind the veto - which is exactly what
// internal/live/lint's schema fallback would do if the veto were consulted
// after it instead of before.
func TestStampGate_GenuinelyUntaggableTypeStillRefuses(t *testing.T) {
	schemas := map[string]providers.Schema{
		"aws_cloudfront_origin_access_control": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":      {Type: cty.String, Computed: true},
				"name":    {Type: cty.String, Required: true},
				"min_ttl": {Type: cty.Number, Optional: true},
				// Deliberately no "tags" or "tags_all": the real schema has
				// none, which is the whole reason this type refuses.
			},
		}},
	}

	report := Dir(t.Context(), filepath.Join("testdata", "stamp-untaggable-with-schema"), Context{Schemas: schemas})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	var found bool
	for _, f := range report.Findings {
		if f.Layer == LayerStamp {
			t.Errorf("stamp refused a type the admission table no longer carries (%s/%s); "+
				"with no row there is nothing to resolve and nothing to stamp, so something admitted it "+
				"behind internal/live/lint's markerless veto", f.Layer, f.ID)
		}
		if f.Layer != LayerLint || f.ID != string(lint.RuleMarkerlessType) {
			continue
		}
		found = true
		if len(f.Sites) == 0 {
			t.Error("refusal has no sites")
		}
		for _, site := range f.Sites {
			if !strings.Contains(site.Detail, lint.UnfindableClause) {
				t.Errorf("the markerless refusal does not carry internal/live/lint.UnfindableClause, "+
					"the shared sentence internal/live/stamp says to an operator about the same fact:\n  %s", site.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("aws_cloudfront_origin_access_control, genuinely untaggable with its own schema present, was not refused: %v", findingIDs(report))
	}

	if got := ClassifyOnboarding(report.Readable(), refusalIDs(report.Findings)); got != OnboardingLanguageBlocked {
		t.Errorf("ClassifyOnboarding = %q, want %q: a configuration naming a type with nowhere to write a marker "+
			"is blocked, and a rung above this one would promise either that ratification is all that stands in "+
			"the way or that no configuration edit is needed - both false for a vetoed type",
			got, OnboardingLanguageBlocked)
	}
}
