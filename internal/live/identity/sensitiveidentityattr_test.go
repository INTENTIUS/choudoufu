// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is GitHub issue #746's review finding B4: internal/live/
// projection's write-back printed one line for two different situations -
// an identity this fork structurally cannot record, and the deliberate
// refusal to put a sensitive attribute in the record store - which is the
// exact distinction the branch's own comment says must never blur.
// [SensitiveIdentityAttr] is what tells them apart, so it is what has to be
// right, and it is asserted here on the RENDERED attribute name rather than
// on a boolean.

// TestSensitiveIdentityAttr_namesOnlyTheDeliberateRefusal asserts the two
// answers by value on a type with no ratified table row, so the wire-schema
// branch is the one under test in both directions.
func TestSensitiveIdentityAttr_namesOnlyTheDeliberateRefusal(t *testing.T) {
	const typeName = "aws_choudoufu_test_no_such_type"
	if _, ratified := LookupType(typeName); ratified {
		t.Fatalf("%s has a ratified row, so this test would measure the ratified branch instead of the schema one", typeName)
	}

	t.Run("a sensitive id is named", func(t *testing.T) {
		schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true, Sensitive: true},
		}}}
		if got := SensitiveIdentityAttr(typeName, schema); got != "id" {
			t.Errorf("SensitiveIdentityAttr = %q, want %q: the whole identity a record would hold is sensitive, which is the deliberate refusal and must be reported as one", got, "id")
		}
	})

	t.Run("a sensitive attribute outside the recorded identity is not the reason", func(t *testing.T) {
		// The same narrowing sensitiveIdentityAttr itself makes (#365
		// population 2): the question is whether the RECORD would carry a
		// secret, not whether the type has one anywhere. Reporting this as
		// a deliberate refusal would tell an operator their secrets
		// setting is why nothing was recorded, which is false.
		schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":     {Type: cty.String, Computed: true},
			"secret": {Type: cty.String, Computed: true, Sensitive: true},
		}}}
		if got := SensitiveIdentityAttr(typeName, schema); got != "" {
			t.Errorf("SensitiveIdentityAttr = %q, want \"\": %q is not part of the identity a record would hold, so sensitivity is not why one was not written", got, got)
		}
	})

	t.Run("a structurally unrecordable identity is not reported as deliberate", func(t *testing.T) {
		// No string "id" at all: LocatedIdentityPlanFor refuses, and with
		// no ratified row there is no components route either. The refusal
		// is structural, and the safe direction is to say so.
		schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"arn": {Type: cty.String, Computed: true},
		}}}
		if got := SensitiveIdentityAttr(typeName, schema); got != "" {
			t.Errorf("SensitiveIdentityAttr = %q, want \"\" for an identity nothing could record either way", got)
		}
	})

	t.Run("no schema block answers structural", func(t *testing.T) {
		if got := SensitiveIdentityAttr(typeName, providers.Schema{}); got != "" {
			t.Errorf("SensitiveIdentityAttr = %q, want \"\" when there is no schema to read sensitivity off at all", got)
		}
	})
}

// TestSensitiveIdentityAttr_readsTheRatifiedComponentsRouteToo covers the
// third branch: the wire-schema route is unusable, so
// [projection.LocatedRecordFrom] falls to the ratified-components route, and
// that route's own sensitivity question ([SensitiveComponentsAttr]) is the
// one that decides. Asserted against a real ratified row rather than an
// invented one, so the branch is exercised the way a run reaches it.
func TestSensitiveIdentityAttr_readsTheRatifiedComponentsRouteToo(t *testing.T) {
	typeName, attr := aRatifiedComponentAttr(t)

	// No "id" on the block, so LocatedIdentityPlanFor's default branch
	// refuses and the ratified route is the one asked. The component
	// attribute is present and sensitive.
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		attr: {Type: cty.String, Required: true, Sensitive: true},
	}}}
	if got := SensitiveIdentityAttr(typeName, schema); got != attr {
		t.Errorf("SensitiveIdentityAttr(%q) = %q, want %q: the ratified components route is what LocatedRecordFrom falls to here, and its refusal is the deliberate one", typeName, got, attr)
	}

	// The same row with the same attribute NOT sensitive: structural, not
	// deliberate. Without this half the assertion above would pass for a
	// function that returned the first component name unconditionally.
	plain := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		attr: {Type: cty.String, Required: true},
	}}}
	if got := SensitiveIdentityAttr(typeName, plain); got != "" {
		t.Errorf("SensitiveIdentityAttr(%q) = %q for a component the provider does not mark sensitive, want \"\"", typeName, got)
	}
}

// aRatifiedComponentAttr returns one ratified type that is not
// ServerAssigned and whose first component reads exactly one attribute, plus
// that attribute's name - chosen deterministically so a failure names the
// same subject twice running. No resource type name is written down here;
// the table is asked for one.
func aRatifiedComponentAttr(t *testing.T) (typeName, attr string) {
	t.Helper()
	best := ""
	bestAttr := ""
	for name, ti := range DefaultTable {
		if ti.ServerAssigned || ti.RecordBacked || len(ti.Components) == 0 {
			continue
		}
		c := ti.Components[0]
		if len(c.Attrs) != 1 || c.Block != "" {
			continue
		}
		if best == "" || name < best {
			best, bestAttr = name, c.Attrs[0]
		}
	}
	if best == "" {
		t.Fatal("no ratified row with a single-attribute first component; every assertion below would be vacuous")
	}
	return best, bestAttr
}
