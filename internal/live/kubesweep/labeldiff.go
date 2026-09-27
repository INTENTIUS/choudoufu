// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import "sort"

// labelWriteBookkeeping are the metadata keys the API server maintains for
// itself, which move on any write and are not a change to the object
// anyone declared. Excluded from [ChangedOutsideLabels] for the reason
// tags_all is excluded from internal/live/liveimport's driftedAttrs: they
// move BECAUSE this write moves, so reporting them would make every label
// write look like a write of something else.
//
//   - managedFields: the patch's own field-manager entry lands here.
//   - resourceVersion and generation: the store's own counters.
//   - labels: the one map a label write exists to change, the same
//     exemption liveimport's changedOutsideLabels makes for the metadata
//     block's labels attribute. A caller that must also bound what moved
//     INSIDE the map checks it itself; internal/live/untag does.
var labelWriteBookkeeping = map[string]bool{
	"managedFields":   true,
	"resourceVersion": true,
	"generation":      true,
	"labels":          true,
}

// ChangedOutsideLabels names the paths at which the object the server
// would store (planned: its answer to a dryRun=All label patch) differs
// from the object it holds now (live), outside the labels map and its own
// bookkeeping. Both writes in patch.go are judged by it: live-import's
// adoption and live-mv's move of a manifest-shape object
// (internal/live/liveimport/manifest.go), and live-untag's release of one
// (internal/live/untag, GitHub issue #1656).
//
// A difference is reported at the shallowest path where the two disagree,
// so a rewritten spec reads "spec.replicas" and not one line per leaf
// beneath it.
func ChangedOutsideLabels(live, planned map[string]any) []string {
	var out []string
	changedOutsideLabelsAt(live, planned, "", &out)
	sort.Strings(out)
	return out
}

func changedOutsideLabelsAt(live, planned map[string]any, prefix string, out *[]string) {
	keys := map[string]bool{}
	for k := range live {
		keys[k] = true
	}
	for k := range planned {
		keys[k] = true
	}
	for k := range keys {
		if prefix == "metadata." && labelWriteBookkeeping[k] {
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
				changedOutsideLabelsAt(lm, pm, path+".", out)
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
		changedOutsideLabelsAt(am, bm, "", &diff)
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
