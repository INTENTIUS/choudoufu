// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is GitHub issue #451's port of internal/live/stamp's #380 fix
// into the node seam: a strict { markers "record" }-selected instance's
// EXISTING live tofu-estate/tofu-address tags must survive a plan that no
// longer declares them, because [NodeResolver.AdjustConfigValue]
// (nodestamp.go) writes nothing for such an instance on purpose - see that
// file's own doc comment for why, and internal/live/stamp/stamp.go's
// SkipMarkersRecord for the HCL-path sibling this mirrors.
//
// The HCL path achieves this by rewriting the resource's own
// configs.Resource.Managed.IgnoreChanges before the graph walk ever
// starts, so that n.processIgnoreChanges (internal/tofu/
// node_resource_abstract_instance.go) reverts tags["tofu-estate"] and
// tags["tofu-address"] back to whatever the PRIOR state already had for
// them, regardless of what the (markers-withheld) configuration value
// says. AdjustConfigValue cannot reach that field - see nodestamp.go's own
// doc comment on why, and tofu.ConfigValueAdjuster's interface contract in
// internal/tofu/resource_identity.go - so this uses the SEPARATE hook that
// contract's doc comment names: tofu.IgnoreChangesAdjuster, an optional
// capability the same *NodeResolver checked with a type assertion at the
// one place internal/tofu already reads n.Config.Managed.IgnoreChanges
// from. See TestLivePlan_markersRecordPreservesExistingMarker_NodeResolve
// in internal/command/live_plan_test.go for the by-value proof, with
// internal/live/stamp gated off entirely.

// AdjustIgnoreChanges implements internal/tofu.IgnoreChangesAdjuster.
//
// It returns the two marker-tag paths exactly when addr is
// [NodeResolver.recordSelected] - the identical verdict
// AdjustConfigValue's own record-selection branch reaches for the same
// instance, from the same two fields (Estate is not consulted here: an
// instance whose ownership marker is withheld by selection is protected
// regardless of whether this run even has an estate name to write with,
// because there is nothing here that WOULD write one either way - the
// point is only to stop something ELSE from planning the existing tags
// away). A type with no tag surface at all, or an instance this run does
// not select, gets nil: nothing to protect, and nothing this pass would
// ever have withheld from it in the first place.
func (n *NodeResolver) AdjustIgnoreChanges(_ context.Context, addr addrs.AbsResourceInstance, schema providers.Schema) []cty.Path {
	if schema.Block == nil {
		return nil
	}
	if _, taggable := markers.TagSurface(schema.Block); !taggable {
		return nil
	}
	if !n.recordSelected(addr, schema) {
		return nil
	}
	return []cty.Path{
		markerTagPath(markers.TagEstate),
		markerTagPath(markers.TagAddress),
	}
}

// markerTagPath is the cty.Path for tags["<key>"], the shape
// n.processIgnoreChanges' traversalsToPaths already produces for an
// operator's own hand-written `ignore_changes = [tags["<key>"]]` - built
// directly as a path here, with no HCL traversal in between, because
// there is no HCL body on this side of the seam to build one from.
func markerTagPath(key string) cty.Path {
	return cty.Path{
		cty.GetAttrStep{Name: tagsArgumentName},
		cty.IndexStep{Key: cty.StringVal(key)},
	}
}
