// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
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

func (aws) OwnershipSurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if markers.HasTagsAttribute(block) {
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
// (internal/live/liveimport's tags.go, internal/live/mv's rewrite.go).
func (aws) Writes(surface markers.Surface) Writes {
	if surface == markers.SurfaceTags {
		return Writes{Create: WriteInCreate, Adopt: WriteTagsPlan}
	}
	return Writes{}
}

func (aws) CarriesAddress() bool { return true }

func (aws) Sweep() Sweep { return SweepTaggingIndex }

func (aws) NewSweeper(cty.Value, bool) (*kubesweep.Client, error) { return nil, nil }
