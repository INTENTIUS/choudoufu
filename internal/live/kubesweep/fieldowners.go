// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"context"
	"encoding/json"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// This file answers the question GitHub issue #1191 (ruled 2026-10-03) and
// #1106 section 3 need asked of a live object: which OTHER field managers
// own a field a field-granular write is about to make?
//
// A field-granular hashicorp/kubernetes type (kubernetes_labels,
// kubernetes_annotations, kubernetes_env, kubernetes_config_map_v1_data,
// kubernetes_secret_v1_data, kubernetes_node_taint) writes some fields of an
// object it does not own, by server-side apply, under a field manager it
// names. Under a live block that manager is the estate's own,
// "choudoufu:<estate>", so metadata.managedFields says, per field, which
// estate wrote it - and a field written by another estate's manager is that
// estate's, whatever label the object itself carries.
//
// The answer is computed from managedFields alone, with no provider in the
// loop: [FieldWrite] names where in the object the write lands and which
// keys under that point it sets, and [FieldOwners] walks every manager's
// FieldsV1 down the same path.

// FieldWrite is where one field-granular write lands in its object.
//
// Root is the FieldsV1 path to the set the write adds members to - for
// metadata.labels, ["f:metadata", "f:labels"]; for one container's env,
// ["f:spec", "f:template", "f:spec", "f:containers", `k:{"name":"app"}`,
// "f:env"]. Members are the FieldsV1 spellings of the members it sets under
// Root: "f:<key>" for a map key, `k:{...}` for an associative-list item.
//
// Atomic is true when Root may be an atomic list - a Node's spec.taints -
// which managedFields records as one leaf with no members, so a manager
// holding that leaf owns the write whole. Every other root is granular: a
// leaf with no members there is what a release leaves behind (an apply of
// the empty map or env list, #1885) and owns nothing.
type FieldWrite struct {
	Root    []string
	Members []string
	Atomic  bool
}

// FieldOwner is one manager's claim on a planned write.
type FieldOwner struct {
	Manager string
	// Members is the subset of the write's members this manager owns,
	// sorted. Empty with Atomic true when the manager owns Root as a whole
	// - an atomic list such as a Node's spec.taints, which managedFields
	// records as one leaf with no members.
	Members []string
	Atomic  bool
}

// FieldOwners reports, for every manager other than exclude that owns any
// part of write on obj, what it owns. Entries for a subresource are
// skipped, as [ManagedMetadataKeys] skips them. The result is sorted by
// manager name, so a message built from it reads the same on every run.
func FieldOwners(obj *unstructured.Unstructured, write FieldWrite, exclude string) []FieldOwner {
	if obj == nil || len(write.Root) == 0 {
		return nil
	}
	byManager := map[string]*FieldOwner{}
	for _, entry := range obj.GetManagedFields() {
		if entry.Manager == exclude || entry.Subresource != "" {
			continue
		}
		if entry.FieldsV1 == nil || len(entry.FieldsV1.Raw) == 0 {
			continue
		}
		members, atomic, found := fieldsAt(entry.FieldsV1.Raw, write.Root)
		if !found || (atomic && !write.Atomic) {
			continue
		}
		var owned []string
		if !atomic {
			for _, m := range write.Members {
				if members[m] {
					owned = append(owned, m)
				}
			}
			if len(owned) == 0 {
				continue
			}
		}
		o := byManager[entry.Manager]
		if o == nil {
			o = &FieldOwner{Manager: entry.Manager}
			byManager[entry.Manager] = o
		}
		o.Atomic = o.Atomic || atomic
		o.Members = append(o.Members, owned...)
	}
	out := make([]FieldOwner, 0, len(byManager))
	for _, o := range byManager {
		sort.Strings(o.Members)
		o.Members = dedupe(o.Members)
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manager < out[j].Manager })
	return out
}

// fieldsAt descends raw, one FieldsV1 document, along path and returns the
// member names under it. atomic is true when the path ends at a leaf - an
// empty set, or one holding only the "." self-marker - which is how
// managedFields records a field owned as a whole.
func fieldsAt(raw []byte, path []string) (members map[string]bool, atomic bool, found bool) {
	cur := raw
	for _, step := range path {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(cur, &m); err != nil {
			return nil, false, false
		}
		next, ok := m[step]
		if !ok {
			return nil, false, false
		}
		cur = next
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(cur, &m); err != nil {
		return nil, false, false
	}
	members = map[string]bool{}
	for k := range m {
		if k == "." {
			continue
		}
		members[k] = true
	}
	return members, len(members) == 0, true
}

// MapMember is the FieldsV1 spelling of a map key.
func MapMember(key string) string { return "f:" + key }

// ListItemMember is the FieldsV1 spelling of an associative-list item with
// the given key fields, e.g. k:{"name":"app"}. encoding/json sorts a map's
// keys, which is the order the API server writes a multi-field key in
// (sigs.k8s.io/structured-merge-diff's PathElement serialisation sorts by
// field name).
func ListItemMember(keys map[string]string) string {
	raw, err := json.Marshal(keys)
	if err != nil {
		return ""
	}
	return "k:" + string(raw)
}

func dedupe(sorted []string) []string {
	if len(sorted) < 2 {
		return sorted
	}
	out := sorted[:1]
	for _, s := range sorted[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

// ObjectReader reads one live object back. *Client is one; it is the one
// method of [LabelPatcher] a field-owner check needs, named on its own so a
// caller holding only a [Sweeper] can ask for exactly this.
type ObjectReader interface {
	ReadObject(ctx context.Context, ref ObjectRef) (obj *unstructured.Unstructured, found bool, err error)
}
