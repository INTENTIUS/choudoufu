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

// ManifestLiveAttr is the computed attribute the provider reads the LIVE
// object back into, the other half of the shape [ManifestSurface] admits.
// It is named because it is load-bearing beyond the shape test: the
// provider's own ReadResource refuses a prior state that does not carry it
// ("Current state of resource has no 'object' attribute"), so anything
// building a prior for one of these types has to keep it - see
// internal/live/projection's residueStubIdentityAttrs.
const ManifestLiveAttr = "object"

// AnnotationSurfaceAttr is the other metadata map beside
// [LabelSurfaceAttr]. No marker is ever written to it - #1016's ruling is
// that the Kubernetes marker is the estate label alone - but the
// provider's computed_fields default governs it exactly as it governs
// labels, so the projection has to treat the two the same way when it
// builds a prior manifest.
const AnnotationSurfaceAttr = "annotations"

// ManifestComputedMetadataAttrs names the metadata maps the provider's own
// computed_fields default governs: ["metadata.annotations",
// "metadata.labels"] at hashicorp/kubernetes 3.2.1, which is what the
// resource uses when the argument is not set.
//
// The list is the DEFAULT, deliberately, and not read back out of a
// configured computed_fields. A configuration that sets the argument can
// name a path outside metadata (this list then says nothing about it, and
// GitHub issue #1177's fix does not reach it) or drop one of these two
// (the provider then diffs that map normally, and a prior built from the
// live object's own values for the declared keys is a better prior for an
// ordinary diff than one built from the configuration, not a worse one).
// Neither case makes the wider list wrong; both are recorded on
// mirrorManifestComputedFields in internal/live/projection.
var ManifestComputedMetadataAttrs = []string{LabelSurfaceAttr, AnnotationSurfaceAttr}

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
	object, ok := block.Attributes[ManifestLiveAttr]
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

// ManifestKey is the natural key GitHub issue #1016 ruled a Kubernetes
// object is bound by, as it is written inside a manifest-surface
// resource's own dynamic argument: apiVersion, kind, and metadata.name
// with metadata.namespace for a namespaced kind. It is the same four
// components internal/live/identity's synthesizeManifestIdentity reads
// out of the CONFIGURATION to render the provider's import id; this
// reads them out of an evaluated object, which is what a migration
// (GitHub issue #1109) has instead of a declaration.
type ManifestKey struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
}

// Complete reports whether the three components that are never optional
// were all found. A cluster-scoped kind has no namespace, so Namespace is
// not among them.
func (k ManifestKey) Complete() bool {
	return k.APIVersion != "" && k.Kind != "" && k.Name != ""
}

// ManifestKeyOf reads the natural key off a manifest-surface resource's
// whole object value, preferring the `manifest` argument - the operator's
// own declaration, which is what a stock state file records for it - and
// falling back to the computed `object` the provider read back, for a
// state that stores manifest as a typed null (which is what the provider
// hands back after an import, before any apply has run).
//
// The second return is false when neither attribute yields all three
// required components, which is the only condition a caller can act on:
// an object this pass cannot name cannot be found on the cluster either.
func ManifestKeyOf(obj cty.Value) (ManifestKey, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || obj.IsMarked() || !obj.Type().IsObjectType() {
		return ManifestKey{}, false
	}
	for _, attr := range []string{ManifestSurfaceAttr, "object"} {
		if !obj.Type().HasAttribute(attr) {
			continue
		}
		if k, ok := manifestKey(obj.GetAttr(attr)); ok {
			return k, true
		}
	}
	return ManifestKey{}, false
}

// manifestKey reads the four components off one evaluated manifest-shaped
// value.
func manifestKey(manifest cty.Value) (ManifestKey, bool) {
	if manifest.IsNull() || !manifest.IsKnown() || manifest.IsMarked() || !manifest.Type().IsObjectType() {
		return ManifestKey{}, false
	}
	str := func(v cty.Value, name string) string {
		if !v.Type().IsObjectType() || !v.Type().HasAttribute(name) {
			return ""
		}
		got := v.GetAttr(name)
		if got.IsNull() || !got.IsKnown() || got.IsMarked() || got.Type() != cty.String {
			return ""
		}
		return got.AsString()
	}
	key := ManifestKey{
		APIVersion: str(manifest, "apiVersion"),
		Kind:       str(manifest, "kind"),
	}
	if manifest.Type().HasAttribute(LabelSurfaceBlock) {
		meta := manifest.GetAttr(LabelSurfaceBlock)
		if !meta.IsNull() && meta.IsKnown() && !meta.IsMarked() && meta.Type().IsObjectType() {
			key.Name = str(meta, "name")
			key.Namespace = str(meta, "namespace")
		}
	}
	return key, key.Complete()
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
