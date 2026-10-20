// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/gcpsweep"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is live-mv's GCP answer (GitHub issue #1119): the same command
// on the hashicorp/google labels map. The markers are labels in the
// object's top-level labels map: tofu-estate, tofu-address carried as an
// encoding of the block address ([markers.GoogleAddressValue], matched and
// never decoded), and tofu-slot where AWS would write one.
//
//   - A rename within one estate rewrites the tofu-address label from the
//     old address's encoding to the new one's. The object has to carry
//     this estate's label and the old encoding. One already carrying the
//     new encoding is a rename a plan and apply got to first, reported as
//     done ([Result.AlreadyMarked]); one carrying any other value, or none,
//     is refused and never overwritten.
//   - A move between estates (-from-estate) rewrites tofu-estate and
//     tofu-address together, in one labels-only write, as the tag path
//     rewrites its two tags.
//
// tofu-slot is carried across as the object holds it. The write is the
// labels-only plan-then-apply internal/live/liveimport's GCP carrier makes
// for an adoption, judged by [mover.checkPlan]: a plan that replaces the
// object, or changes anything outside labels, terraform_labels and
// effective_labels, is refused with [RefusalPlanChangesMoreThanTags].
//
// # How a GCP object is found
//
// Two of mv's three locate paths, never the third:
//
//   - By identity resolution ([mover.locateByIdentity]): the configuration
//     names the object, it is materialized through the projection, and its
//     labels are read off what comes back. This is the ordinary case.
//   - By record ([mover.locateByRecord]): for an instance whose identity
//     the provider assigns, the import identity this estate's record store
//     holds under the old address, read and checked exactly as the
//     identity path checks one. A cross-estate move has no record path
//     (the source estate's store is not this run's).
//   - Never by listing. listclient's sweep reads AWS tag maps and the
//     Resource Groups Tagging API, so it cannot find a GCP object.
//
// Once found, the object goes through the same destination check the AWS
// identity path makes ([mover.checkDestinationFree]): [mover.sweep] asks
// Cloud Asset Inventory, through internal/live/gcpsweep, for every
// resource carrying tofu-estate=<destination> and tofu-address=<the new
// value> in the projects the estate names ([mover.sweepAssets]), and
// [mover.destinationDiags] refuses a move onto an address another object
// already carries. A search that cannot be made (no client, no project,
// the API disabled, permission denied, a page that failed) refuses the
// move by name, as the AWS path refuses when its list call fails.

// relabelsGoogle reports whether a move on surface writes its markers by
// the GCP labels-only plan-then-apply ([substrate.WriteGoogleLabelsPlan]).
// Asked of internal/live/substrate rather than by naming the surface, for
// the reason [relabels] is.
func relabelsGoogle(surface markers.Surface) bool {
	return substrate.WritesOf(surface).Adopt == substrate.WriteGoogleLabelsPlan
}

// findGoogle is [mover.find] for a GCP resource: the identity path for an
// instance the configuration names, and the record path for one whose
// identity the provider assigns. See this file's doc comment for why there
// is no listing path.
func (m *mover) findGoogle(ctx context.Context, resolution identity.Resolution) (*states.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if classTable[resolution.Class].findByListing {
		if obj, recDiags, tried := m.locateByRecord(ctx); tried {
			diags = diags.Append(recDiags)
			if recDiags.HasErrors() {
				return nil, diags
			}
			return obj, diags.Append(m.checkDestinationFree(ctx, listclient.TypeSchema{}))
		}
		why := "this estate's record store holds no identity for it"
		if m.req.FromEstate != "" {
			why = "a cross-estate move cannot read the source estate's record store"
		}
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No marker search path for this resource type",
			fmt.Sprintf(
				"The identity of a %s is provider-assigned (%s), so live-mv can find the GCP resource behind %s only through a recorded identity, and %s. live-mv does not search GCP by listing. Nothing was searched and nothing was written; this is not a report that no such resource exists.",
				m.res.TypeName, discoveryReason(resolution), m.res.Old, why),
		))
	}

	m.res.Path = PathIdentity
	obj, idDiags := m.locateByIdentity(ctx, resolution)
	diags = diags.Append(idDiags)
	if idDiags.HasErrors() {
		return nil, diags
	}
	diags = diags.Append(m.checkDestinationFree(ctx, listclient.TypeSchema{}))
	if diags.HasErrors() {
		return nil, diags
	}
	return obj, diags
}

// SummaryGCPDestinationSearchFailed is the refusal for a GCP move whose
// destination search could not be made ([mover.sweepAssets]).
const SummaryGCPDestinationSearchFailed = "Cannot search for the destination address"

// sweepAssets is [mover.sweep] for a GCP resource: the resources in the
// estate's projects carrying tofu-estate=estate and the new tofu-address
// value, which is the one question the GCP path asks a sweep
// ([mover.checkDestinationFree]). The object being moved is reported under
// [Result.LiveID] when a result names it ([mover.isGoogleSelf]), so
// [mover.destinationDiags] leaves it out, as it leaves out the listed AWS
// object whose identity is the one found.
func (m *mover) sweepAssets(ctx context.Context, estate string) ([]listed, int, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	refused := func(why string) ([]listed, int, tfdiags.Diagnostics) {
		return nil, 0, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			SummaryGCPDestinationSearchFailed,
			fmt.Sprintf(
				"Before writing tofu-address = %q on the %s at %s, live-mv searches Cloud Asset Inventory for anything else in estate %q already carrying it, and %s. Nothing was written: a move that cannot answer that question is refused, as one whose list call fails is.",
				m.res.NewMarker, m.res.TypeName, m.res.LiveID, estate, why),
		))
	}

	if m.req.AssetSearch == nil {
		return refused("this run built no Cloud Asset Inventory client")
	}
	searcher, project, err := m.req.AssetSearch.AssetSearcher(ctx, m.providerAddr)
	if err == nil && searcher == nil {
		err = fmt.Errorf("no client was built")
	}
	if err != nil {
		return refused(fmt.Sprintf("no client could be built from provider configuration %s: %s", m.providerAddr, err))
	}

	projects := discovery.GCPSweepProjects(project, m.req.Resolutions)
	if m.googleProject != "" && !slices.Contains(projects, m.googleProject) {
		projects = append(projects, m.googleProject)
	}
	if len(projects) == 0 {
		return refused("no GCP project is known to search: the provider block names none, GOOGLE_PROJECT and its siblings are unset, and neither the object nor the configuration names one")
	}

	var out []listed
	total := 0
	for _, p := range projects {
		assets, err := searcher.SearchAddress(ctx, p, estate, m.res.NewMarker)
		if err != nil {
			return refused(fmt.Sprintf("the search of project %s failed: %s", p, assetSearchReason(err)))
		}
		total += len(assets)
		for _, a := range assets {
			l := listed{liveID: a.Name, displayName: a.AssetType + " " + a.Name, marker: a.Labels[markers.TagAddress]}
			if m.isGoogleSelf(a.Name) {
				l.liveID = m.res.LiveID
			}
			out = append(out, l)
		}
	}
	m.res.Swept = true
	return out, total, diags
}

// isGoogleSelf reports whether an Asset Inventory full resource name
// ("//storage.googleapis.com/projects/_/buckets/logs") names the object
// being moved: it ends in one of the names the provider gave the object
// (its id, its self_link's path from projects/ on, its import ID), on a
// path boundary.
func (m *mover) isGoogleSelf(name string) bool {
	for _, self := range m.googleSelf {
		if self != "" && (name == self || strings.HasSuffix(name, "/"+self)) {
			return true
		}
	}
	return false
}

// assetSearchReason is the clause naming why a project's search failed,
// with the fix where the error says what it is.
func assetSearchReason(err error) string {
	var disabled *gcpsweep.ServiceDisabledError
	var denied *gcpsweep.PermissionDeniedError
	var partial *gcpsweep.PartialError
	switch {
	case errors.As(err, &disabled):
		return fmt.Sprintf("the Cloud Asset API (%s) is not enabled there; enable it and run live-mv again", gcpsweep.Service)
	case errors.As(err, &denied):
		return fmt.Sprintf("the provider's credential lacks %s there", denied.Permission)
	case errors.As(err, &partial):
		return fmt.Sprintf("a page after the first failed, so none of the answer is used (%s)", err)
	}
	return err.Error()
}

// locateGoogleLabelled is [mover.locateByIdentity]'s GCP half: the object
// has been materialized from the identity the configuration (or a record)
// names, and what remains is to check its tofu-estate and tofu-address
// labels say what this move needs them to say.
//
// The labels are read after [markers.WithGoogleMarkersMirrored]:
// hashicorp/google's labels read keeps only the keys the prior names, so an
// object imported with no prior reads back with a labels map that does not
// name its markers, though effective_labels does.
func (m *mover) locateGoogleLabelled(obj *states.ResourceInstanceObject, resolution identity.Resolution) (*states.ResourceInstanceObject, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	// Marks are the projection's, not the provider's; see [mover.rewrite].
	objVal, _ := obj.Value.UnmarkDeep()
	objVal = markers.WithGoogleMarkersMirrored(objVal)

	m.res.LiveID = resolution.ImportID
	if m.res.LiveID == "" {
		m.res.LiveID = liveIDFromObject(m.res.TypeName, objVal)
	}
	m.googleSelf = []string{resolution.ImportID, substrate.ObjectString(objVal, "id")}
	if link := substrate.ObjectString(objVal, "self_link"); strings.Contains(link, "/projects/") {
		m.googleSelf = append(m.googleSelf, link[strings.Index(link, "/projects/")+1:])
	}
	m.googleProject = substrate.ObjectString(objVal, "project")

	labels, ok := markers.GoogleLabelsOf(objVal)
	if !ok {
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Live resource with no readable labels",
			fmt.Sprintf("The live %s at %s carries no labels map this run can read, so its ownership labels cannot be checked and nothing was written.", m.res.TypeName, m.res.LiveID),
		))
	}
	if d := m.estateRefusal(labels[markers.TagEstate]); d.HasErrors() {
		return nil, diags.Append(d)
	}

	got := labels[markers.TagAddress]
	switch {
	case got == m.res.OldMarker:
		// Found it. On a move that keeps its address this is also the new
		// value, and the estate label is what changes.
		return obj, diags
	case got == m.res.NewMarker && m.req.FromEstate == "":
		m.res.AlreadyMarked = true
		return obj, diags
	case got == "":
		return nil, diags.Append(refuse(
			RefusalNothingAtOldAddress,
			tfdiags.Error,
			"No live resource at the old address",
			fmt.Sprintf(
				"The live %s at %s carries no tofu-address label at all, so it does not carry %s's (%q). An unmarked resource is adopted by a plan or a migration writing its labels, not renamed. Nothing was written.",
				m.res.TypeName, m.res.LiveID, m.res.Old, m.res.OldMarker),
		))
	default:
		return nil, diags.Append(refuse(
			RefusalNothingAtOldAddress,
			tfdiags.Error,
			"Live object carries another address",
			fmt.Sprintf(
				"The live %s at %s carries tofu-address = %q, which encodes neither %s (%q) nor %s (%q). It is bound to another block, so this rename is not its to make. Nothing was written.",
				m.res.TypeName, m.res.LiveID, got, m.res.Old, m.res.OldMarker, m.res.New, m.res.NewMarker),
		))
	}
}

// relabelGoogle is [mover.rewrite] for a GCP resource: one object's
// labels-only change, driven through the provider's own plan/apply pair,
// with tofu-estate set to the destination estate and tofu-address to the
// new address's encoding. Every other label in the configuration's labels
// map, tofu-slot among them, and everything else on the object, is carried
// across untouched; labels another client set stay in effective_labels,
// where the provider leaves them.
func (m *mover) relabelGoogle(ctx context.Context, prior *states.ResourceInstanceObject) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if !markers.ValidGoogleLabelValue(m.req.Estate) {
		// The same refusal the plan's stamp and the migration's carrier
		// make: a legal estate name can be an illegal GCP label value.
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Ownership marker is not a legal label value",
			fmt.Sprintf("%s cannot be moved into estate %q: %s. Nothing was written.", m.res.Anchor, m.req.Estate, markers.NotAGoogleLabelValue(m.req.Estate)),
		))
	}

	priorVal, _ := prior.Value.UnmarkDeep()
	priorVal = markers.WithGoogleMarkersMirrored(priorVal)

	desiredLabels, ok := googleOwnLabels(priorVal)
	if !ok {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Live resource with no readable labels",
			fmt.Sprintf("The live %s at %s carries no labels map this run can read, so there is nowhere to write its ownership labels. Nothing was written.", m.res.TypeName, m.res.LiveID),
		))
	}
	desiredLabels[markers.TagEstate] = m.req.Estate
	desiredLabels[markers.TagAddress] = m.res.NewMarker

	desired, err := withGoogleLabels(m.schema.Block, priorVal, desiredLabels)
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

	m.written()
	log.Printf("[TRACE] live/mv: rewrote the labels on %s %s: tofu-estate %q -> %q, tofu-address %q -> %q",
		m.res.TypeName, m.res.LiveID, m.sourceEstate(), m.req.Estate, m.res.OldMarker, m.res.NewMarker)

	return diags.Append(m.verifyGoogle(newState))
}

// verifyGoogle is [mover.verify] for a GCP resource: the object the
// provider returned from the apply, mirrored the way the locate read it,
// has to carry the destination estate and the new address encoding. A
// mismatch is a warning for the tag path's reason: the write reported no
// error, and the cloud's own API can settle it.
func (m *mover) verifyGoogle(newState cty.Value) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if newState == cty.NilVal || newState.IsNull() {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Unreadable marker after the rewrite",
			fmt.Sprintf("The provider returned no object from the apply on the %s at %s, so this run cannot confirm the tofu-address label now reads %q. The write itself reported no error.", m.res.TypeName, m.res.LiveID, m.res.NewMarker),
		))
	}
	labels, ok := markers.GoogleLabelsOf(markers.WithGoogleMarkersMirrored(newState))
	if ok && labels[markers.TagEstate] == m.req.Estate && labels[markers.TagAddress] == m.res.NewMarker {
		m.res.Verified = true
		return diags
	}
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Warning,
		"Unreadable marker after the rewrite",
		fmt.Sprintf(
			"The label write on the %s at %s reported no error, but the object the provider returned afterwards carries tofu-estate = %q and tofu-address = %q rather than %q and %q. Read the resource's labels with the cloud's own API before rerunning.",
			m.res.TypeName, m.res.LiveID, labels[markers.TagEstate], labels[markers.TagAddress], m.req.Estate, m.res.NewMarker),
	))
}

// googleOwnLabels is the configuration's own labels map on obj, as a
// fresh map the caller may change. A null map reads as empty; a marked or
// unknown one, or an object with no labels attribute, reads as false.
func googleOwnLabels(obj cty.Value) (map[string]string, bool) {
	if obj == cty.NilVal || obj.IsMarked() || obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(markers.GoogleLabelsAttr) {
		return nil, false
	}
	own := obj.GetAttr(markers.GoogleLabelsAttr)
	if own.IsMarked() || !own.IsKnown() {
		return nil, false
	}
	out := map[string]string{}
	if own.IsNull() {
		return out, true
	}
	for it := own.ElementIterator(); it.Next(); {
		k, v := it.Element()
		if k.IsMarked() || v.IsMarked() || k.IsNull() || v.IsNull() || !v.IsKnown() || k.Type() != cty.String || v.Type() != cty.String {
			continue
		}
		out[k.AsString()] = v.AsString()
	}
	return out, true
}

// withGoogleLabels returns obj with its labels attribute replaced, as
// internal/live/liveimport's GCP carrier builds it. terraform_labels and
// effective_labels are left as read: they are computed, and the
// provider's own diff recomputes both from labels.
func withGoogleLabels(block *configschema.Block, obj cty.Value, labels map[string]string) (cty.Value, error) {
	if obj.IsMarked() {
		return cty.NilVal, fmt.Errorf("the object is marked, so its labels are not rewritten")
	}
	attr, ok := block.Attributes[markers.GoogleLabelsAttr]
	if !ok || attr == nil {
		return cty.NilVal, fmt.Errorf("the schema has no %s argument", markers.GoogleLabelsAttr)
	}
	labelVal := cty.MapValEmpty(cty.String)
	if len(labels) > 0 {
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
	vals := obj.AsValueMap()
	vals[markers.GoogleLabelsAttr] = converted
	return cty.ObjectVal(vals), nil
}

// changedOutsideGoogleLabels is [changedOutsideTags] for the three label
// maps a labels-only write moves.
func changedOutsideGoogleLabels(block *configschema.Block, prior, planned cty.Value) []string {
	return changedAttrs(block, prior, planned, map[string]bool{
		markers.GoogleLabelsAttr:          true,
		markers.GoogleTerraformLabelsAttr: true,
		markers.GoogleEffectiveLabelsAttr: true,
	})
}
