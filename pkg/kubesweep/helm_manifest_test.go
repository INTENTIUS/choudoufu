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
	"fmt"
	"sort"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	fakemetadata "k8s.io/client-go/metadata/fake"
	clienttesting "k8s.io/client-go/testing"
)

// GitHub issue #1738 item 4, ruled 2026-09-30 (option A with D's
// labelling): an object is held by a Helm release only if the release's
// manifest lists it. The release record is decoded the way Helm's
// storage driver wrote it, the manifest split into documents, and the
// object matched on kind, namespace and name. An object annotated with a
// live release that its manifest does not list is reported apart
// (Skipped.Unlisted), neither held nor returned for the destroy proposal.

var ingressGVR = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}

// helmEncode is helm.sh/helm/v3 pkg/storage/driver/util.go encodeRelease:
// compact JSON, gzip at best compression, standard base64. The result is
// what the ConfigMap driver stores in data.release as is, and what the
// Secrets driver stores as the bytes of data.release (so the API, which
// base64-encodes Secret data, serves it encoded twice).
func helmEncode(t *testing.T, rls map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(rls)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// helmRelease is the release JSON Helm stores (pkg/release/release.go):
// the fields a reader of the record needs and a chart/config the size of
// a real one's shape, so a decoder that assumed only a manifest would
// trip.
func helmReleaseJSON(ns, name string, revision int, status, manifest string) map[string]any {
	return map[string]any{
		"name":      name,
		"namespace": ns,
		"version":   revision,
		"info": map[string]any{
			"first_deployed": "2026-09-30T10:00:00Z",
			"last_deployed":  "2026-09-30T10:00:00Z",
			"deleted":        "",
			"description":    "Install complete",
			"status":         status,
		},
		"chart": map[string]any{
			"metadata":  map[string]any{"name": name, "version": "0.1.0", "apiVersion": "v2"},
			"templates": []any{map[string]any{"name": "templates/svc.yaml", "data": "YXBpVmVyc2lvbjogdjE="}},
			"values":    map[string]any{"estate": ""},
		},
		"config":   map[string]any{"estate": "smoke-k8s"},
		"manifest": manifest,
		"hooks":    []any{},
	}
}

// helmStored is one revision record as a storage driver writes it, with
// its payload: a Secret's data.release is the encoded release's bytes,
// which the API serves base64-encoded again; a ConfigMap's is the encoded
// release as a string.
func helmStored(t *testing.T, driver, ns, name string, revision int, status, manifest string) *unstructured.Unstructured {
	t.Helper()
	return helmStoredPayload(driver, ns, name, revision, status, helmEncode(t, helmReleaseJSON(ns, name, revision, status, manifest)))
}

func helmStoredPayload(driver, ns, name string, revision int, status, payload string) *unstructured.Unstructured {
	u := helmRecord(driver, ns, name, revision, status)
	if driver == "Secret" {
		u.Object["type"] = "helm.sh/release.v1"
		payload = base64.StdEncoding.EncodeToString([]byte(payload))
	}
	u.Object["data"] = map[string]any{"release": payload}
	return u
}

// manifest renders documents the way Helm joins a release's manifest:
// each one after "---" and a "# Source:" comment.
func manifest(docs ...string) string {
	var b strings.Builder
	for i, d := range docs {
		fmt.Fprintf(&b, "---\n# Source: web/templates/doc%d.yaml\n%s\n", i, strings.TrimSpace(d))
	}
	return b.String()
}

func svcDoc(name, ns string) string {
	d := "apiVersion: v1\nkind: Service\nmetadata:\n  name: " + name + "\n"
	if ns != "" {
		d += "  namespace: " + ns + "\n"
	}
	return d + "spec:\n  ports:\n  - port: 80\n"
}

// helmObject is an estate-labelled object Helm installed: its release
// annotations and helm's own managedFields entry.
func helmObject(apiVersion, kind, ns, name, release, releaseNS string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetLabels(map[string]string{"tofu-estate": "smoke-k8s"})
	ann := map[string]string{HelmReleaseNameAnnotation: release}
	if releaseNS != "" {
		ann[HelmReleaseNamespaceAnnotation] = releaseNS
	}
	u.SetAnnotations(ann)
	u.SetManagedFields([]metav1.ManagedFieldsEntry{helmEntry})
	return u
}

func manifestSweep(objs ...runtime.Object) *fakedynamic.FakeDynamicClient {
	return fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{servicesGVR: "ServiceList", ingressGVR: "IngressList", helmSecretGVR: "SecretList", helmCMGVR: "ConfigMapList"},
		objs...)
}

// metadataOf is the metadata-only view of objs that a metadata client
// serves: the records' labels and names, none of their data.
func metadataOf(t *testing.T, objs ...runtime.Object) *fakemetadata.FakeMetadataClient {
	t.Helper()
	scheme := fakemetadata.NewTestScheme()
	if err := metav1.AddMetaToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	var metas []runtime.Object
	for _, o := range objs {
		u, ok := o.(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("metadataOf: %T", o)
		}
		m := &metav1.PartialObjectMetadata{}
		m.APIVersion, m.Kind = u.GetAPIVersion(), u.GetKind()
		m.Namespace, m.Name = u.GetNamespace(), u.GetName()
		m.Labels, m.Annotations = u.GetLabels(), u.GetAnnotations()
		metas = append(metas, m)
	}
	return fakemetadata.NewSimpleMetadataClient(scheme, metas...)
}

// manifestClient is a [Client] as [New] builds one: a dynamic client and
// a metadata client over the same cluster.
func manifestClient(t *testing.T, objs ...runtime.Object) (*Client, *fakedynamic.FakeDynamicClient, *fakemetadata.FakeMetadataClient) {
	t.Helper()
	dyn := manifestSweep(objs...)
	meta := metadataOf(t, objs...)
	return NewWithMetadata(&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}, dyn, meta), dyn, meta
}

var drivers = []string{"Secret", "ConfigMap"}

func sweepServices(t *testing.T, c *Client) ([]Object, Skipped) {
	t.Helper()
	got, skipped, err := c.List(context.Background(), Kind{GVR: servicesGVR, Kind: "Service", Namespaced: true}, "tofu-estate", "smoke-k8s")
	if err != nil {
		t.Fatal(err)
	}
	return got, skipped
}

func heldNames(hs []HeldObject) []string {
	var out []string
	for _, h := range hs {
		out = append(out, NaturalKey(h.Namespace, h.Name))
	}
	sort.Strings(out)
	return out
}

func eq(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestHelmKeepPolicyDropIsReportedNotHeld: a chart dropped web-old in
// revision 2 under helm.sh/resource-policy: keep, so Helm left it in the
// cluster with its release annotations, and the release's manifest no
// longer lists it. A copy of a Helm object's YAML re-applied by hand
// (web-copy) carries the same annotations and was never in any manifest.
// Neither is the release's, so neither is held; neither is proposed for
// destroy either: both are reported, naming the release.
func TestHelmKeepPolicyDropIsReportedNotHeld(t *testing.T) {
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			c, _, _ := manifestClient(t,
				helmObject("v1", "Service", "web", "web-svc", "web", "web"),
				helmObject("v1", "Service", "web", "web-old", "web", "web"),
				helmObject("v1", "Service", "web", "web-copy", "web", "web"),
				helmStored(t, driver, "web", "web", 1, "superseded", manifest(svcDoc("web-svc", ""), svcDoc("web-old", ""))),
				helmStored(t, driver, "web", "web", 2, "deployed", manifest(svcDoc("web-svc", ""))),
			)
			got, skipped := sweepServices(t, c)
			if len(got) != 0 {
				t.Errorf("orphans = %v, want none: an object annotated with a live release is never proposed for destroy", importIDs(got))
			}
			if h := heldNames(skipped.Held); !eq(h, "web/web-svc") {
				t.Errorf("held = %v, want [web/web-svc]: only what the release's manifest lists is the release's", h)
			}
			if u := heldNames(skipped.Unlisted); !eq(u, "web/web-copy", "web/web-old") {
				t.Errorf("unlisted = %v, want [web/web-copy web/web-old]: annotated with live Helm release web/web, not in its manifest", u)
			}
			for _, u := range skipped.Unlisted {
				if u.HeldBy != "Helm release web/web" || u.Kind != "Service" {
					t.Errorf("unlisted %+v, want it to name Helm release web/web", u)
				}
			}
		})
	}
}

// TestHelmManifestOmittedNamespaceMatches: most charts write no
// metadata.namespace, and Helm installs those documents into the
// release's namespace. Such a document matches the object in the release
// namespace; the same kind and name in another namespace, annotated with
// the release, is not the release's.
func TestHelmManifestOmittedNamespaceMatches(t *testing.T) {
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			c, _, _ := manifestClient(t,
				helmObject("v1", "Service", "web", "web-svc", "web", "web"),
				helmObject("v1", "Service", "other", "web-svc", "web", "web"),
				helmObject("v1", "Service", "shared", "web-x", "web", "web"),
				helmStored(t, driver, "web", "web", 1, "deployed", manifest(svcDoc("web-svc", ""), svcDoc("web-x", "shared"))),
			)
			_, skipped := sweepServices(t, c)
			if h := heldNames(skipped.Held); !eq(h, "shared/web-x", "web/web-svc") {
				t.Errorf("held = %v, want [shared/web-x web/web-svc]: an omitted namespace is the release's, an explicit one is its own", h)
			}
			if u := heldNames(skipped.Unlisted); !eq(u, "other/web-svc") {
				t.Errorf("unlisted = %v, want [other/web-svc]", u)
			}
		})
	}
}

// TestHelmManifestAPIVersionDriftMatches: a chart that renders an
// extensions/v1beta1 Ingress is served back as networking.k8s.io/v1, the
// version the sweep lists. Group and version are not compared; kind is,
// so a Service of the same name is not matched by the Ingress document.
func TestHelmManifestAPIVersionDriftMatches(t *testing.T) {
	ingressDoc := "apiVersion: extensions/v1beta1\nkind: Ingress\nmetadata:\n  name: web\nspec:\n  backend:\n    serviceName: web-svc\n    servicePort: 80\n"
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			c, _, _ := manifestClient(t,
				helmObject("networking.k8s.io/v1", "Ingress", "web", "web", "web", "web"),
				helmObject("v1", "Service", "web", "web", "web", "web"),
				helmStored(t, driver, "web", "web", 1, "deployed", manifest(ingressDoc)),
			)
			got, skipped, err := c.List(context.Background(), Kind{GVR: ingressGVR, Kind: "Ingress", Namespaced: true, APIVersion: "networking.k8s.io/v1"}, "tofu-estate", "smoke-k8s")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 || !eq(heldNames(skipped.Held), "web/web") || len(skipped.Unlisted) != 0 {
				t.Errorf("Ingress: orphans %v held %v unlisted %v, want web/web held: apiVersion drift is not a different object", importIDs(got), heldNames(skipped.Held), heldNames(skipped.Unlisted))
			}
			_, skipped = sweepServices(t, c)
			if len(skipped.Held) != 0 || !eq(heldNames(skipped.Unlisted), "web/web") {
				t.Errorf("Service: held %v unlisted %v, want web/web unlisted: the manifest lists an Ingress of that name, not a Service", heldNames(skipped.Held), heldNames(skipped.Unlisted))
			}
		})
	}
}

// TestHelmFailedLatestUnionsLastDeployed: a failed or pending upgrade's
// manifest may not be what is live, so the latest revision's manifest is
// unioned with the last deployed one's. Revisions before that are not
// read: what only revision 1 listed is not the release's any more.
func TestHelmFailedLatestUnionsLastDeployed(t *testing.T) {
	for _, latest := range []string{"failed", "pending-upgrade"} {
		for _, driver := range drivers {
			t.Run(latest+"/"+driver, func(t *testing.T) {
				c, _, _ := manifestClient(t,
					helmObject("v1", "Service", "web", "a", "web", "web"),
					helmObject("v1", "Service", "web", "b", "web", "web"),
					helmObject("v1", "Service", "web", "c", "web", "web"),
					helmObject("v1", "Service", "web", "new", "web", "web"),
					helmStored(t, driver, "web", "web", 1, "superseded", manifest(svcDoc("a", ""), svcDoc("c", ""))),
					helmStored(t, driver, "web", "web", 2, "deployed", manifest(svcDoc("a", ""), svcDoc("b", ""))),
					helmStored(t, driver, "web", "web", 3, latest, manifest(svcDoc("a", ""), svcDoc("new", ""))),
				)
				got, skipped := sweepServices(t, c)
				if len(got) != 0 {
					t.Errorf("orphans = %v, want none", importIDs(got))
				}
				if h := heldNames(skipped.Held); !eq(h, "web/a", "web/b", "web/new") {
					t.Errorf("held = %v, want [web/a web/b web/new]: the %s revision 3 unioned with deployed revision 2", h, latest)
				}
				if u := heldNames(skipped.Unlisted); !eq(u, "web/c") {
					t.Errorf("unlisted = %v, want [web/c]: only superseded revision 1 listed it", u)
				}
			})
		}
	}
}

// TestHelmCorruptPayloadHolds: a record whose payload does not decode -
// not base64, not gzip, not JSON, a manifest document that is not YAML,
// or no payload at all - says nothing about what the release lists, and
// holds every object annotated with it. Never an orphan, never unlisted.
func TestHelmCorruptPayloadHolds(t *testing.T) {
	notGzip := base64.StdEncoding.EncodeToString([]byte("\x1f\x8b\x08garbage"))
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write([]byte(`{"name":"web","manifest":`))
	_ = w.Close()
	notJSON := base64.StdEncoding.EncodeToString(gz.Bytes())
	badYAML := helmEncode(t, helmReleaseJSON("web", "web", 1, "deployed", "---\napiVersion: v1\nkind: Service\nmetadata: [unclosed\n"))
	for name, payload := range map[string]string{
		"not base64": "%%% not base64 %%%",
		"not gzip":   notGzip,
		"not JSON":   notJSON,
		"bad YAML":   badYAML,
	} {
		for _, driver := range drivers {
			t.Run(name+"/"+driver, func(t *testing.T) {
				c, _, _ := manifestClient(t,
					helmObject("v1", "Service", "web", "web-svc", "web", "web"),
					helmStoredPayload(driver, "web", "web", 1, "deployed", payload),
				)
				got, skipped := sweepServices(t, c)
				if len(got) != 0 || len(skipped.Unlisted) != 0 || !eq(heldNames(skipped.Held), "web/web-svc") {
					t.Errorf("orphans %v held %v unlisted %v, want web/web-svc held: an undecodable record holds", importIDs(got), heldNames(skipped.Held), heldNames(skipped.Unlisted))
				}
			})
		}
	}
	for _, driver := range drivers {
		t.Run("no payload/"+driver, func(t *testing.T) {
			c, _, _ := manifestClient(t,
				helmObject("v1", "Service", "web", "web-svc", "web", "web"),
				helmRecord(driver, "web", "web", 1, "deployed"),
			)
			_, skipped := sweepServices(t, c)
			if len(skipped.Unlisted) != 0 || !eq(heldNames(skipped.Held), "web/web-svc") {
				t.Errorf("held %v unlisted %v, want web/web-svc held", heldNames(skipped.Held), heldNames(skipped.Unlisted))
			}
		})
	}
}

// TestHelmReleaseReadsAreMetadataThenOneGet pins the read cost: the
// release's history is listed metadata-only, once per release for the
// whole Client, however many kinds and objects name it; and only the
// records the decision needs are fetched in full - one when the latest
// revision is the deployed one, two when it is not. Ten revisions of
// payload are never downloaded to read one.
func TestHelmReleaseReadsAreMetadataThenOneGet(t *testing.T) {
	for _, tc := range []struct {
		latest   string
		wantGets int
	}{{"deployed", 1}, {"failed", 2}} {
		for _, driver := range drivers {
			t.Run(tc.latest+"/"+driver, func(t *testing.T) {
				store := "secrets"
				if driver == "ConfigMap" {
					store = "configmaps"
				}
				objs := []runtime.Object{
					helmObject("v1", "Service", "web", "web-svc", "web", "web"),
					helmObject("v1", "Service", "web", "web-svc2", "web", "web"),
					helmObject("networking.k8s.io/v1", "Ingress", "web", "web", "web", "web"),
				}
				docs := manifest(svcDoc("web-svc", ""), svcDoc("web-svc2", ""), "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: web\n")
				for rev := 1; rev <= 9; rev++ {
					objs = append(objs, helmStored(t, driver, "web", "web", rev, "superseded", docs))
				}
				objs = append(objs, helmStored(t, driver, "web", "web", 10, "deployed", docs))
				if tc.latest != "deployed" {
					objs = append(objs, helmStored(t, driver, "web", "web", 11, tc.latest, docs))
				}
				c, dyn, meta := manifestClient(t, objs...)
				dyn.ClearActions()
				meta.ClearActions()

				_, skipped := sweepServices(t, c)
				if len(skipped.Held) != 2 {
					t.Fatalf("held = %v, want both Services", heldNames(skipped.Held))
				}
				if _, skipped, err := c.List(context.Background(), Kind{GVR: ingressGVR, Kind: "Ingress", Namespaced: true}, "tofu-estate", "smoke-k8s"); err != nil || len(skipped.Held) != 1 {
					t.Fatalf("Ingress: held = %v, err %v", heldNames(skipped.Held), err)
				}
				// The post-apply look lists the same kind again on the same Client.
				sweepServices(t, c)

				count := func(actions []clienttesting.Action, verb, resource string) int {
					n := 0
					for _, a := range actions {
						if a.GetVerb() == verb && a.GetResource().Resource == resource {
							n++
						}
					}
					return n
				}
				if n := count(dyn.Actions(), "list", store); n != 0 {
					t.Errorf("full lists of %s = %d, want 0: release history is listed metadata-only", store, n)
				}
				if n := count(meta.Actions(), "list", store); n != 1 {
					t.Errorf("metadata lists of %s = %d, want 1 for the Client, across three List calls and two kinds", store, n)
				}
				if n := count(dyn.Actions(), "get", store); n != tc.wantGets {
					t.Errorf("GETs of %s = %d, want %d: the latest revision and the last deployed one, once each", store, n, tc.wantGets)
				}
			})
		}
	}
}

// TestHelmRecordGetRefusedHolds: a role that may list the release's
// records but not get one (the grant before #1738 item 4 asked for list
// alone) cannot read the manifest, and holds, as the annotation alone did
// before: a missing grant never turns a release's object into an orphan
// or a finding.
func TestHelmRecordGetRefusedHolds(t *testing.T) {
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			c, dyn, _ := manifestClient(t,
				helmObject("v1", "Service", "web", "web-svc", "web", "web"),
				helmObject("v1", "Service", "web", "web-old", "web", "web"),
				helmStored(t, driver, "web", "web", 1, "deployed", manifest(svcDoc("web-svc", ""))),
			)
			dyn.PrependReactor("get", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: action.GetResource().Resource}, "sh.helm.release.v1.web.v1", fmt.Errorf("RBAC: get not granted"))
			})
			got, skipped := sweepServices(t, c)
			if len(got) != 0 || len(skipped.Unlisted) != 0 || !eq(heldNames(skipped.Held), "web/web-old", "web/web-svc") {
				t.Errorf("orphans %v held %v unlisted %v, want both held: an unread manifest holds", importIDs(got), heldNames(skipped.Held), heldNames(skipped.Unlisted))
			}
		})
	}
}
