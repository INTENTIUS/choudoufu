// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"fmt"
	"regexp"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// This file is the Kubernetes marker surface (GitHub issue #1061, under
// #1016's ruling of 2026-09-11): the second shape a marker can be carried
// in, beside the AWS tag map [TagSurface] describes.
//
// # One label, not two tags
//
// On AWS the marker is two tags because AWS hands back opaque ids and
// generated names, so the object has to carry the configuration address
// that owns it (tofu-address) as well as the estate (tofu-estate); there is
// no other way from a live object back to a line of configuration.
// Kubernetes returns the natural key - group, kind, namespace, name - with
// the name authored in the configuration this fork already parses, so
// re-binding goes through the key and the address never goes on the
// object. #1016 measured the alternative against the identity golden:
// nearly half of real addresses are illegal as a label value outright (the
// instance-key colon), and a 63-character cap binds at once on ordinary
// module-nested shapes. So the Kubernetes marker is [TagEstate] alone,
// written into metadata.labels, and [TagAddress], the continuation tags and
// [TagSlot] do not carry over.
//
// # What a label value may be
//
// Verified against k8s.io/apimachinery's validation (#1016): a label value
// is at most 63 characters and matches
// (([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?. An estate name
// ([ValidEstateName]) is up to 128 characters of [a-z0-9-] starting with a
// letter, so a name over 63 characters, or one ending in a hyphen, is a
// legal estate and an illegal label. [ValidLabelValue] is that check, and
// the stamp refuses such an estate on a Kubernetes resource rather than
// writing a label the API server rejects.

// LabelMaxValue is the longest label value Kubernetes accepts.
const LabelMaxValue = 63

// labelValuePattern is the API server's own grammar for a label value.
var labelValuePattern = regexp.MustCompile(`^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$`)

// ValidLabelValue reports whether s can be written as a Kubernetes label
// value: at most [LabelMaxValue] characters and within the API server's
// grammar. The empty string is a legal label value to Kubernetes but never
// a marker, so it is refused here.
func ValidLabelValue(s string) bool {
	return s != "" && len(s) <= LabelMaxValue && labelValuePattern.MatchString(s)
}

// LabelSurfaceBlock is the nested block a Kubernetes resource's labels live
// in, and LabelSurfaceAttr the attribute inside it.
const (
	LabelSurfaceBlock = "metadata"
	LabelSurfaceAttr  = "labels"
)

// LabelSurface reports whether a resource type carries its marker as a
// Kubernetes label: a "metadata" nested block of list nesting with exactly
// one item, holding a settable "labels" map of strings. That is the shape
// every hashicorp/kubernetes resource with object metadata shares (75 of
// the provider's 82 types at 3.2.1; kubernetes_manifest, which takes a
// whole manifest as one dynamic attribute, is not one of them), and it is
// read from the schema, never from a list of type names, for the same
// reason [Taggable] is.
//
// A type that is [Taggable] is never also a label surface: the two are
// disjoint by construction, since no Kubernetes type has a top-level tags
// map and no AWS type has a metadata block, and a caller checks
// [TagSurface] first so that the AWS shape keeps every behaviour it has.
// The returned attribute is the labels map's schema.
func LabelSurface(block *configschema.Block) (*configschema.Attribute, bool) {
	if block == nil {
		return nil, false
	}
	if _, taggable := TagSurface(block); taggable {
		return nil, false
	}
	nested, ok := block.BlockTypes[LabelSurfaceBlock]
	if !ok || nested == nil {
		return nil, false
	}
	if nested.Nesting != configschema.NestingList || nested.MinItems != 1 || nested.MaxItems != 1 {
		return nil, false
	}
	attr, ok := nested.Block.Attributes[LabelSurfaceAttr]
	if !ok || attr == nil {
		return nil, false
	}
	if !attr.Optional && !attr.Required {
		return nil, false
	}
	if !attr.Type.IsMapType() {
		return nil, false
	}
	if et := attr.Type.ElementType(); et != cty.String && et != cty.DynamicPseudoType {
		return nil, false
	}
	return attr, true
}

// LabelSurfacePath is the cty.Path of one label key on a label-surface
// resource: metadata[0].labels["<key>"], the path an operator's own
// `ignore_changes = [metadata[0].labels["<key>"]]` would name.
func LabelSurfacePath(key string) cty.Path {
	return cty.Path{
		cty.GetAttrStep{Name: LabelSurfaceBlock},
		cty.IndexStep{Key: cty.NumberIntVal(0)},
		cty.GetAttrStep{Name: LabelSurfaceAttr},
		cty.IndexStep{Key: cty.StringVal(key)},
	}
}

// LabelsOf reads the labels map off a live or planned object of a
// label-surface type: metadata[0].labels, the sibling of [TagsOf] for the
// Kubernetes shape. The second return distinguishes "this object has no
// metadata.labels at all" from "the object carries no labels".
func LabelsOf(obj cty.Value) (map[string]string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || obj.IsMarked() || !obj.Type().IsObjectType() {
		return nil, false
	}
	if !obj.Type().HasAttribute(LabelSurfaceBlock) {
		return nil, false
	}
	meta := obj.GetAttr(LabelSurfaceBlock)
	if meta.IsNull() || !meta.IsKnown() || meta.IsMarked() {
		// A marked metadata block is not read: internal/live/marksafe's
		// discipline is that a marked value never reaches an element
		// read, and there is nothing here worth unmarking for.
		return nil, false
	}
	if !meta.CanIterateElements() || meta.LengthInt() != 1 {
		return nil, false
	}
	it := meta.ElementIterator()
	it.Next()
	_, elem := it.Element()
	if elem.IsNull() || !elem.IsKnown() || elem.IsMarked() || !elem.Type().IsObjectType() || !elem.Type().HasAttribute(LabelSurfaceAttr) {
		return nil, false
	}
	labels := elem.GetAttr(LabelSurfaceAttr)
	out := map[string]string{}
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

// NotALabelValue is the clause a stamp refusal carries when an estate name
// cannot be a Kubernetes label value.
func NotALabelValue(estate string) string {
	return fmt.Sprintf("the estate name %q is not a legal Kubernetes label value (at most %d characters of letters, digits, '-', '_' and '.', beginning and ending with a letter or digit)", estate, LabelMaxValue)
}

// WithLabels returns obj - a live or planned object of a label-surface type
// - with its metadata[0].labels replaced by labels, every other attribute
// of the metadata block and of the object carried across untouched. It is
// the write-side sibling of [LabelsOf], and the one seam both marker
// writers on this surface go through: internal/live/liveimport stamps an
// adopted object's estate label with it (#1073) and internal/live/mv moves
// an object between estates with it (#1081's fifth item), so the two
// cannot disagree about what a labels-only rewrite leaves alone. Refusals
// are errors rather than silent fallbacks: a marked metadata block is
// never read (internal/live/marksafe - the live object came off the
// provider unmarked, so a mark here is a bug upstream of the write), and
// a metadata block that is not exactly one element is not the shape
// [LabelSurface] admitted.
func WithLabels(block *configschema.Block, obj cty.Value, labels map[string]string) (cty.Value, error) {
	nested, ok := block.BlockTypes[LabelSurfaceBlock]
	if !ok || nested == nil {
		return cty.NilVal, fmt.Errorf("no %s block in the schema", LabelSurfaceBlock)
	}
	attr, ok := nested.Block.Attributes[LabelSurfaceAttr]
	if !ok || attr == nil {
		return cty.NilVal, fmt.Errorf("no %s attribute in the %s block", LabelSurfaceAttr, LabelSurfaceBlock)
	}
	meta := obj.GetAttr(LabelSurfaceBlock)
	if meta.IsMarked() {
		return cty.NilVal, fmt.Errorf("the live object's %s block is marked", LabelSurfaceBlock)
	}
	if meta.IsNull() || !meta.IsKnown() || !meta.CanIterateElements() || meta.LengthInt() != 1 {
		return cty.NilVal, fmt.Errorf("the live object's %s block is not exactly one element", LabelSurfaceBlock)
	}
	it := meta.ElementIterator()
	it.Next()
	_, elem := it.Element()
	if elem.IsMarked() {
		return cty.NilVal, fmt.Errorf("the live object's %s element is marked", LabelSurfaceBlock)
	}
	if elem.IsNull() || !elem.IsKnown() || !elem.Type().IsObjectType() {
		return cty.NilVal, fmt.Errorf("the live object's %s element is not an object", LabelSurfaceBlock)
	}

	var labelVal cty.Value
	if len(labels) == 0 {
		labelVal = cty.MapValEmpty(cty.String)
	} else {
		vals := make(map[string]cty.Value, len(labels))
		for k, v := range labels {
			vals[k] = cty.StringVal(v)
		}
		labelVal = cty.MapVal(vals)
	}
	converted, err := convert.Convert(labelVal, attr.Type)
	if err != nil {
		return cty.NilVal, err
	}

	elemAttrs := elem.AsValueMap()
	if elemAttrs == nil {
		elemAttrs = map[string]cty.Value{}
	}
	elemAttrs[LabelSurfaceAttr] = converted
	newMeta := cty.ListVal([]cty.Value{cty.ObjectVal(elemAttrs)})

	vals := make(map[string]cty.Value, len(block.Attributes)+len(block.BlockTypes))
	for name := range block.Attributes {
		vals[name] = obj.GetAttr(name)
	}
	for name := range block.BlockTypes {
		vals[name] = obj.GetAttr(name)
	}
	vals[LabelSurfaceBlock] = newMeta
	return cty.ObjectVal(vals), nil
}
