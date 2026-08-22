// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #365, slice 2: the `markers "record"` selection, asserted the
// way [TestIdentityGolden] asserts everything else - by the VALUE each
// instance renders, not by whether anything refused.
//
// HANDOFF.md is explicit about why this is the test that has to exist for a
// change to identity resolution: "convergence is never evidence an identity
// is right: assert the rendered identity by value". A selection that routed
// the wrong instance, or routed the right one to a class carrying a
// fabricated identity, raises no finding, moves no count, and reads as a
// success in every other instrument this repository has.
//
// The golden itself cannot cover this. It sweeps with NO provider schemas
// (see identityGoldenAnalyze), and the selection deliberately fails closed
// without them - which is its own claim, pinned by
// TestStrictMarkersRecordFailsClosedWithNoSchemas below.

// strictMarkersSchemas is the two types the fixture declares, described the
// way a provider serves them, with only what the predicates under test
// actually read.
//
// Both carry a settable tags map, so markers.Taggable is true for each: the
// whole point of the selection is that these types CAN carry a marker and
// the operator has chosen that they will not. Both carry a top-level string
// "id", which is what identity.LocatedIdentityPlanFor reads. Neither carries
// an identity schema, which puts the plan on its documented-import branch -
// and neither type is in identity.IDNotProvenWholeTypes, so `id` stands as
// the whole identity for both.
//
// No Sensitive attribute anywhere, deliberately: the sensitive-identity
// refusal has its own test, and a stray sensitive attribute here would make
// this fixture prove the wrong thing.
func strictMarkersSchemas() map[string]providers.Schema {
	block := func(extra map[string]*configschema.Attribute) providers.Schema {
		attrs := map[string]*configschema.Attribute{
			"id":       {Type: cty.String, Computed: true},
			"tags":     {Type: cty.Map(cty.String), Optional: true},
			"tags_all": {Type: cty.Map(cty.String), Computed: true},
		}
		for k, v := range extra {
			attrs[k] = v
		}
		return providers.Schema{Block: &configschema.Block{Attributes: attrs}}
	}
	return map[string]providers.Schema{
		"aws_ebs_volume": block(map[string]*configschema.Attribute{
			"availability_zone": {Type: cty.String, Required: true},
			"size":              {Type: cty.Number, Optional: true},
		}),
		"aws_vpc": block(map[string]*configschema.Attribute{
			"cidr_block": {Type: cty.String, Optional: true},
		}),
	}
}

// TestStrictMarkersRecordRendersItsIdentityByValue is the safety-rule test
// for this slice.
//
// Both instances are asserted, in the identity golden's own column order and
// spelling, because the claim has two halves and a test that checked only
// the first would pass on a selection that swallowed the whole
// configuration:
//
//   - the SELECTED instance resolves to RECORD_LOCATED with no rendered
//     identity and no identity attributes. Empty is the right value here and
//     is not a gap: a located instance's identity is not in the
//     configuration at all, it is the string the record store holds, which
//     is why renderedIdentity returns "" for every class but the two that
//     claim to know an answer. Anything else in these columns would be an
//     identity this layer invented.
//
//   - the UNSELECTED instance renders exactly what it renders with no strict
//     block at all. That is the half a leaking selection breaks, and it is
//     checked against the same run rather than against a remembered string.
func TestStrictMarkersRecordRendersItsIdentityByValue(t *testing.T) {
	report := Dir(t.Context(), filepath.Join("testdata", "strict-markers-record"), Context{Schemas: strictMarkersSchemas()})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	got := renderStrictMarkerIdentities(report)
	want := []string{
		"aws_ebs_volume.selected\tRECORD_LOCATED\t\t",
		"aws_vpc.unselected\tNEEDS_DISCOVERY\t\t",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rendered identities differ\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The lint half of the same claim. Nothing may refuse this
	// configuration: marker_repair = "never" is implemented here because the
	// selection gives the resource somewhere else to hold its identity, and
	// ignore_changes = [tags] on the selected resource is honoured for the
	// same reason.
	if len(report.Findings) != 0 {
		t.Errorf("the composed configuration was refused, and it is the shape the two toggles exist to make work: %v", report.Findings)
	}
}

// TestStrictMarkersRecordFailsClosedWithNoSchemas pins the direction every
// layer takes when it cannot verify the selection.
//
// identity.SelectedLocatedRefusal's two remaining conditions are schema
// reads, and a predicate that cannot run must not admit. What that has to
// mean end to end is that the run behaves as though the selection were not
// there at all: the resource resolves through its ordinary route and keeps
// its marker. The alternative - honouring the selection unverified - would
// withhold a marker from a resource whose identity a record may not be able
// to hold, which is the wrong-identity risk the whole slice is built around.
//
// The consequence for the OTHER half is asserted here too, because it is the
// half that looks like a regression if you meet it without this comment: the
// ignore_changes refusal comes back. That is correct. With the selection not
// applied, the marker IS written, and discarding the write is the "created
// unfindable" failure RuleIgnoreChanges exists to prevent.
func TestStrictMarkersRecordFailsClosedWithNoSchemas(t *testing.T) {
	report := Dir(t.Context(), filepath.Join("testdata", "strict-markers-record"), Context{})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}
	if report.Schemas {
		t.Fatal("report.Schemas is true with no schemas given")
	}

	for _, res := range report.Identities {
		if res.Class == identity.ClassRecordLocated {
			t.Errorf("%s was routed to %s with no schema to verify the selection against; the predicate must fail closed",
				res.Addr, res.Class)
		}
	}

	assertFindingRule(t, report, lint.RuleIgnoreChanges,
		"with the selection unverifiable, the marker is still written, so discarding the write must still be refused")
}

// TestStrictMarkersRefusesAnUnrecordableIdentity is the condition an
// operator's choice may not skip.
//
// aws_cognito_user_pool_client is in identity.IDNotProvenWholeTypes: its
// documented import string is composite and nothing corroborates that the
// exported `id` is the whole of it. Recording `id` would store a fragment
// that reads back as a whole identity, which is the defect
// LocatedIdentityPlanFor exists to close, and no selection may reopen it.
//
// The fixture's schema is deliberately the most permissive one the type
// could have - a top-level string `id`, a settable tags map, no identity
// schema - so that the refusal is demonstrably coming from the documented-
// import verdict rather than from a schema that happens to be thin.
func TestStrictMarkersRefusesAnUnrecordableIdentity(t *testing.T) {
	schemas := strictMarkersSchemas()
	schemas["aws_cognito_user_pool_client"] = providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":           {Type: cty.String, Computed: true},
			"user_pool_id": {Type: cty.String, Required: true},
			"name":         {Type: cty.String, Optional: true},
			"tags":         {Type: cty.Map(cty.String), Optional: true},
		},
	}}

	report := Dir(t.Context(), filepath.Join("testdata", "strict-markers-unrecordable"), Context{Schemas: schemas})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	assertFindingRule(t, report, lint.RuleStrictMarkersUnrecordable,
		"a selection naming a type whose whole identity no record can hold must be refused, not quietly dropped")
}

// renderStrictMarkerIdentities renders a report's resolutions in the identity
// golden's own columns - address, class, rendered identity, identity
// attributes - so that this test and the golden are asserting the same thing
// about the same values, spelled the same way.
func renderStrictMarkerIdentities(report Report) []string {
	out := make([]string, 0, len(report.Identities))
	for _, res := range report.Identities {
		out = append(out, strings.Join([]string{
			res.Addr.String(),
			string(res.Class),
			renderedIdentity(res),
			renderedIdentityAttrs(res),
		}, "\t"))
	}
	sort.Strings(out)
	return out
}

// assertFindingRule fails unless some lint finding in the report carries
// rule.
func assertFindingRule(t *testing.T, report Report, rule lint.Rule, why string) {
	t.Helper()
	for _, f := range report.Findings {
		if f.Layer != LayerLint {
			continue
		}
		if strings.Contains(f.ID, string(rule)) || f.ID == rule.Summary() {
			return
		}
	}
	t.Errorf("no %s finding: %s\nfindings: %v", rule, why, report.Findings)
}
