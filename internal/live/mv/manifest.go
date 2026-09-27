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

	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the same-estate rename of a manifest-declared Kubernetes
// object (GitHub issue #1639): the object carries its block address in
// metadata.annotations[markers.AddressAnnotation], so renaming the block
// rewrites that one annotation. The write is not the provider's, for the
// reason internal/live/kubesweep/patch.go gives: a kubernetes_manifest
// object is one dynamic argument, so a write through the provider is a
// re-apply of every field it manages. It is one merge patch under the
// run's own credential, sent first as a dry run and judged by
// [kubesweep.ChangedOutsideMarkers], the check live-import's adoption of
// the same shape makes.
//
// The patch goes under [kubesweep.DefaultFieldManager]. A block that names
// its own field_manager is not read here: the patch sets the value the
// provider's next apply sets too, so the two managers share the field
// rather than conflict over it.

// reannotateManifest finds the manifest-declared object this rename's
// anchor names, checks its estate label and its address annotation the
// way [mover.checkEstateLabel] checks a metadata block's, and rewrites the
// annotation to the new address. A dry run stops after the checks.
func (m *mover) reannotateManifest(ctx context.Context) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

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
	apiVersion, kind, namespace, name, ok := kubesweep.ParseManifestImportID(resolution.ImportID)
	if !ok {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot name the live object",
			fmt.Sprintf("The configuration's identity for %s (%q) is not an apiVersion, kind, namespace and name this run can read, so the live object cannot be found to rewrite its %s annotation. Nothing was read and nothing was written.", m.res.Anchor, resolution.ImportID, markers.AddressAnnotation),
		))
	}
	ref := kubesweep.ObjectRef{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name}

	if m.req.Clusters == nil {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No cluster client",
			fmt.Sprintf("Renaming %s rewrites the %s annotation on %s through the cluster's own API, and this run was given no cluster client to reach it with. Nothing was read and nothing was written.", m.res.Anchor, markers.AddressAnnotation, ref),
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
			fmt.Sprintf("Renaming %s rewrites the %s annotation on %s through the cluster's own API, and no client for provider %s could be built: %s. Nothing was written.", m.res.Anchor, markers.AddressAnnotation, ref, m.providerAddr, why),
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
			"No live object to rename",
			fmt.Sprintf("The cluster serves no object at %s, which is what %s's configuration names. Nothing was written.", ref, m.res.Anchor),
		))
	}

	// The estate cases are the metadata block's, word for word
	// ([mover.estateRefusal]).
	if d := m.estateRefusal(live.GetLabels()[markers.TagEstate]); d.HasErrors() {
		return diags.Append(d)
	}
	if d := m.checkAddressAnnotation(live.GetAnnotations()[markers.AddressAnnotation]); d.HasErrors() {
		return diags.Append(d)
	}
	if m.res.AlreadyMarked {
		m.res.Verified = true
		return diags
	}
	if m.req.DryRun {
		return diags
	}

	annotations := map[string]string{markers.AddressAnnotation: m.res.NewMarker}
	dry, rejected, err := patcher.PatchMarkers(ctx, ref, nil, annotations, "", true)
	switch {
	case err != nil:
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot plan the marker rewrite",
			fmt.Sprintf("The annotation write on %s could not be submitted to the cluster for a dry run: %s. Nothing was written.", ref, err),
		))
	case rejected != "":
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot plan the marker rewrite",
			fmt.Sprintf("The API server refused the annotation write on %s: %s. Nothing was written.", ref, rejected),
		))
	}
	extra := kubesweep.ChangedOutsideMarkers(live.Object, dry.Object, markers.AddressAnnotation)
	if !maps.Equal(live.GetLabels(), dry.GetLabels()) {
		// ChangedOutsideMarkers sets the labels map aside for a write that
		// sets a label; this one sets none, so a label that moved is
		// something else moving it.
		extra = append(extra, "metadata.labels")
	}
	if len(extra) > 0 {
		return diags.Append(refuse(
			RefusalPlanChangesMoreThanTags,
			tfdiags.Error,
			"Unexpected changes in the marker rewrite",
			fmt.Sprintf(
				"Rewriting the %s annotation on %s would also change %s, which the server's own dry run of the patch reports. A rename of a Kubernetes object writes only that annotation, so nothing was written. Something between this client and the stored object - a mutating admission webhook, most likely - rewrites more than was asked for.",
				markers.AddressAnnotation, ref, strings.Join(extra, ", ")),
		))
	}

	written, rejected, err := patcher.PatchMarkers(ctx, ref, nil, annotations, "", false)
	switch {
	case err != nil:
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Failed marker rewrite",
			fmt.Sprintf("The annotation write on %s failed: %s. The write may have partly landed: read the object's annotations with kubectl before deciding what to do next - if %s already names %s, the rename is done.", ref, err, markers.AddressAnnotation, m.res.NewMarker),
		))
	case rejected != "":
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Failed marker rewrite",
			fmt.Sprintf("The API server refused the annotation write on %s: %s. Nothing was written.", ref, rejected),
		))
	}
	m.res.Written = true
	log.Printf("[TRACE] stateless/mv: rewrote %s on %s: %q -> %q", markers.AddressAnnotation, ref, m.res.OldMarker, m.res.NewMarker)

	if written != nil && written.GetAnnotations()[markers.AddressAnnotation] == m.res.NewMarker {
		m.res.Verified = true
		return diags
	}
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Warning,
		"Unreadable marker after the rewrite",
		fmt.Sprintf("The annotation write on %s reported no error, but the object the server returned does not carry %s = %q. Read the object's annotations with kubectl before rerunning.", ref, markers.AddressAnnotation, m.res.NewMarker),
	))
}
