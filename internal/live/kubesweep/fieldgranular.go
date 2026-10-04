// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/structured-merge-diff/v6/fieldpath"
)

// This file is what the three follow-ups to GitHub issue #1191 (PR #1828)
// ask of a cluster about the field-granular types - kubernetes_labels,
// kubernetes_annotations, kubernetes_env, kubernetes_config_map_v1_data,
// kubernetes_secret_v1_data and kubernetes_node_taint - whose ownership
// marker is the server-side-apply field manager "choudoufu:<estate>":
//
//   - where in an object each of them writes ([FieldGranularMapRoot],
//     [PodSpecRoot], [TaintsRoot]), so that the plan-time boundary, the
//     orphan sweep and the migration hand-over read the same paths;
//   - which objects a manager owns any field of
//     ([FieldManagedLister.ListFieldManaged]), which is the only way to
//     find the fields of a block that has been deleted from configuration:
//     nothing else on the object names the estate;
//   - and how ownership of exactly a write's fields moves from one manager
//     to another ([TransferFieldOwnership]), which is what a migration off
//     a stock state file - written under the provider's default manager,
//     "Terraform" - owes these types instead of a label.

// FieldGranularMapAttrs are the map attributes a field-granular type
// writes into its object, in the order the boundary reads them, each with
// the FieldsV1 path to the map it writes ([FieldGranularMapRoot]).
var FieldGranularMapAttrs = []string{"labels", "annotations", "template_annotations", "data"}

// FieldGranularMapRoot is the FieldsV1 path to the map attr writes:
// metadata.labels, metadata.annotations, the pod template's annotations,
// or the object's data. ok is false for any other attribute.
func FieldGranularMapRoot(attr string) (root []string, ok bool) {
	switch attr {
	case "labels":
		return []string{"f:metadata", "f:labels"}, true
	case "annotations":
		return []string{"f:metadata", "f:annotations"}, true
	case "template_annotations":
		return []string{"f:spec", "f:template", "f:metadata", "f:annotations"}, true
	case "data":
		return []string{"f:data"}, true
	}
	return nil, false
}

// PodSpecRoot is the FieldsV1 path to the pod spec a kind keeps its
// containers in: a Pod's own spec, a CronJob's job template's pod
// template, and every other workload's pod template.
func PodSpecRoot(kind string) []string {
	switch kind {
	case "Pod":
		return []string{"f:spec"}
	case "CronJob":
		return []string{"f:spec", "f:jobTemplate", "f:spec", "f:template", "f:spec"}
	default:
		return []string{"f:spec", "f:template", "f:spec"}
	}
}

// EnvRoot is the FieldsV1 path to one container's env: the container (or,
// with init, init container) named container, under kind's pod spec.
func EnvRoot(kind, container string, init bool) []string {
	list := "f:containers"
	if init {
		list = "f:initContainers"
	}
	root := append([]string(nil), PodSpecRoot(kind)...)
	return append(root, list, ListItemMember(map[string]string{"name": container}), "f:env")
}

// TaintsRoot is the FieldsV1 path to a Node's taints.
var TaintsRoot = []string{"f:spec", "f:taints"}

// FieldGranularFixedKind is the object a field-granular type that names no
// kind patches. hashicorp/kubernetes hardcodes it per type, and names each
// such type after the patched kind's own type plus the field it writes:
// kubernetes_config_map_v1_data patches what kubernetes_config_map_v1
// manages, kubernetes_node_taint what kubernetes_node would. So the last
// segment is dropped and the kind is [KindOfType]'s, the same join the
// sweep makes. Every kind reached this way at 3.2.1 (ConfigMap, Secret,
// Node) is in the core group, whose API version is "v1".
func FieldGranularFixedKind(typeName string) (apiVersion, kind string) {
	i := strings.LastIndex(typeName, "_")
	if i <= 0 {
		return "", ""
	}
	kind, _, ok := KindOfType(typeName[:i])
	if !ok {
		return "", ""
	}
	return "v1", kind
}

// OwnedAt reports what one FieldsV1 document owns at root: the member
// names under it, sorted, and atomic true when root is owned as a leaf. It
// is [FieldOwners]' per-manager reading, exposed for a caller holding one
// manager's fields rather than a whole object.
func OwnedAt(raw []byte, root []string) (members []string, atomic bool, found bool) {
	set, atomic, found := fieldsAt(raw, root)
	if !found {
		return nil, false, false
	}
	for m := range set {
		members = append(members, m)
	}
	sort.Strings(members)
	return members, atomic, true
}

// EnvContainers reads, from one manager's FieldsV1 document on an object
// of kind, the containers whose env that manager owns any of: names under
// spec.containers, and initNames under spec.initContainers.
func EnvContainers(raw []byte, kind string) (names, initNames []string) {
	for _, list := range []struct {
		field string
		out   *[]string
	}{{"f:containers", &names}, {"f:initContainers", &initNames}} {
		root := append(append([]string(nil), PodSpecRoot(kind)...), list.field)
		items, _, found := fieldsAt(raw, root)
		if !found {
			continue
		}
		for item := range items {
			name, ok := containerName(item)
			if !ok {
				continue
			}
			envRoot := append(append([]string(nil), root...), item, "f:env")
			if members, atomic, found := fieldsAt(raw, envRoot); found && (len(members) > 0 || atomic) {
				*list.out = append(*list.out, name)
			}
		}
		sort.Strings(*list.out)
	}
	return names, initNames
}

// containerName reads the name out of a k:{"name":"..."} list-item key.
func containerName(item string) (string, bool) {
	raw, ok := strings.CutPrefix(item, "k:")
	if !ok {
		return "", false
	}
	var keys map[string]string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return "", false
	}
	name, ok := keys["name"]
	return name, ok && name != "" && len(keys) == 1
}

// FieldManagedObject is one live object some fields of which a given
// field manager owns, with exactly what it owns.
type FieldManagedObject struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	// Fields is the manager's FieldsV1 document: the union of every Apply
	// entry it has on the object outside a subresource.
	Fields []byte
}

// FieldManagedLister lists, for one kind, every object a field manager
// owns any field of through server-side apply. [Client] implements it; it
// is its own interface rather than a [Sweeper] method so that a test's
// sweeper need not grow one, and a caller holding a sweeper asks for it
// with a type assertion.
//
// The API server cannot select on managedFields, so the list is every
// object of the kind, metadata only where the client has a metadata
// client, filtered here. That is the cost of a marker that lives in
// managedFields rather than in a label (GitHub issue #1191's ruling): one
// unselected list per kind.
type FieldManagedLister interface {
	ListFieldManaged(ctx context.Context, k Kind, manager string) ([]FieldManagedObject, error)
}

var _ FieldManagedLister = (*Client)(nil)

// ListFieldManaged implements [FieldManagedLister].
func (c *Client) ListFieldManaged(ctx context.Context, k Kind, manager string) ([]FieldManagedObject, error) {
	if manager == "" {
		return nil, fmt.Errorf("listing by field manager needs a manager name")
	}
	var out []FieldManagedObject
	add := func(namespace, name string, labels, annotations map[string]string, entries []metav1.ManagedFieldsEntry) error {
		// The estate's own record Secrets are never a field-granular
		// block's, whoever wrote them; the label leg sets them aside the
		// same way ([RecordStoreObject]).
		probe := &unstructured.Unstructured{}
		probe.SetLabels(labels)
		probe.SetAnnotations(annotations)
		if RecordStoreObject(probe) {
			return nil
		}
		fields, ok, err := ManagerFields(entries, manager)
		if err != nil {
			return fmt.Errorf("%s %s: %w", k.Kind, NaturalKey(namespace, name), err)
		}
		if ok {
			out = append(out, FieldManagedObject{APIVersion: k.APIVersion, Kind: k.Kind, Namespace: namespace, Name: name, Fields: fields})
		}
		return nil
	}
	opts := metav1.ListOptions{}
	for {
		var cont string
		if c.meta != nil {
			list, err := c.meta.Resource(k.GVR).Namespace(metav1.NamespaceAll).List(ctx, opts)
			if err != nil {
				return nil, c.creds.explain(err)
			}
			for _, item := range list.Items {
				if err := add(item.GetNamespace(), item.GetName(), item.GetLabels(), item.GetAnnotations(), item.GetManagedFields()); err != nil {
					return nil, err
				}
			}
			cont = list.GetContinue()
		} else {
			list, err := c.dyn.Resource(k.GVR).Namespace(metav1.NamespaceAll).List(ctx, opts)
			if err != nil {
				return nil, c.creds.explain(err)
			}
			for _, item := range list.Items {
				if err := add(item.GetNamespace(), item.GetName(), item.GetLabels(), item.GetAnnotations(), item.GetManagedFields()); err != nil {
					return nil, err
				}
			}
			cont = list.GetContinue()
		}
		if cont == "" {
			break
		}
		opts.Continue = cont
	}
	sort.Slice(out, func(i, j int) bool {
		return NaturalKey(out[i].Namespace, out[i].Name) < NaturalKey(out[j].Namespace, out[j].Name)
	})
	return out, nil
}

// ManagerFields is the union of manager's Apply entries outside a
// subresource, as one FieldsV1 document; ok is false when manager has
// none or they own nothing. An Update entry under the same name is not
// counted: the provider writes these types by server-side apply only, and
// an Update entry's fields are not ones an Apply under the name can
// release.
func ManagerFields(entries []metav1.ManagedFieldsEntry, manager string) (raw []byte, ok bool, err error) {
	union := fieldpath.NewSet()
	for _, e := range entries {
		if e.Manager != manager || e.Operation != metav1.ManagedFieldsOperationApply || e.Subresource != "" || e.FieldsV1 == nil || len(e.FieldsV1.Raw) == 0 {
			continue
		}
		s := &fieldpath.Set{}
		if err := s.FromJSON(bytes.NewReader(e.FieldsV1.Raw)); err != nil {
			return nil, false, fmt.Errorf("decoding %s's Apply entry: %w", manager, err)
		}
		union = union.Union(s)
	}
	if union.Empty() {
		return nil, false, nil
	}
	raw, err = union.ToJSON()
	if err != nil {
		return nil, false, fmt.Errorf("encoding %s's fields: %w", manager, err)
	}
	return raw, true, nil
}

// fieldsV1Path turns a path of FieldsV1 keys ("f:metadata", `k:{...}`)
// into the structured path the server's own set type uses, by parsing the
// one-leaf document it spells.
func fieldsV1Path(keys []string) (fieldpath.Path, error) {
	doc := "{}"
	for i := len(keys) - 1; i >= 0; i-- {
		k, err := json.Marshal(keys[i])
		if err != nil {
			return nil, err
		}
		doc = "{" + string(k) + ":" + doc + "}"
	}
	s := &fieldpath.Set{}
	if err := s.FromJSON(strings.NewReader(doc)); err != nil {
		return nil, fmt.Errorf("FieldsV1 path %s: %w", strings.Join(keys, "."), err)
	}
	var out fieldpath.Path
	n := 0
	s.Iterate(func(p fieldpath.Path) {
		n++
		out = p.Copy()
	})
	if n != 1 {
		return nil, fmt.Errorf("FieldsV1 path %s spells %d paths, not one", strings.Join(keys, "."), n)
	}
	return out, nil
}

func hasPathPrefix(p, prefix fieldpath.Path) bool {
	if len(p) < len(prefix) {
		return false
	}
	for i := range prefix {
		if !p[i].Equals(prefix[i]) {
			return false
		}
	}
	return true
}

// TransferFieldOwnership returns entries with every field of writes that
// from's entries own moved into to's Apply entry - created, from the first
// entry anything moved out of, when to has none - and changed false when
// from owned none of them.
//
// "A field of writes" is a write's member and everything under it (an env
// item's name and value as well as the item), plus the write's root when
// from owns the root itself and nothing else under it after the move: an
// atomic list such as a Node's taints is owned as one leaf, and a map's
// own "." marker is left to no one otherwise. Nothing outside the writes
// moves, so a key from wrote that the configuration does not name stays
// from's, and every other manager's entries are returned as they were:
// this hands fields over from exactly one manager, which is what lets a
// migration take the provider's default manager's fields without ever
// taking another estate's.
func TransferFieldOwnership(entries []metav1.ManagedFieldsEntry, from, to string, writes []FieldWrite) ([]metav1.ManagedFieldsEntry, bool, error) {
	if from == "" || to == "" || from == to {
		return entries, false, nil
	}
	var prefixes, roots []fieldpath.Path
	for _, w := range writes {
		if len(w.Root) == 0 {
			continue
		}
		root, err := fieldsV1Path(w.Root)
		if err != nil {
			return nil, false, err
		}
		roots = append(roots, root)
		for _, m := range w.Members {
			p, err := fieldsV1Path(append(append([]string(nil), w.Root...), m))
			if err != nil {
				return nil, false, err
			}
			prefixes = append(prefixes, p)
		}
	}
	if len(roots) == 0 {
		return entries, false, nil
	}

	out := make([]metav1.ManagedFieldsEntry, 0, len(entries)+1)
	moved := fieldpath.NewSet()
	var template *metav1.ManagedFieldsEntry
	for _, e := range entries {
		if e.Manager != from || e.Subresource != "" || e.FieldsV1 == nil {
			out = append(out, e)
			continue
		}
		owned := &fieldpath.Set{}
		if err := owned.FromJSON(bytes.NewReader(e.FieldsV1.Raw)); err != nil {
			return nil, false, fmt.Errorf("decoding %s's %s entry: %w", e.Manager, e.Operation, err)
		}
		take := fieldpath.NewSet()
		owned.Iterate(func(p fieldpath.Path) {
			for _, prefix := range prefixes {
				if hasPathPrefix(p, prefix) {
					take.Insert(p.Copy())
					return
				}
			}
		})
		rest := owned.Difference(take)
		for _, root := range roots {
			if !rest.Has(root) {
				continue
			}
			under := false
			rest.Iterate(func(p fieldpath.Path) {
				if len(p) > len(root) && hasPathPrefix(p, root) {
					under = true
				}
			})
			if !under {
				take.Insert(root)
				rest = rest.Difference(fieldpath.NewSet(root))
			}
		}
		if take.Empty() {
			out = append(out, e)
			continue
		}
		moved = moved.Union(take)
		if template == nil {
			template = e.DeepCopy()
		}
		if rest.Empty() {
			continue
		}
		raw, err := rest.ToJSON()
		if err != nil {
			return nil, false, fmt.Errorf("encoding %s's %s entry: %w", e.Manager, e.Operation, err)
		}
		e.FieldsV1 = &metav1.FieldsV1{Raw: raw}
		out = append(out, e)
	}
	if template == nil {
		return entries, false, nil
	}

	for i := range out {
		e := &out[i]
		if e.Manager != to || e.Operation != metav1.ManagedFieldsOperationApply || e.Subresource != "" {
			continue
		}
		owned := &fieldpath.Set{}
		if e.FieldsV1 != nil {
			if err := owned.FromJSON(bytes.NewReader(e.FieldsV1.Raw)); err != nil {
				return nil, false, fmt.Errorf("decoding %s's %s entry: %w", e.Manager, e.Operation, err)
			}
		}
		raw, err := owned.Union(moved).ToJSON()
		if err != nil {
			return nil, false, fmt.Errorf("encoding %s's %s entry: %w", e.Manager, e.Operation, err)
		}
		e.FieldsV1 = &metav1.FieldsV1{Raw: raw}
		return out, true, nil
	}
	raw, err := moved.ToJSON()
	if err != nil {
		return nil, false, fmt.Errorf("encoding %s's Apply entry: %w", to, err)
	}
	template.Manager = to
	template.Operation = metav1.ManagedFieldsOperationApply
	template.FieldsV1 = &metav1.FieldsV1{Raw: raw}
	return append(out, *template), true, nil
}

// FieldTransferer moves ownership of a field-granular write's fields from
// one manager to another on a live object. [Client] implements it.
type FieldTransferer interface {
	ReadObject(ctx context.Context, ref ObjectRef) (obj *unstructured.Unstructured, found bool, err error)
	TransferFields(ctx context.Context, ref ObjectRef, from, to string, writes []FieldWrite) (moved bool, err error)
}

var _ FieldTransferer = (*Client)(nil)

// TransferFields implements [FieldTransferer]: [TransferFieldOwnership]
// over the object as read, sent as one JSON patch replacing
// metadata.managedFields and nothing else, pinned to the resourceVersion
// read so a write landing in between is answered with a conflict - the
// transfer is then recomputed from a fresh read - rather than having its
// ownership overwritten. It changes no field's value, so the server
// records no Update entry for it; the same request
// [Client.handMarkersToApply] makes for the marker labels (GitHub issue
// #1704). moved is false, with nothing sent, when from owns none of the
// writes' fields.
func (c *Client) TransferFields(ctx context.Context, ref ObjectRef, from, to string, writes []FieldWrite) (bool, error) {
	if ref.APIVersion == "" || ref.Kind == "" || ref.Name == "" {
		return false, fmt.Errorf("an object needs an apiVersion, a kind and a name to hand its fields over")
	}
	client, err := c.resourceClient(ref.APIVersion, ref.Kind, ref.Namespace)
	if err != nil {
		return false, err
	}
	obj, err := client.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("reading %s %s: %w", ref.Kind, NaturalKey(ref.Namespace, ref.Name), err)
	}
	for attempt := 0; ; attempt++ {
		entries, changed, err := TransferFieldOwnership(obj.GetManagedFields(), from, to, writes)
		if err != nil {
			return false, err
		}
		if !changed {
			return false, nil
		}
		raw, err := json.Marshal([]map[string]any{
			{"op": "replace", "path": "/metadata/managedFields", "value": entries},
			{"op": "replace", "path": "/metadata/resourceVersion", "value": obj.GetResourceVersion()},
		})
		if err != nil {
			return false, fmt.Errorf("building the ownership patch: %w", err)
		}
		_, err = client.Patch(ctx, ref.Name, types.JSONPatchType, raw, metav1.PatchOptions{FieldManager: to})
		if err == nil {
			return true, nil
		}
		if !apierrors.IsConflict(err) || attempt >= ownershipRetries {
			return false, fmt.Errorf("handing the fields of %s %s from %q to %q: %w", ref.Kind, NaturalKey(ref.Namespace, ref.Name), from, to, err)
		}
		fresh, getErr := client.Get(ctx, ref.Name, metav1.GetOptions{})
		if getErr != nil {
			return false, fmt.Errorf("handing the fields of %s %s from %q to %q: %w; re-reading after that conflict: %w", ref.Kind, NaturalKey(ref.Namespace, ref.Name), from, to, err, getErr)
		}
		obj = fresh
	}
}
