// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package markerstest holds the two resource schemas every package that
// decides "may a marker be written here" has to agree about, so that the
// packages asserting it do not each rebuild the block and thereby rebuild the
// disagreement the assertion exists to catch.
//
// It exists because [github.com/intentius/choudoufu/internal/live/markers].Taggable
// had four copies. internal/live/liveimport, internal/live/mv and
// internal/live/untag each wrote out the same shape test - a top-level,
// settable "tags" attribute typed map(string) - each with a comment saying it
// matched the others. When GitHub issue #243 added a fifth clause to
// [markers.TagSurface] (VocabularyRefusal: a tags map whose keys the provider
// has documented as its own namespace is not a marker surface), stamping
// stopped writing markers into those maps and all three copies did not.
// All three of those packages WRITE - liveimport stamps an adopted object,
// mv rewrites an address on a live one, untag strips a marker off - so the
// divergence was a wrong marker on a real object, not a missing one.
//
// The three now call markers.Taggable. These blocks are what keeps them
// calling it: a test that only pinned the shape would pass against a
// re-inlined four-clause copy.
package markerstest

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// VocabularyRefusedBlock has a tags attribute of exactly the shape the four
// copies tested for, and a description saying its keys are the provider's own
// namespace. markers.Taggable refuses it; every four-clause copy admits it.
//
// The description is hashicorp/google's own phrasing for a resource-manager
// tag binding, which is the case issue #243 was written against.
func VocabularyRefusedBlock() *configschema.Block {
	return &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"tags": {
				Type:     cty.Map(cty.String),
				Optional: true,
				Description: "A map of resource manager tags. Resource manager tag keys " +
					"and values have the same definition as resource manager tags. " +
					"Keys must be in the format tagKeys/{tag_key_id}, and values are " +
					"in the format tagValues/456.",
			},
		},
	}
}

// FreeFormTagsBlock is the same shape with nothing said about its keys: the
// ordinary AWS-style tags map, which every predicate must still admit. It
// pairs with [VocabularyRefusedBlock] so that a package asserting the refusal
// cannot pass by refusing everything.
func FreeFormTagsBlock() *configschema.Block {
	return &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"tags": {
				Type:     cty.Map(cty.String),
				Optional: true,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// The synthetic tags-only configuration (GitHub issue #373)
// ---------------------------------------------------------------------------

// TagsOnlyConfigBlock is the second shape the three writing packages have to
// agree about, for the same reason as the first: each of internal/live/liveimport,
// internal/live/mv and internal/live/untag carries its own copy of the
// configValue helper that turns a live object into the configuration a
// tags-only write pretends to have been given, and the three used to be
// character-identical only by convention.
//
// Issue #373 changed all three at once - an optional+computed attribute is
// now null in that synthetic config, not carried across from the read -
// because a provider that gates a CustomizeDiff on "is this argument set in
// the raw config" refuses a tag write that never touched the argument. A copy
// left behind would refuse where the other two proceed, on the same estate,
// depending only on which verb the operator ran.
//
// One attribute of every kind that decides something:
//
//   - id: computed-only, never settable, null in the config either way;
//   - cidr_block: required, always carried;
//   - description: optional and NOT computed, always carried - nulling it
//     would propose a removal, since objchange takes a non-computed
//     attribute's config value verbatim;
//   - connectivity_type, secondary_count: optional AND computed, the class
//     issue #373 is about;
//   - routing: optional+computed with a NestedType, the one exception -
//     recursed into rather than nulled, because objchange reads a null config
//     there as a deliberate removal;
//   - tags: the argument the write asserts, never nulled.
func TagsOnlyConfigBlock() *configschema.Block {
	return &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":                {Type: cty.String, Computed: true},
		"cidr_block":        {Type: cty.String, Required: true},
		"description":       {Type: cty.String, Optional: true},
		"connectivity_type": {Type: cty.String, Optional: true, Computed: true},
		"secondary_count":   {Type: cty.Number, Optional: true, Computed: true},
		"tags":              {Type: cty.Map(cty.String), Optional: true},
		"routing": {
			Optional: true,
			Computed: true,
			NestedType: &configschema.Object{
				Nesting: configschema.NestingSingle,
				Attributes: map[string]*configschema.Attribute{
					"mode":     {Type: cty.String, Optional: true},
					"resolved": {Type: cty.String, Optional: true, Computed: true},
				},
			},
		},
	}}
}

// TagsOnlyConfigObject is a fully-populated read of a [TagsOnlyConfigBlock]
// resource - the way an accurate provider read answers, which is the
// condition issue #373 needs to reproduce at all. Its predecessor hid the bug
// for months by returning an almost-empty object.
func TagsOnlyConfigObject() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":                cty.StringVal("nat-0abc"),
		"cidr_block":        cty.StringVal("10.0.0.0/16"),
		"description":       cty.StringVal("written by hand"),
		"connectivity_type": cty.StringVal("public"),
		"secondary_count":   cty.NumberIntVal(0),
		"tags":              cty.MapVal(map[string]cty.Value{"Name": cty.StringVal("gw")}),
		"routing": cty.ObjectVal(map[string]cty.Value{
			"mode":     cty.StringVal("weighted"),
			"resolved": cty.StringVal("filled in by the provider"),
		}),
	})
}

// TagsOnlyConfigNulled names the top-level attributes the claim that asserts
// least leaves null: every Computed attribute, whether or not a configuration
// could also have set it. Stated as data so all three packages assert the
// same answer rather than three restatements of it.
func TagsOnlyConfigNulled() []string {
	return []string{"id", "connectivity_type", "secondary_count"}
}

// TagsOnlyConfigProviderOnly names the subset of [TagsOnlyConfigNulled] that
// is null under BOTH claims: Computed and not settable at all, which no
// configuration can have written. The pre-#373 rule and the post-#373 one
// agree about exactly these.
func TagsOnlyConfigProviderOnly() []string {
	return []string{"id"}
}

// TagsOnlyConfigOptionalComputed is the rest of [TagsOnlyConfigNulled]: the
// optional+computed attributes, null under the claim that asserts least and
// carried across under the one that asserts most. The disagreement between
// the two claims is exactly this list.
func TagsOnlyConfigOptionalComputed() []string {
	return []string{"connectivity_type", "secondary_count"}
}

// TagsOnlyConfigCarried names what every claim carries across unchanged: the
// tag map being written, and the arguments only a configuration can supply.
func TagsOnlyConfigCarried() []string {
	return []string{"cidr_block", "description", "tags"}
}

// FreeFormTagsObject is a live read of a [FreeFormTagsBlock] resource. It
// pairs with that block for the case a tags-only write treats specially: a
// type with no optional+computed attribute at all, where the two claims
// produce the same configuration and there is nothing to fall back to.
func FreeFormTagsObject() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"tags": cty.MapVal(map[string]cty.Value{"Name": cty.StringVal("gw")}),
	})
}
