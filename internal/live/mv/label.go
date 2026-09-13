// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"fmt"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is live-mv's Kubernetes answer (GitHub issue #1081's fifth
// item, under #1016's ruling): the same command, on the second marker
// surface. On AWS the marker is two tags and a rename rewrites one of them;
// on Kubernetes the marker is the single tofu-estate label
// [markers.LabelSurface] describes, with no address on the object, so the
// two halves of this command come apart:
//
//   - A rename within one estate has NOTHING to write on the cluster. The
//     object is bound to its block by group, kind, namespace and name, all
//     of which the configuration authors, so renaming the block is the
//     whole rename and the next plan is empty. live-mv says so, plainly,
//     and exits 0 ([Result.NothingToWrite]) rather than refusing: an
//     operator porting an AWS runbook that ends every rename with live-mv
//     should not have a step that fails on the substrate where the step
//     is unnecessary. What it still does is the local half every rename
//     makes: the estate's own record store, when it has one, is re-keyed
//     from the old address to the new ([mover.propagateModuleRename]).
//   - A move between estates (-from-estate) is one label write,
//     tofu-estate=<new>, on the object. That write is what
//     live/kubernetes/estate-boundary.yaml governs: the admission policy
//     reads the label off the object as it is (the estate being left) and
//     as it would be (the estate being entered) and asks the API server's
//     own authorizer whether the caller holds both. So the write goes
//     through the provider, under the run's own credential, exactly as an
//     AWS retag goes through the provider under the run's IAM - the carve
//     is one command on both substrates, and the fence is the cluster's.
//
// The write itself is the labels-only plan-then-apply internal/live/
// liveimport's label carrier already makes for a migration, through the
// same [markers.WithLabels] seam, judged by the same rule: a plan that
// would also rename the object, move it between namespaces or change
// anything outside metadata.labels is refused, never applied.
//
// A manifest-declared object ([markers.ManifestSurface], the shape every
// custom resource is declared through) carries the same label inside
// manifest.metadata.labels, where no schema types it; that rewrite is not
// built here yet and is refused by name ([SummaryManifestMoveUnsupported])
// with the equivalent kubectl write, which the same policy governs.

// Surface is where the ownership marker lives on the live object, which
// decides what a rename or a move has to write.
type Surface string

const (
	// SurfaceTags is the AWS shape: tofu-estate and tofu-address in the
	// object's tags map ([markers.TagSurface]). The zero value, so every
	// caller and test that never heard of a second surface keeps reading
	// the tag path it always did.
	SurfaceTags Surface = ""

	// SurfaceLabel is the Kubernetes shape: tofu-estate alone, in
	// metadata[0].labels ([markers.LabelSurface]).
	SurfaceLabel Surface = "LABEL"

	// SurfaceManifest is the kubernetes_manifest shape: the same one label,
	// inside a dynamic manifest argument ([markers.ManifestSurface]).
	SurfaceManifest Surface = "MANIFEST"
)

// SummaryManifestMoveUnsupported is the summary [surfaceOf]'s caller raises
// for a cross-estate move of a manifest-declared object. Exported for the
// reason [SummaryLocatedRenameUnsupported] is.
const SummaryManifestMoveUnsupported = "Moving a manifest-declared object between estates"

// surfaceOf reads the marker surface off the resource type's schema, never
// off its name: the same predicates the stamp and the label carrier use,
// asked in the order those packages ask them (a taggable type is never a
// label surface, by [markers.LabelSurface]'s own construction).
func surfaceOf(block *configschema.Block) Surface {
	switch {
	case markers.Taggable(block):
		return SurfaceTags
	case markers.ManifestSurface(block):
		return SurfaceManifest
	default:
		if _, ok := markers.LabelSurface(block); ok {
			return SurfaceLabel
		}
		return SurfaceTags
	}
}

// locateLabelled is [mover.locateByIdentity]'s label-surface half: the
// object has been materialized from the natural key the configuration
// names, and what remains is to read its tofu-estate label and check it
// says what a move needs it to say. The address never enters into it -
// there is no tofu-address label to match - so the cases are exactly the
// estate cases the tag path has, and each keeps the tag path's refusal
// code where one exists.
func (m *mover) locateLabelled(obj *states.ResourceInstanceObject, resolution identity.Resolution) (*states.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	// Marks are the projection's, not the provider's; see [mover.rewrite].
	objVal, _ := obj.Value.UnmarkDeep()
	labels, ok := markers.LabelsOf(objVal)
	if !ok {
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Live object with no readable labels",
			fmt.Sprintf("The live %s at %s carries no %s.%s map this run can read, so its ownership label cannot be checked and nothing was written.", m.res.TypeName, m.res.Anchor, markers.LabelSurfaceBlock, markers.LabelSurfaceAttr),
		))
	}

	m.res.LiveID = resolution.ImportID
	if m.res.LiveID == "" {
		m.res.LiveID = liveIDFromObject(m.res.TypeName, objVal)
	}

	estate := labels[markers.TagEstate]
	switch {
	case estate == m.sourceEstate():
		// Found it: the object carries the estate it is being moved out of.
		return obj, diags
	case estate == m.req.Estate:
		return nil, diags.Append(refuse(
			RefusalNewAddressClaimed,
			tfdiags.Error,
			"Live object already in this estate",
			fmt.Sprintf(
				"The live %s at %s already carries tofu-estate = %q. This move appears to have already run: there is nothing left to write.",
				m.res.TypeName, m.res.LiveID, estate),
		))
	case estate == "":
		return nil, diags.Append(refuse(
			RefusalNothingAtOldAddress,
			tfdiags.Error,
			"Live object carries no ownership label",
			fmt.Sprintf(
				"The live %s at %s carries no tofu-estate label at all, so it is not estate %q's to move. An unlabelled object is adopted by a plan or a migration writing its label, not moved. Nothing was written.",
				m.res.TypeName, m.res.LiveID, m.req.FromEstate),
		))
	default:
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Live resource owned by another estate",
			fmt.Sprintf(
				"The live %s at %s carries tofu-estate = %q, and this move is from estate %q. Only the estate that owns an object can move it out; name that estate in -from-estate, or adopt the object instead. Nothing was written.",
				m.res.TypeName, m.res.LiveID, estate, m.req.FromEstate),
		))
	}
}

// relabel is [mover.rewrite] for the label surface: one object's
// labels-only change, driven through the provider's own plan/apply pair,
// with tofu-estate set to the destination estate and every other label,
// and everything else on the object, carried across untouched. The plan
// is judged by [mover.checkPlan], whose "nothing but the marker moved"
// test reads [changedOutsideLabels] on this surface.
func (m *mover) relabel(ctx context.Context, prior *states.ResourceInstanceObject) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if !markers.ValidLabelValue(m.req.Estate) {
		// The same refusal the plan's stamp and the migration's carrier
		// make, for the same reason: a legal estate name can be an illegal
		// label value, and the API server would reject the write.
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Ownership marker is not a legal label value",
			fmt.Sprintf("%s cannot be moved into estate %q: %s. Nothing was written.", m.res.Anchor, m.req.Estate, markers.NotALabelValue(m.req.Estate)),
		))
	}

	priorVal, _ := prior.Value.UnmarkDeep()
	labels, ok := markers.LabelsOf(priorVal)
	if !ok {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Live object with no readable labels",
			fmt.Sprintf("The live %s at %s carries no %s.%s map this run can read, so there is nowhere to write its ownership label. Nothing was written.", m.res.TypeName, m.res.LiveID, markers.LabelSurfaceBlock, markers.LabelSurfaceAttr),
		))
	}

	desiredLabels := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		desiredLabels[k] = v
	}
	desiredLabels[markers.TagEstate] = m.req.Estate
	desired, err := markers.WithLabels(m.schema.Block, priorVal, desiredLabels)
	if err != nil {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot build the rewritten object",
			fmt.Sprintf("The labels of the live %s could not be replaced: %s.", m.res.TypeName, err),
		))
	}

	newState, applyDiags := m.planAndApply(ctx, prior, priorVal, desired, "label")
	diags = diags.Append(applyDiags)
	if applyDiags.HasErrors() {
		return diags
	}

	m.res.Written = true
	log.Printf("[TRACE] stateless/mv: rewrote tofu-estate on %s %s: %q -> %q",
		m.res.TypeName, m.res.LiveID, m.req.FromEstate, m.req.Estate)

	return diags.Append(m.verifyLabel(newState))
}

// verifyLabel is [mover.verify] for the label surface: the object the
// provider returned from the apply has to carry the destination estate's
// label. The read is the trustworthy half here - the Kubernetes provider
// reads the object back after every write - so a mismatch is still only a
// warning, for the same reason the tag path's is: the write itself
// reported no error, and kubectl can settle it.
func (m *mover) verifyLabel(newState cty.Value) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if newState == cty.NilVal || newState.IsNull() {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Unreadable marker after the rewrite",
			fmt.Sprintf("The provider returned no object from the apply on the %s at %s, so this run cannot confirm the tofu-estate label now reads %q. The write itself reported no error.", m.res.TypeName, m.res.LiveID, m.req.Estate),
		))
	}
	labels, ok := markers.LabelsOf(newState)
	if got := labels[markers.TagEstate]; ok && got == m.req.Estate {
		m.res.Verified = true
		return diags
	}
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Warning,
		"Unreadable marker after the rewrite",
		fmt.Sprintf(
			"The label write on the %s at %s reported no error, but the object the provider returned afterwards carries tofu-estate = %q rather than %q. Read the object's labels with kubectl before rerunning.",
			m.res.TypeName, m.res.LiveID, labels[markers.TagEstate], m.req.Estate),
	))
}

// changedOutsideLabels is [changedOutsideTags] for the label surface: every
// top-level argument but the metadata block is compared whole, and inside
// the metadata block every attribute but labels is compared, so a plan
// that would also rename the object, move it between namespaces or rewrite
// its annotations is caught while the one map this write exists to change
// is not. The same rule internal/live/liveimport's label carrier applies.
func changedOutsideLabels(block *configschema.Block, prior, planned cty.Value) []string {
	out := changedAttrs(block, prior, planned, map[string]bool{markers.LabelSurfaceBlock: true})
	nested, ok := block.BlockTypes[markers.LabelSurfaceBlock]
	if !ok || nested == nil || prior == cty.NilVal || prior.IsNull() || planned == cty.NilVal || planned.IsNull() {
		return out
	}
	one := func(v cty.Value) (cty.Value, bool) {
		if v.IsMarked() {
			return cty.NilVal, false
		}
		if v.IsNull() || !v.IsKnown() || !v.CanIterateElements() || v.LengthInt() != 1 {
			return cty.NilVal, false
		}
		it := v.ElementIterator()
		it.Next()
		_, elem := it.Element()
		return elem, true
	}
	priorElem, pok := one(prior.GetAttr(markers.LabelSurfaceBlock))
	plannedElem, nok := one(planned.GetAttr(markers.LabelSurfaceBlock))
	if !pok || !nok {
		return out
	}
	for _, c := range changedAttrs(&nested.Block, priorElem, plannedElem, map[string]bool{markers.LabelSurfaceAttr: true}) {
		out = append(out, markers.LabelSurfaceBlock+"."+c)
	}
	return out
}

// manifestMoveRefusal is the refusal for a cross-estate move of a
// manifest-declared object, naming the kubectl write that makes the same
// governed change.
func manifestMoveRefusal(typeName string, anchor fmt.Stringer, from, to string) tfdiags.Diagnostic {
	return tfdiags.Sourceless(
		tfdiags.Error,
		SummaryManifestMoveUnsupported,
		fmt.Sprintf(
			"%s is a %s, which carries its whole object in one dynamic manifest argument; its ownership label sits inside manifest.metadata.labels, where no schema types it, and live-mv does not rewrite that shape yet. The move is the same one governed label write, made with the cluster's own client under a principal holding both estates: kubectl label <kind> <name> -n <namespace> %s=%s --overwrite. The admission policy in live/kubernetes/estate-boundary.yaml judges that write exactly as it would judge this command's, reading the label as it is (%s) and as it would be (%s). Nothing was read and nothing was written.",
			anchor, typeName, markers.TagEstate, to, from, to),
	)
}
