// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// AWS is the hashicorp/aws family: tofu-estate and tofu-address in a
// settable top-level tags map.
var AWS Substrate = aws{}

type aws struct{}

func (aws) Name() string { return "aws" }

func (aws) Surfaces() []markers.Surface { return []markers.Surface{markers.SurfaceTags} }

func (aws) SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if markers.Taggable(block) {
		return markers.SurfaceTags, true
	}
	return "", false
}

func (aws) MarkersOf(surface markers.Surface, obj cty.Value) (map[string]string, bool) {
	if surface == markers.SurfaceTags {
		return markers.TagsOf(obj)
	}
	return nil, false
}

// Writes: the tags map is set in the create call, and an existing object's
// is rewritten by a plan-then-apply that may change nothing else
// (internal/live/liveimport's tags.go, internal/live/mv's rewrite.go). A
// type whose create call cannot carry tags is marked through the Tagging
// API once the create returns (GitHub issue #1084, #1587).
func (aws) Writes(surface markers.Surface) Writes {
	if surface == markers.SurfaceTags {
		return Writes{Create: WriteInCreate, Adopt: WriteTagsPlan, PostCreate: WriteTaggingAPI}
	}
	return Writes{}
}

func (aws) CarriesAddress() bool { return true }

func (aws) Sweep() Sweep { return SweepTaggingIndex }

func (aws) NewSweeper(cty.Value, bool) (Sweeper, error) { return nil, nil }

// ---- GitHub issue #1584: the answers the projection's shadow enum held ----

// CreateCollidesOnKey is false: AWS is out of scope of #1546's ruling.
// There a create of an existing object often succeeds, renames or is
// idempotent rather than conflicting, and that is per type and unmeasured.
func (aws) CreateCollidesOnKey(markers.Surface) bool { return false }

// CarrierPhrase: the wording the ownership read used before a second
// surface existed, unchanged.
func (aws) CarrierPhrase(surface markers.Surface) string {
	if surface == markers.SurfaceTags {
		return "tags attribute"
	}
	return ""
}

// NotACarrier is [markers.NotAMarkerSurface]: no settable tags map, or one
// whose keys the marker vocabulary cannot round-trip.
func (aws) NotACarrier(block *configschema.Block, typeName string) string {
	return markers.NotAMarkerSurface(block, typeName)
}

// ---- GitHub issue #1587: the post-create marker write ----

// MarkerWriter is [WriteTaggingAPI] for every AWS provider configuration:
// internal/command builds the Tagging API client signed as that
// configuration's own principal.
func (aws) MarkerWriter(addrs.AbsProviderConfig) Write { return WriteTaggingAPI }

// ---- GitHub issue #1649: the carrier's wholly-known read ----

// CarrierPaths: both maps [markers.TagsOf] reads.
func (aws) CarrierPaths(surface markers.Surface) []cty.Path {
	if surface == markers.SurfaceTags {
		return []cty.Path{cty.GetAttrPath("tags"), cty.GetAttrPath("tags_all")}
	}
	return nil
}

func (aws) MarkerNoun(surface markers.Surface) string {
	if surface == markers.SurfaceTags {
		return "tag"
	}
	return ""
}
