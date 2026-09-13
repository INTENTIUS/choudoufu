// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// This file is the third shape a marker can be carried in (GitHub issue
// #1079, ruled 2026-09-12), beside the AWS tag map [TagSurface] and the
// Kubernetes metadata block [LabelSurface]: a resource whose whole object
// is one dynamic `manifest` argument - hashicorp/kubernetes's
// kubernetes_manifest, the type every custom resource is declared through.
// The marker is the same one label [LabelSurface] writes, [TagEstate], and
// it goes to the same place on the live object, metadata.labels; what
// differs is only where that map sits in the configuration value: inside
// the manifest's own object constructor, at manifest.metadata.labels,
// rather than in a metadata block the schema types.
//
// Two consequences of the argument being dynamic, both of which the stamp
// in internal/live/projection (nodestamp_manifest.go) lives with rather
// than fights. First, an object constructor evaluates to an OBJECT type,
// not a map: `labels = { app = "web" }` is cty.Object({app: string}), and a
// labels value handed to the stamp is one or the other depending on how the
// operator wrote it (a map only through an explicit tomap() or a map-typed
// variable). Second, the provider's own drift rule for the field: its
// computed_fields default names metadata.labels, so a label the API server
// or a controller adds never churns the plan - and neither does one it
// removes, which is why the projection mirrors the live object's marker
// into the prior state it builds (see the projection's own doc comment on
// that) rather than relying on the provider to notice a stripped label.

// ManifestSurfaceAttr is the one attribute a manifest-surface resource
// carries its whole object in.
const ManifestSurfaceAttr = "manifest"

// ManifestSurface reports whether a resource type carries its marker inside
// a dynamic manifest argument: a required `manifest` of dynamic type, a
// computed `object` of dynamic type the provider reads back, and no
// metadata block of its own (that would be [LabelSurface]). It is read from
// the schema, never from a list of type names, for the same reason
// [Taggable] and [LabelSurface] are; hashicorp/kubernetes's
// kubernetes_manifest is the one type with this shape at 3.2.1, and
// internal/live/identity admits the same shape for identity resolution
// (identity.ManifestShape is this function).
//
// A type that is [Taggable] or a [LabelSurface] is never also a manifest
// surface, by construction: those two require a tags map or a metadata
// block, and this one refuses both.
func ManifestSurface(block *configschema.Block) bool {
	if block == nil {
		return false
	}
	if _, taggable := TagSurface(block); taggable {
		return false
	}
	if _, ok := block.BlockTypes[LabelSurfaceBlock]; ok {
		return false
	}
	manifest, ok := block.Attributes[ManifestSurfaceAttr]
	if !ok || manifest == nil || !manifest.Required || manifest.Type != cty.DynamicPseudoType {
		return false
	}
	object, ok := block.Attributes["object"]
	if !ok || object == nil || !object.Computed || object.Type != cty.DynamicPseudoType {
		return false
	}
	return true
}

// ManifestLabelPath is the cty.Path of one label key on a manifest-surface
// resource: manifest.metadata.labels["<key>"], the path an operator's own
// `ignore_changes = [manifest.metadata.labels["<key>"]]` would name.
func ManifestLabelPath(key string) cty.Path {
	return cty.Path{
		cty.GetAttrStep{Name: ManifestSurfaceAttr},
		cty.GetAttrStep{Name: LabelSurfaceBlock},
		cty.GetAttrStep{Name: LabelSurfaceAttr},
		cty.IndexStep{Key: cty.StringVal(key)},
	}
}

// ManifestLabelsOf reads the labels map off a manifest-surface resource's
// configuration or planned value: manifest.metadata.labels, the sibling of
// [LabelsOf] for the manifest shape. Because the argument is dynamic the
// labels may be an object (an object constructor's own type) or a map; both
// are read. The second return distinguishes "no manifest, or no
// metadata.labels inside it, this function can read" from "the object
// carries no labels".
func ManifestLabelsOf(obj cty.Value) (map[string]string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || obj.IsMarked() || !obj.Type().IsObjectType() {
		return nil, false
	}
	if !obj.Type().HasAttribute(ManifestSurfaceAttr) {
		return nil, false
	}
	return manifestLabels(obj.GetAttr(ManifestSurfaceAttr))
}

// manifestLabels reads metadata.labels off an evaluated manifest value.
func manifestLabels(manifest cty.Value) (map[string]string, bool) {
	if manifest.IsNull() || !manifest.IsKnown() || manifest.IsMarked() || !manifest.Type().IsObjectType() {
		return nil, false
	}
	if !manifest.Type().HasAttribute(LabelSurfaceBlock) {
		return nil, false
	}
	meta := manifest.GetAttr(LabelSurfaceBlock)
	if meta.IsNull() || !meta.IsKnown() || meta.IsMarked() || !meta.Type().IsObjectType() {
		return nil, false
	}
	out := map[string]string{}
	if !meta.Type().HasAttribute(LabelSurfaceAttr) {
		return out, true
	}
	labels := meta.GetAttr(LabelSurfaceAttr)
	if labels.IsNull() || !labels.IsKnown() {
		return out, true
	}
	if labels.IsMarked() || !labels.CanIterateElements() {
		return nil, false
	}
	for lit := labels.ElementIterator(); lit.Next(); {
		k, v := lit.Element()
		if k.Type() != cty.String || k.IsNull() || v.IsNull() || !v.IsKnown() || v.IsMarked() || v.Type() != cty.String {
			continue
		}
		out[k.AsString()] = v.AsString()
	}
	return out, true
}
