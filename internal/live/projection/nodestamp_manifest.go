// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the manifest third of the node-path stamp (GitHub issue
// #1079, ruled 2026-09-12): the same seam [NodeResolver.AdjustConfigValue]
// uses to write two tags into an AWS tags map and one label into a
// Kubernetes metadata block, writing that one label into a
// kubernetes_manifest block's manifest.metadata.labels. See
// [markers.ManifestSurface] for the shape and why the labels value may be
// an object rather than a map.
//
// Everything the label branch decides, this branch decides the same way
// and from the same fields (nodestamp_labels.go lists them); what differs
// is the value being rebuilt - an object constructor's own type, two levels
// deep, with no schema typing any of it - and one thing the dynamic
// argument forces on the read side. The provider's computed_fields default
// names metadata.labels, so once the object exists the provider takes the
// live labels as the truth of that field and a label stripped out of band
// never churns its plan. The stamp therefore has a second call site: the
// configured seed the projection hands the provider for an imported object
// ([configuredAttrsSeed], build.go) is stamped too, so that a prior state
// rebuilt with no cache carries the same manifest the stamped configuration
// does and the replan stays empty ([stampManifestSeed]).

// stampedManifest returns manifestVal - the evaluated manifest argument, an
// object whose metadata attribute is another object - with this instance's
// tofu-estate label added to metadata.labels, preserving every label the
// configuration already declares, the container type it wrote them in
// (object or map) and the value's own marks.
func (n *NodeResolver) stampedManifest(addr addrs.AbsResourceInstance, manifestVal cty.Value) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if !markers.ValidLabelValue(n.Estate) {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryMarkerNotALabel,
			fmt.Sprintf("%s cannot carry this estate's ownership marker: %s. Rename the estate in the live block, or keep this resource out of a Kubernetes estate.", addr, markers.NotALabelValue(n.Estate))))
		return manifestVal, diags
	}

	manifestVal, manifestMarks := manifestVal.Unmark()
	unchanged := func() (cty.Value, tfdiags.Diagnostics) { return manifestVal.WithMarks(manifestMarks), diags }

	switch {
	case manifestVal.IsNull():
		// A required argument the provider itself refuses when null;
		// nothing for this pass to do.
		return unchanged()
	case !manifestVal.IsKnown():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestUnresolved,
			fmt.Sprintf("%s's manifest is not yet known, so its ownership marker could not be set at the node; the configuration's own value is used unchanged.", addr)))
		return unchanged()
	case !manifestVal.Type().IsObjectType() && !manifestVal.Type().IsMapType():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestUnmergeable,
			fmt.Sprintf("%s's manifest evaluated to a %s, not an object; the configuration's own value is used unchanged.", addr, manifestVal.Type().FriendlyName())))
		return unchanged()
	}
	if manifestVal.Type().IsMapType() {
		// A map manifest (a map-typed variable) holds one element type,
		// which cannot describe an object with a string apiVersion and an
		// object metadata; the provider refuses it before anything here
		// matters, and this pass does not read into it.
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestUnmergeable,
			fmt.Sprintf("%s's manifest evaluated to a %s rather than an object constructor; the configuration's own value is used unchanged.", addr, manifestVal.Type().FriendlyName())))
		return unchanged()
	}
	if !manifestVal.Type().HasAttribute(markers.LabelSurfaceBlock) {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestUnmergeable,
			fmt.Sprintf("%s's manifest has no metadata this pass can write a label into; the configuration's own value is used unchanged.", addr)))
		return unchanged()
	}
	metaVal := manifestVal.GetAttr(markers.LabelSurfaceBlock)
	if metaVal.IsMarked() {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestMarked,
			fmt.Sprintf("%s's manifest.metadata is marked as a whole, which this pass will not unmark; its ownership marker was left for an operator to write.", addr)))
		return unchanged()
	}
	switch {
	case metaVal.IsNull():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestUnmergeable,
			fmt.Sprintf("%s's manifest.metadata is null; the configuration's own value is used unchanged.", addr)))
		return unchanged()
	case !metaVal.IsKnown():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestUnresolved,
			fmt.Sprintf("%s's manifest.metadata is not yet known, so its ownership marker could not be set at the node; the configuration's own value is used unchanged.", addr)))
		return unchanged()
	case !metaVal.Type().IsObjectType():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestUnmergeable,
			fmt.Sprintf("%s's manifest.metadata evaluated to a %s, not an object; the configuration's own value is used unchanged.", addr, metaVal.Type().FriendlyName())))
		return unchanged()
	}

	labelsVal := cty.NullVal(cty.EmptyObject)
	if metaVal.Type().HasAttribute(markers.LabelSurfaceAttr) {
		labelsVal = metaVal.GetAttr(markers.LabelSurfaceAttr)
	}
	labelsVal, labelMarks := labelsVal.Unmark()

	elems := map[string]cty.Value{}
	asMap := false
	switch {
	case labelsVal.IsNull():
		// No labels in configuration: the marker is the whole map, and
		// it is written in the shape an object constructor would have
		// produced unless the operator typed the absent value as a map.
		asMap = labelsVal.Type().IsMapType()
	case !labelsVal.IsKnown():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestUnresolved,
			fmt.Sprintf("%s's manifest.metadata.labels is not yet known, so its ownership marker could not be set at the node; the configuration's own value is used unchanged.", addr)))
		return unchanged()
	case !labelsVal.Type().IsObjectType() && !labelsVal.Type().IsMapType():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestUnmergeable,
			fmt.Sprintf("%s's manifest.metadata.labels evaluated to a %s, not an object or a map; the configuration's own value is used unchanged.", addr, labelsVal.Type().FriendlyName())))
		return unchanged()
	default:
		asMap = labelsVal.Type().IsMapType()
		for lit := labelsVal.ElementIterator(); lit.Next(); {
			k, v := lit.Element()
			if k.Type() != cty.String || k.IsNull() {
				continue
			}
			if v.Type() != cty.String && v.Type() != cty.DynamicPseudoType {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestUnmergeable,
					fmt.Sprintf("%s's manifest.metadata.labels holds a non-string value at key %q; the configuration's own value is used unchanged.", addr, k.AsString())))
				return unchanged()
			}
			elems[k.AsString()] = v
		}
	}

	untagKey := n.PolicyUntag[addr.String()]
	if untagKey != markers.TagEstate {
		diags = diags.Append(markerConflictDiag(addr, elems, markers.TagEstate, n.Estate))
		if diags.HasErrors() {
			return unchanged()
		}
		elems[markers.TagEstate] = cty.StringVal(n.Estate)
	}
	if len(elems) == 0 {
		// Only reachable under an untag of tofu-estate on a manifest with
		// no labels of its own: nothing to write, nothing to change.
		return unchanged()
	}

	var newLabels cty.Value
	if asMap {
		newLabels = cty.MapVal(elems)
	} else {
		newLabels = cty.ObjectVal(elems)
	}
	metaAttrs := metaVal.AsValueMap()
	if metaAttrs == nil {
		metaAttrs = map[string]cty.Value{}
	}
	metaAttrs[markers.LabelSurfaceAttr] = newLabels.WithMarks(labelMarks)
	manifestAttrs := manifestVal.AsValueMap()
	manifestAttrs[markers.LabelSurfaceBlock] = cty.ObjectVal(metaAttrs)
	return cty.ObjectVal(manifestAttrs).WithMarks(manifestMarks), diags
}

// stampManifestSeed is the stamp's second call site (see this file's own
// doc comment): given the configured seed [configuredAttrsSeed] built for
// an instance of a manifest-surface type, it returns the seed with the
// manifest's metadata.labels carrying the estate's marker, exactly as
// [NodeResolver.stampedManifest] would write it into the configuration.
// Without it the imported prior carries the unstamped manifest, the
// provider sees the label as a configuration change to a computed field,
// and every plan built without the cache proposes the same update.
//
// A seed that cannot be stamped (a refusal the node stamp will raise again
// on its own, with its diagnostics, when the plan runs) is returned as it
// was: this function never adds a diagnostic, because the same sentence
// would otherwise reach the operator twice.
func stampManifestSeed(addr addrs.AbsResourceInstance, seed map[string]cty.Value, estate string) map[string]cty.Value {
	manifest, ok := seed[markers.ManifestSurfaceAttr]
	if !ok || estate == "" {
		return seed
	}
	stamped, diags := (&NodeResolver{Estate: estate}).stampedManifest(addr, manifest)
	if diags.HasErrors() {
		return seed
	}
	out := make(map[string]cty.Value, len(seed))
	for k, v := range seed {
		out[k] = v
	}
	out[markers.ManifestSurfaceAttr] = stamped
	return out
}

// The manifest branch's own diagnostic summaries, siblings of the label
// branch's; registered in refusals.go.
const (
	SummaryManifestUnmergeable = "Cannot merge ownership markers into this manifest value"
	SummaryManifestUnresolved  = "Cannot set ownership markers on an unresolved manifest value"
	SummaryManifestMarked      = "Cannot set ownership markers on a marked manifest value"
)

// mirrorManifestMarker is the read side of the manifest stamp. The
// provider's computed_fields default names metadata.labels, so when it
// plans an update it takes the LIVE labels as that field's truth whenever
// the configured labels have not changed since the prior manifest: a label
// stripped out of band never churns its plan, and the object would stay
// outside the estate's boundary (claim 23's admission policy, the sweep)
// with an empty plan saying everything was fine. The projection therefore
// carries the live object's answer for the one marker key into the prior
// manifest it builds: if the live object lacks tofu-estate, or carries a
// different value, the prior manifest's labels say the same, the stamped
// configuration then differs from the prior at metadata.labels, and the
// provider plans the update that writes the label back. Every other label
// is left to the provider's own rule. A value the projection cannot read
// without unmarking (marksafe's discipline) is returned as it was.
func mirrorManifestMarker(v cty.Value, block *configschema.Block) cty.Value {
	if !markers.ManifestSurface(block) || v == cty.NilVal || v.IsNull() || !v.IsKnown() || v.IsMarked() || !v.Type().IsObjectType() {
		return v
	}
	if !v.Type().HasAttribute(markers.ManifestSurfaceAttr) || !v.Type().HasAttribute("object") {
		return v
	}
	manifest := v.GetAttr(markers.ManifestSurfaceAttr)
	live := v.GetAttr("object")
	manifestLabels, meta, ok := manifestLabelsMap(manifest)
	if !ok {
		return v
	}
	current, has := manifestLabels[markers.TagEstate]
	if !has {
		// Nothing stamped in the prior manifest: the stamp on the
		// configuration side is the whole story, and the seed already
		// carries it for a cache-less read.
		return v
	}
	liveLabels, liveOK := liveLabelsMap(live)
	if !liveOK {
		return v
	}
	liveVal, liveHas := liveLabels[markers.TagEstate]
	if liveHas && current.IsKnown() && !current.IsMarked() && current.Type() == cty.String && liveVal == current.AsString() {
		return v
	}
	elems := make(map[string]cty.Value, len(manifestLabels))
	for k, val := range manifestLabels {
		if k == markers.TagEstate {
			continue
		}
		elems[k] = val
	}
	if liveHas {
		elems[markers.TagEstate] = cty.StringVal(liveVal)
	}
	var newLabels cty.Value
	switch {
	case len(elems) == 0:
		newLabels = cty.EmptyObjectVal
	case meta.GetAttr(markers.LabelSurfaceAttr).Type().IsMapType():
		newLabels = cty.MapVal(elems)
	default:
		newLabels = cty.ObjectVal(elems)
	}
	if manifest.IsMarked() || meta.IsMarked() {
		// Already refused by the helpers above; restated here on the
		// same variables so the proof is local to the reads.
		return v
	}
	metaAttrs := meta.AsValueMap()
	metaAttrs[markers.LabelSurfaceAttr] = newLabels
	manifestAttrs := manifest.AsValueMap()
	manifestAttrs[markers.LabelSurfaceBlock] = cty.ObjectVal(metaAttrs)
	attrs := v.AsValueMap()
	attrs[markers.ManifestSurfaceAttr] = cty.ObjectVal(manifestAttrs)
	return cty.ObjectVal(attrs)
}

// manifestLabelsMap reads a prior manifest's metadata.labels as values,
// with the metadata object it came from. Absent, null or unknown labels
// read as none; anything marked, or not an object, refuses.
func manifestLabelsMap(manifest cty.Value) (map[string]cty.Value, cty.Value, bool) {
	if manifest.IsNull() || !manifest.IsKnown() || manifest.IsMarked() || !manifest.Type().IsObjectType() || !manifest.Type().HasAttribute(markers.LabelSurfaceBlock) {
		return nil, cty.NilVal, false
	}
	meta := manifest.GetAttr(markers.LabelSurfaceBlock)
	if meta.IsNull() || !meta.IsKnown() || meta.IsMarked() || !meta.Type().IsObjectType() || !meta.Type().HasAttribute(markers.LabelSurfaceAttr) {
		return nil, cty.NilVal, false
	}
	labels := meta.GetAttr(markers.LabelSurfaceAttr)
	out := map[string]cty.Value{}
	if labels.IsNull() || !labels.IsKnown() {
		return out, meta, true
	}
	if labels.IsMarked() || !labels.CanIterateElements() {
		return nil, cty.NilVal, false
	}
	for it := labels.ElementIterator(); it.Next(); {
		k, val := it.Element()
		if k.Type() != cty.String || k.IsNull() {
			continue
		}
		out[k.AsString()] = val
	}
	return out, meta, true
}

// liveLabelsMap reads the live object's metadata.labels as strings: the
// provider hands `object` back typed by the kind's OpenAPI schema, where
// labels is a map of strings, null when the object carries none.
func liveLabelsMap(object cty.Value) (map[string]string, bool) {
	if object.IsNull() || !object.IsKnown() || object.IsMarked() || !object.Type().IsObjectType() || !object.Type().HasAttribute(markers.LabelSurfaceBlock) {
		return nil, false
	}
	meta := object.GetAttr(markers.LabelSurfaceBlock)
	if meta.IsNull() || !meta.IsKnown() || meta.IsMarked() || !meta.Type().IsObjectType() {
		return nil, false
	}
	out := map[string]string{}
	if !meta.Type().HasAttribute(markers.LabelSurfaceAttr) {
		return out, true
	}
	labels := meta.GetAttr(markers.LabelSurfaceAttr)
	if labels.IsNull() || !labels.IsKnown() {
		return out, true
	}
	if labels.IsMarked() || !labels.CanIterateElements() {
		return nil, false
	}
	for it := labels.ElementIterator(); it.Next(); {
		k, val := it.Element()
		if k.Type() != cty.String || k.IsNull() || val.IsNull() || !val.IsKnown() || val.IsMarked() || val.Type() != cty.String {
			continue
		}
		out[k.AsString()] = val.AsString()
	}
	return out, true
}
