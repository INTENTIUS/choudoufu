// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// These are issue #309's boundary, asserted from both sides.
//
// The widening in tools/row-gen/markerless.go put 18 untaggable types with a
// partly read-only primary identifier into [MarkerlessTypes], which is
// [LocatedType]'s first condition and therefore the door onto the record
// store. Most of them must NOT walk through it: their exported `id` is a
// server-minted leaf under a config-known parent, and recording it would
// trade an honest refusal for an import of a fragment on the next run.
//
// So the property under test is not "the veto widened" - that is visible in
// a generated file and proves nothing about behaviour. It is that the
// widened population splits exactly where the evidence splits it, and that
// each half's verdict comes from the rule it is supposed to come from.

// aMarkerlessTypeIn returns one member of [MarkerlessTypes] that no ratified
// row covers and that the doc-derived rule does (want=true) or does not
// (want=false) refuse, chosen deterministically.
func aMarkerlessTypeIn(t *testing.T, wantUnproven bool) string {
	t.Helper()
	var names []string
	for name := range MarkerlessTypes {
		if _, ratified := LookupType(name); ratified {
			continue
		}
		_, unproven := IDNotProvenWholeTypes[name]
		if unproven != wantUnproven {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		t.Fatalf("no unratified markerless type with IDNotProvenWholeTypes membership %v, so this assertion would be vacuous", wantUnproven)
	}
	sort.Strings(names)
	return names[0]
}

// TestLocatedTypeRefusesAnUnprovenCompositeID is the mutation check, run as
// a pair rather than as one assertion.
//
// A single "this type is refused" assertion cannot tell a refusal by the
// doc-derived rule from a refusal by any of [LocatedType]'s other
// conditions, and both subjects here are handed the SAME schema - clean, one
// string id, nothing sensitive - precisely so that the schema cannot be what
// separates them. The only difference between the two runs is membership in
// [IDNotProvenWholeTypes], so a difference in verdict is that membership and
// nothing else.
//
// If the doc rule were deleted, the first half fails. If the doc rule were
// widened to everything, the second half fails. That is the boundary.
func TestLocatedTypeRefusesAnUnprovenCompositeID(t *testing.T) {
	clean := locatedSchema(nil)

	unproven := aMarkerlessTypeIn(t, true)
	if LocatedType(unproven, map[string]providers.Schema{unproven: clean}) {
		t.Errorf("LocatedType(%q) = true. Its Import section documents a composite string and no source "+
			"corroborates that `id` is that whole string, so a located record of `id` may be a fragment. "+
			"Recording a fragment is a wrong identity, and a wrong identity is invisible until the NEXT "+
			"run's import fails.", unproven)
	}

	proven := aMarkerlessTypeIn(t, false)
	if !LocatedType(proven, map[string]providers.Schema{proven: clean}) {
		t.Errorf("LocatedType(%q) = false with the identical clean schema the refusal above was measured "+
			"against. That makes the refusal above evidence of nothing - some other condition is deciding "+
			"both verdicts and this test is measuring itself.", proven)
	}
}

// TestLocatedIdentityComponentsPrefersTheWireSchemaOverTheDocs pins the
// order of the two sources, which is the thing most likely to be got wrong
// by a later edit and the thing no aggregate would notice.
//
// A type can be in [IDNotProvenWholeTypes] - its page documents a composite
// import its `id` bullet does not corroborate - and ALSO carry a wire
// identity schema requiring `id` plus a parent. When both are true, #329's
// composite branch answers the question completely: the record carries the
// identity OBJECT, component by component, and the undecidable string is
// never consulted. Refusing such a type would withdraw a correct, fully
// recordable identity on the strength of a documentation ambiguity that no
// longer matters.
//
// Measured at the commit that widened the veto: 9 markerless types are in
// both sets, aws_api_gateway_resource among them - which is one of the 18
// the widening admitted, so this is the branch that carries the widening's
// only schema-backed gain.
func TestLocatedIdentityComponentsPrefersTheWireSchemaOverTheDocs(t *testing.T) {
	unproven := aMarkerlessTypeIn(t, true)

	// The same type, once with no wire identity schema and once with a
	// composite one. Nothing else differs.
	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":          {Type: cty.String, Computed: true},
		"rest_api_id": {Type: cty.String, Required: true},
	}}

	if _, recordable := LocatedIdentityComponents(unproven, providers.Schema{Block: block}); recordable {
		t.Errorf("%s: recordable = true with no wire identity schema. The docs are then the only source "+
			"and they do not settle whether `id` is the whole import string.", unproven)
	}

	withSchema := providers.Schema{Block: block, IdentitySchema: &configschema.Object{
		Nesting: configschema.NestingSingle,
		Attributes: map[string]*configschema.Attribute{
			"id":          {Type: cty.String, Required: true},
			"rest_api_id": {Type: cty.String, Required: true},
		},
	}}
	components, recordable := LocatedIdentityComponents(unproven, withSchema)
	if !recordable {
		t.Fatalf("%s: recordable = false even though the provider's own identity schema names every "+
			"component and the block carries all of them as strings. The doc rule is overriding the "+
			"stronger source.", unproven)
	}
	want := []string{"id", "rest_api_id"}
	if len(components) != len(want) || components[0] != want[0] || components[1] != want[1] {
		t.Errorf("%s: components = %v, want %v - the whole identity, not the undecidable string", unproven, components, want)
	}
}

// TestIDNotProvenWholeTypesIsAboutCompositeImportsOnly holds the set's own
// bound against the artifact it is derived from.
//
// The rule's population is types whose Import section documents a COMPOSITE
// string. A type with a flat documented import belongs nowhere near this
// set: `id` being the whole string is what flat means, and sweeping such a
// type in would withdraw a working located admission for no evidence at all.
// This is the "a mask wider than its label" check - it asks what the set
// bounds, not who is in it.
func TestIDNotProvenWholeTypesIsAboutCompositeImportsOnly(t *testing.T) {
	if len(IDNotProvenWholeTypes) == 0 {
		t.Fatal("IDNotProvenWholeTypes is empty, so every assertion that reads it passes vacuously")
	}
	// Every member must be absent from DefaultTable's *reason* to exist -
	// that is, membership must never be the thing that decides an ADMITTED
	// type's identity. LocatedType returns false for a ratified row before
	// it ever reaches the doc rule, and this is what keeps that true if the
	// order is ever rearranged.
	var admitted []string
	for name := range IDNotProvenWholeTypes {
		if _, ok := LookupType(name); ok {
			admitted = append(admitted, name)
		}
	}
	sort.Strings(admitted)
	if len(admitted) == 0 {
		t.Fatal("no member of IDNotProvenWholeTypes has a ratified row, so the guard below proves nothing " +
			"about the ordering it is written for. If the table really no longer overlaps this set, delete " +
			"this half rather than leaving a pin on nothing.")
	}
	clean := locatedSchema(nil)
	for _, name := range admitted {
		if LocatedType(name, map[string]providers.Schema{name: clean}) {
			t.Errorf("LocatedType(%q) = true for a type with a ratified row. A ratified row is admitted by "+
				"its own path and must never be re-routed through the record store.", name)
		}
	}
}
