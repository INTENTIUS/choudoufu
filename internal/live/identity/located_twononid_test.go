// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file covers corpus-alb-complete/test_plan's remaining wall: two
// aws_lb_target_group_attachment lambda-target instances refusing "Null
// identity argument" on port, read HANDOFF's fifth row: a lambda target
// group genuinely has no port, so the null is honest and the instance
// belongs on the record rung.
//
// [compositeIdentity] required "id" to be one of a type's 2+ required
// identity-schema attributes before this file existed. Measured directly
// against the real hashicorp/aws 6.59.0 provider (pluginschema.ResourceTypes,
// no tofu in the loop): aws_lb_target_group_attachment's own wire identity
// schema requires target_group_arn and target_id - neither is "id" - and
// marks account_id/availability_zone/port/quic_server_id/region optional.
// twoNonIDIdentitySchema reproduces exactly that shape, reduced to the
// attributes this mechanism reads.
func twoNonIDIdentitySchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"target_group_arn":  {Type: cty.String, Required: true},
			"target_id":         {Type: cty.String, Required: true},
			"port":              {Type: cty.Number, Optional: true, Computed: true},
			"availability_zone": {Type: cty.String, Optional: true, Computed: true},
			"quic_server_id":    {Type: cty.String, Optional: true, Computed: true},
			"id":                {Type: cty.String, Computed: true},
		}},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"target_group_arn":  {Type: cty.String, Required: true},
				"target_id":         {Type: cty.String, Required: true},
				"account_id":        {Type: cty.String, Optional: true},
				"availability_zone": {Type: cty.String, Optional: true},
				"port":              {Type: cty.Number, Optional: true},
				"quic_server_id":    {Type: cty.String, Optional: true},
				"region":            {Type: cty.String, Optional: true},
			},
		},
	}
}

// TestCompositeIdentityDropsTheIDRequirement is [compositeIdentity] in
// isolation: the len(required)<2 case is unchanged, and the len(required)>=2
// case no longer depends on "id" being one of the members.
func TestCompositeIdentityDropsTheIDRequirement(t *testing.T) {
	cases := []struct {
		name     string
		required []string
		want     bool
	}{
		{"empty", nil, false},
		{"single, id", []string{"id"}, false},
		{"single, not id", []string{"arn"}, false},
		{"two, includes id", []string{"id", "parent"}, true},
		{"two, neither id", []string{"target_group_arn", "target_id"}, true},
		{"three, none id", []string{"cluster_name", "node_group_name", "region"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compositeIdentity(tc.required); got != tc.want {
				t.Errorf("compositeIdentity(%v) = %v, want %v", tc.required, got, tc.want)
			}
		})
	}
}

// TestLocatedIdentityPlanForTwoRequiredNonIDAttributes is the full
// [LocatedIdentityPlanFor] entry point against the real
// aws_lb_target_group_attachment shape: Components carries exactly the two
// REQUIRED attributes, sorted, and OptionalComponents carries the five
// optional ones, sorted - neither list names the type, because
// [identityAttrs] read straight off the schema is what produced them.
func TestLocatedIdentityPlanForTwoRequiredNonIDAttributes(t *testing.T) {
	plan, recordable := LocatedIdentityPlanFor("aws_lb_target_group_attachment", twoNonIDIdentitySchema())
	if !recordable {
		t.Fatal("LocatedIdentityPlanFor refused a type whose two required identity attributes are both plain top-level strings on the block")
	}
	if !plan.Composite() {
		t.Fatal("plan.Composite() = false; a 2+-required wire identity schema must take the identity-object route regardless of whether \"id\" is one of its members")
	}
	wantComponents := []string{"target_group_arn", "target_id"}
	if !reflect.DeepEqual(plan.Components, wantComponents) {
		t.Errorf("Components = %v, want %v", plan.Components, wantComponents)
	}
	wantOptional := []string{"account_id", "availability_zone", "port", "quic_server_id", "region"}
	if !reflect.DeepEqual(plan.OptionalComponents, wantOptional) {
		t.Errorf("OptionalComponents = %v, want %v", plan.OptionalComponents, wantOptional)
	}
	if plan.Composed() || plan.Named() {
		t.Errorf("plan = %+v; a composite plan is never also composed or named", plan)
	}
}

// TestLocatedIdentityPlanForSingleNonIDAttributeIsUnchanged is the
// boundary [compositeIdentity]'s own doc comment names: a type whose wire
// identity schema requires exactly ONE non-"id" attribute is NOT reclassified
// by this widening - it still resolves through the bare-"id" default,
// exactly as it did before this file existed. This is the mutation check for
// the len(required)<2 guard: dropping it entirely (rather than only
// dropping the "id membership" condition) would silently move this
// population's identity from the string ["id"] onto an object of one
// attribute, changing what a working population records for no defect.
func TestLocatedIdentityPlanForSingleNonIDAttributeIsUnchanged(t *testing.T) {
	schema := providers.Schema{
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":  {Type: cty.String, Computed: true},
			"arn": {Type: cty.String, Required: true},
		}},
		IdentitySchema: &configschema.Object{
			Nesting:    configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{"arn": {Type: cty.String, Required: true}},
		},
	}
	plan, recordable := LocatedIdentityPlanFor("test_single_required", schema)
	if !recordable {
		t.Fatal("LocatedIdentityPlanFor refused a type whose identity schema names \"id\" as sufficient by omission")
	}
	if plan.Composite() {
		t.Errorf("plan.Composite() = true; a single required, non-\"id\" attribute must still resolve through the bare \"id\" default (the provider's own d.SetId already captured it), not the identity-object route")
	}
}

// TestLocatedIdentityOptionalIncludesPresentExcludesAbsent is
// [LocatedIdentityOptional] by value: a present string, a present number
// (rendered to plain decimal - the port shape), a null, an empty string and
// a sensitive-marked value are each handled the way
// [LocatedIdentityPlan.OptionalComponents]' own doc comment promises -
// included when there is a real value to read, silently left out otherwise,
// and NEVER a reason to fail the call.
func TestLocatedIdentityOptionalIncludesPresentExcludesAbsent(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{
		"availability_zone": cty.StringVal("us-east-1a"),
		"port":              cty.NumberIntVal(80),
		"quic_server_id":    cty.NullVal(cty.String),
		"account_id":        cty.StringVal(""),
		"region":            cty.StringVal("us-east-1").Mark("sensitive"),
	})
	got := LocatedIdentityOptional(obj, []string{"availability_zone", "port", "quic_server_id", "account_id", "region", "not_on_object"})
	want := map[string]string{"availability_zone": "us-east-1a", "port": "80"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LocatedIdentityOptional = %v, want %v", got, want)
	}
}

// TestLocatedIdentityOptionalNilForNoOptionalComponents pins the nil-map
// contract [LocatedIdentityPlan.OptionalComponents]'s doc comment states,
// for a type - most of [DefaultTable] - whose plan carries none: the caller
// (LocatedRecordFrom) ranges over this result unconditionally, and ranging
// over a nil map is a documented no-op, never a nil dereference.
func TestLocatedIdentityOptionalNilForNoOptionalComponents(t *testing.T) {
	if got := LocatedIdentityOptional(cty.EmptyObjectVal, nil); got != nil {
		t.Errorf("LocatedIdentityOptional(_, nil) = %v, want nil", got)
	}
}

// TestLocatedIdentityOptionalNeverRefusesTheWhole is
// aws_lb_target_group_attachment's own lambda-target case, read literally:
// EVERY optional component absent - the shape a lambda target's applied
// object actually has (no port, no availability_zone, no quic_server_id) -
// still returns ok in the sense that matters: an empty map, not a failure a
// caller has to special-case. [LocatedIdentity] (the REQUIRED half) is what
// still governs whether the record as a whole is written.
func TestLocatedIdentityOptionalNeverRefusesTheWhole(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{
		"availability_zone": cty.NullVal(cty.String),
		"port":              cty.NullVal(cty.Number),
		"quic_server_id":    cty.NullVal(cty.String),
	})
	got := LocatedIdentityOptional(obj, []string{"availability_zone", "port", "quic_server_id"})
	if len(got) != 0 {
		t.Errorf("LocatedIdentityOptional = %v, want an empty result - every optional component is null, the lambda-target shape this unit exists for", got)
	}
}

// TestSensitiveIdentityAttrSweepsOptionalComponents is the wrong-marker
// guard's own boundary: a type whose OPTIONAL identity component happens to
// be Sensitive must refuse record-located admission entirely - the record
// this mechanism could otherwise opportunistically write for it - the same
// way a sensitive REQUIRED component already does. Without this,
// [LocatedIdentityOptional] could be handed a schema
// [recordableIdentitySchema] had already cleared to record a secret into,
// because the sensitivity sweep only ever looked at Components.
func TestSensitiveIdentityAttrSweepsOptionalComponents(t *testing.T) {
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"required_a": {Type: cty.String, Required: true},
		"required_b": {Type: cty.String, Required: true},
		"opt_secret": {Type: cty.String, Optional: true, Sensitive: true},
	}}}
	plan := LocatedIdentityPlan{
		Components:         []string{"required_a", "required_b"},
		OptionalComponents: []string{"opt_secret"},
	}
	if attr := sensitiveIdentityAttr(plan, schema); attr != "opt_secret" {
		t.Fatalf("sensitiveIdentityAttr = %q, want %q - an optional component this route would opportunistically record must be swept for secrecy exactly like a required one", attr, "opt_secret")
	}
}
