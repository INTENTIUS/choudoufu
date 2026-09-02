// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #364 unit B found this the hard way: corpus-rds-complete-postgres's
// aws_default_route_table record-first read failed with the provider's own
// "Error: empty result", traced to a DescribeRouteTables call filtered by
// vpc-id=<the route table's OWN id> - because the record [LocatedRecordFrom]
// wrote held that bare "id", and this type's ratified row
// (table_generated.go, settled by issue #332 against real stock terraform)
// has always said the identity is the PARENT VPC's id
// (IdentityAttrs ["vpc_id"]), not the route table's own. Nothing in
// [LocatedIdentityPlanFor] read that row before this fix, so every write
// path through [LocatedRecordFrom] recorded a wrong identity for any type
// shaped this way - invisible until read-first (unit B) actually re-imported
// it.
//
// aws_default_route_table's own schema carries no wire IdentitySchema at
// this provider version (confirmed against the trace log, not assumed), so
// this is exactly the [!compositeIdentity] branch's population, and the
// fixture below serves the same shape rather than a caricature.
func defaultRouteTableSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":     {Type: cty.String, Computed: true},
				"vpc_id": {Type: cty.String, Computed: true},
			},
		},
	}
}

func TestLocatedIdentityPlanPrefersARatifiedNamedAttrOverBareID(t *testing.T) {
	plan, recordable := LocatedIdentityPlanFor("aws_default_route_table", defaultRouteTableSchema())
	if !recordable {
		t.Fatal("recordable = false, want true: this type has a usable ratified identity attribute")
	}
	if !plan.Named() {
		t.Fatalf("plan.Named() = false, want true: aws_default_route_table's ratified row names vpc_id, not id.\nplan = %+v", plan)
	}
	if plan.Attr != "vpc_id" {
		t.Errorf("plan.Attr = %q, want %q", plan.Attr, "vpc_id")
	}
	if plan.Composite() || plan.Composed() {
		t.Errorf("plan also carries a composite or composed shape, which should be mutually exclusive with Named(): %+v", plan)
	}

	obj := cty.ObjectVal(map[string]cty.Value{
		"id":     cty.StringVal("rtb-2162d0201b3454d33"),
		"vpc_id": cty.StringVal("vpc-0a86e666"),
	})
	got, ok := LocatedNamedAttr(obj, plan.Attr)
	if !ok || got != "vpc-0a86e666" {
		t.Fatalf("LocatedNamedAttr(obj, %q) = (%q, %v), want (%q, true).\n"+
			"Recording the route table's own id here reproduces GitHub issue #332's exact failure: "+
			"stock terraform's own `terraform import aws_default_route_table.x rtb-...` fails with "+
			"\"Error: empty result\", and only `vpc-...` succeeds.",
			plan.Attr, got, ok, "vpc-0a86e666")
	}
}

// TestLocatedIdentityPlanNamedAttrDoesNotShadowARealComposite is the
// mutual-exclusion half: a type whose wire identity schema DOES require more
// than "id" must still classify as Composite, never as Named - Named only
// ever applies to the branch a silent (or trivially-"id") wire schema
// leaves open.
func TestLocatedIdentityPlanNamedAttrDoesNotShadowARealComposite(t *testing.T) {
	plan, recordable := LocatedIdentityPlanFor("aws_default_route_table", compositeIdentitySchema())
	if !recordable {
		t.Fatal("recordable = false, want true")
	}
	if plan.Named() {
		t.Errorf("plan.Named() = true for a type whose wire identity schema is composite; the composite branch must win: %+v", plan)
	}
	if !plan.Composite() {
		t.Errorf("plan.Composite() = false, want true: compositeIdentitySchema() requires api_id alongside id")
	}
}

// TestLocatedIdentityPlanNamedAttrIsNotConsultedWithoutARatifiedRow: a type
// with no table entry at all - the ordinary case for most callers, which
// pass resourceType="" in every other test in this file - still falls
// through to the bare-id default. This is what keeps the whole existing
// suite in this file passing unchanged: every other case there names no
// resource type, so namedIdentityAttr never fires for them.
func TestLocatedIdentityPlanNamedAttrIsNotConsultedWithoutARatifiedRow(t *testing.T) {
	plan, recordable := LocatedIdentityPlanFor("", defaultRouteTableSchema())
	if !recordable {
		t.Fatal("recordable = false, want true: id is present and a string, which the bare default admits")
	}
	if plan.Named() {
		t.Errorf("plan.Named() = true for a resource type with no ratified row at all: %+v", plan)
	}
}

// TestLocatedIdentityPlanNamedAttrRequiresTheAttrOnThisSchema: a ratified row
// naming an attribute the RUNNING provider's schema does not actually carry
// (an older or a caricature schema) must not be trusted blind - there is
// nothing to read it out of, so the bare "id" default is the safer of the
// two available answers, exactly [namedIdentityAttr]'s own doc comment.
func TestLocatedIdentityPlanNamedAttrRequiresTheAttrOnThisSchema(t *testing.T) {
	schema := providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true},
				// No vpc_id attribute on this schema at all.
			},
		},
	}
	plan, recordable := LocatedIdentityPlanFor("aws_default_route_table", schema)
	if !recordable {
		t.Fatal("recordable = false, want true: falls back to the bare id default")
	}
	if plan.Named() {
		t.Errorf("plan.Named() = true for a schema that does not carry the ratified attribute at all: %+v", plan)
	}
}
