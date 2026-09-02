// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is GitHub issue #401 family 1's writer half: a type whose wire
// identity schema names nothing better than the bare "id" default
// ([LocatedRecordFrom]'s own default branch, reached because
// aws_acm_certificate_validation has no identity schema at all) can still
// carry a schema-fallback-synthesized identity
// ([identity.SynthesizeTypeIdentity] - the same admission ratify.go's
// admittedByProviderSchema and identity.Derivable already grant it). Before
// this, LocatedRecordFrom wrote ONLY the bare id, and a later record-first
// stub (materializeFromRecord) could never place anything under
// certificate_arn - the one identity attribute the provider's own
// ImportResourceState stub would have carried, had the type implemented
// one. certificateValidationSchema() (noimporter_test.go, same package) is
// reused rather than duplicated: it is the type's real 6.59.0 schema, read
// against a real cold-deployed corpus-alb-complete estate.

// TestLocatedRecordFromCapturesSchemaFallbackComponentsForNoImporterTypes is
// the ordinary case: the real corpus values (a real aws_acm_certificate_
// validation instance this unit's own migrate run read from floci) produce
// a record carrying BOTH the bare id (unchanged from before this unit) AND
// the schema-fallback-derived certificate_arn component - "alongside", not
// "instead of", which is the whole point: the bare id is still what a
// genuine ReadResource PriorState needs, and certificate_arn is what
// SynthesizeStub can now also place.
func TestLocatedRecordFromCapturesSchemaFallbackComponentsForNoImporterTypes(t *testing.T) {
	const arn = "arn:aws:acm:eu-west-1:000000000000:certificate/35f61756-c1d3-4d41-a96c-fa5bf5ae221c"
	const importID = "2026-08-24 22:42:29.573 +0000 UTC"
	const fqdn = "_414e175cf70d8016c7341240b11167af.terraform-aws-modules.modules.tf"

	obj := cty.ObjectVal(map[string]cty.Value{
		"certificate_arn":         cty.StringVal(arn),
		"validation_record_fqdns": cty.SetVal([]cty.Value{cty.StringVal(fqdn)}),
		"id":                      cty.StringVal(importID),
	})

	rec, ok := LocatedRecordFrom("aws_acm_certificate_validation", certificateValidationSchema(), obj)
	if !ok {
		t.Fatal("LocatedRecordFrom refused an instance with a real, present certificate_arn and id")
	}
	if rec.ImportID != importID {
		t.Errorf("ImportID = %q, want %q - the bare id default must survive this change untouched", rec.ImportID, importID)
	}
	wantComponents := map[string]string{"certificate_arn": arn}
	if len(rec.Components) != len(wantComponents) || rec.Components["certificate_arn"] != arn {
		t.Errorf("Components = %#v, want %#v - the schema-fallback identity's own resolved value, read off the same real object", rec.Components, wantComponents)
	}
}

// TestLocatedRecordFromBareIDDefaultUnaffectedWhenNoSchemaFallbackApplies is
// the mutation/boundary check that proves the enrichment above is gated,
// not unconditional: a resource type with no ratified row, no wire identity
// schema and no schema-fallback admission either (this fixture's own
// "test_no_fallback_type" is refused by [identity.SynthesizeTypeIdentity]
// because the provider serves it no schema at all) must still record
// exactly what it always did - the bare id, and nothing under Components -
// proving the family-1 addition never fires for a type it cannot honestly
// answer for.
func TestLocatedRecordFromBareIDDefaultUnaffectedWhenNoSchemaFallbackApplies(t *testing.T) {
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name": {Type: cty.String, Optional: true, Computed: true},
		"id":   {Type: cty.String, Computed: true},
	}}}
	obj := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("whatever"),
		"id":   cty.StringVal("bare-id-only"),
	})

	// this test's own provider schema map deliberately excludes the type
	// name, so SynthesizeTypeIdentity's "provider serves no schema at all"
	// refusal fires - the boundary this test exists to pin.
	rec, ok := LocatedRecordFrom("test_no_fallback_type", schema, obj)
	if !ok {
		t.Fatal("LocatedRecordFrom refused an instance with a plain id attribute present - the ordinary bare-id default must still succeed")
	}
	if rec.ImportID != "bare-id-only" {
		t.Errorf("ImportID = %q, want %q", rec.ImportID, "bare-id-only")
	}
	if len(rec.Components) != 0 {
		t.Errorf("Components = %#v, want empty - no schema-fallback identity was admitted for this type, so nothing should have been added", rec.Components)
	}
}

// TestLocatedRecordFromSchemaFallbackComponentsRefusesASensitiveComponent is
// the sensitivity boundary [identity.SensitiveComponentsAttr] exists for,
// asked here exactly as [locatedRatifiedComponentsRecord]'s own equivalent
// test already asks it: a schema-fallback identity that would read a
// Sensitive, non-Deprecated schema attribute must never be recorded, even
// though the bare id itself carries nothing sensitive and is still written.
// There is no real aws_* type sharing certificate_arn's exact shape with a
// Sensitive flag, so this is a hand-built fixture proving the gate fires,
// not a real-world regression pin.
func TestLocatedRecordFromSchemaFallbackComponentsRefusesASensitiveComponent(t *testing.T) {
	schema := providers.Schema{
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"secret_arn": {Type: cty.String, Required: true, Sensitive: true},
			"id":         {Type: cty.String, Computed: true},
		}},
		// A real identity schema naming secret_arn as the sole required
		// identity attribute - the same shape certificateValidationSchema()
		// carries for certificate_arn - so identity.SynthesizeTypeIdentity
		// actually admits the type and produces a component to check,
		// rather than refusing for the unrelated reason of having no
		// identity schema at all. That is what makes this test exercise
		// the SENSITIVITY gate specifically, not merely "some gate fired".
		IdentitySchema: &configschema.Object{
			Nesting:    configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{"secret_arn": {Type: cty.String, Required: true}},
		},
	}
	obj := cty.ObjectVal(map[string]cty.Value{
		"secret_arn": cty.StringVal("arn:aws:secret:1"),
		"id":         cty.StringVal("bare-id-only"),
	})

	comps, ok := schemaFallbackComponentsRecord("test_sensitive_fallback_type", schema, obj)
	if ok {
		t.Fatalf("schemaFallbackComponentsRecord recorded %#v for a Sensitive identity attribute - it must refuse, the same rule locatedRatifiedComponentsRecord already holds", comps)
	}
}
