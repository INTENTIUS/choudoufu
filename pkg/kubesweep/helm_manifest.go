// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

// What a Helm release holds (GitHub issue #1738 item 4, ruled 2026-09-30:
// option A with D's labelling).
//
// Helm's release annotation says which release installed an object, and
// nothing more: an object a chart dropped under helm.sh/resource-policy:
// keep keeps it, and so does a copy of a Helm object's YAML applied by
// hand. So an annotated object is held only when the release's own
// manifest lists it. The manifest is read off the release record the
// storage driver keeps (decoded here with apimachinery and the standard
// library; no Helm SDK import), split into documents, and matched on kind,
// namespace and name:
//
//   - group and version are not compared, because a chart rendered at
//     extensions/v1beta1 is served at networking.k8s.io/v1. A cross-group
//     kind collision can only widen the hold.
//   - a document with no metadata.namespace is the release namespace's,
//     which is where Helm installs it. A cluster-scoped object is matched
//     on kind and name alone.
//   - the latest revision's manifest is unioned with the last deployed
//     one's, since a failed or pending upgrade's manifest may not be
//     what is live.
//   - a record that cannot be fetched or decoded, or a manifest that does
//     not parse, holds every object annotated with the release. Never the
//     other way.
//
// An annotated object the manifest does not list is not proposed for
// destroy either: [Client.List] reports it in [Skipped.Unlisted], so a
// matching bug can never become a destroy proposal, only a finding.
//
// Reads: the release's history is listed metadata-only (labels decide
// liveness and which revisions matter), and only the one or two records
// the decision needs are fetched in full. The answer is kept for the
// Client's life, so the sweep's kinds and the post-apply look share it.

// helmContents is what a live release's records say it lists.
type helmContents struct {
	// unknown is set when a needed record could not be read or decoded:
	// every object annotated with the release is held.
	unknown bool
	// listed are the manifest's objects by kind, namespace (the release's
	// when the document gave none) and name; clusterListed the same by
	// kind and name, for a cluster-scoped object.
	listed        map[[3]string]bool
	clusterListed map[[2]string]bool
}

// lists reports whether the release holds the object: it is listed, or
// the release's contents are unknown.
func (h *helmContents) lists(kind, namespace, name string) bool {
	if h.unknown {
		return true
	}
	if namespace == "" {
		return h.clusterListed[[2]string{kind, name}]
	}
	return h.listed[[3]string{kind, namespace, name}]
}

// helmRecordRef is one history record as the metadata listing shows it,
// and the full object when the listing already carried it.
type helmRecordRef struct {
	name   string
	labels map[string]string
	full   *unstructured.Unstructured
}

// helmReleaseContents answers for rel: nil for a release that does not
// exist (no record in either store, or the latest reads uninstalled;
// GitHub issues #1625, #1738), otherwise what it lists. A release with no
// namespace annotation - Helm always writes one since 3.2, so this is a
// pre-3.2 object or a hand-crafted annotation - is looked up in the
// object's own namespace, and the answer is remembered under the
// namespace actually asked.
//
// A live record in either store is enough on its own: a store the cluster
// would not list could only have added a record. Short of that, a store
// that could not be listed is reported as an error - a Forbidden one as
// the cluster's own denial, which the sweep names as a missing grant
// (#1582): never as "gone", which would turn a coverage gap into a
// proposal to destroy a release's own object.
func (c *Client) helmReleaseContents(ctx context.Context, rel Release, objNamespace string) (*helmContents, error) {
	ns := rel.Namespace
	if ns == "" {
		ns = objNamespace
	}
	key := Release{Namespace: ns, Name: rel.Name}
	c.helmMu.Lock()
	if v, ok := c.helmCache[key]; ok {
		c.helmMu.Unlock()
		return v, nil
	}
	c.helmMu.Unlock()

	var unread error
	var found *helmContents
	for _, store := range helmReleaseStores {
		refs, err := c.helmRecords(ctx, store, ns, rel.Name)
		if err != nil {
			if unread == nil {
				unread = fmt.Errorf("checking whether %s still exists: %w", key.String(), err)
			}
			continue
		}
		if helmReleaseLive(refs) {
			found = c.helmManifests(ctx, store, ns, refs)
			break
		}
	}
	if found == nil && unread != nil {
		return nil, unread
	}
	c.helmMu.Lock()
	if c.helmCache == nil {
		c.helmCache = map[Release]*helmContents{}
	}
	// nil is remembered too: the release is gone.
	c.helmCache[key] = found
	c.helmMu.Unlock()
	return found, nil
}

// helmRecords lists one store's records for a release: metadata only
// through the metadata client [New] builds, or in full through the
// dynamic client for a [NewWith] client that has none.
func (c *Client) helmRecords(ctx context.Context, store schema.GroupVersionResource, ns, name string) ([]helmRecordRef, error) {
	opts := metav1.ListOptions{LabelSelector: "owner=helm,name=" + name}
	var refs []helmRecordRef
	if c.meta != nil {
		list, err := c.meta.Resource(store).Namespace(ns).List(ctx, opts)
		if err != nil {
			return nil, err
		}
		for _, m := range list.Items {
			refs = append(refs, helmRecordRef{name: m.Name, labels: m.Labels})
		}
		return refs, nil
	}
	list, err := c.dyn.Resource(store).Namespace(ns).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for i := range list.Items {
		refs = append(refs, helmRecordRef{name: list.Items[i].GetName(), labels: list.Items[i].GetLabels(), full: &list.Items[i]})
	}
	return refs, nil
}

// helmManifests reads the manifests of a live release's latest revision
// and its last deployed one, which are the same record when the latest is
// deployed. Anything short of a decoded manifest for each - a revision
// order the labels do not give, a GET that fails, a payload that does not
// decode - is unknown contents, which holds.
func (c *Client) helmManifests(ctx context.Context, store schema.GroupVersionResource, ns string, refs []helmRecordRef) *helmContents {
	latest, deployed := -1, -1
	latestV, deployedV := -1, -1
	for i, r := range refs {
		v, err := strconv.Atoi(r.labels["version"])
		if err != nil {
			return &helmContents{unknown: true}
		}
		if v > latestV {
			latest, latestV = i, v
		}
		if r.labels["status"] == "deployed" && v > deployedV {
			deployed, deployedV = i, v
		}
	}
	need := []int{latest}
	if deployed >= 0 && deployed != latest {
		need = append(need, deployed)
	}
	h := &helmContents{listed: map[[3]string]bool{}, clusterListed: map[[2]string]bool{}}
	for _, i := range need {
		obj := refs[i].full
		if obj == nil {
			got, err := c.dyn.Resource(store).Namespace(ns).Get(ctx, refs[i].name, metav1.GetOptions{})
			if err != nil {
				return &helmContents{unknown: true}
			}
			obj = got
		}
		m, err := helmRecordManifest(obj, store.Resource == "secrets")
		if err != nil {
			return &helmContents{unknown: true}
		}
		if err := h.add(m, ns); err != nil {
			return &helmContents{unknown: true}
		}
	}
	return h
}

// helmPayloadLimit bounds a release's decompressed JSON. The largest
// measured on #1738 was 5 MB; anything past this is not read, and holds.
const helmPayloadLimit = 256 << 20

// helmRecordManifest decodes one record's data.release the way Helm's
// storage driver encoded it (helm.sh/helm/v3 pkg/storage/driver/util.go):
// base64, gzip when the gzip magic leads, then the release's JSON. A
// Secret's data is base64 once more on the wire.
func helmRecordManifest(obj *unstructured.Unstructured, secret bool) (string, error) {
	payload, ok, err := unstructured.NestedString(obj.Object, "data", "release")
	if err != nil || !ok {
		return "", fmt.Errorf("record %s has no data.release", obj.GetName())
	}
	if secret {
		b, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", err
		}
		payload = string(b)
	}
	return helmDecodeManifest(payload)
}

var gzipMagic = []byte{0x1f, 0x8b, 0x08}

func helmDecodeManifest(payload string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", err
	}
	if len(b) > 3 && bytes.Equal(b[:3], gzipMagic) {
		r, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return "", err
		}
		b, err = io.ReadAll(io.LimitReader(r, helmPayloadLimit+1))
		if err != nil {
			return "", err
		}
		if len(b) > helmPayloadLimit {
			return "", errors.New("release payload over the read limit")
		}
	}
	var rls struct {
		Name     string `json:"name"`
		Manifest string `json:"manifest"`
	}
	if err := json.Unmarshal(b, &rls); err != nil {
		return "", err
	}
	// Helm's Release marshals its manifest with omitempty, so a release
	// with no manifest is a real release; one with no name is not one.
	if rls.Name == "" {
		return "", errors.New("release payload names no release")
	}
	return rls.Manifest, nil
}

// add splits a manifest into its documents and records each object, the
// release namespace standing in for an omitted one.
func (h *helmContents) add(manifest, releaseNS string) error {
	d := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	for {
		var doc map[string]any
		err := d.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if doc == nil {
			// An empty document: a template that rendered nothing.
			continue
		}
		if err := h.addObject(doc, releaseNS); err != nil {
			return err
		}
	}
}

func (h *helmContents) addObject(doc map[string]any, releaseNS string) error {
	u := unstructured.Unstructured{Object: doc}
	kind := u.GetKind()
	if items, ok := doc["items"].([]any); ok && strings.HasSuffix(kind, "List") {
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				return fmt.Errorf("a %s item is not an object", kind)
			}
			if err := h.addObject(m, releaseNS); err != nil {
				return err
			}
		}
		return nil
	}
	name := u.GetName()
	if kind == "" || name == "" {
		return errors.New("a manifest document names no kind or no name")
	}
	ns := u.GetNamespace()
	if ns == "" {
		ns = releaseNS
	}
	h.listed[[3]string{kind, ns, name}] = true
	h.clusterListed[[2]string{kind, name}] = true
	return nil
}

// helmReleaseLive reports whether one store's records for a release say
// it is live: the latest revision's status is anything but uninstalled.
// Latest is the highest version label, compared as a number (v10 follows
// v9). A record whose version does not read as a number leaves the order
// unknown, and then the release is live unless every record reads
// uninstalled or superseded - the answer that holds when unsure.
func helmReleaseLive(records []helmRecordRef) bool {
	if len(records) == 0 {
		return false
	}
	latest, latestStatus, ordered := -1, "", true
	for _, r := range records {
		v, err := strconv.Atoi(r.labels["version"])
		if err != nil {
			ordered = false
			break
		}
		if v > latest {
			latest, latestStatus = v, r.labels["status"]
		}
	}
	if ordered {
		return latestStatus != helmStatusUninstalled
	}
	for _, r := range records {
		switch r.labels["status"] {
		case helmStatusUninstalled, "superseded":
		default:
			return true
		}
	}
	return false
}
