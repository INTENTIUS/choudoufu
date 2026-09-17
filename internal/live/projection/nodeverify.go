// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is the other half of the node-path stamp (GitHub issue #1192).
// [NodeResolver.AdjustConfigValue] writes the marker into the value the
// provider is sent; this reads the object the provider returned from
// ApplyResourceChange and says whether the marker is in it.
//
// Why that is worth doing, and why nobody noticed it was missing: a marker
// write is the one write in this fork whose failure is invisible from
// inside the run. Every other argument a server rewrites shows up as drift
// on the next plan and converges or does not, exactly as it does on stock.
// A rewritten marker does not read as drift, it reads as ownership - the
// estate's own object comes back looking like somebody else's - and under
// `declared_untagged = "adopt"` it does not even read as that, because the
// adopting update is re-proposed and re-applied on every run and every run
// reports it as a change that happened.
//
// Measured on kind v1.36.1 with hashicorp/kubernetes 3.2.1 and a
// MutatingAdmissionPolicy removing the tofu-estate label (the reproduction
// on #1192, and step 6 of the k8s-the-server-gets-the-last-word smoke
// claim): the object's resourceVersion was 547 after the create and still
// 547 after two adopting applies that each printed "Apply complete!
// Resources: 0 added, 1 changed, 0 destroyed" and exited 0. Nothing was
// written by either of them.
//
// What this costs: nothing. No read is issued. The provider had already
// returned the stored object - the same trace shows core computing the
// exact fact, `.metadata[0].labels: element "tofu-estate" has vanished`,
// and logging it at WARN because objchange.AssertObjectCompatible's result
// is tolerated for a legacy-SDK provider. The information was in the
// process the whole time; only the judgement was missing.
//
// What it does not cover, said plainly: a provider that returns the planned
// value rather than re-reading its resource gives this nothing to compare,
// and [carrierMarkers] deliberately stays silent for one that returns the
// carrier attribute as null. That is the AWS side of #1192, still open: an
// Organizations tag policy can rewrite a tag on the way in, and whether the
// hashicorp/aws provider hands back the stored tags or the ones it sent is
// per resource type and unmeasured.

// SummaryMarkerNotStored is the one diagnostic this file raises, at either
// severity. See [NodeResolver.VerifyAppliedMarkers] for which and why.
const SummaryMarkerNotStored = "Ownership marker was not stored"

// The hook is reached by a type assertion on the value
// internal/tofu.EvalContext.ConfigValueAdjuster returns, so a signature that
// drifted would not fail to compile - it would silently stop being called
// and this whole file would become dead code that still passes every test
// naming it directly. That is exactly the shape of check that cannot fail,
// so the implementation is asserted here instead.
var _ tofu.AppliedMarkerVerifier = (*NodeResolver)(nil)

// VerifyAppliedMarkers implements internal/tofu.AppliedMarkerVerifier.
func (n *NodeResolver) VerifyAppliedMarkers(_ context.Context, addr addrs.AbsResourceInstance, action plans.Action, planned, applied cty.Value, schema providers.Schema) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if n.Estate == "" || schema.Block == nil {
		return diags
	}
	switch action {
	case plans.Create, plans.Update:
	default:
		// A delete has no marker to keep, and a no-op made no write. A
		// replace reaches apply split into its Create and Delete halves,
		// so the create half is judged like any other create.
		return diags
	}

	want, wantOK := carrierMarkers(planned, schema)
	if !wantOK || len(want) == 0 {
		// Nothing was stamped: an untaggable type, a record-rung
		// selection, a policy untag, or a surface this pass could not
		// write into and has already warned about.
		return diags
	}
	got, gotOK := carrierMarkers(applied, schema)
	if !gotOK {
		// The provider returned no carrier at all - a null tags map, an
		// absent metadata block. That is an absence of information, not
		// evidence the marker was discarded, and this file never turns the
		// one into the other.
		return diags
	}

	var lost []string
	estateLost := false
	for _, key := range []string{markers.TagEstate, markers.TagAddress, markers.TagSlot} {
		wantVal, sent := want[key]
		if !sent {
			continue
		}
		gotVal, stored := got[key]
		switch {
		case stored && gotVal == wantVal:
			continue
		case stored:
			lost = append(lost, fmt.Sprintf("%s: sent %q, stored %q", key, wantVal, gotVal))
		default:
			lost = append(lost, fmt.Sprintf("%s: sent %q, not stored", key, wantVal))
		}
		if key == markers.TagEstate {
			estateLost = true
		}
	}
	if len(lost) == 0 {
		return diags
	}
	sort.Strings(lost)

	// The severity split, and it is the whole of #1192's second half.
	//
	// A create that loses its marker has still added a real object, and
	// the run's "1 added" is true about the object even though it is
	// silent about the marker. The next plan is loud on its own: it finds
	// an unmarked object at the declared name, says so, and the apply
	// after that fails on the name. A warning is enough to name what the
	// wedge will be about.
	//
	// An update that loses the estate marker has changed nothing that
	// lasted and will be re-proposed identically on every future run. That
	// is the shape with no other alarm anywhere: under
	// `declared_untagged = "adopt"` the run prints "0 added, 1 changed, 0
	// destroyed" and exits 0, for ever, over an object no marker says is
	// this estate's. An exit code is what a nightly gate reads, so a
	// warning there is the same silence in a different font. This is not a
	// refusal that strands anything - the write already happened, the
	// object is exactly as it was before the run, and nothing is rolled
	// back - it is the run declining to report a success it does not have.
	did := "created"
	if action == plans.Update {
		did = "updated"
	}
	severity := tfdiags.Warning
	consequence := fmt.Sprintf("The object exists and is otherwise as the configuration describes; what it does not carry is the marker that would make it this estate's. The next plan will read it as a resource outside estate %q at this block's name, and the apply after that will fail on the name it already holds.", n.Estate)
	if action == plans.Update && estateLost {
		severity = tfdiags.Error
		consequence = fmt.Sprintf("Nothing this run wrote to that object lasted: it carries no marker naming estate %q, so this estate does not own it, and every run from here will propose and apply this same write and report it as a change that happened. This run does not report one.", n.Estate)
	}

	diags = diags.Append(tfdiags.Sourceless(severity, SummaryMarkerNotStored,
		fmt.Sprintf(
			"%s was %s, but the object the provider returned after the write does not carry the ownership marker this run sent:\n  - %s\n\n%s\n\nSomething between this run and the stored object removed it: a Kubernetes admission policy or controller enforcing a label scheme, or an AWS Organizations tag policy. Permit %s where that is configured, or set markers = record for this type in the live block so the estate's identity for it is held in the record store instead of on the object.",
			addr, did, strings.Join(lost, "\n  - "), consequence, markers.TagEstate,
		)))
	return diags
}

// carrierMarkers reads the marker-carrying map off a planned or applied
// object, dispatching on the same three surfaces
// [NodeResolver.AdjustConfigValue] writes into.
//
// The second return is the load-bearing one, and it is not what
// [markers.TagsOf] and [markers.LabelsOf] report: those answer "is this
// type taggable", which is true of a resource whose provider handed back a
// null tags map. Here the question is whether the returned object says
// anything about its own markers, so the carrier attribute itself must be
// present, known and non-null. A populated-but-marker-less map is a
// contradiction of what was sent; a null one is a provider that did not
// answer, and the caller must not treat the two alike.
func carrierMarkers(obj cty.Value, schema providers.Schema) (map[string]string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || obj.IsMarked() || !obj.Type().IsObjectType() {
		return nil, false
	}

	if _, taggable := markers.TagSurface(schema.Block); taggable {
		out := map[string]string{}
		found := false
		// tags is read second so an explicitly set tag wins over the same
		// key arriving through tags_all, matching [markers.TagsOf].
		for _, name := range []string{"tags_all", "tags"} {
			if !obj.Type().HasAttribute(name) {
				continue
			}
			v := obj.GetAttr(name)
			if v.IsNull() || !v.IsKnown() || v.IsMarked() || !v.CanIterateElements() {
				continue
			}
			found = true
			collectStrings(v, out)
		}
		return out, found
	}

	if _, labelled := markers.LabelSurface(schema.Block); labelled {
		labels, ok := labelSurfaceLabels(obj)
		if !ok {
			return nil, false
		}
		out := map[string]string{}
		collectStrings(labels, out)
		return out, true
	}

	if markers.ManifestSurface(schema.Block) {
		out, ok := markers.ManifestLabelsOf(obj)
		if !ok {
			return nil, false
		}
		return out, true
	}

	return nil, false
}

// labelSurfaceLabels returns metadata[0].labels as a value, only when it is
// really there: present, known and non-null at every step. A null labels
// map is reported as absent, which is the distinction this file's whole
// discrimination rests on.
func labelSurfaceLabels(obj cty.Value) (cty.Value, bool) {
	if !obj.Type().HasAttribute(markers.LabelSurfaceBlock) {
		return cty.NilVal, false
	}
	meta := obj.GetAttr(markers.LabelSurfaceBlock)
	if meta.IsNull() || !meta.IsKnown() || meta.IsMarked() || !meta.CanIterateElements() || meta.LengthInt() != 1 {
		return cty.NilVal, false
	}
	it := meta.ElementIterator()
	it.Next()
	_, elem := it.Element()
	if elem.IsNull() || !elem.IsKnown() || elem.IsMarked() || !elem.Type().IsObjectType() || !elem.Type().HasAttribute(markers.LabelSurfaceAttr) {
		return cty.NilVal, false
	}
	labels := elem.GetAttr(markers.LabelSurfaceAttr)
	if labels.IsNull() || !labels.IsKnown() || labels.IsMarked() || !labels.CanIterateElements() {
		return cty.NilVal, false
	}
	return labels, true
}

// collectStrings copies a map value's string entries into out, skipping
// anything that is not a plain known string on both sides.
func collectStrings(v cty.Value, out map[string]string) {
	for it := v.ElementIterator(); it.Next(); {
		k, val := it.Element()
		if k.Type() != cty.String || k.IsNull() || val.IsNull() || !val.IsKnown() || val.IsMarked() || val.Type() != cty.String {
			continue
		}
		out[k.AsString()] = val.AsString()
	}
}
