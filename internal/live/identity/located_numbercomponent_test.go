// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestLocatedIdentityPlanNumberComponent is GitHub issue #671's pin: an
// identity component the resource block carries as a NUMBER (an ECS task
// definition's revision) must not refuse the whole record. Before the fix
// the required-component loop admitted only cty.String while the
// optional-component reader one branch over accepted numbers, and the
// write-back skipped the type's record silently - 78 of 79 on the
// terralith, invisible unless you counted.
func TestLocatedIdentityPlanNumberComponent(t *testing.T) {
	schema := providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":       {Type: cty.String, Computed: true},
				"family":   {Type: cty.String, Required: true},
				"revision": {Type: cty.Number, Computed: true},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"family":   {Type: cty.String, Required: true},
				"revision": {Type: cty.Number, Required: true},
			},
		},
	}

	plan, recordable := LocatedIdentityPlanFor("aws_ecs_task_definition", schema)
	if !recordable {
		t.Fatal("a composite identity with a number component is refused - issue #671's silent-skip shape is back")
	}
	if !plan.Composite() {
		t.Fatalf("expected a composite plan, got %+v", plan)
	}

	obj := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("probe671"),
		"family":   cty.StringVal("probe671"),
		"revision": cty.NumberIntVal(7),
	})
	got, ok := LocatedIdentity(obj, plan.Components)
	if !ok {
		t.Fatal("LocatedIdentity refused the object")
	}
	if got["family"] != "probe671" || got["revision"] != "7" {
		t.Errorf("components = %v, want family=probe671 revision=7 (plain decimal digits)", got)
	}

	// The red half: a fractional number is a shape no import round-trip has
	// ever verified, so the reader refuses it and the record stays unwritten
	// rather than guessed.
	frac := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("probe671"),
		"family":   cty.StringVal("probe671"),
		"revision": cty.NumberFloatVal(7.5),
	})
	if _, ok := LocatedIdentity(frac, plan.Components); ok {
		t.Error("a fractional component was rendered - the integral gate is gone")
	}
}
