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
	"github.com/intentius/choudoufu/internal/tofu"
)

// The server-set keys of an Optional+Computed metadata map (epic #1885,
// reference-k8s-workloads' test_plan).
//
// hashicorp/kubernetes declares kubernetes_job_v1's (and kubernetes_job's)
// metadata.labels Optional+Computed: the API server copies a Job's pod
// template labels onto the Job, and a configuration that declares no labels
// leaves them null, which the provider reads as "keep what the server set".
// Stock's replan is empty. The node stamp ([NodeResolver.stampedMetadata])
// writes tofu-estate into that null, so the configuration now names one
// label, and the provider plans the removal of every label the server set:
//
//	~ labels = {
//	    - "app" = "migrate" -> null
//
// on every plan, with or without a state file. A non-empty diff also makes
// the legacy SDK rebuild the planned object from its flatmap, which turns
// the prior's null spec.selector.match_labels into {} - the second line of
// the same measured plan - where an empty diff answers the prior unchanged.
//
// What a configuration that leaves an Optional+Computed map null claims is
// the server's keys, so this carries the prior object's keys into the
// stamped map for exactly that case: the attribute is Computed, the
// configuration as written left it null, and the stamp made it a value.
// The ownership markers themselves - the tofu-estate label and the address
// annotation - are never carried from the prior: the stamp alone decides
// them, so an untag still releases one. A map the configuration declares is
// left exactly as stamped, the way stock plans it.

var _ tofu.PriorConfigValueAdjuster = (*NodeResolver)(nil)

// AdjustConfigValueToPrior implements internal/tofu.PriorConfigValueAdjuster.
func (n *NodeResolver) AdjustConfigValueToPrior(_ context.Context, _ addrs.AbsResourceInstance, evaluated, adjusted, prior cty.Value, schema providers.Schema) cty.Value {
	if n.Estate == "" || schema.Block == nil {
		return adjusted
	}
	if _, ok := markers.LabelSurface(schema.Block); !ok {
		return adjusted
	}
	nested := schema.Block.BlockTypes[markers.LabelSurfaceBlock]
	if nested == nil {
		return adjusted
	}
	out := adjusted
	for _, name := range []string{markers.LabelSurfaceAttr, markers.AnnotationSurfaceAttr} {
		attr := nested.Block.Attributes[name]
		if attr == nil || !attr.Computed || !attr.Type.IsMapType() {
			continue
		}
		written, ok := soleMetadataLeaf(evaluated, name)
		if !ok || !written.IsNull() {
			continue
		}
		priorMap, ok := soleMetadataLeaf(prior, name)
		if !ok {
			continue
		}
		stamped, ok := soleMetadataLeaf(out, name)
		if !ok {
			continue
		}
		merged, changed := withServerKeys(stamped, priorMap)
		if !changed {
			continue
		}
		out = withMetadataLeaf(out, name, merged)
	}
	return out
}

// soleMetadataLeaf reads metadata[0].<name> off obj, unmarked at every level
// it passes through. false when obj is not that shape or any level is
// marked or unknown.
func soleMetadataLeaf(obj cty.Value, name string) (cty.Value, bool) {
	if obj == cty.NilVal || obj.IsMarked() || obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(markers.LabelSurfaceBlock) {
		return cty.NilVal, false
	}
	meta := obj.GetAttr(markers.LabelSurfaceBlock)
	if meta.IsMarked() || meta.IsNull() || !meta.IsKnown() || !meta.Type().IsListType() || meta.LengthInt() != 1 {
		return cty.NilVal, false
	}
	elem := meta.Index(cty.NumberIntVal(0))
	if elem.IsMarked() || elem.IsNull() || !elem.IsKnown() || !elem.Type().IsObjectType() || !elem.Type().HasAttribute(name) {
		return cty.NilVal, false
	}
	leaf := elem.GetAttr(name)
	if leaf.IsMarked() || !leaf.IsKnown() {
		return cty.NilVal, false
	}
	return leaf, true
}

// withServerKeys returns stamped with every key of prior it lacks added,
// except the ownership markers, which only the stamp writes. false when
// nothing was added, or either value is not a known string map.
func withServerKeys(stamped, prior cty.Value) (cty.Value, bool) {
	if stamped.IsMarked() || prior.IsMarked() || stamped.IsNull() || prior.IsNull() ||
		!stamped.Type().IsMapType() || !prior.Type().IsMapType() || !stamped.IsWhollyKnown() || !prior.IsWhollyKnown() {
		return stamped, false
	}
	elems := map[string]cty.Value{}
	for it := stamped.ElementIterator(); it.Next(); {
		k, v := it.Element()
		elems[k.AsString()] = v
	}
	added := false
	for it := prior.ElementIterator(); it.Next(); {
		k, v := it.Element()
		key := k.AsString()
		if key == markers.TagEstate || key == markers.AddressAnnotation {
			continue
		}
		if _, has := elems[key]; has {
			continue
		}
		if !v.Type().Equals(stamped.Type().ElementType()) {
			return stamped, false
		}
		elems[key] = v
		added = true
	}
	if !added {
		return stamped, false
	}
	return cty.MapVal(elems), true
}

// withMetadataLeaf returns obj with metadata[0].<name> set to v. obj is the
// shape [soleMetadataLeaf] already accepted.
func withMetadataLeaf(obj cty.Value, name string, v cty.Value) cty.Value {
	if obj.IsMarked() {
		return obj
	}
	meta := obj.GetAttr(markers.LabelSurfaceBlock)
	if meta.IsMarked() {
		return obj
	}
	elem := meta.Index(cty.NumberIntVal(0))
	if elem.IsMarked() {
		return obj
	}
	elemAttrs := elem.AsValueMap()
	elemAttrs[name] = v
	attrs := obj.AsValueMap()
	attrs[markers.LabelSurfaceBlock] = cty.ListVal([]cty.Value{cty.ObjectVal(elemAttrs)})
	return cty.ObjectVal(attrs)
}
