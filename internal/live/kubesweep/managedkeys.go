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
// It is #1211's SAFETY RAIL and not its source. The source of a proposed
// removal is the estate's own record of what the configuration last
// declared (internal/live/projection's residueFields.ManifestMetadataKeys);
// this narrows that set so a key our field manager does not own is never
// proposed for deletion. The rail can only decline a candidate, never
// invent one.
//
// That split is measured rather than chosen. managedFields was tried as
// the source first (PR #1259, kind v1.36.1, hashicorp/kubernetes 3.2.1)
// and it does not work on this provider: computed_fields makes the apply
// resend every key the object already had, foreign ones included, and
// server-side apply then records OUR manager as their writer. One apply
// later the API server's own kubernetes.io/metadata.name has Terraform as
// its only owner, a removal rule built on that set proposes deleting it,
// the server writes it straight back, and the plan churns for ever. So
// managedFields answers "who wrote this field" exactly, and the question
// is "did this configuration declare it". launderedEntries in this
// package's own test file pins that reading verbatim.
//
// As a rail the laundering is harmless - it makes the set permissive,
// and a permissive rail declines nothing it should have passed.
//
// The one catch is access: the hashicorp/kubernetes provider strips
// managedFields out of the `object` attribute it hands back (its own
// RemoveServerSideFields), so reading it takes a GET the provider path
// does not make, which is what [Client.ReadObject] is for. The projection
// only makes that GET when a removal candidate already exists, so a
// converged estate pays nothing.

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
