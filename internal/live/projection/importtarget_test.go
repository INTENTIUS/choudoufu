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

// compositeSchema is a type whose identity takes two attributes and whose
// legacy import-ID grammar joins them with a separator no schema carries -
// the shape GitHub issue #105 is about.
func compositeSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{},
		IdentitySchema: &configschema.Object{
			Attributes: map[string]*configschema.Attribute{
				"cluster": {Type: cty.String, Required: true},
				"name":    {Type: cty.String, Required: true},
			},
			Nesting: configschema.NestingSingle,
		},
	}
}

// TestImportTargetUsesTheIdentityObjectForACompositeWithNoImportID is #105's
// happy path: the configuration supplies both attributes, the schema accepts
// them, and the import asks by identity object.
func TestImportTargetUsesTheIdentityObjectForACompositeWithNoImportID(t *testing.T) {
	target := importTarget(wanted{
		importID: "",
		values:   map[string]string{"cluster": "prod", "name": "svc"},
	}, compositeSchema())

	if !target.IsIdentityBased() {
		t.Fatalf("a composite with both identity attributes supplied was not imported by identity object: %+v", target)
	}
	got := target.Identity.GetAttr("cluster")
	if got.AsString() != "prod" {
		t.Errorf("cluster = %q, want \"prod\"", got.AsString())
	}
}

// TestImportTargetRefusesRatherThanApproximatingACompositeID is the guard
// #105 asks for, at the last place it can be checked.
//
// When the identity object cannot be built - here because the configuration
// supplies only one of the two required attributes - there is no string to
// fall back to, and the run must stop rather than import by something that
// looks plausible. Component values are concatenated with nothing between
// them, so the string this would otherwise have been is "prod": not empty,
// so every guard that tests for emptiness would have passed it through to a
// real account.
func TestImportTargetRefusesRatherThanApproximatingACompositeID(t *testing.T) {
	target := importTarget(wanted{
		importID: "",
		values:   map[string]string{"cluster": "prod"},
	}, compositeSchema())

	if target.IsIdentityBased() {
		t.Fatal("an identity object was built from half the required attributes")
	}
	if target.IsIDBased() {
		t.Fatalf("an import ID was invented for a type that has none: %q", target.ID)
	}

	// And importAndRead refuses such a target before it reaches a provider.
	_, _, diags := importAndRead(t.Context(), nil, compositeSchema(), "aws_composite_thing", target, "", cty.NilVal, false)
	if !diags.HasErrors() {
		t.Fatal("a target with neither an identity nor an ID was sent to the provider")
	}
	if got := diags[0].Description().Summary; got != "Empty import identity" {
		t.Errorf("refusal summary = %q, want \"Empty import identity\"", got)
	}
}

// TestImportTargetStillFallsBackForATypeThatHasAnImportID pins the other
// direction: #105 changed nothing for a type whose import ID is a real
// string. A failed identity build still drops to it, which is what every run
// did before any of this.
func TestImportTargetStillFallsBackForATypeThatHasAnImportID(t *testing.T) {
	target := importTarget(wanted{
		importID: "prod/svc",
		values:   map[string]string{"cluster": "prod"},
	}, compositeSchema())

	if !target.IsIDBased() || target.ID != "prod/svc" {
		t.Fatalf("the string fallback was lost: %+v", target)
	}
}
