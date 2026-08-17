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
	"github.com/intentius/choudoufu/internal/live/identity"
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
// server-assigned and its real provider schema has no tags/tags_all argument
// at all - there is nowhere to carry a marker, and no static identity
// either, so an unmarked apply of it genuinely creates a resource this
// configuration can never find again. Gating per type must not silently
// swallow a real "untaggable" classification along with the "unknown" one
// issue #230 is about.
//
// The type here used to be aws_cloudfront_cache_policy, and swapping it is
// issue #272's own negative case rather than housekeeping. Both types are
// untaggable, server-assigned, in one service, listed by Cloud Control the
// same way, and reached by the same estates. What separates them is only the
// WORDING of two AWS-authored texts: the cache policy's argument reference
// and CloudFormation schema both call its name unique, and the origin access
// control's call it neither - its create error names "the specified
// parameters" rather than the name, which suggests the dedup key is the
// whole tuple. So the cache policy became findable and this one did not.
//
// Keeping the fixture on the type that did NOT clear the bar is what makes
// this test a pin on the bar itself. Widen the uniqueness rule far enough to
// let a bare "A name to identify ..." through and this test goes red,
// because the type it names would become admissible.
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
				"id":                                {Type: cty.String, Computed: true},
				"name":                              {Type: cty.String, Required: true},
				"origin_access_control_origin_type": {Type: cty.String, Required: true},
				"signing_behavior":                  {Type: cty.String, Required: true},
				"signing_protocol":                  {Type: cty.String, Required: true},
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

// TestStampGate_UntaggableTypeUnderARecordStoreIsAdmitted is the other side
// of the split the test above now guards, and GitHub issue #270's claim
// measured through the same instrument tools/refusal-probe uses.
//
// Same type, same schema, one block different: a record_store in the live
// block. The type has nowhere to write a marker either way - that is a fact
// about the MARKER, which answers "may I delete this". What the store
// supplies is the other question, "which object is this", and for an object
// choudoufu created it can, because choudoufu minted the ID.
//
// Four things are asserted, and the last two are the ones that would
// otherwise let a bad fix look like a good one:
//
//   - the markerless refusal is gone, which is the change;
//   - resolution classifies the instance RECORD_LOCATED rather than
//     resolving it some other way;
//   - stamp still does not fire. A located instance is not
//     ClassNeedsDiscovery, so it is not in Result.DiscoveryCausesByBlock,
//     so internal/live/stamp's mustStamp is false and the untaggable skip
//     stays silent. If that chain broke, the run would lint clean and then
//     stop at APPLY - a plan refusal traded for an apply refusal, which is
//     exactly the trade this mechanism is forbidden to make;
//   - the configuration is no longer language-blocked by this rule.
func TestStampGate_UntaggableTypeUnderARecordStoreIsAdmitted(t *testing.T) {
	const typeName = "aws_cloudfront_cache_policy"
	schemas := map[string]providers.Schema{
		typeName: {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":      {Type: cty.String, Computed: true},
				"name":    {Type: cty.String, Required: true},
				"min_ttl": {Type: cty.Number, Optional: true},
				// Still no tags: the type is as markerless as it ever was.
			},
		}},
	}
	if _, markerless := identity.MarkerlessTypes[typeName]; !markerless {
		t.Fatalf("%s left identity.MarkerlessTypes, so this test no longer exercises the located path", typeName)
	}

	report := Dir(t.Context(), filepath.Join("testdata", "stamp-untaggable-record-located"), Context{Schemas: schemas})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	for _, f := range report.Findings {
		switch {
		case f.Layer == LayerLint && f.ID == string(lint.RuleMarkerlessType):
			t.Errorf("the markerless refusal still fires under a record_store. A marker answers \"may I delete this\"; an identity answers \"which object is this\", and the store supplies the second.\n  %v", f.Sites)
		case f.Layer == LayerLint && f.ID == string(lint.RuleUnadmittedType):
			t.Errorf("the type fell through to unadmitted-type instead of being located:\n  %v", f.Sites)
		case f.Layer == LayerStamp:
			t.Errorf("stamp fired on a record-located type (%s/%s). A located instance has no marker to write and must never be asked for one, or the run lints clean and fails at apply.", f.Layer, f.ID)
		}
	}

	var located int
	for _, res := range report.Identities {
		if res.Class == identity.ClassRecordLocated {
			located++
		}
	}
	if located != 1 {
		t.Errorf("resolution produced %d RECORD_LOCATED instances, want 1; classes were %v", located, classesOf(report))
	}

	if got := ClassifyOnboarding(report.Readable(), refusalIDs(report.Findings)); got == OnboardingLanguageBlocked {
		t.Errorf("ClassifyOnboarding = %q: the one refusal that put this configuration there is gone", got)
	}
}

// classesOf renders the identity classes a report resolved, for a failure
// message that says what happened instead of what did not.
func classesOf(report Report) map[identity.Class]int {
	out := map[identity.Class]int{}
	for _, res := range report.Identities {
		out[res.Class]++
	}
	return out
}
