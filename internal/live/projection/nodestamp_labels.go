// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the Kubernetes half of the node-path stamp (GitHub issue
// #1061): the same seam [NodeResolver.AdjustConfigValue] uses to write two
// tags into an AWS resource's tags map, writing one label into a
// Kubernetes resource's metadata[0].labels. See [markers.LabelSurface] for
// why it is one label and not two, and what a label value may be.
//
// Everything the tag branch decides, this branch decides the same way,
// from the same fields: no estate name writes nothing; a strict { markers
// "record" } selection writes nothing and [NodeResolver.AdjustIgnoreChanges]
// protects the existing label instead; a policy untag of tofu-estate for
// this instance skips the write; a label the configuration already sets
// to another estate is the same fatal [SummaryMarkerConflict] the tag
// branch raises, word for word, because an operator must not be able to
// tell the two substrates' refusals apart. What differs is only the shape
// of the value being rebuilt: a one-element list holding an object whose
// labels attribute is the map, rather than the map itself.

// stampedMetadata returns metaVal - the evaluated metadata block, a
// one-element list of objects - with this instance's tofu-estate label
// added to its element's labels map, preserving every label the
// configuration already declares and the value's own marks.
func (n *NodeResolver) stampedMetadata(addr addrs.AbsResourceInstance, metaVal cty.Value) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if !markers.ValidLabelValue(n.Estate) {
		// Refused rather than written: the API server would reject the
		// create, or worse accept a truncated form some client silently
		// produced. The estate is legal; it is the substrate that cannot
		// carry it, and the message says which.
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryMarkerNotALabel,
			fmt.Sprintf("%s cannot carry this estate's ownership marker: %s. Rename the estate in the live block, or keep this resource out of a Kubernetes estate.", addr, markers.NotALabelValue(n.Estate))))
		return metaVal, diags
	}

	metaVal, metaMarks := metaVal.Unmark()

	switch {
	case metaVal.IsNull():
		// A metadata block is required by every label-surface schema
		// (MinItems 1), so a null here is a configuration the provider
		// itself will refuse; nothing for this pass to do.
		return metaVal.WithMarks(metaMarks), diags
	case !metaVal.IsKnown():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot set ownership markers on an unresolved metadata block",
			fmt.Sprintf("%s's metadata block is not yet known, so its ownership marker could not be set at the node; the configuration's own value is used unchanged.", addr)))
		return metaVal.WithMarks(metaMarks), diags
	case !metaVal.CanIterateElements() || metaVal.LengthInt() != 1:
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot merge ownership markers into this metadata block",
			fmt.Sprintf("%s's metadata block evaluated to %s rather than exactly one block; the configuration's own value is used unchanged.", addr, metaVal.Type().FriendlyName())))
		return metaVal.WithMarks(metaMarks), diags
	}

	it := metaVal.ElementIterator()
	it.Next()
	_, elem := it.Element()
	if elem.IsNull() || !elem.IsKnown() || !elem.Type().IsObjectType() || !elem.Type().HasAttribute(markers.LabelSurfaceAttr) {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot merge ownership markers into this metadata block",
			fmt.Sprintf("%s's metadata block has no labels attribute this pass can write into; the configuration's own value is used unchanged.", addr)))
		return metaVal.WithMarks(metaMarks), diags
	}
	if elem.IsMarked() {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot set ownership markers on a marked metadata block",
			fmt.Sprintf("%s's metadata block is marked as a whole, which this pass will not unmark; its ownership marker was left for an operator to write.", addr)))
		return metaVal.WithMarks(metaMarks), diags
	}

	labelsVal := elem.GetAttr(markers.LabelSurfaceAttr)
	labelsVal, labelMarks := labelsVal.Unmark()

	elems := map[string]cty.Value{}
	switch {
	case labelsVal.IsNull():
		// No labels in configuration: the marker is the whole map.
	case !labelsVal.IsKnown():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot set ownership markers on an unresolved labels value",
			fmt.Sprintf("%s's metadata.labels is not yet known, so its ownership marker could not be set at the node; the configuration's own value is used unchanged.", addr)))
		return metaVal.WithMarks(metaMarks), diags
	case !labelsVal.Type().IsMapType():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot merge ownership markers into this labels value",
			fmt.Sprintf("%s's metadata.labels evaluated to a %s, not a map; the configuration's own value is used unchanged.", addr, labelsVal.Type().FriendlyName())))
		return metaVal.WithMarks(metaMarks), diags
	case labelsVal.LengthInt() > 0:
		for lit := labelsVal.ElementIterator(); lit.Next(); {
			k, v := lit.Element()
			if v.Type() != cty.String && v.Type() != cty.DynamicPseudoType {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot merge ownership markers into this labels value",
					fmt.Sprintf("%s's metadata.labels holds a non-string value at key %q; the configuration's own value is used unchanged.", addr, k.AsString())))
				return metaVal.WithMarks(metaMarks), diags
			}
			elems[k.AsString()] = v
		}
	}

	untagKey := n.PolicyUntag[addr.String()]
	if untagKey != markers.TagEstate {
		diags = diags.Append(markerConflictDiag(addr, elems, markers.TagEstate, n.Estate))
		if diags.HasErrors() {
			return metaVal.WithMarks(metaMarks), diags
		}
		elems[markers.TagEstate] = cty.StringVal(n.Estate)
	}
	if len(elems) == 0 {
		// Only reachable under an untag of tofu-estate on a resource with
		// no labels of its own: nothing to write, nothing to change.
		return metaVal.WithMarks(metaMarks), diags
	}

	elemAttrs := elem.AsValueMap()
	elemAttrs[markers.LabelSurfaceAttr] = cty.MapVal(elems).WithMarks(labelMarks)
	return cty.ListVal([]cty.Value{cty.ObjectVal(elemAttrs)}).WithMarks(metaMarks), diags
}

// SummaryMarkerNotALabel names the fatal diagnostic raised when an estate
// name cannot be carried as a Kubernetes label value.
const SummaryMarkerNotALabel = "Ownership marker is not a legal label value"
