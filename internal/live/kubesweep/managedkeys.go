// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"encoding/json"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// This file answers one question about a live object, for GitHub issue
// #1211: which metadata.labels and metadata.annotations keys did THIS
// estate's own field manager write?
//
// It exists because a stateless run has nowhere else to learn it. A label
// deleted from a kubernetes_manifest's configuration is not in the
// configuration any more, and a stateless run keeps no last-applied copy
// of what the configuration used to say, so the prior state the
// projection builds cannot carry the deleted key and the provider's
// computed_fields rule leaves the live value alone - the plan says "No
// changes" against an object that still has the label. See
// mirrorManifestComputedFields in internal/live/projection.
//
// metadata.managedFields is the exact answer and the API server maintains
// it: server-side apply records, per field manager, every field that
// manager last wrote. It is the same argument the estate label rests on -
// the authority is on the object, not in a cache beside it. The one
// catch is access: the hashicorp/kubernetes provider strips managedFields
// out of the `object` attribute it hands back (its own
// RemoveServerSideFields), so reading it takes a GET the provider path
// does not make, which is what [Client.ReadObject] is for.
//
// The key set matters exactly, not approximately. Mirroring the live maps
// wholesale instead makes the removal plan, but it also proposes deleting
// every key some OTHER manager wrote - kubectl's, a controller's, and the
// API server's own kubernetes.io/metadata.name on a Namespace - which the
// server then writes straight back, so the plan never converges. GitHub
// issue #1211's scouting measured that churn; ManagedMetadataKeys is the
// narrow set that does not produce it.

// ManagedMetadataKeys reports which keys of each named metadata map
// fieldManager owns on obj, read from the object's own
// metadata.managedFields.
//
// fields names the metadata maps to answer for - "labels" and
// "annotations", as [markers.ManifestComputedMetadataAttrs] spells them.
// They are passed in rather than hardcoded so that the map this returns
// is keyed by exactly the names the caller loops over; a caller and a
// callee that spell the same field differently is a defect this signature
// cannot have. Every named field gets an entry, empty when the manager
// owns nothing there.
//
// present reports whether obj carries managedFields AT ALL, which is the
// distinction a caller has to act on:
//
//   - present, with an empty set for a field: this manager owns nothing
//     there. That is an exact answer, not a failure - an object adopted
//     from another tool, or one this fork has never applied, genuinely
//     has no keys of ours to remove.
//   - not present: the object carries no managedFields entries whatsoever
//     (a cluster predating the mechanism, or a client that stripped them
//     on the way through). Nothing can be concluded, and a caller that
//     treats it as "owns nothing" is silently back to the wrong answer.
//
// fieldManager defaults to [DefaultFieldManager] when empty. Entries for
// a subresource are skipped, as [ControllerMade] skips them, because a
// status writer is not a writer of the object's own metadata; entries for
// every operation are unioned, because both an Apply and an Update entry
// under our manager's name record fields our manager wrote.
func ManagedMetadataKeys(obj *unstructured.Unstructured, fieldManager string, fields []string) (keys map[string]map[string]bool, present bool) {
	keys = make(map[string]map[string]bool, len(fields))
	for _, f := range fields {
		keys[f] = map[string]bool{}
	}
	if obj == nil {
		return keys, false
	}
	if fieldManager == "" {
		fieldManager = DefaultFieldManager
	}
	entries := obj.GetManagedFields()
	if len(entries) == 0 {
		return keys, false
	}
	for _, entry := range entries {
		if entry.Manager != fieldManager || entry.Subresource != "" {
			continue
		}
		if entry.FieldsV1 == nil || len(entry.FieldsV1.Raw) == 0 {
			continue
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(entry.FieldsV1.Raw, &top); err != nil {
			continue
		}
		metaRaw, ok := top["f:metadata"]
		if !ok {
			continue
		}
		var meta map[string]json.RawMessage
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			continue
		}
		for _, field := range fields {
			raw, ok := meta["f:"+field]
			if !ok {
				continue
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				continue
			}
			for encoded := range m {
				// A set's members are spelled "f:<key>". The lone "."
				// entry is the set itself rather than a member, and a
				// label key may hold dots and slashes
				// (app.kubernetes.io/name), so the prefix is the only
				// thing that may be read off the spelling.
				key, ok := strings.CutPrefix(encoded, "f:")
				if !ok || key == "" {
					continue
				}
				keys[field][key] = true
			}
		}
	}
	return keys, true
}
