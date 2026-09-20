// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the third carrier live-import can write a marker through
// (GitHub issue #1109), beside tags.go's AWS tags map and labels.go's
// Kubernetes metadata block: the manifest shape, [markers.ManifestSurface],
// which is hashicorp/kubernetes's kubernetes_manifest and so every custom
// resource an estate declares.
//
// # What was wrong
//
// ratify.go decided a type's carrier as tags, then the label surface, else
// [ratifyUntaggable]. The manifest shape was neither, so EVERY
// kubernetes_manifest entry in a stock state file was ratified UNTAGGABLE:
// bound by its natural key, counted as migrated, and never given the
// tofu-estate label. The object then planned empty - the projection's seed
// and its live mirror both leave a prior with no label alone - but it was
// outside the boundary. The sweep did not list it, the admission policy of
// live/kubernetes/estate-boundary.yaml did not fence it, live-ls did not
// show it, and the migrate report said nothing at all, because UNTAGGABLE
// is a legitimate outcome for an AWS type. That silence was the defect.
//
// # The write, and why it is not the provider's
//
// Ruled 2026-09-13 by the maintainer, on #1109 and #1104 together: the
// label is one API merge patch under the caller's own credential, through
// [kubesweep.LabelPatcher], shared with live-mv's cross-estate move. That
// file's doc comment argues the mechanism. What this file adds is the
// two refusals that are the safety of the feature, both of which have to
// hold for a migration that is allowed to touch a custom resource at all:
//
//  1. An object already labelled for ANOTHER estate is never adopted. It
//     is the same refusal [approveLabel] makes on a metadata block and
//     [approveOne] makes on a tags map, in the same words.
//  2. A write that would change anything beyond the labels map is
//     refused. The patch body names one key under metadata.labels, so the
//     REQUEST cannot reach anything else - but a mutating admission
//     webhook on the way past can, and "the request is small" is an
//     assertion rather than a check. So the patch is sent first with
//     dryRun=All, and the object the server answers with is diffed
//     against the live object by [changedOutsideManifestLabels]. A
//     migration that can silently rewrite a custom resource's spec is
//     worse than one that does nothing.
//
// Verification is not this file's: a manifest-shape instance goes through
// ratifyOne's ordinary read path, the provider's own ReadResource against
// the object the stock state recorded, and reaches VERIFIED or DRIFTED by
// the same comparison every other type reaches it by.

// manifestSurface reports whether a resource type carries its marker
// inside a dynamic manifest argument. It is [markers.ManifestSurface] and
// nothing else, for the reason [taggable] is [markers.Taggable] and
// [labelSurface] is [markers.LabelSurface] and nothing else: one
// definition of the shape, read from the provider's schema, so that
// identity, the projection's stamp and this migration can never admit
// different sets of types.
func manifestSurface(block *configschema.Block) bool {
	return markers.ManifestSurface(block)
}

// manifestFieldManagerBlock is the block a kubernetes_manifest resource
// names its server-side-apply field manager in, and the attribute inside
// it. Read off the state's own recorded object rather than assumed, so a
// block that set one keeps it; unset, the patch falls back to
// [kubesweep.DefaultFieldManager].
const (
	manifestFieldManagerBlock = "field_manager"
	manifestFieldManagerAttr  = "name"
)

// manifestFieldManager reads the field manager the migrated block
// declared, or "" when it declared none.
func manifestFieldManager(obj cty.Value) string {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() {
		return ""
	}
	if !obj.Type().HasAttribute(manifestFieldManagerBlock) {
		return ""
	}
	blocks := obj.GetAttr(manifestFieldManagerBlock)
	if blocks.IsNull() || !blocks.IsKnown() || blocks.IsMarked() || !blocks.CanIterateElements() {
		return ""
	}
	for it := blocks.ElementIterator(); it.Next(); {
		_, elem := it.Element()
		if elem.IsNull() || !elem.IsKnown() || elem.IsMarked() || !elem.Type().IsObjectType() {
			continue
		}
		if !elem.Type().HasAttribute(manifestFieldManagerAttr) {
			continue
		}
		name := elem.GetAttr(manifestFieldManagerAttr)
		if name.IsNull() || !name.IsKnown() || name.IsMarked() || name.Type() != cty.String {
			continue
		}
		if s := name.AsString(); s != "" {
			return s
		}
	}
	return ""
}

// seedManifestKeys is GitHub issue #1391: the metadata.labels and
// metadata.annotations keys a migrated manifest-shaped instance's
// configuration last declared, seeded into the estate's record so that the
// first key REMOVED after the migration is proposed for removal.
//
// Until this existed the apply write-back was the only writer of
// [projection.residueFields.ManifestMetadataKeys], so an estate that
// migrated and then deleted its state file - which is what the adopt page
// tells an operator to do - had no record of what it used to declare at
// all. A label dropped from the configuration was then quietly kept on the
// live object for ever, where stock reads its last-applied manifest and
// removes it. The read side's degradation is deliberate and silent (a
// missing record proposes removing nothing), so nothing said so.
//
// stateObj is the STATE FILE's own recorded object, never the live read.
// For a stampable instance [Ratify] hands the residue classifier the live
// read, and this question is not about the live object: it is "what did
// the last apply DECLARE", which only the state's recorded `manifest`
// answers. Whether the provider's own ReadResource carries `manifest`
// through unchanged is not something this has to know.
//
// The marker key is added on top, because the write-back's own key set
// includes it: this fork declares tofu-estate on the configuration's
// behalf on every plan of a stamped manifest instance, and the migration
// writes exactly that label by merge patch ([approveManifest]). Seeding it
// makes the record a migration leaves identical to the one an apply
// leaves. It can never turn into a proposed removal, because the key the
// seed adds is the key the stamp puts back into every later plan's prior
// manifest, and a removal candidate has to be absent from that.
//
// nil for anything that is not manifest-shaped, which is the whole of the
// "a typed kubernetes_* entry gets nothing new" guarantee.
func seedManifestKeys(schema providers.Schema, stateObj cty.Value) map[string][]string {
	if schema.Block == nil || !manifestSurface(schema.Block) {
		return nil
	}
	keys, ok := projection.ManifestDeclaredKeys(stateObj)
	if !ok {
		return nil
	}
	labels := keys[markers.LabelSurfaceAttr]
	for _, k := range labels {
		if k == markers.TagEstate {
			return keys
		}
	}
	keys[markers.LabelSurfaceAttr] = append(labels, markers.TagEstate)
	sort.Strings(keys[markers.LabelSurfaceAttr])
	return keys
}

// approveManifest is [approveOne] for a manifest-shape resource: read the
// live object through the cluster client, refuse what must be refused,
// dry-run the one-label merge patch and check the server's answer changes
// nothing but the labels, then send it for real.
func approveManifest(ctx context.Context, estate string, addr addrs.AbsResourceInstance, e *eligible) StampOutcome {
	out := StampOutcome{Addr: addr, TypeName: e.typeName}

	if !markers.ValidLabelValue(estate) {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("%s cannot carry this estate's ownership marker: %s. Nothing was written.", e.typeName, markers.NotALabelValue(estate))
		return out
	}
	if !e.manifestKey.Complete() {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The state records no apiVersion, kind and metadata.name for this %s, so this run cannot name the live object to label. Nothing was written.", e.typeName)
		return out
	}
	if e.patcher == nil {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("No cluster client could be built for this %s, so the tofu-estate label could not be written: %s. Nothing was written.", e.typeName, e.patcherErr)
		return out
	}

	ref := kubesweep.ObjectRef{
		APIVersion: e.manifestKey.APIVersion,
		Kind:       e.manifestKey.Kind,
		Namespace:  e.manifestKey.Namespace,
		Name:       e.manifestKey.Name,
	}
	live, found, err := e.patcher.ReadObject(ctx, ref)
	if err != nil {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The cluster could not be asked about %s: %s. Nothing was written.", ref, err)
		return out
	}
	if !found {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The cluster serves no object at %s, so there is nothing to label. Nothing was written.", ref)
		return out
	}

	switch got := live.GetLabels()[markers.TagEstate]; {
	case got == estate:
		out.Outcome = OutcomeAlreadyStamped
		out.Detail = "Already carries this estate's label; nothing written."
		return out
	case got != "":
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("Carries the label tofu-estate = %q, owned by another estate. A migration never adopts another estate's object; nothing was written.", got)
		return out
	}

	dry, rejected, err := e.patcher.PatchLabel(ctx, ref, markers.TagEstate, estate, e.fieldManager, true)
	if err != nil {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The label write on %s could not be submitted to the cluster for a dry run: %s. Nothing was written.", ref, err)
		return out
	}
	if rejected != "" {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The API server refused the label write on %s: %s. Nothing was written.", ref, rejected)
		return out
	}
	if extra := changedOutsideManifestLabels(live.Object, dry.Object); len(extra) > 0 {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("Labelling this %s would also change %s, which the server's own dry run of the patch reports. Approve is a labels-only write on a Kubernetes object; nothing was written. Something between this client and the stored object - a mutating admission webhook, most likely - rewrites more than was asked for, and that has to be resolved first.", e.typeName, strings.Join(extra, ", "))
		return out
	}

	written, rejected, err := e.patcher.PatchLabel(ctx, ref, markers.TagEstate, estate, e.fieldManager, false)
	if err != nil {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The label write on %s failed: %s. The write may have partly landed; read the object's labels with kubectl before deciding what to do next.", ref, err)
		return out
	}
	if rejected != "" {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The API server refused the label write on %s: %s. Nothing was written.", ref, rejected)
		return out
	}

	out.Outcome = OutcomeStamped
	out.Detail = "Wrote the tofu-estate label. The Kubernetes marker carries no address: the object is re-bound by the apiVersion, kind, namespace and name inside its manifest."
	if written == nil || written.GetLabels()[markers.TagEstate] != estate {
		out.Detail = "The write reported no error, but the object read back afterwards does not carry the tofu-estate label. Verify with kubectl before relying on this."
	}
	return out
}

// manifestBookkeeping are the metadata keys the API server maintains for
// itself, which move on any write and are not a change to the object
// anyone declared. Excluded from [changedOutsideManifestLabels] for the
// reason tags_all is excluded from [driftedAttrs]: they move BECAUSE this
// write moves, so reporting them would make every label write look like a
// write of something else.
//
//   - managedFields: the patch's own field-manager entry lands here.
//   - resourceVersion and generation: the store's own counters.
//   - labels: the one map this write exists to change, the same
//     exemption [changedOutsideLabels] makes for the metadata block's
//     labels attribute.
var manifestBookkeeping = map[string]bool{
	"managedFields":   true,
	"resourceVersion": true,
	"generation":      true,
	"labels":          true,
}

// changedOutsideManifestLabels names the paths at which the object the
// server would store differs from the object it holds now, outside the
// labels map and its own bookkeeping. It is [changedOutsideLabels] for
// the manifest shape, judged on the API server's own dry-run answer
// rather than on a provider's plan, because the manifest shape's write
// does not go through a provider.
//
// A difference is reported at the shallowest path where the two disagree,
// so a rewritten spec reads "spec.replicas" and not one line per leaf
// beneath it.
func changedOutsideManifestLabels(live, planned map[string]any) []string {
	var out []string
	changedOutsideManifestLabelsAt(live, planned, "", &out)
	sort.Strings(out)
	return out
}

func changedOutsideManifestLabelsAt(live, planned map[string]any, prefix string, out *[]string) {
	keys := map[string]bool{}
	for k := range live {
		keys[k] = true
	}
	for k := range planned {
		keys[k] = true
	}
	for k := range keys {
		if prefix == "metadata." && manifestBookkeeping[k] {
			continue
		}
		path := prefix + k
		lv, lok := live[k]
		pv, pok := planned[k]
		switch {
		case !lok || !pok:
			*out = append(*out, path)
		default:
			lm, lIsMap := lv.(map[string]any)
			pm, pIsMap := pv.(map[string]any)
			if lIsMap && pIsMap {
				changedOutsideManifestLabelsAt(lm, pm, path+".", out)
				continue
			}
			if !sameJSONValue(lv, pv) {
				*out = append(*out, path)
			}
		}
	}
}

// sameJSONValue compares two decoded JSON values structurally. It is
// fmt.Sprint over the two rather than reflect.DeepEqual so that a list
// whose elements are maps compares by content in a stable order - the
// values here come from the same decoder on both sides, so their key
// iteration is not what varies; what varies is the numeric type a
// resourceVersion or a replica count decodes to, and both sides decode it
// the same way.
func sameJSONValue(a, b any) bool {
	am, aIsMap := a.(map[string]any)
	bm, bIsMap := b.(map[string]any)
	if aIsMap != bIsMap {
		return false
	}
	if aIsMap {
		var diff []string
		changedOutsideManifestLabelsAt(am, bm, "", &diff)
		return len(diff) == 0
	}
	al, aIsList := a.([]any)
	bl, bIsList := b.([]any)
	if aIsList != bIsList {
		return false
	}
	if aIsList {
		if len(al) != len(bl) {
			return false
		}
		for i := range al {
			if !sameJSONValue(al[i], bl[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}
