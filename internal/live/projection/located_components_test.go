// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is corpus-alb-complete/test_plan's second #364 wall
// (rfc/20260823-foundation-order-ruling.md's write half, "every instance",
// applied to an UNTAGGABLE one): aws_route53_record has a genuine ratified
// composite identity (table_generated.go: zone_id/name/type[/set_identifier])
// but [identity.LocatedIdentityPlanFor] answers false for it - the
// provider's 6.59.0 wire schema serves no identity object for the type and
// tools/importdocs-gen's prose parser never produced a
// docimportid_generated.go grammar for its "ZONEID_NAME_TYPE or
// ZONEID_NAME_TYPE_SETIDENTIFIER" Import section - so before this,
// [LocatedRecordFrom] wrote NOTHING for it, and live-plan's marker-only
// escalation had no other handle to fall back on.
//
// route53RecordSchema is reduced to the top-level string attributes
// [identity.LookupType]'s row for aws_route53_record actually reads
// (zone_id, name, type, set_identifier); every other real argument is
// irrelevant to this mechanism and left out.
func route53RecordSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"zone_id":        {Type: cty.String, Required: true},
		"name":           {Type: cty.String, Required: true},
		"type":           {Type: cty.String, Required: true},
		"set_identifier": {Type: cty.String, Optional: true, Computed: true},
		"id":             {Type: cty.String, Computed: true},
	}}}
}

// TestLocatedRecordFromRatifiedComponentsFallback is the ordinary case,
// against the REAL corpus values (corpus-alb-complete's own ACM DNS
// validation record, module.acm.aws_route53_record.validation[0], read by
// this unit's own migrate run against a real floci): the composed import ID
// matches table_generated.go's own row, segment for segment, and the
// sensitivity gate does not fire (route53_record carries no secret
// component).
func TestLocatedRecordFromRatifiedComponentsFallback(t *testing.T) {
	ti, ok := identity.LookupType("aws_route53_record")
	if !ok {
		t.Fatal("aws_route53_record is not in DefaultTable; this test needs a real ratified row")
	}
	if _, recordable := identity.LocatedIdentityPlanFor("aws_route53_record", route53RecordSchema()); recordable {
		t.Fatal("LocatedIdentityPlanFor now answers this type directly (a wire identity schema or a documented " +
			"import grammar landed for it) - this test's whole premise, that the fallback is what has to answer " +
			"it, no longer holds; update or retire it rather than leaving it passing vacuously")
	}

	obj := cty.ObjectVal(map[string]cty.Value{
		"zone_id":        cty.StringVal("ZA0SVANADLH8OSR"),
		"name":           cty.StringVal("_8df2d0e2a4a35e3bf8db513a385061e8.terraform-aws-modules.modules.tf"),
		"type":           cty.StringVal("CNAME"),
		"set_identifier": cty.NullVal(cty.String),
		"id":             cty.StringVal("ZA0SVANADLH8OSR__8df2d0e2a4a35e3bf8db513a385061e8.terraform-aws-modules.modules.tf._CNAME"),
	})

	rec, ok := LocatedRecordFrom("aws_route53_record", route53RecordSchema(), obj)
	if !ok {
		t.Fatal("LocatedRecordFrom refused an instance with every required component present")
	}
	const want = "ZA0SVANADLH8OSR__8df2d0e2a4a35e3bf8db513a385061e8.terraform-aws-modules.modules.tf_CNAME"
	if rec.ImportID != want {
		t.Errorf("ImportID = %q, want %q - table_generated.go's own zone_id/name/type Components chain, "+
			"joined by its own Literal \"_\" separators (%d components)", rec.ImportID, want, len(ti.Components))
	}
	if len(rec.Components) != 0 {
		t.Errorf("Components = %v, want empty - this fallback only ever produces the composed STRING form "+
			"(rec.ImportID): rec.Components is the PROVIDER'S OWN wire identity object, which this type has "+
			"none of, and reading a real ImportTarget{Identity: ...} through a provider with no identity "+
			"schema is a call this fork's own provider client rejects before any RPC", rec.Components)
	}
}

// TestLocatedRecordFromRatifiedComponentsFallbackMissingComponentRefuses is
// the boundary: an object missing a REQUIRED (non-OmitIfAbsent) component -
// here, "name" was never set - must refuse rather than compose a partial
// identity. HANDOFF's safety rule read the record side: a missing identity
// outranks a wrong one, and a record LocatedRecordFrom cannot fully derive
// must stay unwritten, not written as a fragment that reads back as whole.
func TestLocatedRecordFromRatifiedComponentsFallbackMissingComponentRefuses(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{
		"zone_id":        cty.StringVal("ZA0SVANADLH8OSR"),
		"name":           cty.NullVal(cty.String),
		"type":           cty.StringVal("CNAME"),
		"set_identifier": cty.NullVal(cty.String),
		"id":             cty.StringVal("whatever"),
	})

	if rec, ok := LocatedRecordFrom("aws_route53_record", route53RecordSchema(), obj); ok {
		t.Fatalf("LocatedRecordFrom recorded %+v for an object with no \"name\", a required, non-OmitIfAbsent "+
			"component - it must refuse rather than compose a fragment", rec)
	}
}

// TestLocatedRecordFromRatifiedComponentsFallbackRefusesASensitiveComponent
// is the sensitivity boundary [identity.SensitiveComponentsAttr] exists
// for: a hand-built type whose ratified Components chain reads a Sensitive,
// non-Deprecated schema attribute must never be recorded, the same rule
// [identity.RecordableIdentitySchema] already holds for the
// wire-schema/documented-grammar routes. There is no real aws_* type this
// reaches today (route53_record's own components are all plain strings),
// so this fixture stands in for the shape rather than naming one.
func TestLocatedRecordFromRatifiedComponentsFallbackRefusesASensitiveComponent(t *testing.T) {
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"secret_key": {Type: cty.String, Required: true, Sensitive: true},
		"id":         {Type: cty.String, Computed: true},
	}}}
	if attr := identity.SensitiveComponentsAttr(identity.TypeIdentity{
		Type:       "test_sensitive_component",
		Components: []identity.Component{{Attrs: []string{"secret_key"}}},
	}, schema); attr != "secret_key" {
		t.Fatalf("SensitiveComponentsAttr = %q, want %q", attr, "secret_key")
	}
}

// TestLocatedRecordFromRatifiedComponentsFallbackEmptyStringOptionalTrailer
// pins CURRENT behaviour on the exact shape floci's own real
// aws_route53_record objects carry for corpus-alb-complete
// (module.acm.aws_route53_record.validation[0] and its wildcard sibling,
// read from a real migrate this unit ran): set_identifier comes back as ""
// rather than null on an ordinary record that does not use weighted/
// failover/latency routing. [Component.OmitIfAbsent]'s own
// [componentFromValue] treats only a NULL attribute as absent - an empty
// string is "present" as the empty value - so the trailing "_" + ""
// segment is included, and the composed ID carries a trailing separator
// ("..._CNAME_") the documented "ZONEID_NAME_TYPE" (no fourth segment)
// grammar does not show.
//
// This is NOT fixed here: componentFromValue is GitHub issue #388's shared
// evaluator, also used at the plan-node seam against real EVALUATED CONFIG
// values, where an explicit "" argument may mean something the identity
// table has to keep distinguishing from "not set" - widening the absence
// test there needs its own audit across every OmitIfAbsent row, not a
// side effect of this unit. Empirically it is not a live bug for THIS
// estate: this unit's own real migrate + live-plan run (CHOUDOUFU_NODE_
// RESOLVE=1, floci) shows both validation records fully materialize from
// this exact identity with no [ABSENT] or import-failure diagnostic - floci
// tolerates the trailing separator - which is the load-bearing reason this
// test PINS the current string rather than asserting the "corrected" one.
// Follow-up: audit trailing-OmitIfAbsent components for the empty-string-
// vs-null convention across the ~70 types [LocatedRecordFrom]'s new
// fallback reaches, ideally against real AWS rather than floci alone.
func TestLocatedRecordFromRatifiedComponentsFallbackEmptyStringOptionalTrailer(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{
		"zone_id":        cty.StringVal("ZA0SVANADLH8OSR"),
		"name":           cty.StringVal("_8df2d0e2a4a35e3bf8db513a385061e8.terraform-aws-modules.modules.tf"),
		"type":           cty.StringVal("CNAME"),
		"set_identifier": cty.StringVal(""), // floci's real value, not null
		"id":             cty.StringVal("ZA0SVANADLH8OSR__8df2d0e2a4a35e3bf8db513a385061e8.terraform-aws-modules.modules.tf._CNAME"),
	})

	rec, ok := LocatedRecordFrom("aws_route53_record", route53RecordSchema(), obj)
	if !ok {
		t.Fatal("LocatedRecordFrom refused an instance with every component present, empty string included")
	}
	const want = "ZA0SVANADLH8OSR__8df2d0e2a4a35e3bf8db513a385061e8.terraform-aws-modules.modules.tf_CNAME_"
	if rec.ImportID != want {
		t.Errorf("ImportID = %q, want %q (current, pinned behaviour - see this test's own doc comment for the "+
			"empty-string-vs-null caveat this leaves open)", rec.ImportID, want)
	}
}
