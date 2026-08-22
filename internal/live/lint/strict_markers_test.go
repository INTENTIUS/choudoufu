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
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #365, slice 2: the `markers "record"` selection's lint
// matrix, refusals and admissions side by side.
//
// The admissions are half the test on purpose. A rule that refuses a
// selection is easy to write and easy to write too widely, and every
// over-refusal here lands on a configuration HANDOFF.md's third principle
// exists to make work.

// TestStrictMarkersMatrix walks every shape the selection can take with no
// provider schemas - which is what CheckContext passes, and what a caller
// running before a provider has started has.
func TestStrictMarkersMatrix(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  string
		want []wantIssue
	}{
		{
			name: "a selection naming neither a type nor an address",
			dir:  "testdata/strict-markers-empty",
			want: []wantIssue{{
				rule:      RuleStrictMarkers,
				construct: `strict.markers "record"`,
				file:      "testdata/strict-markers-empty/main.tf",
				line:      10,
			}},
		},
		{
			name: "a selection with no record_store to hold what it moves",
			dir:  "testdata/strict-markers-no-record-store",
			want: []wantIssue{{
				rule:      RuleStrictMarkers,
				construct: `strict.markers "record"`,
				file:      "testdata/strict-markers-no-record-store/main.tf",
				line:      8,
			}},
		},
		{
			name: "an address naming one instance rather than a resource block",
			dir:  "testdata/strict-markers-instance-address",
			want: []wantIssue{{
				rule:      RuleStrictMarkers,
				construct: `strict.markers "record" addresses = ["aws_vpc.main[0]"]`,
				file:      "testdata/strict-markers-instance-address/main.tf",
				line:      11,
			}},
		},
		{
			name: "an address naming no resource in this configuration",
			dir:  "testdata/strict-markers-unknown-address",
			want: []wantIssue{{
				rule:      RuleStrictMarkers,
				construct: `strict.markers "record" addresses = ["aws_vpc.typo"]`,
				file:      "testdata/strict-markers-unknown-address/main.tf",
				line:      11,
			}},
		},
		{
			// The admission that makes marker_repair = "never" reachable at
			// all, and the reason slice 1's refusal narrowed rather than
			// went away.
			name: "marker_repair = never with a selection to give it a mechanism",
			dir:  "testdata/strict-never-with-selection",
			want: nil,
		},
		{
			// A module-qualified address is the shape an estate with modules
			// needs, and the one an address comparison keyed on the local
			// address alone would get wrong.
			name: "a module-qualified address",
			dir:  "testdata/strict-markers-module-address",
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadConfigDir(t, tc.dir)
			assertIssues(t, CheckContext(t.Context(), cfg), tc.want)
		})
	}
}

// TestStrictMarkersInstanceAddressNamesTheWholeResource is the half of the
// instance-address refusal that makes it actionable.
//
// "You cannot name an instance" with nothing else said leaves an operator
// guessing at the fix, and the fix is one string away: the same address with
// the key dropped. It is in the message, and this test is what keeps it
// there.
func TestStrictMarkersInstanceAddressNamesTheWholeResource(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/strict-markers-instance-address")
	issues := CheckContext(t.Context(), cfg)
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1: %v", len(issues), issues)
	}
	if !strings.Contains(issues[0].Detail, `"aws_vpc.main"`) {
		t.Errorf("the refusal does not name the whole-resource form to use instead: %s", issues[0].Detail)
	}
	if !strings.Contains(issues[0].Detail, "resource BLOCK") {
		t.Errorf("the refusal does not say what the selection's unit is: %s", issues[0].Detail)
	}
}

// TestStrictMarkersUnrecordableNeedsSchemas pins the asymmetry, in both
// directions, on one fixture.
//
// The type is selected and its identity is one a record cannot hold. With no
// schema the rule says nothing - not because the selection is fine, but
// because the question is a schema read and an unanswerable question is not
// a finding. With the schema it refuses, naming the condition.
//
// The direction is what makes the silence safe: a selection nothing verified
// is a selection nothing HONOURS either, so the marker is written and the
// resource stays findable. TestStamp_markersRecordIsNotHonouredForAn
// UnrecordableType is that leg.
func TestStrictMarkersUnrecordableNeedsSchemas(t *testing.T) {
	const typeName = "aws_glue_partition"
	if _, unproven := identity.IDNotProvenWholeTypes[typeName]; !unproven {
		t.Skipf("%s left identity.IDNotProvenWholeTypes; pick another member for this test", typeName)
	}
	// The second half of the same premise, and the one that moved this
	// fixture off its previous subject: a type the documented-grammar route
	// CAN compose is recordable after all, so it stops being an example of
	// this refusal without anything here changing. The fixture's own comment
	// says why this subject is not reachable that way.
	if _, described := identity.DocumentedImportIDs[typeName]; described {
		t.Skipf("%s gained a documented import grammar, so a record can hold its whole identity; "+
			"pick another member of identity.IDNotProvenWholeTypes that has none", typeName)
	}

	cfg := loadConfigDir(t, "testdata/strict-markers-unrecordable")

	if issues := CheckContext(t.Context(), cfg); len(issues) != 0 {
		t.Errorf("with no schemas the rule spoke anyway: %v", issues)
	}

	schemas := map[string]providers.Schema{
		typeName: {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":            {Type: cty.String, Computed: true},
			"database_name": {Type: cty.String, Required: true},
			"table_name":    {Type: cty.String, Required: true},
			"tags":          {Type: cty.Map(cty.String), Optional: true},
		}}},
	}
	issues := CheckWith(t.Context(), cfg, Context{Schemas: schemas})
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want exactly 1 %s: %v", len(issues), RuleStrictMarkersUnrecordable, issues)
	}
	if issues[0].Rule != RuleStrictMarkersUnrecordable {
		t.Fatalf("got rule %q, want %q: %s", issues[0].Rule, RuleStrictMarkersUnrecordable, issues[0])
	}
	if !strings.Contains(issues[0].Detail, "fragment") {
		t.Errorf("the refusal does not say what would go wrong - a recorded fragment read back as a whole identity: %s", issues[0].Detail)
	}
}

// TestStrictMarkersLiftsIgnoreChangesOnlyForTheSelected is the join point
// between slice 1's toggle and slice 2's selection, and the assertion is the
// pair rather than either half.
//
// The fixture declares two resources, both with ignore_changes = [tags], and
// selects one. Exactly one refusal must survive, on the resource the
// selection does not cover - which is also what makes an estate-wide
// marker_repair = "never" meet its limit loudly rather than silently.
func TestStrictMarkersLiftsIgnoreChangesOnlyForTheSelected(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/strict-markers-ignore-changes")

	schemas := map[string]providers.Schema{
		"aws_vpc":    markersRecordTestSchema(),
		"aws_subnet": markersRecordTestSchema(),
	}
	issues := CheckWith(t.Context(), cfg, Context{Schemas: schemas})

	if len(issues) != 1 {
		t.Fatalf("got %d issues, want exactly 1: %v", len(issues), issues)
	}
	if issues[0].Rule != RuleIgnoreChanges {
		t.Fatalf("got rule %q, want %q: %s", issues[0].Rule, RuleIgnoreChanges, issues[0])
	}
	if !strings.Contains(issues[0].Construct, "aws_subnet.private") {
		t.Errorf("the surviving refusal is on %s; it must be the UNSELECTED resource, aws_subnet.private", issues[0].Construct)
	}
}

// TestStrictMarkersIgnoreChangesNeedsBothHalves is the other side of the
// same join: neither toggle alone lifts the refusal.
//
// The selection alone does not, because ignore_changes = [tags] written for
// an unrelated reason must not silently acquire a new meaning. And
// marker_repair = "never" alone cannot even reach the question -
// checkLiveStrict refuses it - which this asserts by value rather than by
// argument, since "it is refused earlier" is exactly the kind of claim that
// stops being true after a refactor.
func TestStrictMarkersIgnoreChangesNeedsBothHalves(t *testing.T) {
	schemas := map[string]providers.Schema{
		"aws_vpc":    markersRecordTestSchema(),
		"aws_subnet": markersRecordTestSchema(),
	}

	t.Run("selection with no marker_repair", func(t *testing.T) {
		cfg := loadConfigDir(t, "testdata/strict-markers-ignore-changes-no-repair")
		issues := CheckWith(t.Context(), cfg, Context{Schemas: schemas})

		var ignore int
		for _, issue := range issues {
			if issue.Rule == RuleIgnoreChanges {
				ignore++
			}
		}
		if ignore != 1 {
			t.Fatalf("got %d ignore-changes issues, want 1 - the selection alone must not lift the refusal: %v", ignore, issues)
		}
	})

	t.Run("marker_repair with no selection", func(t *testing.T) {
		cfg := loadConfigDir(t, "testdata/strict-markers-ignore-changes-no-selection")
		issues := CheckWith(t.Context(), cfg, Context{Schemas: schemas})

		var repair, ignore int
		for _, issue := range issues {
			switch issue.Rule {
			case RuleStrictMarkerRepair:
				repair++
			case RuleIgnoreChanges:
				ignore++
			}
		}
		if repair != 1 {
			t.Errorf("got %d strict-marker-repair issues, want 1: %v", repair, issues)
		}
		if ignore != 1 {
			t.Errorf("got %d ignore-changes issues, want 1 - marker_repair alone must not lift the refusal: %v", ignore, issues)
		}
	})
}

// markersRecordTestSchema is a taggable type with a top-level string id: the
// shape that passes every condition identity.SelectedLocatedRefusal checks,
// so that a test using it is testing the selection rather than the schema.
func markersRecordTestSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":         {Type: cty.String, Computed: true},
		"cidr_block": {Type: cty.String, Optional: true},
		"vpc_id":     {Type: cty.String, Optional: true},
		"tags":       {Type: cty.Map(cty.String), Optional: true},
	}}}
}
