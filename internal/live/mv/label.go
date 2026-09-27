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
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is live-mv's Kubernetes answer (GitHub issue #1081's fifth
// item, under #1016's ruling): the same command, on the second marker
// surface. On AWS the marker is two tags and a rename rewrites one of them.
// On Kubernetes the ownership marker is the single tofu-estate label
// [markers.LabelSurface] describes, and since GitHub issue #1639 (#1605's
// ruling of 2026-09-26) the block address sits beside it in an annotation,
// [markers.AddressAnnotation]. So the two halves of this command are:
//
//   - A rename within one estate rewrites the address annotation from the
//     old address to the new, and nothing else. It used to have nothing to
//     write on the cluster, because the object is bound to its block by
//     group, kind, namespace and name, which the configuration authors;
//     that binding is unchanged, and the annotation is a join key beside
//     it, not a boundary. The object is found the way a cross-estate move
//     finds it, by the natural key the configuration names, and it has to
//     carry this estate's label. Its annotation may be absent (an object
//     stamped before #1639) or name the old address; one already naming
//     the new address is a rename a plan and apply got to first, reported
//     as done ([Result.AlreadyMarked]); one naming a third address is
//     refused, never overwritten. The estate's own record store, when it
//     has one, is re-keyed from the old address to the new
//     ([mover.propagateModuleRename]), as before.
//   - A move between estates (-from-estate) is one label write,
//     tofu-estate=<new>, on the object, with the address annotation
//     written beside it. That write is what
//     live/kubernetes/estate-boundary.yaml governs: the admission policy
//     reads the label off the object as it is (the estate being left) and
//     as it would be (the estate being entered) and asks the API server's
//     own authorizer whether the caller holds both. It reads no
//     annotation: the fence stays label-only by the same ruling. So the
//     write goes through the provider, under the run's own credential,
//     exactly as an AWS retag goes through the provider under the run's
//     IAM - the carve is one command on both substrates, and the fence is
//     the cluster's.
//
// The write on the metadata-block shape is the markers-only plan-then-apply
// internal/live/liveimport's label carrier already makes for a migration,
// through the same [markers.WithMetadataMaps] seam, judged by the same
// rule: a plan that would also rename the object, move it between
// namespaces, or change anything outside metadata.labels and the address
// annotation is refused, never applied.
//
// A manifest-declared object ([markers.ManifestSurface], the shape every
// custom resource is declared through) carries the same label and
// annotation inside manifest.metadata, where no schema types them. Its
// rename is one annotation merge patch to the API server (manifest.go);
// its cross-estate move is not built here yet and is refused by name
// ([SummaryManifestMoveUnsupported]) with the equivalent kubectl write,
// which the same policy governs.

// SummaryManifestMoveUnsupported is the summary [surfaceOf]'s caller raises
// for a cross-estate move of a manifest-declared object. Exported for the
// reason [SummaryLocatedRenameUnsupported] is.
const SummaryManifestMoveUnsupported = "Moving a manifest-declared object between estates"

// surfaceOf reads the marker surface off the resource type's schema, never
// off its name: [substrate.SurfaceOf], the question live-import's carrier
// choice asks too (GitHub issue #1118). A type that carries no marker
// surface at all reads as the zero Surface, which [Move] sends down the tag
// path it has always taken, where the tag path finds no tags map to read
// ("Resource type with no tags").
//
// GitHub issue #1584: this package used to map the answer onto its own
// Surface enum. It now keeps the [markers.Surface] itself, and asks
// [substrate] what that surface means for a move ([relabels],
// [Result.MarkerCarriesAddress]) rather than naming the surfaces, so a new
// one is taught in internal/live/substrate alone.
func surfaceOf(block *configschema.Block) markers.Surface {
	surface, _ := substrate.SurfaceOf(block)
	return surface
}

// relabels reports whether a move on surface writes its marker by the
// labels-only plan-then-apply of [mover.relabel] ([substrate.WriteLabelsPlan],
// the Kubernetes metadata-block shape). Every other surface, and the zero
// one, takes the tag path; the manifest shape never gets this far (Move
// refuses it by name first).
func relabels(surface markers.Surface) bool {
	return substrate.WritesOf(surface).Adopt == substrate.WriteLabelsPlan
}

// MarkerCarriesAddress reports whether the marker map on this result's
// object holds a tofu-address key ([substrate.AddressInMarkers]): the tag
// path. True as well for a type with no marker surface and for a Result
// that stopped before its surface was read: both are on the tag path, and
// the report for them is the tag report it has always been. A Kubernetes
// object carries its address too ([substrate.CarriesAddress], GitHub issue
// #1641), in an annotation outside its label map, and is false here: its
// rewrite is the annotation's, not a tag's.
func (r *Result) MarkerCarriesAddress() bool {
	return r.Surface == "" || substrate.AddressInMarkers(r.Surface)
}

// annotates reports whether a label-surface schema's metadata block has an
// annotations map the address annotation can be written into. Every
// hashicorp/kubernetes object-metadata type does; one that did not would
// carry no address, and a rename of it keeps the old "nothing to write".
func annotates(block *configschema.Block) bool {
	nested, ok := block.BlockTypes[markers.LabelSurfaceBlock]
	if !ok || nested == nil {
		return false
	}
	_, ok = nested.Block.Attributes[markers.AnnotationSurfaceAttr]
	return ok
}

// checkAddressAnnotation judges the address annotation a Kubernetes object
// carries (GitHub issue #1639) against this rename. got is the annotation's
// value, "" when the object carries none. Absent, or naming the old
// address, is the rename's to rewrite. Naming the new address already is a
// same-estate rename a plan and apply got to first, which sets
// [Result.AlreadyMarked]; on a cross-estate move the address is rewritten
// with the label either way, so that is not "already done" there. Naming
// any other address is refused: the object belongs to another block of
// this estate, and a rename never overwrites a marker it did not expect.
func (m *mover) checkAddressAnnotation(got string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if got == "" {
		return diags
	}
	observed := markers.EscapeAddress(got)
	switch {
	case markers.AddressMatches(observed, m.res.New.String()):
		if m.req.FromEstate == "" {
			m.res.AlreadyMarked = true
		}
		return diags
	case markers.AddressMatches(observed, m.res.Old.String()):
		return diags
	}
	return diags.Append(refuse(
		RefusalNothingAtOldAddress,
		tfdiags.Error,
		"Live object carries another address",
		fmt.Sprintf(
			"The live %s at %s carries %s = %q, which is neither %s nor %s. It is bound to another block, so this rename is not its to make. Nothing was written.",
			m.res.TypeName, m.res.LiveID, markers.AddressAnnotation, got, m.res.Old, m.res.New),
	))
}

// locateLabelled is [mover.locateByIdentity]'s label-surface half: the
// object has been materialized from the natural key the configuration
// names, and what remains is to read its tofu-estate label and check it
// says what a move needs it to say, and then its address annotation
// ([mover.checkAddressAnnotation]). The estate cases are exactly the ones
// the tag path has, and each keeps the tag path's refusal code where one
// exists.
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

	return m.checkEstateLabel(obj, labels[markers.TagEstate], objVal)
}

// checkEstateLabel is [mover.locateLabelled]'s estate cases, given the
// tofu-estate label the object carries, followed by the address
// annotation's ([mover.checkAddressAnnotation]). objVal is the unmarked
// object, read for the annotation when the schema has one; obj is what is
// returned on success.
func (m *mover) checkEstateLabel(obj *states.ResourceInstanceObject, estate string, objVal cty.Value) (*states.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if d := m.estateRefusal(estate); d.HasErrors() {
		return nil, diags.Append(d)
	}
	// Found it: the object carries the estate it is being moved out of, or
	// the one a rename stays within.
	if !annotates(m.schema.Block) {
		return obj, diags
	}
	ann, ok := markers.AnnotationsOf(objVal)
	if !ok {
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Live object with no readable annotations",
			fmt.Sprintf("The live %s at %s carries no %s.%s map this run can read, so its address annotation cannot be checked and nothing was written.", m.res.TypeName, m.res.LiveID, markers.LabelSurfaceBlock, markers.AnnotationSurfaceAttr),
		))
	}
	if d := m.checkAddressAnnotation(ann[markers.AddressAnnotation]); d.HasErrors() {
		return nil, diags.Append(d)
	}
	return obj, diags
}

// estateRefusal is the refusal for a Kubernetes object whose tofu-estate
// label is not the estate this move or rename is looked for under
// ([mover.sourceEstate]), and nothing when it is. Both Kubernetes shapes
// ask it: the metadata block ([mover.checkEstateLabel]) and the manifest
// (manifest.go).
func (m *mover) estateRefusal(estate string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	switch {
	case estate == m.sourceEstate():
		return diags
	case estate == m.req.Estate:
		return diags.Append(refuse(
			RefusalNewAddressClaimed,
			tfdiags.Error,
			"Live object already in this estate",
			fmt.Sprintf(
				"The live %s at %s already carries tofu-estate = %q. This move appears to have already run: there is nothing left to write.",
				m.res.TypeName, m.res.LiveID, estate),
		))
	case estate == "":
		return diags.Append(refuse(
			RefusalNothingAtOldAddress,
			tfdiags.Error,
			"Live object carries no ownership label",
			fmt.Sprintf(
				"The live %s at %s carries no tofu-estate label at all, so it is not estate %q's to move. An unlabelled object is adopted by a plan or a migration writing its label, not moved. Nothing was written.",
				m.res.TypeName, m.res.LiveID, m.sourceEstate()),
		))
	default:
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Live resource owned by another estate",
			fmt.Sprintf(
				"The live %s at %s carries tofu-estate = %q, and this move is from estate %q. Only the estate that owns an object can move it out; name that estate in -from-estate, or adopt the object instead. Nothing was written.",
				m.res.TypeName, m.res.LiveID, estate, m.sourceEstate()),
		))
	}
}

// relabel is [mover.rewrite] for the label surface: one object's
// markers-only change, driven through the provider's own plan/apply pair,
// with tofu-estate set to the destination estate and the address
// annotation set to the new address (GitHub issue #1639), every other
// label and annotation, and everything else on the object, carried across
// untouched. On a same-estate rename the label is already the estate and
// the annotation is the whole write. The plan is judged by
// [mover.checkPlan], whose "nothing but the markers moved" test reads
// [changedOutsideLabels] on this surface.
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
	var desiredAnnotations map[string]string
	if annotates(m.schema.Block) {
		ann, ok := markers.AnnotationsOf(priorVal)
		if !ok {
			return diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Live object with no readable annotations",
				fmt.Sprintf("The live %s at %s carries no %s.%s map this run can read, so there is nowhere to write its address annotation. Nothing was written.", m.res.TypeName, m.res.LiveID, markers.LabelSurfaceBlock, markers.AnnotationSurfaceAttr),
			))
		}
		desiredAnnotations = make(map[string]string, len(ann)+1)
		for k, v := range ann {
			desiredAnnotations[k] = v
		}
		desiredAnnotations[markers.AddressAnnotation] = m.res.NewMarker
	}
	desired, err := markers.WithMetadataMaps(m.schema.Block, priorVal, desiredLabels, desiredAnnotations)
	if err != nil {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot build the rewritten object",
			fmt.Sprintf("The labels of the live %s could not be replaced: %s.", m.res.TypeName, err),
		))
	}

	what := "label"
	if m.req.FromEstate == "" {
		what = "address annotation"
	}
	newState, applyDiags := m.planAndApply(ctx, prior, priorVal, desired, what)
	diags = diags.Append(applyDiags)
	if applyDiags.HasErrors() {
		return diags
	}

	m.res.Written = true
	log.Printf("[TRACE] stateless/mv: rewrote the markers on %s %s: tofu-estate %q -> %q, %s -> %q",
		m.res.TypeName, m.res.LiveID, m.sourceEstate(), m.req.Estate, markers.AddressAnnotation, m.res.NewMarker)

	return diags.Append(m.verifyLabel(newState))
}

// verifyLabel is [mover.verify] for the label surface: the object the
// provider returned from the apply has to carry the destination estate's
// label and, where the schema has annotations, the new address in its
// address annotation. The read is the trustworthy half here - the
// Kubernetes provider reads the object back after every write - so a
// mismatch is still only a warning, for the same reason the tag path's is:
// the write itself reported no error, and kubectl can settle it.
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
	if got := labels[markers.TagEstate]; !ok || got != m.req.Estate {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Unreadable marker after the rewrite",
			fmt.Sprintf(
				"The label write on the %s at %s reported no error, but the object the provider returned afterwards carries tofu-estate = %q rather than %q. Read the object's labels with kubectl before rerunning.",
				m.res.TypeName, m.res.LiveID, labels[markers.TagEstate], m.req.Estate),
		))
	}
	if annotates(m.schema.Block) {
		ann, _ := markers.AnnotationsOf(newState)
		if got := ann[markers.AddressAnnotation]; got != m.res.NewMarker {
			return diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"Unreadable marker after the rewrite",
				fmt.Sprintf(
					"The write on the %s at %s reported no error, but the object the provider returned afterwards carries %s = %q rather than %q. Read the object's annotations with kubectl before rerunning.",
					m.res.TypeName, m.res.LiveID, markers.AddressAnnotation, got, m.res.NewMarker),
			))
		}
	}
	m.res.Verified = true
	return diags
}

// changedOutsideLabels is [changedOutsideTags] for the label surface: every
// top-level argument but the metadata block is compared whole, and inside
// the metadata block every attribute but labels is compared, annotations
// with the address annotation set aside, so a plan that would also rename
// the object, move it between namespaces or rewrite another annotation is
// caught while the markers this write exists to change are not. The same rule internal/live/liveimport's label carrier applies.
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
	for _, c := range changedAttrs(&nested.Block, priorElem, plannedElem, map[string]bool{markers.LabelSurfaceAttr: true, markers.AnnotationSurfaceAttr: true}) {
		out = append(out, markers.LabelSurfaceBlock+"."+c)
	}
	// GitHub issue #1639: the address annotation is the one annotation
	// this write may move; every other one is still compared.
	if _, has := nested.Block.Attributes[markers.AnnotationSurfaceAttr]; has && markers.AnnotationsChangedBesides(prior, planned, markers.AddressAnnotation) {
		out = append(out, markers.LabelSurfaceBlock+"."+markers.AnnotationSurfaceAttr)
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
