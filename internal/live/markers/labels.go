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
// re-binding goes through the key. #1016 measured putting the address in a
// label against the identity golden: nearly half of real addresses are
// illegal as a label value outright (the instance-key colon), and a
// 63-character cap binds at once on ordinary module-nested shapes. So the
// Kubernetes ownership marker is [TagEstate] alone, written into
// metadata.labels, and the continuation tags and [TagSlot] do not carry
// over.
//
// The address itself does go on the object, since GitHub issue #1605's
// ruling (2026-09-26), in an annotation rather than a label:
// [AddressAnnotation], carrying the same escaped value [TagAddress] carries
// on AWS. An annotation value has neither the grammar nor the length cap,
// so nothing splits or continues. It is a join key and not a boundary: the
// admission fence reads the estate label alone. See annotations.go.
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
// one item (declared, or by [UndeclaredSingleObjectMetadata]), holding a
// settable "labels" map of strings. That is the shape
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
//
//markers:surface labels
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
	if nested.Nesting != configschema.NestingList {
		return nil, false
	}
	if (nested.MinItems != 1 || nested.MaxItems != 1) && !UndeclaredSingleObjectMetadata(nested) {
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

// UndeclaredSingleObjectMetadata reports whether nested is a "metadata"
// list block whose size the wire schema leaves undeclared (min_items and
// max_items both 0) but whose own fields say it describes exactly one
// object: a settable string "name" and a server-minted string "uid".
//
// That is how a plugin-framework provider renders Kubernetes object
// metadata. A framework list block cannot declare its bounds in the
// schema; the limit is a validator the wire protocol does not carry.
// hashicorp/kubernetes 3.3.0 moved kubernetes_namespace_v1 to the
// framework, and its metadata block went from min_items = max_items = 1
// to undeclared, with "Exactly one metadata block is required" in its
// description instead (corpus-quickpizza, 2026-10-02). An object has one
// name and one uid, so a block carrying both is one block per object
// whatever its declared bounds say; a declared lower bound with no upper
// one (min 1, max 0) is a list that chose to be unbounded and is not
// this.
//
// It is the bounds half of [LabelSurface] and of the Kubernetes
// substrate's object-metadata shape, so the two cannot disagree about
// which blocks are singletons.
func UndeclaredSingleObjectMetadata(nested *configschema.NestedBlock) bool {
	if nested == nil || nested.Nesting != configschema.NestingList || nested.MinItems != 0 || nested.MaxItems != 0 {
		return false
	}
	attrs := nested.Block.Attributes
	name, ok := attrs["name"]
	if !ok || name == nil || name.Type != cty.String || (!name.Optional && !name.Required) {
		return false
	}
	uid, ok := attrs["uid"]
	return ok && uid != nil && uid.Type == cty.String && uid.Computed && !uid.Optional && !uid.Required
}

// LabelSurfacePath is the cty.Path of one label key on a label-surface
// resource: metadata[0].labels["<key>"], the path an operator's own
// `ignore_changes = [metadata[0].labels["<key>"]]` would name.
//
//markers:surface labels
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
//
//markers:surface labels
func LabelsOf(obj cty.Value) (map[string]string, bool) {
	return metadataMapOf(obj, LabelSurfaceAttr)
}

// metadataMapOf reads one string map, attr, out of a label-surface
// object's metadata[0]: the labels [LabelsOf] reads, or the annotations
// [AnnotationsOf] reads. The second return distinguishes "no such map this
// function can read" from "the map is empty".
func metadataMapOf(obj cty.Value, attr string) (map[string]string, bool) {
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
	if elem.IsNull() || !elem.IsKnown() || elem.IsMarked() || !elem.Type().IsObjectType() || !elem.Type().HasAttribute(attr) {
		return nil, false
	}
	m := elem.GetAttr(attr)
	out := map[string]string{}
	if m.IsNull() || !m.IsKnown() {
		return out, true
	}
	if m.IsMarked() || !m.CanIterateElements() {
		return nil, false
	}
	for lit := m.ElementIterator(); lit.Next(); {
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
//
//markers:surface labels
func WithLabels(block *configschema.Block, obj cty.Value, labels map[string]string) (cty.Value, error) {
	return WithMetadataMaps(block, obj, labels, nil)
}

// WithMetadataMaps is [WithLabels] that can also replace the metadata
// block's annotations: labels replaces metadata[0].labels, and a non-nil
// annotations replaces metadata[0].annotations (GitHub issue #1639, where
// the address annotation is written beside the estate label). A nil
// annotations leaves that attribute exactly as obj carries it, which is
// what [WithLabels] is.
//
//markers:surface labels
func WithMetadataMaps(block *configschema.Block, obj cty.Value, labels, annotations map[string]string) (cty.Value, error) {
	nested, ok := block.BlockTypes[LabelSurfaceBlock]
	if !ok || nested == nil {
		return cty.NilVal, fmt.Errorf("no %s block in the schema", LabelSurfaceBlock)
	}
	attr, ok := nested.Block.Attributes[LabelSurfaceAttr]
	if !ok || attr == nil {
		return cty.NilVal, fmt.Errorf("no %s attribute in the %s block", LabelSurfaceAttr, LabelSurfaceBlock)
	}
	var annAttr *configschema.Attribute
	if annotations != nil {
		annAttr, ok = nested.Block.Attributes[AnnotationSurfaceAttr]
		if !ok || annAttr == nil {
			return cty.NilVal, fmt.Errorf("no %s attribute in the %s block", AnnotationSurfaceAttr, LabelSurfaceBlock)
		}
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

	converted, err := stringMapAs(labels, attr.Type)
	if err != nil {
		return cty.NilVal, err
	}
	elemAttrs := elem.AsValueMap()
	if elemAttrs == nil {
		elemAttrs = map[string]cty.Value{}
	}
	elemAttrs[LabelSurfaceAttr] = converted
	if annotations != nil {
		convAnn, err := stringMapAs(annotations, annAttr.Type)
		if err != nil {
			return cty.NilVal, err
		}
		elemAttrs[AnnotationSurfaceAttr] = convAnn
	}
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

// AssertMetadataMaps returns cfg, a synthetic configuration for a
// label-surface object, with metadata[0].labels and (where the schema has
// it) metadata[0].annotations carried over verbatim from desired, the
// object the write wants to produce. Anything else in cfg is left exactly
// as it is.
//
// A synthetic configuration that claims least nulls every computed
// attribute, and hashicorp/kubernetes declares kubernetes_job_v1's (and
// kubernetes_job's) metadata.labels Optional+Computed, because the API
// server copies a Job's pod-template labels onto the Job. A null config
// for such an attribute makes objchange.ProposedNew answer the prior
// value, so the plan carried the object's old labels, was accepted as a
// clean labels-only change because nothing changed at all, and the apply
// wrote nothing while the stamp reported success (GitHub issue #1885's
// reference-k8s-workloads, first run). The maps a marker write exists to
// change are always claimed as set, whatever the claim on the rest.
//
// cfg is returned unchanged when either object is not the one-element
// metadata shape [LabelSurface] admits.
//
//markers:surface labels
func AssertMetadataMaps(block *configschema.Block, cfg, desired cty.Value) cty.Value {
	if _, ok := LabelSurface(block); !ok {
		return cfg
	}
	nested := block.BlockTypes[LabelSurfaceBlock]
	sole := func(obj cty.Value) (cty.Value, bool) {
		if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || obj.IsMarked() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(LabelSurfaceBlock) {
			return cty.NilVal, false
		}
		meta := obj.GetAttr(LabelSurfaceBlock)
		if meta.IsNull() || !meta.IsKnown() || meta.IsMarked() || !meta.Type().IsListType() || meta.LengthInt() != 1 {
			return cty.NilVal, false
		}
		elem := meta.Index(cty.NumberIntVal(0))
		if elem.IsNull() || !elem.IsKnown() || elem.IsMarked() || !elem.Type().IsObjectType() {
			return cty.NilVal, false
		}
		return elem, true
	}
	from, ok := sole(desired)
	if !ok {
		return cfg
	}
	to, ok := sole(cfg)
	if !ok {
		return cfg
	}
	// sole already refused marked values; these are the guards
	// internal/live/marksafe can see in this function's own body.
	if to.IsMarked() {
		return cfg
	}
	elemAttrs := to.AsValueMap()
	for _, name := range []string{LabelSurfaceAttr, AnnotationSurfaceAttr} {
		if _, has := nested.Block.Attributes[name]; has && from.Type().HasAttribute(name) && to.Type().HasAttribute(name) {
			elemAttrs[name] = from.GetAttr(name)
		}
	}
	if cfg.IsMarked() {
		return cfg
	}
	vals := cfg.AsValueMap()
	vals[LabelSurfaceBlock] = cty.ListVal([]cty.Value{cty.ObjectVal(elemAttrs)})
	return cty.ObjectVal(vals)
}

// stringMapAs builds m as a cty map of strings converted to want, the
// schema's own type for the attribute being written.
func stringMapAs(m map[string]string, want cty.Type) (cty.Value, error) {
	var v cty.Value
	if len(m) == 0 {
		v = cty.MapValEmpty(cty.String)
	} else {
		vals := make(map[string]cty.Value, len(m))
		for k, s := range m {
			vals[k] = cty.StringVal(s)
		}
		v = cty.MapVal(vals)
	}
	return convert.Convert(v, want)
}
