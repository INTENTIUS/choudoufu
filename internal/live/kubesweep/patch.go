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
	"maps"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/structured-merge-diff/v6/fieldpath"
)

// This file holds the two WRITES this package makes. Each is one merge
// patch confined to the ownership markers, metadata.labels[tofu-estate]
// and, since GitHub issue #1639, the address annotation
// metadata.annotations[choudoufu.intentius.io/tofu-address] beside it,
// under the caller's own credential, sent first with dryRun=All so the
// server's verdict - its validation, its admission policies, its RBAC - is
// read before anything is persisted:
//
//   - [Client.PatchMarkers] sets the markers (GitHub issues #1104 and
//     #1109, ruled 2026-09-13 by the maintainer on both): live-import's
//     adoption of a manifest-shape object, and live-mv's cross-estate move
//     and rename of one.
//   - [Client.DeleteMarkers] removes them, the body naming each key with a
//     null value (GitHub issue #1656, ruled 2026-09-27): live-untag's
//     release of a manifest-shape orphan under undeclared_tagged = "untag".
//
// Every caller diffs the dry-run answer against the live object with
// [ChangedOutsideMarkers] before sending the real write, so the argument
// below holds for each.
//
// # Why a patch rather than a write through the provider
//
// The alternative the two issues weighed was a labels-only plan and apply
// through hashicorp/kubernetes, rebuilding the whole manifest from what
// the state recorded and merging the label into it. That is what the
// built-in object-metadata types do (internal/live/liveimport/labels.go),
// and it is the right shape there, because those types have a typed
// metadata block and the provider's plan can be asserted to touch nothing
// else. kubernetes_manifest has no such block: its whole object is one
// dynamic argument, so a write through the provider is a re-apply of
// every field it manages, computed from a state file that may be days
// stale. The blast radius of the smallest possible mistake is the whole
// custom resource. A merge patch naming one key under metadata.labels
// cannot, by the shape of the request, reach anything else; and the
// dry-run answer is diffed against the live object anyway
// (internal/live/liveimport/manifest.go, internal/live/untag/manifest.go)
// so that a mutating admission
// webhook rewriting the spec on the way past is caught rather than
// assumed away.
//
// # The field manager
//
// The patch names the manager the provider itself writes under.
// [DefaultFieldManager] is the provider's own default, measured rather
// than read from a document: after `terraform apply` of a
// kubernetes_manifest block on kind 1.36, the object's
// metadata.managedFields holds one entry, `manager: Terraform,
// operation: Apply`. A block that sets `field_manager { name = ... }`
// overrides it, and the caller passes that name instead.
//
// Naming the manager is not enough on its own (GitHub issue #1704).
// managedFields keys ownership on manager AND operation, so a merge patch
// under "Terraform" records a `Terraform, Update` entry owning the markers
// beside the provider's `Terraform, Apply` entry, and the provider's next
// server-side apply that CHANGES a marker - a moved-block rename rewrites
// the address annotation - fails with "conflict with \"Terraform\"". A
// marker whose value never changes never showed it, which is why the
// tofu-estate label written this way before #1639 did not. So after a
// real (not dry-run) [Client.PatchMarkers], [Client.handMarkersToApply]
// moves the marker keys, and only those, from the Update entry into the
// Apply entry - the same rewrite kubectl's client-side to server-side
// apply migration makes (k8s.io/client-go/util/csaupgrade), narrowed to
// the marker paths. The provider's apply then owns the markers alone and
// changes them without force_conflicts.
//
// The patch itself cannot simply be a server-side apply under the
// provider's manager, which would have been one request: an Apply's body
// is that manager's whole intent, so an Apply naming only the markers
// under "Terraform" releases every other field the provider applied, and
// the server deletes each one no other manager owns. Measured against
// client-go's field-managed tracker (the API server's own managedfields
// code): after the provider's apply of a CronTab, a markers-only Apply
// under "Terraform" left spec null and the configuration's own labels
// gone. Nor can it be an Apply under a manager of its own: the provider's
// apply would then conflict with that manager instead.
//
// [Client.DeleteMarkers] needs no such step: removing a key removes every
// manager's ownership of it, so nothing is left for a later apply to
// conflict with (TestDeleteMarkersLeavesNoOwnerBehind).
//
// If #1106 section 3 rules that the estate is the field manager, the
// transfer targets that manager's Apply entry instead; the manager is a
// parameter.

// DefaultFieldManager is the field manager hashicorp/kubernetes writes a
// kubernetes_manifest object under when the block sets no
// `field_manager { name = ... }`. See this file's doc comment for how it
// was established.
const DefaultFieldManager = "Terraform"

// ObjectRef names one live object by the natural key GitHub issue #1016
// ruled a Kubernetes object is bound by: its group-version, its kind, and
// its namespace and name (Namespace empty for a cluster-scoped kind).
type ObjectRef struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
}

// String renders the ref the way [ManifestImportID] renders it, so that a
// message about an object reads the same wherever it was produced.
func (r ObjectRef) String() string {
	return ManifestImportID(r.APIVersion, r.Kind, r.Namespace, r.Name)
}

// LabelPatcher is the cluster half of a label write, narrowed to the two
// calls a caller needs: read the object as it is, and set one label on
// it. [Client] implements it against a real API server; a test stands in
// for one.
//
// Both live-import's adoption of a manifest-shape object (#1109) and
// live-mv's cross-estate move of one (#1104) make exactly this write, so
// there is one implementation of it and one place its safety is argued.
type LabelPatcher interface {
	// ReadObject returns the live object at ref. found is false, with a
	// nil error, when the server answers that no such object exists; an
	// error is a cluster that could not answer, which is never the same
	// thing.
	ReadObject(ctx context.Context, ref ObjectRef) (obj *unstructured.Unstructured, found bool, err error)

	// PatchMarkers sets every entry of labels into metadata.labels and
	// every entry of annotations into metadata.annotations on the object
	// at ref, through ONE merge patch under fieldManager, and returns the
	// object the server produced. A real (not dry-run) write then moves
	// the markers' ownership from fieldManager's Update entry to its Apply
	// entry, so the provider's own apply can change them later without a
	// field manager conflict (GitHub issue #1704; this file's doc comment
	// says why it is a second request). The label is the tofu-estate marker; the
	// annotation is the block address beside it (GitHub issue #1639), and
	// the two go in one request so an object is never left carrying one
	// without the other by a write that half landed.
	//
	// With dryRun the server validates, defaults, runs admission and
	// persists nothing, so the returned object is what the real write
	// would have stored. rejected carries the server's own words for a
	// refusal it answered with (a validation failure, an admission
	// policy's denial, a 403 from RBAC) and is empty when the server
	// accepted; err is a cluster that could not answer at all.
	PatchMarkers(ctx context.Context, ref ObjectRef, labels, annotations map[string]string, fieldManager string, dryRun bool) (obj *unstructured.Unstructured, rejected string, err error)
}

var _ LabelPatcher = (*Client)(nil)

// resourceClient resolves the dynamic client for one kind at one exact
// apiVersion, the group-version's own resource list naming the resource
// the kind is served as and whether it is namespaced - the same discovery
// request [Client.Serves] makes, and asked at the version the caller
// names rather than the group's preferred one, because a kind served at
// v2 alone does not serve an object written for v1.
func (c *Client) resourceClient(apiVersion, kind, namespace string) (dynamic.ResourceInterface, error) {
	list, err := c.disc.ServerResourcesForGroupVersion(apiVersion)
	if err != nil {
		return nil, fmt.Errorf("API discovery for %s: %w", apiVersion, err)
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, fmt.Errorf("apiVersion %q: %w", apiVersion, err)
	}
	var res *metav1.APIResource
	if list != nil {
		for i := range list.APIResources {
			r := &list.APIResources[i]
			if r.Kind == kind && !strings.Contains(r.Name, "/") {
				res = r
				break
			}
		}
	}
	if res == nil {
		return nil, fmt.Errorf("the cluster serves no kind %s at apiVersion %s", kind, apiVersion)
	}
	if !res.Namespaced {
		return c.dyn.Resource(gv.WithResource(res.Name)), nil
	}
	return c.dyn.Resource(gv.WithResource(res.Name)).Namespace(namespace), nil
}

// ReadObject implements [LabelPatcher].
func (c *Client) ReadObject(ctx context.Context, ref ObjectRef) (*unstructured.Unstructured, bool, error) {
	if ref.APIVersion == "" || ref.Kind == "" || ref.Name == "" {
		return nil, false, fmt.Errorf("an object needs an apiVersion, a kind and a name to be read")
	}
	client, err := c.resourceClient(ref.APIVersion, ref.Kind, ref.Namespace)
	if err != nil {
		return nil, false, err
	}
	obj, err := client.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading %s %s: %w", ref.Kind, NaturalKey(ref.Namespace, ref.Name), err)
	}
	return obj, true, nil
}

// PatchMarkers implements [LabelPatcher]. The body is a JSON merge patch
// naming the given keys inside metadata.labels and metadata.annotations
// and nothing else, so the request itself cannot carry a change to any
// other field; what the SERVER then does with it is the caller's to
// check, which is what the dry run is for.
func (c *Client) PatchMarkers(ctx context.Context, ref ObjectRef, labels, annotations map[string]string, fieldManager string, dryRun bool) (*unstructured.Unstructured, string, error) {
	if ref.APIVersion == "" || ref.Kind == "" || ref.Name == "" {
		return nil, "", fmt.Errorf("an object needs an apiVersion, a kind and a name to be patched")
	}
	if len(labels) == 0 && len(annotations) == 0 {
		return nil, "", fmt.Errorf("a marker patch needs a label or an annotation to write")
	}
	for k := range labels {
		if k == "" {
			return nil, "", fmt.Errorf("a label patch needs a label key")
		}
	}
	for k := range annotations {
		if k == "" {
			return nil, "", fmt.Errorf("an annotation patch needs an annotation key")
		}
	}
	meta := map[string]any{}
	if len(labels) > 0 {
		meta["labels"] = labels
	}
	if len(annotations) > 0 {
		meta["annotations"] = annotations
	}
	obj, rejected, err := c.mergePatch(ctx, ref, map[string]any{"metadata": meta}, fieldManager, dryRun)
	if err != nil || rejected != "" || dryRun {
		return obj, rejected, err
	}
	return c.handMarkersToApply(ctx, ref, obj, slices.Collect(maps.Keys(labels)), slices.Collect(maps.Keys(annotations)), fieldManager)
}

// handMarkersToApply moves ownership of the marker keys a merge patch just
// wrote from the field manager's Update entry to its Apply entry (GitHub
// issue #1704). See this file's "The field manager" section for why.
//
// The request is a JSON patch replacing metadata.managedFields and nothing
// else, pinned to the resourceVersion the marker patch returned so that a
// write landing in between fails with a conflict rather than having its
// ownership overwritten; it changes no field value, so the server records
// no new Update entry for it. It is not dry-run first: the only thing it
// can change is the ownership bookkeeping [ChangedOutsideMarkers] already
// sets aside, and the marker patch before it was.
func (c *Client) handMarkersToApply(ctx context.Context, ref ObjectRef, obj *unstructured.Unstructured, labels, annotations []string, fieldManager string) (*unstructured.Unstructured, string, error) {
	if fieldManager == "" {
		fieldManager = DefaultFieldManager
	}
	client, err := c.resourceClient(ref.APIVersion, ref.Kind, ref.Namespace)
	if err != nil {
		return nil, "", err
	}
	failed := func(err error) error {
		return fmt.Errorf("the markers on %s %s were written under field manager %q, but handing their ownership to that manager's server-side apply failed, so the provider's next apply that changes a marker will report a field manager conflict: %w", ref.Kind, NaturalKey(ref.Namespace, ref.Name), fieldManager, err)
	}
	// A controller that writes the object between the marker patch and
	// this one - a Deployment's status, measured on kind during #1704's
	// smoke run - moves the resourceVersion and the server answers 409.
	// The transfer is recomputed from a fresh read and sent again.
	for attempt := 0; ; attempt++ {
		entries, changed, err := transferMarkerOwnership(obj.GetManagedFields(), fieldManager, labels, annotations)
		if err != nil {
			return nil, "", failed(err)
		}
		if !changed {
			return obj, "", nil
		}
		raw, err := json.Marshal([]map[string]any{
			{"op": "replace", "path": "/metadata/managedFields", "value": entries},
			{"op": "replace", "path": "/metadata/resourceVersion", "value": obj.GetResourceVersion()},
		})
		if err != nil {
			return nil, "", fmt.Errorf("building the ownership patch: %w", err)
		}
		out, err := client.Patch(ctx, ref.Name, types.JSONPatchType, raw, metav1.PatchOptions{FieldManager: fieldManager})
		if err == nil {
			return out, "", nil
		}
		if !apierrors.IsConflict(err) || attempt >= ownershipRetries {
			return nil, "", failed(err)
		}
		fresh, getErr := client.Get(ctx, ref.Name, metav1.GetOptions{})
		if getErr != nil {
			return nil, "", failed(fmt.Errorf("%w; re-reading after that conflict: %w", err, getErr))
		}
		obj = fresh
	}
}

// MarkersHeldByUpdate reports whether fieldManager's Update entry on obj
// still owns any of the given marker keys, which is what a
// [Client.PatchMarkers] killed between its two requests leaves behind
// (GitHub issue #1764): the values are already right, so a caller that
// judges by them alone writes nothing, and the provider's next apply that
// changes a marker conflicts with that entry. A caller whose markers
// already read right re-sends [Client.PatchMarkers] when this is true; it
// is false for an object whose markers the Apply entry already holds, so
// a clean rerun still sends nothing. fieldManager defaults to
// [DefaultFieldManager] when empty. A managedFields entry that cannot be
// decoded answers true with the error, so the caller's write reports it.
func MarkersHeldByUpdate(obj *unstructured.Unstructured, fieldManager string, labels, annotations []string) (bool, error) {
	if obj == nil {
		return false, nil
	}
	if fieldManager == "" {
		fieldManager = DefaultFieldManager
	}
	_, held, err := transferMarkerOwnership(obj.GetManagedFields(), fieldManager, labels, annotations)
	if err != nil {
		return true, err
	}
	return held, nil
}

// ownershipRetries bounds how many times [Client.handMarkersToApply]
// re-reads and resends after a resourceVersion conflict.
const ownershipRetries = 5

// transferMarkerOwnership returns entries with every marker key (the given
// label and annotation keys) that manager's Update entries own moved into
// manager's Apply entry, created from the Update entry when there is none.
// Nothing else moves: a field an Update under the same name owned for any
// other reason stays an Update field, because an Apply entry's fields are
// the ones the provider's next apply removes when its manifest stops
// naming them, and the markers are the only fields the provider's manifest
// is known to name (the node stamp puts them there). The labels and
// annotations maps themselves move too when the Update entry owns them and
// owns no other key inside them, so that an emptied Update entry is
// dropped rather than left holding a bare map. changed is false when no
// Update entry owned a marker.
func transferMarkerOwnership(entries []metav1.ManagedFieldsEntry, manager string, labels, annotations []string) ([]metav1.ManagedFieldsEntry, bool, error) {
	markerSet := fieldpath.NewSet()
	for _, k := range labels {
		markerSet.Insert(fieldpath.MakePathOrDie("metadata", "labels", k))
	}
	for _, k := range annotations {
		markerSet.Insert(fieldpath.MakePathOrDie("metadata", "annotations", k))
	}
	parents := []fieldpath.Path{
		fieldpath.MakePathOrDie("metadata", "labels"),
		fieldpath.MakePathOrDie("metadata", "annotations"),
	}

	out := make([]metav1.ManagedFieldsEntry, 0, len(entries))
	moved := fieldpath.NewSet()
	var from *metav1.ManagedFieldsEntry
	for _, e := range entries {
		if e.Manager != manager || e.Operation != metav1.ManagedFieldsOperationUpdate || e.Subresource != "" || e.FieldsV1 == nil {
			out = append(out, e)
			continue
		}
		owned := &fieldpath.Set{}
		if err := owned.FromJSON(bytes.NewReader(e.FieldsV1.Raw)); err != nil {
			return nil, false, fmt.Errorf("decoding %s's %s entry: %w", e.Manager, e.Operation, err)
		}
		take := owned.Intersection(markerSet)
		if take.Empty() {
			out = append(out, e)
			continue
		}
		rest := owned.Difference(markerSet)
		for _, parent := range parents {
			if rest.Has(parent) && childrenOf(rest, parent).Empty() {
				take.Insert(parent)
				rest = rest.Difference(fieldpath.NewSet(parent))
			}
		}
		moved = moved.Union(take)
		if from == nil {
			from = e.DeepCopy()
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
	if from == nil {
		return entries, false, nil
	}

	for i := range out {
		e := &out[i]
		if e.Manager != manager || e.Operation != metav1.ManagedFieldsOperationApply || e.Subresource != "" {
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

	// No Apply entry under this name yet (the object was not created by
	// the provider's apply): the Update entry's markers become one.
	raw, err := moved.ToJSON()
	if err != nil {
		return nil, false, fmt.Errorf("encoding %s's Apply entry: %w", manager, err)
	}
	from.Operation = metav1.ManagedFieldsOperationApply
	from.FieldsV1 = &metav1.FieldsV1{Raw: raw}
	return append(out, *from), true, nil
}

// childrenOf is the part of s strictly under path p.
func childrenOf(s *fieldpath.Set, p fieldpath.Path) *fieldpath.Set {
	for _, pe := range p {
		s = s.WithPrefix(pe)
	}
	return s
}

// LabelReleaser is the cluster half of a marker release (GitHub issue
// #1656): read the object as it is, and delete marker keys from it. It is
// what internal/live/untag needs to release a manifest-shape orphan under
// undeclared_tagged = "untag". [Client] implements it against a real API
// server; a test stands in for one.
type LabelReleaser interface {
	// ReadObject is [LabelPatcher.ReadObject].
	ReadObject(ctx context.Context, ref ObjectRef) (obj *unstructured.Unstructured, found bool, err error)

	// DeleteMarkers removes every key in labels from metadata.labels and
	// every key in annotations from metadata.annotations on the object at
	// ref, through ONE merge patch under fieldManager, and returns the
	// object the server produced. pin, when set, makes the patch apply only
	// to the object version it names ([ObjectPin]). dryRun, rejected and
	// err mean what they mean on [LabelPatcher.PatchMarkers].
	DeleteMarkers(ctx context.Context, ref ObjectRef, pin ObjectPin, labels, annotations []string, fieldManager string, dryRun bool) (obj *unstructured.Unstructured, rejected string, err error)
}

// ObjectPin names the exact object a write was decided against: its
// metadata.resourceVersion and metadata.uid. A merge patch carrying them
// is refused by the API server with a conflict when the stored object's
// differ - it was written since (resourceVersion) or deleted and recreated
// under the same name (uid) - so a write checked against what an object
// carried cannot land on what it carries now. GitHub issue #1743: an untag
// release checked the estate label, and live-mv could move the object to
// another estate between that read and the patch. The zero value pins
// nothing.
type ObjectPin struct {
	ResourceVersion string
	UID             string
}

// PinOf is the [ObjectPin] for obj as read.
func PinOf(obj *unstructured.Unstructured) ObjectPin {
	if obj == nil {
		return ObjectPin{}
	}
	return ObjectPin{ResourceVersion: obj.GetResourceVersion(), UID: string(obj.GetUID())}
}

var _ LabelReleaser = (*Client)(nil)

// DeleteMarkers implements [LabelReleaser]. The body is a JSON merge patch
// naming each key inside metadata.labels and metadata.annotations with a
// null value, which RFC 7386 defines as "remove this key", and nothing
// else - the request cannot carry a change to any other field, for the
// same reason [Client.PatchMarkers]'s cannot. A key the object does not
// carry is a no-op on the server, not an error. A set pin adds
// metadata.resourceVersion and metadata.uid, which change nothing on a
// stored object that already carries them and refuse the patch on one
// that does not ([ObjectPin]).
func (c *Client) DeleteMarkers(ctx context.Context, ref ObjectRef, pin ObjectPin, labels, annotations []string, fieldManager string, dryRun bool) (*unstructured.Unstructured, string, error) {
	if ref.APIVersion == "" || ref.Kind == "" || ref.Name == "" {
		return nil, "", fmt.Errorf("an object needs an apiVersion, a kind and a name to be patched")
	}
	if len(labels) == 0 && len(annotations) == 0 {
		return nil, "", fmt.Errorf("a marker release needs a label or an annotation to delete")
	}
	meta := map[string]any{}
	for field, keys := range map[string][]string{"labels": labels, "annotations": annotations} {
		if len(keys) == 0 {
			continue
		}
		m := make(map[string]any, len(keys))
		for _, k := range keys {
			if k == "" {
				return nil, "", fmt.Errorf("a marker release cannot name an empty key")
			}
			m[k] = nil
		}
		meta[field] = m
	}
	if pin.ResourceVersion != "" {
		meta["resourceVersion"] = pin.ResourceVersion
	}
	if pin.UID != "" {
		meta["uid"] = pin.UID
	}
	return c.mergePatch(ctx, ref, map[string]any{"metadata": meta}, fieldManager, dryRun)
}

// mergePatch sends body as a JSON merge patch to the object at ref: the
// one request both writes in this file make.
func (c *Client) mergePatch(ctx context.Context, ref ObjectRef, body map[string]any, fieldManager string, dryRun bool) (*unstructured.Unstructured, string, error) {
	if fieldManager == "" {
		fieldManager = DefaultFieldManager
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, "", fmt.Errorf("building the marker patch: %w", err)
	}
	client, err := c.resourceClient(ref.APIVersion, ref.Kind, ref.Namespace)
	if err != nil {
		return nil, "", err
	}
	opts := metav1.PatchOptions{FieldManager: fieldManager}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	obj, err := client.Patch(ctx, ref.Name, types.MergePatchType, raw, opts)
	if err != nil {
		if rejected, msg := serverVerdict(err); rejected {
			return nil, msg, nil
		}
		return nil, "", fmt.Errorf("patching %s %s: %w", ref.Kind, NaturalKey(ref.Namespace, ref.Name), err)
	}
	return obj, "", nil
}
