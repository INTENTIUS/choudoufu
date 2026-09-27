// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"github.com/zclconf/go-cty/cty"
)

// This file is the Kubernetes address annotation (GitHub issue #1639, step
// 1 of #1605's ruling of 2026-09-26): every Kubernetes object choudoufu
// creates, adopts or renames carries its block address in
// metadata.annotations[AddressAnnotation], beside the tofu-estate label.
//
// Why an annotation and not a label is #1016's measurement, restated on
// [LabelSurface]: an address is often not a legal label value, and never
// has to be a legal annotation value. The value is [EscapeAddress] of the
// instance address, the exact bytes [TagAddress] carries on AWS, so a
// reader that already matches an AWS tofu-address ([AddressMatches],
// discovery's normalisation) matches this one with no second grammar.
// Nothing splits: an annotation has no per-value cap to continue past.
//
// The key sits under choudoufu.intentius.io/, the prefix the record store's
// own annotation (staterecord.KubernetesRecordKeyAnnotation,
// choudoufu.intentius.io/record-key) already established for this fork's
// annotations on a cluster; a test in internal/live/staterecord pins that
// the two keep one prefix.
//
// What it is not: a boundary. The admission fence
// (live/kubernetes/estate-boundary.yaml) reads the estate label and nothing
// else, by the same ruling. Its readers are the sweep's address join
// (#1640), which binds an object no natural key declares to the block the
// annotation names, and the node's #1617 refusal, which since
// substrate.Kubernetes.CarriesAddress flipped (#1641) stands only where the
// sweep found an object of the type without the annotation.

// AddressAnnotation is the annotation key a Kubernetes object's block
// address is carried under. The value is [EscapeAddress] of the instance
// address.
const AddressAnnotation = "choudoufu.intentius.io/tofu-address"

// LabelAnnotationPath is the cty.Path of one annotation key on a
// label-surface resource: metadata[0].annotations["<key>"], the path an
// operator's own `ignore_changes = [metadata[0].annotations["<key>"]]`
// would name.
//
//markers:surface labels
func LabelAnnotationPath(key string) cty.Path {
	return cty.Path{
		cty.GetAttrStep{Name: LabelSurfaceBlock},
		cty.IndexStep{Key: cty.NumberIntVal(0)},
		cty.GetAttrStep{Name: AnnotationSurfaceAttr},
		cty.IndexStep{Key: cty.StringVal(key)},
	}
}

// AnnotationsOf reads the annotations map off a live or planned object of a
// label-surface type: metadata[0].annotations, the sibling of [LabelsOf].
// The second return distinguishes "this object has no metadata.annotations
// this function can read" from "the object carries no annotations".
//
//markers:surface labels
func AnnotationsOf(obj cty.Value) (map[string]string, bool) {
	return metadataMapOf(obj, AnnotationSurfaceAttr)
}

// ManifestAnnotationPath is the cty.Path of one annotation key on a
// manifest-surface resource: manifest.metadata.annotations["<key>"].
//
//markers:surface manifest
func ManifestAnnotationPath(key string) cty.Path {
	return cty.Path{
		cty.GetAttrStep{Name: ManifestSurfaceAttr},
		cty.GetAttrStep{Name: LabelSurfaceBlock},
		cty.GetAttrStep{Name: AnnotationSurfaceAttr},
		cty.IndexStep{Key: cty.StringVal(key)},
	}
}

// ManifestAnnotationsOf reads the annotations off a manifest-surface
// resource's configuration or planned value, manifest.metadata.annotations,
// the sibling of [ManifestLabelsOf]; an object constructor's object and a
// map are both read.
//
//markers:surface manifest
func ManifestAnnotationsOf(obj cty.Value) (map[string]string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || obj.IsMarked() || !obj.Type().IsObjectType() {
		return nil, false
	}
	if !obj.Type().HasAttribute(ManifestSurfaceAttr) {
		return nil, false
	}
	return manifestMetadataMap(obj.GetAttr(ManifestSurfaceAttr), AnnotationSurfaceAttr)
}

// AnnotationsChangedBesides reports whether prior and planned - two values
// of one label-surface object - carry different metadata[0].annotations
// once key is set aside on both sides. It is the annotations half of a
// "nothing but the markers moved" check (GitHub issue #1639): a marker
// write may add or rewrite the address annotation, and any other
// annotation it moves is a change the write was not asked to make. One
// side readable and the other not reads as a difference, so a value that
// became unreadable is refused rather than waved through; neither side
// readable (a schema with no annotations attribute) is no difference.
//
//markers:surface labels
func AnnotationsChangedBesides(prior, planned cty.Value, key string) bool {
	p, pok := AnnotationsOf(prior)
	n, nok := AnnotationsOf(planned)
	if pok != nok {
		return true
	}
	if !pok {
		return false
	}
	delete(p, key)
	delete(n, key)
	if len(p) != len(n) {
		return true
	}
	for k, v := range p {
		if got, ok := n[k]; !ok || got != v {
			return true
		}
	}
	return false
}
