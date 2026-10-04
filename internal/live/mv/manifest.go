// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"fmt"
	"log"
	"maps"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// This file is the marker write on a manifest-declared Kubernetes object,
// for both of live-mv's cases:
//
//   - the same-estate rename (GitHub issue #1639): the object carries its
//     block address in metadata.annotations[markers.AddressAnnotation], so
//     renaming the block rewrites that one annotation;
//   - the cross-estate move (GitHub issue #1104): the object's
//     metadata.labels[tofu-estate] is rewritten from -from-estate to the
//     destination estate, and the address annotation with it, in the same
//     request.
//
// The write is not the provider's, for the reason
// internal/live/kubesweep/patch.go gives: a kubernetes_manifest object is
// one dynamic argument, so a write through the provider is a re-apply of
// every field it manages. It is one merge patch under the run's own
// credential (the kubeconfig the provider block names), so an admission
// policy such as live/kubernetes/estate-boundary.yaml judges it exactly as
// it judges `kubectl label`. It is sent first as a dry run (dryRun=All)
// and judged by [kubesweep.ChangedOutsideMarkers], the check live-import's
// adoption of the same shape makes, plus a check of the labels map that
// check sets aside. On a move, -dry-run sends that dry run too and stops,
// so the server's verdict on the move prints before anything is written.
//
// The patch goes under the field manager the block declares
// ([identity.ManifestFieldManager]; [kubesweep.DefaultFieldManager] when it
// declares none), and [kubesweep.Client.PatchMarkers] hands the markers
// to that manager's server-side apply entry afterwards (GitHub issue
// #1704), so the provider's next apply that changes one - the next rename -
// owns it rather than conflicting with the patch. Written under
// "Terraform" for a block that names its own field_manager, the annotation
// went to the wrong Apply entry and that next apply conflicted (#1720).
//
// A run killed between those two requests leaves the markers written and
// their ownership with the manager's Update entry. Its rerun finishes the
// hand-off rather than reporting the write done: a rename's when the
// annotation already names the new address (GitHub issue #1764), a move's
// when the object already carries the destination estate and the new
// address ([mover.unfinishedMove], #1858). Either is sent again through
// the same dry run as a first write.
//
// Nothing is projected after a move: the next plan in the destination
// configuration seeds the stamped manifest and mirrors the live label into
// the prior, so its replan is empty.

// rewriteManifest finds the manifest-declared object this rename or move's
// anchor names, checks its estate label and its address annotation the
// way [mover.checkEstateLabel] checks a metadata block's, and rewrites the
// markers: the address annotation on a rename, the estate label and the
// address annotation on a move. A dry run of a rename stops after the
// checks; a dry run of a move stops after the server's own dry run of the
// patch.
func (m *mover) rewriteManifest(ctx context.Context) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	move := m.req.FromEstate != ""
	// what names the write in every diagnostic below.
	what, verb, noun := "annotation write", "Renaming", "rename"
	if move {
		what, verb, noun = "label write", "Moving", "move"
	}

	m.res.Path = PathIdentity
	resolution, ok := resolutionFor(m.req, m.res.Anchor)
	if !ok {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No identity resolution for the resource being renamed",
			fmt.Sprintf("%s is declared but identity resolution produced nothing for it. This is a bug.", m.res.Anchor),
		))
	}
	m.res.LiveID = resolution.ImportID
	if move && !markers.ValidLabelValue(m.req.Estate) {
		// The refusal [mover.relabel] makes on the metadata-block shape,
		// for the same reason: a legal estate name can be an illegal label
		// value, and the API server would reject the write.
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Ownership marker is not a legal label value",
			fmt.Sprintf("%s cannot be moved into estate %q: %s. Nothing was read and nothing was written.", m.res.Anchor, m.req.Estate, markers.NotALabelValue(m.req.Estate)),
		))
	}
	apiVersion, kind, namespace, name, ok := kubesweep.ParseManifestImportID(resolution.ImportID)
	if !ok {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot name the live object",
			fmt.Sprintf("The configuration's identity for %s (%q) is not an apiVersion, kind, namespace and name this run can read, so the live object cannot be found to rewrite its markers. Nothing was read and nothing was written.", m.res.Anchor, resolution.ImportID),
		))
	}
	ref := kubesweep.ObjectRef{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name}

	if m.req.Clusters == nil {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No cluster client",
			fmt.Sprintf("%s %s makes the %s on %s through the cluster's own API, and this run was given no cluster client to reach it with. Nothing was read and nothing was written.", verb, m.res.Anchor, what, ref),
		))
	}
	patcher, err := m.req.Clusters.LabelPatcher(ctx, m.providerAddr)
	if err != nil || patcher == nil {
		why := "none could be built"
		if err != nil {
			why = err.Error()
		}
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No cluster client",
			fmt.Sprintf("%s %s makes the %s on %s through the cluster's own API, and no client for provider %s could be built: %s. Nothing was written.", verb, m.res.Anchor, what, ref, m.providerAddr, why),
		))
	}

	live, found, err := patcher.ReadObject(ctx, ref)
	if err != nil {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot read the live object",
			fmt.Sprintf("The cluster could not be asked about %s: %s. Nothing was written.", ref, err),
		))
	}
	if !found {
		return diags.Append(refuse(
			RefusalNothingAtOldAddress,
			tfdiags.Error,
			"No live object to "+noun,
			fmt.Sprintf("The cluster serves no object at %s, which is what %s's configuration names. Nothing was written.", ref, m.res.Anchor),
		))
	}

	fieldManager, err := identity.ManifestFieldManager(ctx, m.req.Config, m.res.Anchor)

	// The estate cases are the metadata block's, word for word
	// ([mover.estateRefusal]): on a move the object must carry
	// tofu-estate = -from-estate exactly, since an object in any other
	// estate is not this caller's to relabel. The one exception is a move
	// an earlier live-mv started and did not finish ([mover.unfinishedMove]):
	// it is sent again, through the same dry run, to complete the hand-off.
	if d := m.estateRefusal(live.GetLabels()[markers.TagEstate]); d.HasErrors() {
		if err != nil || !m.unfinishedMove(live, fieldManager) {
			return diags.Append(d)
		}
		log.Printf("[TRACE] live/mv: %s already carries %s = %q and %s = %q but field manager %q's Update entry still holds a marker; re-sending the move", ref, markers.TagEstate, m.req.Estate, markers.AddressAnnotation, m.res.NewMarker, fieldManager)
	}
	if d := m.checkAddressAnnotation(live.GetAnnotations()[markers.AddressAnnotation]); d.HasErrors() {
		return diags.Append(d)
	}
	if m.res.AlreadyMarked {
		// A rename only ([mover.checkAddressAnnotation] never sets this on
		// a move). The object carries the new address already. That is the
		// whole rename when the block's manager holds the annotation in its
		// Apply entry; when the manager's Update entry still holds it, an
		// earlier live-mv was killed between PatchMarkers' two requests
		// (GitHub issue #1764) and the provider's next rename would
		// conflict with that entry, so the write is sent again to finish
		// the hand-off. A field manager this run cannot read leaves the
		// rerun as it was before #1764: verified, nothing written.
		held := false
		if err == nil {
			held, _ = kubesweep.MarkersHeldByUpdate(live, fieldManager, nil, []string{markers.AddressAnnotation})
		}
		if !held {
			m.res.Verified = true
			return diags
		}
		m.res.AlreadyMarked = false
		log.Printf("[TRACE] live/mv: %s on %s already names %q but field manager %q's Update entry still holds it; re-sending the write", markers.AddressAnnotation, ref, m.res.NewMarker, fieldManager)
	}
	if err != nil {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot tell the block's field manager",
			fmt.Sprintf("%s %s makes the %s on %s under the field manager the block's provider applies it with, so that manager owns the markers afterwards: %s. Written under any other manager, the provider's next apply that changes a marker would report a field manager conflict. Nothing was written; declare the field_manager name as a literal, a variable or a local.", verb, m.res.Anchor, what, ref, err),
		))
	}
	if m.req.DryRun && !move {
		return diags
	}

	var labels map[string]string
	if move {
		labels = map[string]string{markers.TagEstate: m.req.Estate}
	}
	annotations := map[string]string{markers.AddressAnnotation: m.res.NewMarker}
	dry, rejected, err := patcher.PatchMarkers(ctx, ref, labels, annotations, fieldManager, true)
	switch {
	case err != nil:
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot plan the marker rewrite",
			fmt.Sprintf("The %s on %s could not be submitted to the cluster for a dry run: %s. Nothing was written.", what, ref, err),
		))
	case rejected != "":
		// On a move this is where an estate-boundary admission policy
		// answers, in its own words, before anything is persisted.
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot plan the marker rewrite",
			fmt.Sprintf("The API server refused the %s on %s: %s. Nothing was written.", what, ref, rejected),
		))
	}
	if extra := manifestPatchExtra(live, dry, labels); len(extra) > 0 {
		return diags.Append(refuse(
			RefusalPlanChangesMoreThanTags,
			tfdiags.Error,
			"Unexpected changes in the marker rewrite",
			fmt.Sprintf(
				"The %s on %s would also change %s, which the server's own dry run of the patch reports. A rename or move of a Kubernetes object writes only its ownership markers (the %s label and the %s annotation), so nothing was written. Something between this client and the stored object - a mutating admission webhook, most likely - rewrites more than was asked for.",
				what, ref, strings.Join(extra, ", "), markers.TagEstate, markers.AddressAnnotation),
		))
	}
	if m.req.DryRun {
		// A move's -dry-run: the server has judged the write, its
		// validation and admission included, and nothing was persisted.
		return diags
	}

	written, rejected, err := patcher.PatchMarkers(ctx, ref, labels, annotations, fieldManager, false)
	switch {
	case err != nil:
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Failed marker rewrite",
			fmt.Sprintf("The %s on %s failed: %s. The %s is not finished. Rerun the same live-mv command; it completes a write that partly landed.", what, ref, err, noun),
		))
	case rejected != "":
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Failed marker rewrite",
			fmt.Sprintf("The API server refused the %s on %s: %s. Nothing was written.", what, ref, rejected),
		))
	}
	m.res.Written = true
	if move {
		log.Printf("[TRACE] live/mv: moved %s from estate %q to %q; %s %q -> %q", ref, m.req.FromEstate, m.req.Estate, markers.AddressAnnotation, m.res.OldMarker, m.res.NewMarker)
	} else {
		log.Printf("[TRACE] live/mv: rewrote %s on %s: %q -> %q", markers.AddressAnnotation, ref, m.res.OldMarker, m.res.NewMarker)
	}

	if written != nil && written.GetAnnotations()[markers.AddressAnnotation] == m.res.NewMarker &&
		(!move || written.GetLabels()[markers.TagEstate] == m.req.Estate) {
		m.res.Verified = true
		return diags
	}
	want := fmt.Sprintf("%s = %q", markers.AddressAnnotation, m.res.NewMarker)
	if move {
		want = fmt.Sprintf("%s = %q and %s", markers.TagEstate, m.req.Estate, want)
	}
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Warning,
		"Unreadable marker after the rewrite",
		fmt.Sprintf("The %s on %s reported no error, but the object the server returned does not carry %s. Read the object's labels and annotations with kubectl before rerunning.", what, ref, want),
	))
}

// manifestPatchExtra names what the server's dry run of a marker patch
// (dry) changes on live beyond the markers the patch sets: everything
// [kubesweep.ChangedOutsideMarkers] reports, plus "metadata.labels" when
// the labels map differs from live's in any key but the ones in labels,
// or a key in labels does not read back as written. ChangedOutsideMarkers
// sets the labels map aside wholesale, because the label write exists to
// change it; a move sets one label and a rename sets none, so any other
// label that moved is something else moving it.
func manifestPatchExtra(live, dry *unstructured.Unstructured, labels map[string]string) []string {
	extra := kubesweep.ChangedOutsideMarkers(live.Object, dry.Object, markers.AddressAnnotation)
	want := maps.Clone(live.GetLabels())
	if want == nil {
		want = map[string]string{}
	}
	maps.Copy(want, labels)
	if !maps.Equal(want, dry.GetLabels()) {
		extra = append(extra, "metadata.labels")
	}
	return extra
}

// unfinishedMove reports whether live is what a cross-estate move killed
// between [kubesweep.Client.PatchMarkers]' two requests leaves behind
// (GitHub issue #1858, the move's half of #1764): the merge patch landed,
// so the object already carries tofu-estate = the destination and the
// address annotation = the new address, but the hand-off to the block's
// field manager's Apply entry did not, so that manager's Update entry
// still holds one of the markers. [mover.estateRefusal] would answer
// "already in this estate", and the provider's next apply that changes
// the label would then conflict with that Update entry. A move that
// finished, or an object that reached the destination any other way,
// holds neither marker in an Update entry of that manager and is refused
// as before. A managedFields entry that cannot be decoded is refused as
// before too, rather than read as unfinished.
func (m *mover) unfinishedMove(live *unstructured.Unstructured, fieldManager string) bool {
	if m.req.FromEstate == "" || live.GetLabels()[markers.TagEstate] != m.req.Estate {
		return false
	}
	if live.GetAnnotations()[markers.AddressAnnotation] != m.res.NewMarker {
		return false
	}
	held, err := kubesweep.MarkersHeldByUpdate(live, fieldManager, []string{markers.TagEstate}, []string{markers.AddressAnnotation})
	return err == nil && held
}
