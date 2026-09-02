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

// This file covers GitHub issue #329: a located record could only ever hold
// one string, so a type whose identity is a server-minted LEAF under a
// config-known PARENT recorded the leaf and handed that fragment to the next
// run's import.
//
// The shape every fixture here reproduces is the real one, measured against
// hashicorp/aws 6.59.0: the provider sets "id" to the bare leaf, its own
// identity schema requires the parent alongside "id", and the documented
// import expects both.

// compositeIdentitySchema builds the schema of a type whose identity is
// composite in exactly the way this issue is about: a required config-known
// parent argument, a computed server-minted "id", and an identity schema
// naming both plus the ambient context the provider fills in itself.
func compositeIdentitySchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":        {Type: cty.String, Computed: true},
				"api_id":    {Type: cty.String, Required: true},
				"route_key": {Type: cty.String, Required: true},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"api_id":     {Type: cty.String, Required: true},
				"id":         {Type: cty.String, Required: true},
				"account_id": {Type: cty.String, Optional: true},
				"region":     {Type: cty.String, Optional: true},
			},
		},
	}
}

// TestLocatedIdentityPlanClassifiesByTheProvidersOwnIdentitySchema is
// the derivation, one shape per case. No case names a provider resource
// type, because the rule reads none: it reads a block and an identity
// schema.
func TestLocatedIdentityPlanClassifiesByTheProvidersOwnIdentitySchema(t *testing.T) {
	stringAttr := func(names ...string) map[string]*configschema.Attribute {
		out := make(map[string]*configschema.Attribute, len(names))
		for _, n := range names {
			out[n] = &configschema.Attribute{Type: cty.String, Computed: true}
		}
		return out
	}
	requiredIdentity := func(names ...string) *configschema.Object {
		out := &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{}}
		for _, n := range names {
			out.Attributes[n] = &configschema.Attribute{Type: cty.String, Required: true}
		}
		return out
	}

	cases := []struct {
		name string
		// resourceType is what the doc-derived leg reads. Empty means a
		// type no scraped page describes, which is the shape every case
		// written before [IDNotProvenWholeTypes] existed was implicitly
		// testing - so the pre-existing cases keep their meaning and the
		// two that name a real type say why they do.
		resourceType   string
		schema         providers.Schema
		wantComponents []string
		wantRecordable bool
		why            string
	}{
		{
			name:           "no identity schema at all, string id",
			schema:         providers.Schema{Block: &configschema.Block{Attributes: stringAttr("id")}},
			wantComponents: nil,
			wantRecordable: true,
			why:            "the population the mechanism shipped for; the string is the whole identity and nothing about it changes",
		},
		{
			name:           "no identity schema, no string id",
			schema:         providers.Schema{Block: &configschema.Block{Attributes: stringAttr("arn")}},
			wantComponents: nil,
			wantRecordable: false,
			why:            "nothing to record, which is where this type already was",
		},
		{
			name: "identity schema requiring id alone",
			schema: providers.Schema{
				Block:          &configschema.Block{Attributes: stringAttr("id")},
				IdentitySchema: requiredIdentity("id"),
			},
			wantComponents: nil,
			wantRecordable: true,
			why:            "the provider agrees the string is the identity; the string path stands",
		},
		{
			name: "identity schema requiring something other than id",
			schema: providers.Schema{
				Block:          &configschema.Block{Attributes: stringAttr("id", "arn")},
				IdentitySchema: requiredIdentity("arn"),
			},
			wantComponents: nil,
			wantRecordable: true,
			why: "the provider's own d.SetId put that value in id, so today's rule already serves this type correctly. " +
				"Reclassifying it would change what a working population records for no defect.",
		},
		{
			name:           "identity schema requiring a parent alongside id",
			schema:         compositeIdentitySchema(),
			wantComponents: []string{"api_id", "id"},
			wantRecordable: true,
			why:            "the defect population: id is present AND insufficient, so the record must carry the object",
		},
		{
			name: "composite whose parent component the block does not carry",
			schema: providers.Schema{
				Block:          &configschema.Block{Attributes: stringAttr("id")},
				IdentitySchema: requiredIdentity("api_id", "id"),
			},
			wantComponents: nil,
			wantRecordable: false,
			why: "the applied object has no api_id to read, so the record would be a fragment. " +
				"Refusing is the whole point: a partial record reads back as a whole identity.",
		},
		{
			name: "composite whose parent component is not a string",
			schema: providers.Schema{
				Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
					"id":     {Type: cty.String, Computed: true},
					"api_id": {Type: cty.List(cty.String), Required: true},
				}},
				IdentitySchema: requiredIdentity("api_id", "id"),
			},
			wantComponents: nil,
			wantRecordable: false,
			why:            "a component with structure in it is not one a per-attribute record can hold",
		},
		{
			name:           "no block at all",
			schema:         providers.Schema{IdentitySchema: requiredIdentity("api_id", "id")},
			wantComponents: nil,
			wantRecordable: false,
			why:            "fails closed, exactly as LocatedType does for an absent schema",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, recordable := LocatedIdentityPlanFor(tc.resourceType, tc.schema)
			if recordable != tc.wantRecordable {
				t.Errorf("recordable = %v, want %v.\n%s", recordable, tc.wantRecordable, tc.why)
			}
			if !reflect.DeepEqual(got.Components, tc.wantComponents) {
				t.Errorf("components = %v, want %v.\n%s", got.Components, tc.wantComponents, tc.why)
			}
			if got.Composed() {
				t.Errorf("plan carries a documented import-string grammar (%v joined by %q), but no case here names a type the scrape describes.\n%s",
					got.ImportIDParts, got.ImportIDSeparator, tc.why)
			}
		})
	}
}

// TestLocatedTypeRefusesACompositeItCannotRecordInFull is the safety gate,
// asserted through the admission predicate rather than through the
// derivation it delegates to.
//
// The trade this forbids is the one [LocatedType]'s own doc comment forbids,
// wearing a disguise: admitting a type whose identity can only be recorded
// in PART writes a record, keeps the plan clean, and fails on the NEXT run's
// import. That is a plan refusal traded for a deferred apply-time failure.
func TestLocatedTypeRefusesACompositeItCannotRecordInFull(t *testing.T) {
	markerless := aMarkerlessType(t)

	// The whole composite is readable: admitted.
	whole := map[string]providers.Schema{markerless: compositeIdentitySchema()}
	if !LocatedType(markerless, whole) {
		t.Error("LocatedType refused a composite identity every component of which is readable off the applied object")
	}

	// The parent component is gone from the block, so only the leaf could
	// ever be recorded: refused.
	partial := compositeIdentitySchema()
	partial.Block = &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":        {Type: cty.String, Computed: true},
		"route_key": {Type: cty.String, Required: true},
	}}
	if LocatedType(markerless, map[string]providers.Schema{markerless: partial}) {
		t.Error("LocatedType admitted a type whose identity schema requires a component the block does not carry.\n" +
			"Only the leaf could be recorded, so the next run would import a fragment - a wrong identity, which is invisible to every verdict-level check.")
	}
}

// TestLocatedIdentityReadsEveryComponentOrNone pins the all-or-nothing rule,
// by VALUE on the resolving case and by refusal on each way one component
// can be unusable.
//
// The mutation this is written against is an implementation that skipped an
// unreadable component instead of failing: the map would come back short,
// [LocatedStore.Put] would accept it, and the record would be a fragment
// that later reads back as a whole identity.
func TestLocatedIdentityReadsEveryComponentOrNone(t *testing.T) {
	components := []string{"api_id", "id"}
	obj := func(apiID, id cty.Value) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{
			"api_id":    apiID,
			"id":        id,
			"route_key": cty.StringVal("GET /pets"),
		})
	}

	got, ok := LocatedIdentity(obj(cty.StringVal("aabbccddee"), cty.StringVal("1122334")), components)
	if !ok {
		t.Fatal("LocatedIdentity refused an object carrying every component")
	}
	want := map[string]string{"api_id": "aabbccddee", "id": "1122334"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("identity = %v, want %v", got, want)
	}

	// Nothing to record for a type whose identity is the string.
	if got, ok := LocatedIdentity(obj(cty.StringVal("a"), cty.StringVal("b")), nil); !ok || got != nil {
		t.Errorf("LocatedIdentity(_, nil) = %v, %v; want nil, true - a type with no components has no object to record", got, ok)
	}

	refusals := []struct {
		name string
		obj  cty.Value
	}{
		{"parent component null", obj(cty.NullVal(cty.String), cty.StringVal("1122334"))},
		{"parent component unknown", obj(cty.UnknownVal(cty.String), cty.StringVal("1122334"))},
		{"parent component empty", obj(cty.StringVal(""), cty.StringVal("1122334"))},
		{"parent component sensitive", obj(cty.StringVal("aabbccddee").Mark("sensitive"), cty.StringVal("1122334"))},
		{"leaf null", obj(cty.StringVal("aabbccddee"), cty.NullVal(cty.String))},
		{"leaf missing", cty.ObjectVal(map[string]cty.Value{"api_id": cty.StringVal("aabbccddee")})},
		{"not an object", cty.StringVal("aabbccddee/1122334")},
		{"null object", cty.NullVal(cty.Object(map[string]cty.Type{"api_id": cty.String, "id": cty.String}))},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := LocatedIdentity(tc.obj, components)
			if ok || got != nil {
				t.Errorf("LocatedIdentity = %v, %v; want nil, false.\nA record carrying SOME of a composite identity is worse than none: no record proposes a create, a partial one is imported as though it were complete.", got, ok)
			}
		})
	}
}
