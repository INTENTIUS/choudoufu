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
