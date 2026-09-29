// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package untag

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// releaseManifest is [releaseOne] for a manifest-shape target, a
// kubernetes_manifest orphan (GitHub issue #1656, ruled 2026-09-27: allow
// the label-delete patch). Its object is one dynamic argument, so there is
// no typed labels map for a provider plan to be confined to; the release
// is the second write internal/live/kubesweep/patch.go makes, one merge
// patch deleting key from metadata.labels and the block-address
// annotation (markers.AddressAnnotation, GitHub issue #1639) from
// metadata.annotations, under the same safety live-import's adoption patch
// has (internal/live/liveimport/manifest.go):
//
//  1. read the object; gone is nothing to release, and an object carrying
//     neither marker is done;
//  2. send the patch with dryRun=All, and refuse if the server's answer
//     differs from the live object anywhere but those two keys - outside
//     the markers ([kubesweep.ChangedOutsideMarkers]) or inside the labels
//     map (a webhook that adds or drops another label);
//  3. send it for real, and confirm the object the server returns carries
//     neither marker.
//
// The patch goes under [kubesweep.DefaultFieldManager]: an undeclared
// orphan has no configuration to name a field_manager block, and a merge
// patch that removes a key leaves no field for a manager to own.
func releaseManifest(ctx context.Context, cluster kubesweep.LabelReleaser, key string, t Target) Outcome {
	out := Outcome{Target: t}

	if cluster == nil {
		out.Detail = fmt.Sprintf(
			"%s carries its whole object in one dynamic manifest argument, so its %q label is released by an API patch rather than a provider plan, and this run has no cluster client for the provider configuration it releases through. Release it with the cluster's own client: kubectl label <kind> <name> -n <namespace> %s- and kubectl annotate <kind> <name> -n <namespace> %s- (the object is %s). Nothing was read and nothing was changed.",
			t.TypeName, key, key, markers.AddressAnnotation, t.ImportID)
		return out
	}

	apiVersion, kind, namespace, name, ok := kubesweep.ParseManifestImportID(t.ImportID)
	if !ok {
		out.Detail = fmt.Sprintf("The import identifier %q does not name an apiVersion, kind and name, so this run cannot name the live object to release. Nothing was read and nothing was changed.", t.ImportID)
		return out
	}
	ref := kubesweep.ObjectRef{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name}

	live, found, err := cluster.ReadObject(ctx, ref)
	if err != nil {
		out.Detail = fmt.Sprintf("The cluster could not be asked about %s: %s. Nothing was changed.", ref, err)
		return out
	}
	if !found || live == nil {
		out.OK = true
		out.Detail = fmt.Sprintf("The live system reports that this %s no longer exists; there is nothing to release a label from.", t.TypeName)
		return out
	}
	_, hasLabel := live.GetLabels()[key]
	_, hasAddress := live.GetAnnotations()[markers.AddressAnnotation]
	if !hasLabel && !hasAddress {
		out.OK = true
		out.Detail = fmt.Sprintf("Already carries no %q label; nothing to release.", key)
		return out
	}

	keys := []string{key}
	annotations := []string{markers.AddressAnnotation}
	dry, rejected, err := cluster.DeleteMarkers(ctx, ref, keys, annotations, kubesweep.DefaultFieldManager, true)
	if err != nil {
		out.Detail = fmt.Sprintf("The label release on %s could not be submitted to the cluster for a dry run: %s. Nothing was changed.", ref, err)
		return out
	}
	if rejected != "" {
		out.Detail = fmt.Sprintf("The API server refused the label release on %s: %s. Nothing was changed.", ref, rejected)
		return out
	}
	if dry == nil {
		out.Detail = fmt.Sprintf("The cluster's dry run of the label release on %s returned no object to check. Nothing was changed.", ref)
		return out
	}
	extra := kubesweep.ChangedOutsideMarkers(live.Object, dry.Object, markers.AddressAnnotation)
	extra = append(extra, labelsMovedBesides(live, dry, keys)...)
	if len(extra) > 0 {
		sort.Strings(extra)
		out.Detail = fmt.Sprintf("Releasing the %q label from this %s would also change %s, which the server's own dry run of the patch reports. An untag is a labels-only write on a Kubernetes object; nothing was changed. Something between this client and the stored object - a mutating admission webhook, most likely - rewrites more than was asked for, and that has to be resolved first.", key, t.TypeName, strings.Join(extra, ", "))
		return out
	}

	written, rejected, err := cluster.DeleteMarkers(ctx, ref, keys, annotations, kubesweep.DefaultFieldManager, false)
	if err != nil {
		out.Detail = fmt.Sprintf("The label release on %s failed: %s. The write may have partly landed; read the object's labels with kubectl before deciding what to do next.", ref, err)
		return out
	}
	if rejected != "" {
		out.Detail = fmt.Sprintf("The API server refused the label release on %s: %s. Nothing was changed.", ref, rejected)
		return out
	}
	if written == nil {
		out.Detail = fmt.Sprintf("The cluster reported no error but returned no object for %s, so the release could not be confirmed. Verify with kubectl before relying on this.", ref)
		return out
	}
	if _, still := written.GetLabels()[key]; still {
		out.Detail = fmt.Sprintf("The cluster reported no error, but %q is still present on the object it returned. Verify with kubectl before relying on this.", key)
		return out
	}
	if _, still := written.GetAnnotations()[markers.AddressAnnotation]; still {
		out.Detail = fmt.Sprintf("The cluster reported no error, but the %q annotation is still present on the object it returned. Verify with kubectl before relying on this.", markers.AddressAnnotation)
		return out
	}

	out.OK = true
	out.Detail = fmt.Sprintf("Released %q. This resource is no longer managed by this estate.", key)
	return out
}

// labelsMovedBesides names every label, other than the released keys,
// whose value the dry-run answer does not carry exactly as the live object
// does: the one map [kubesweep.ChangedOutsideMarkers] leaves to its caller.
func labelsMovedBesides(live, dry *unstructured.Unstructured, released []string) []string {
	skip := make(map[string]bool, len(released))
	for _, k := range released {
		skip[k] = true
	}
	before, after := live.GetLabels(), dry.GetLabels()
	var out []string
	for k, v := range before {
		if skip[k] {
			continue
		}
		if got, ok := after[k]; !ok || got != v {
			out = append(out, "metadata.labels."+k)
		}
	}
	for k := range after {
		if skip[k] {
			continue
		}
		if _, ok := before[k]; !ok {
			out = append(out, "metadata.labels."+k)
		}
	}
	return out
}
