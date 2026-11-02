// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import "sort"

// This file is the check a marker patch ([LabelPatcher]) is judged by: the
// server's dry-run answer, diffed against the live object outside the
// markers. It moved here from internal/live/liveimport's manifest carrier
// when live-mv's manifest rename came to need the same judgement (GitHub
// issue #1639), so the two cannot disagree about what a markers-only
// write leaves alone.

// manifestBookkeeping are the metadata keys the API server maintains for
// itself, which move on any write and are not a change to the object
// anyone declared. Excluded from [ChangedOutsideMarkers] for the reason
// internal/live/liveimport excludes tags_all from its drift check: they
// move BECAUSE this write moves, so reporting them would make every label
// write look like a write of something else.
//
//   - managedFields: the patch's own field-manager entry lands here.
//   - resourceVersion and generation: the store's own counters.
//   - labels: the one map the label write exists to change, the same
//     exemption liveimport's changedOutsideLabels makes for the metadata
//     block's labels attribute.
//
// Annotations are not exempt wholesale: the write adds one annotation, the
// block address (GitHub issue #1639), and [ChangedOutsideMarkers] sets
// that one key aside and compares every other annotation.
var manifestBookkeeping = map[string]bool{
	"managedFields":   true,
	"resourceVersion": true,
	"generation":      true,
	"labels":          true,
}

// ChangedOutsideMarkers names the paths at which planned - the object the
// server answered a dry run of a marker patch with - differs from live, the
// object it holds now, outside the labels map, the one annotation
// annotationKey names (the block address, GitHub issue #1639) and the
// server's own bookkeeping. It is how a marker patch is judged: live-import's
// adoption of a manifest-declared object and live-mv's rename of one both
// refuse a write whose dry run reports anything here, because something
// between the client and the store (a mutating admission webhook, most
// likely) rewrote more than was asked for.
//
// A difference is reported at the shallowest path where the two disagree,
// so a rewritten spec reads "spec.replicas" and not one line per leaf
// beneath it.
func ChangedOutsideMarkers(live, planned map[string]any, annotationKey string) []string {
	var out []string
	live, planned = withoutAnnotation(live, annotationKey), withoutAnnotation(planned, annotationKey)
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

// withoutAnnotation returns obj with metadata.annotations' key entry
// removed, and the annotations map removed with it when that was its only
// entry, so that a write adding the annotation to an object that had none
// compares equal outside it. obj itself is not modified: the maps on the
// path to the key are copied.
func withoutAnnotation(obj map[string]any, key string) map[string]any {
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return obj
	}
	ann, ok := meta["annotations"].(map[string]any)
	if !ok {
		return obj
	}
	if _, has := ann[key]; !has {
		return obj
	}
	newAnn := make(map[string]any, len(ann))
	for k, v := range ann {
		if k != key {
			newAnn[k] = v
		}
	}
	newMeta := make(map[string]any, len(meta))
	for k, v := range meta {
		newMeta[k] = v
	}
	if len(newAnn) == 0 {
		delete(newMeta, "annotations")
	} else {
		newMeta["annotations"] = newAnn
	}
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		out[k] = v
	}
	out["metadata"] = newMeta
	return out
}
