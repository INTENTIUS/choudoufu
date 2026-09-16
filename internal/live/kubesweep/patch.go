// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// This file is the one WRITE this package makes, and it is one label
// (GitHub issues #1104 and #1109, ruled 2026-09-13 by the maintainer on
// both): a merge patch that sets metadata.labels[tofu-estate] on a live
// object, under the caller's own credential, sent first with dryRun=All
// so the server's verdict - its validation, its admission policies, its
// RBAC - is read before anything is persisted.
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
// (internal/live/liveimport/manifest.go) so that a mutating admission
// webhook rewriting the spec on the way past is caught rather than
// assumed away.
//
// # The field manager
//
// The patch names the manager the provider itself writes under, so that
// the provider's next server-side apply of the same label does not meet a
// competing owner. [DefaultFieldManager] is the provider's own default,
// measured rather than read from a document: after `terraform apply` of a
// kubernetes_manifest block on kind 1.36, the object's
// metadata.managedFields holds one entry, `manager: Terraform,
// operation: Apply`. A block that sets `field_manager { name = ... }`
// overrides it, and the caller passes that name instead.
//
// If #1106 section 3 rules that the estate is the field manager, this
// same write becomes a server-side apply under choudoufu:<estate> and
// the conflict report comes with it; nothing here is shaped to make that
// harder - the manager is a parameter, and the patch body is already the
// apply body an SSA would send.

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

	// PatchLabel sets metadata.labels[key] = value on the object at ref
	// through a merge patch under fieldManager, and returns the object
	// the server produced.
	//
	// With dryRun the server validates, defaults, runs admission and
	// persists nothing, so the returned object is what the real write
	// would have stored. rejected carries the server's own words for a
	// refusal it answered with (a validation failure, an admission
	// policy's denial, a 403 from RBAC) and is empty when the server
	// accepted; err is a cluster that could not answer at all.
	PatchLabel(ctx context.Context, ref ObjectRef, key, value, fieldManager string, dryRun bool) (obj *unstructured.Unstructured, rejected string, err error)
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

// PatchLabel implements [LabelPatcher]. The body is a JSON merge patch
// naming one key inside metadata.labels and nothing else, so the request
// itself cannot carry a change to any other field; what the SERVER then
// does with it is the caller's to check, which is what the dry run is
// for.
func (c *Client) PatchLabel(ctx context.Context, ref ObjectRef, key, value, fieldManager string, dryRun bool) (*unstructured.Unstructured, string, error) {
	if ref.APIVersion == "" || ref.Kind == "" || ref.Name == "" {
		return nil, "", fmt.Errorf("an object needs an apiVersion, a kind and a name to be patched")
	}
	if key == "" {
		return nil, "", fmt.Errorf("a label patch needs a label key")
	}
	if fieldManager == "" {
		fieldManager = DefaultFieldManager
	}
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"labels": map[string]any{key: value},
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("building the label patch: %w", err)
	}
	client, err := c.resourceClient(ref.APIVersion, ref.Kind, ref.Namespace)
	if err != nil {
		return nil, "", err
	}
	opts := metav1.PatchOptions{FieldManager: fieldManager}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	obj, err := client.Patch(ctx, ref.Name, types.MergePatchType, body, opts)
	if err != nil {
		if rejected, msg := serverVerdict(err); rejected {
			return nil, msg, nil
		}
		return nil, "", fmt.Errorf("patching %s %s: %w", ref.Kind, NaturalKey(ref.Namespace, ref.Name), err)
	}
	return obj, "", nil
}
