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
	} else {
		// GitHub issue #1002: see stampedTags' own note.
		n.noteUntagRelease(addr, markers.TagEstate, elems)
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

// mirrorManifestComputedFields is the read side of the manifest stamp, and
// GitHub issue #1177's fix.
//
// [configuredAttrsSeed] seeds the prior `manifest` from the CURRENT
// configuration, on the rule its own doc comment argues: a non-Computed
// attribute is one nothing but configuration can ever set, so a persisted
// state file's prior for it could hold nothing else. On this type that rule
// is half true. A state file holds what was LAST APPLIED, and last-applied
// and currently-configured differ in exactly one situation - the operator
// has edited the configuration and not applied it yet - which is the
// situation a plan exists to report. For nearly every provider the
// difference is invisible, because [objchange.ProposedNew] takes the
// configured value for a non-Computed attribute whatever the prior holds.
//
// This provider is the exception, and it is why #1177 exists. Its
// computed_fields argument (default metadata.annotations and
// metadata.labels) tells PlanResourceChange to take the LIVE object's
// value at those paths unless the configuration differs from the PRIOR
// MANIFEST. Seed the prior manifest from the configuration and that
// comparison compares the configuration against itself: it can never
// differ, the live value always wins, and every edit to a label or an
// annotation plans as "No changes." and applies as nothing written. The
// finding was settled against stock rather than inferred - a stock state
// file doctored to hold exactly what the seed produces (prior manifest
// carrying the NEW label, object left at the old one) makes STOCK print
// "No changes." for the same edit - so what is wrong is the prior handed
// over, not this fork's diff.
//
// What this does about it: every key in the mirrored set at
// metadata.labels and metadata.annotations takes the LIVE object's value
// for that key, or is dropped when the live object has no such key. The
// mirrored set is
//
//	(keys the prior manifest already carries) ∪ (removed[field])
//
// where the prior manifest is the seed, so its keys are exactly the keys
// the CONFIGURATION declares, and removed is GitHub issue #1211's other
// half: the keys this estate's own record says it declared at the last
// apply and the configuration no longer declares. For a key the
// configuration declares, this reproduces what a state file's
// last-applied value says; for a key the configuration USED to declare,
// the record is what remembers it, which is the whole of #1211's fix -
// and it is the same thing a stock state file's last-applied manifest
// is doing for stock. See manifestkeys.go for why the record and not
// the live object's metadata.managedFields, which was tried, measured
// and refuted.
//
// The marker arm - [markers.TagEstate] alone - is a special case of the
// first half, not a separate one.
//
// # Why exactly that set, and not the live maps wholesale
//
// computed_fields exists so that a label or an annotation the API SERVER or
// a controller adds does not churn the plan, and Kubernetes adds plenty:
// kubernetes.io/metadata.name on every Namespace,
// kubectl.kubernetes.io/last-applied-configuration, cert-manager.io/*,
// meta.helm.sh/*. Mirroring the live maps wholesale would put those in the
// prior, the configuration would then differ from the prior, and the
// provider's rule takes the configuration for the WHOLE path when it does -
// so every plan would propose deleting every server-added key, forever,
// against a server that re-adds them. #1211's scouting measured that
// exact churn on a real cluster: the wholesale mirror made the removal
// plan AND proposed deleting `kubernetes.io/metadata.name` and a key
// `kubectl label` had written, the server wrote both straight back, and
// the next plan proposed the same deletions again.
//
// The removed set is immune to that by construction. Nothing the
// configuration never declared can be in it, because the only way into
// the record is being declared at an apply - so a key the API server or
// another manager wrote is never mirrored, the configuration and the
// prior agree about it, and the live value stands. That is what
// `k8s-a-label-is-a-change` step 5 requires and what the wholesale
// mirror could not deliver.
//
// removed is nil for every non-Kubernetes read, nil for an instance with
// no record, and nil whenever the run found a candidate it will not act
// on - see [manifestRemovalKeys], which is where that last case earns
// its warning. Nil restores exactly the behaviour #1177 shipped: correct
// for everything the configuration declares, and blind to a removal.
//
// # Where this still differs from a state-backed run
//
//   - A declared label or annotation changed or deleted OUT OF BAND churns
//     the plan here, where stock's computed_fields swallows it. That is the
//     direction #1177 asks for: it is what makes an out-of-band `kubectl
//     label` on a declared key visible to a saved plan's staleness check,
//     and the marker arm has always behaved this way for tofu-estate for
//     the same reason.
//   - A key removed from the configuration that this estate has no
//     record of ever declaring is still not proposed for removal - a
//     first plan after a fresh clone with no record store reachable, or
//     an estate migrated before this member existed. That degrades to
//     the pre-#1211 answer, which is quiet, rather than to churn.
//
// A value that cannot be read without unmarking (marksafe's discipline) is
// returned as it was, per map: one marked labels value does not cost the
// annotations their mirror.
func mirrorManifestComputedFields(v cty.Value, block *configschema.Block, removed map[string]map[string]bool) cty.Value {
	if !markers.ManifestSurface(block) || v == cty.NilVal || v.IsNull() || !v.IsKnown() || v.IsMarked() || !v.Type().IsObjectType() {
		return v
	}
	if !v.Type().HasAttribute(markers.ManifestSurfaceAttr) || !v.Type().HasAttribute(markers.ManifestLiveAttr) {
		return v
	}
	manifest := v.GetAttr(markers.ManifestSurfaceAttr)
	meta, ok := manifestMetadata(manifest)
	if !ok {
		return v
	}
	liveMeta, ok := manifestMetadata(v.GetAttr(markers.ManifestLiveAttr))
	if !ok {
		return v
	}

	if manifest.IsMarked() || meta.IsMarked() {
		// Already refused by [manifestMetadata] above; restated here on
		// the same two variables so the proof is local to the reads that
		// need it, which is what internal/live/marksafe asks of every
		// AsValueMap call site.
		return v
	}

	metaAttrs := meta.AsValueMap()
	changed := false
	for _, field := range markers.ManifestComputedMetadataAttrs {
		live, ok := liveMetadataMap(liveMeta, field)
		if !ok {
			continue
		}
		cur, has := metaAttrs[field]
		if !has {
			// GitHub issue #1211's last-key case. Deleting the final
			// annotation from a configuration deletes the whole
			// `annotations` map with it, so there is no prior map to
			// widen - and skipping the field here is how PR #1259's
			// first smoke step failed. The removed keys the object
			// still carries are put in a map of their own, which the
			// configuration then differs from (it has no such
			// attribute at all) exactly as it differs from a widened
			// one.
			//
			// metadata.labels never reaches this branch on a stamped
			// estate, because the marker is always in it; annotations
			// reach it whenever the last one is removed.
			added, ok := removedOnlyMetadataMap(removed[field], live)
			if !ok {
				continue
			}
			metaAttrs[field] = added
			changed = true
			continue
		}
		mirrored, ok := mirrorMetadataMap(cur, live, removed[field])
		if !ok || mirrored.RawEquals(cur) {
			continue
		}
		metaAttrs[field] = mirrored
		changed = true
	}
	if !changed {
		return v
	}

	manifestAttrs := manifest.AsValueMap()
	manifestAttrs[markers.LabelSurfaceBlock] = cty.ObjectVal(metaAttrs)
	attrs := v.AsValueMap()
	attrs[markers.ManifestSurfaceAttr] = cty.ObjectVal(manifestAttrs)
	return cty.ObjectVal(attrs)
}

// manifestMetadata reads the metadata object off one manifest-shaped value:
// the prior manifest the seed built, or the live object the provider read
// back, both of which are objects with a metadata object inside. Anything
// absent, null, unknown, marked or not an object refuses, because the
// caller rebuilds what this returns and a marked value is never taken
// apart.
func manifestMetadata(manifest cty.Value) (cty.Value, bool) {
	if manifest == cty.NilVal || manifest.IsNull() || !manifest.IsKnown() || manifest.IsMarked() {
		return cty.NilVal, false
	}
	if !manifest.Type().IsObjectType() || !manifest.Type().HasAttribute(markers.LabelSurfaceBlock) {
		return cty.NilVal, false
	}
	meta := manifest.GetAttr(markers.LabelSurfaceBlock)
	if meta.IsNull() || !meta.IsKnown() || meta.IsMarked() || !meta.Type().IsObjectType() {
		return cty.NilVal, false
	}
	return meta, true
}

// liveMetadataMap reads one of the live object's metadata string maps. The
// provider types them from the kind's own OpenAPI schema, where labels and
// annotations are both a map of strings, null when the object carries none
// - so an attribute that is absent and one that is null both read as "this
// object has no keys here", which is what they are, and not as a failure.
// The second return is false only when the map is there but cannot be read.
func liveMetadataMap(meta cty.Value, field string) (map[string]string, bool) {
	out := map[string]string{}
	if !meta.Type().HasAttribute(field) {
		return out, true
	}
	m := meta.GetAttr(field)
	if m.IsNull() || !m.IsKnown() {
		return out, true
	}
	if m.IsMarked() || !m.CanIterateElements() {
		return nil, false
	}
	for it := m.ElementIterator(); it.Next(); {
		k, val := it.Element()
		if k.Type() != cty.String || k.IsNull() || val.IsNull() || !val.IsKnown() || val.IsMarked() || val.Type() != cty.String {
			continue
		}
		out[k.AsString()] = val.AsString()
	}
	return out, true
}

// removedOnlyMetadataMap builds a prior metadata map for a field the
// prior manifest has no attribute for at all: the live value of every
// key the record says was declared and the configuration has dropped,
// and nothing else.
//
// This is the shape a configuration takes when its LAST annotation is
// deleted - the `annotations` map goes with the key - and without it a
// removal that empties a map is invisible, which is GitHub issue #1211 for
// exactly the configurations most likely to hit it.
//
// An object rather than a map, because the configuration side of this
// comparison is an object constructor and there is no prior container type
// to preserve here. false when nothing in the removed set is still on the
// object, which leaves the attribute absent, which is correct: absent on
// both sides is agreement, and there is nothing to remove.
func removedOnlyMetadataMap(removed map[string]bool, live map[string]string) (cty.Value, bool) {
	elems := map[string]cty.Value{}
	for key := range removed {
		got, ok := live[key]
		if !ok {
			continue
		}
		elems[key] = cty.StringVal(got)
	}
	if len(elems) == 0 {
		return cty.NilVal, false
	}
	return cty.ObjectVal(elems), true
}

// mirrorMetadataMap rebuilds one of the prior manifest's metadata maps with
// the live object's value for every key it already carries, plus every key
// in removed, dropping any key the live object does not have. The container
// type the configuration wrote is preserved - an object constructor's own
// object type, or a map where the operator typed one - because the
// manifest argument is dynamic and the value the provider compares against
// is this one. A removed key the configuration does not declare widens an
// object type by one attribute, which is exactly the difference that makes
// the removal plan.
//
// removed is nil for every caller before GitHub issue #1211 and for every
// run with no record to read one from, and a nil map reads as empty, so
// the union is then the prior's own keys and this behaves precisely as
// #1177 shipped it.
//
// It refuses (false, the caller leaves the map as it was) rather than
// guessing: a null or unknown map has no declared key to mirror, and a
// non-string value is not something a label or an annotation can hold, so
// one is a sign the caller is not looking at what it thinks it is.
func mirrorMetadataMap(cur cty.Value, live map[string]string, removed map[string]bool) (cty.Value, bool) {
	if cur.IsNull() || !cur.IsKnown() || cur.IsMarked() || !cur.CanIterateElements() {
		return cty.NilVal, false
	}
	elems := map[string]cty.Value{}
	for it := cur.ElementIterator(); it.Next(); {
		k, val := it.Element()
		if k.Type() != cty.String || k.IsNull() {
			return cty.NilVal, false
		}
		if val.IsMarked() || (!val.IsNull() && val.IsKnown() && val.Type() != cty.String) {
			return cty.NilVal, false
		}
		got, ok := live[k.AsString()]
		if !ok {
			continue
		}
		elems[k.AsString()] = cty.StringVal(got)
	}
	// GitHub issue #1211. A key this estate's record says it declared,
	// which the live object still has and the configuration no longer
	// names: it enters the prior with its live value, the configuration
	// differs from the prior, and the provider plans the removal. A key
	// nothing ever declared is not here, which is why this cannot churn -
	// see the caller's doc comment.
	for key := range removed {
		if _, already := elems[key]; already {
			continue
		}
		got, ok := live[key]
		if !ok {
			continue
		}
		elems[key] = cty.StringVal(got)
	}
	asMap := cur.Type().IsMapType()
	switch {
	case len(elems) == 0 && asMap:
		return cty.MapValEmpty(cty.String), true
	case len(elems) == 0:
		return cty.EmptyObjectVal, true
	case asMap:
		return cty.MapVal(elems), true
	default:
		return cty.ObjectVal(elems), true
	}
}
