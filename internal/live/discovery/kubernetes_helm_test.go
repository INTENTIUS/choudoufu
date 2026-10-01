// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	clienttesting "k8s.io/client-go/testing"

	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// fixedKindsClient is the real [kubesweep.Client] over a fake dynamic
// client, with Kinds answered from a fixed list: the fake discovery client
// serves no resource lists, and what this file tests is List's exclusion
// and the sweep's use of it, not API discovery.
type fixedKindsClient struct {
	*kubesweep.Client
	kinds []kubesweep.Kind
}

func (c fixedKindsClient) Kinds(context.Context, []string, string) ([]kubesweep.Kind, []string, error) {
	return c.kinds, nil, nil
}

// helmReleaseSecret is the Helm release history Secret [kubesweep.Client]
// checks for before calling an annotated object held (GitHub issue #1625).
func helmReleaseSecret(ns, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Secret")
	u.SetNamespace(ns)
	u.SetName("sh.helm.release.v1." + name + ".v1")
	u.SetLabels(map[string]string{"owner": "helm", "name": name, "status": "deployed"})
	return u
}

// helmConfigMap is a ConfigMap as `helm install` leaves it: Helm's release
// annotations, the managed-by label, and a managedFields entry naming the
// helm client as the author of its content. The estate's label is on it
// because a chart value put it there, which is the shape
// live/kubernetes/COMPATIBILITY.md warned about (GitHub issue #1607).
func helmConfigMap(ns, name, release string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("ConfigMap")
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetLabels(map[string]string{
		"tofu-estate":                  "smoke-k8s",
		"app.kubernetes.io/managed-by": "Helm",
	})
	u.SetAnnotations(map[string]string{
		"meta.helm.sh/release-name":      release,
		"meta.helm.sh/release-namespace": ns,
	})
	u.SetManagedFields([]metav1.ManagedFieldsEntry{{
		Manager:   "helm",
		Operation: metav1.ManagedFieldsOperationUpdate,
		FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:data":{".":{},"f:greeting":{}},"f:metadata":{"f:annotations":{".":{},"f:meta.helm.sh/release-name":{},"f:meta.helm.sh/release-namespace":{}},"f:labels":{".":{},"f:app.kubernetes.io/managed-by":{},"f:tofu-estate":{}}}}`)},
	}})
	return u
}

// TestKubernetesSweepHoldsHelmReleaseObjects (GitHub issue #1607, ruled on
// #1604): an object carrying Helm's release annotation and the estate's
// label, which no block declares, is controller-held. The sweep does not
// propose it as an orphan, and reports it with its release. A plain
// undeclared object beside it is still an orphan, so the test can tell "the
// exclusion held" from "the sweep saw nothing".
func TestKubernetesSweepHoldsHelmReleaseObjects(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	stray := &unstructured.Unstructured{}
	stray.SetAPIVersion("v1")
	stray.SetKind("ConfigMap")
	stray.SetNamespace("smoke-k8s")
	stray.SetName("stray")
	stray.SetLabels(map[string]string{"tofu-estate": "smoke-k8s"})
	secretGVR := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "ConfigMapList", secretGVR: "SecretList"},
		helmConfigMap("smoke-k8s", "web-greeting", "web"),
		helmReleaseSecret("smoke-k8s", "web"),
		stray,
	)
	cm := kubesweep.Kind{GVR: gvr, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map_v1"}}
	req := Request{
		Estate:   "smoke-k8s",
		Sweepers: []Sweeper{KubernetesSweep{Client: fixedKindsClient{Client: kubesweep.NewWith(&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}, dyn), kinds: []kubesweep.Kind{cm}}, Types: []string{"kubernetes_config_map_v1"}}},
	}
	res := &Result{}
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	var orphans []string
	for _, o := range res.Orphans {
		orphans = append(orphans, o.ImportID)
	}
	if len(orphans) != 1 || orphans[0] != "smoke-k8s/stray" {
		t.Fatalf("orphans = %v, want only smoke-k8s/stray: an object a Helm release holds is never proposed for removal", orphans)
	}
	// One list for both substrates (the 2026-09-26 ruling on #1604): a
	// Helm-held object is reported where an ACK-made bucket is.
	if len(res.ControllerHeld) != 1 {
		t.Fatalf("held = %+v, want the one Helm release object", res.ControllerHeld)
	}
	h := res.ControllerHeld[0]
	if h.TypeName != "kubernetes_config_map_v1" || h.Kind != "ConfigMap" || h.ImportID != "smoke-k8s/web-greeting" || h.Controller != "Helm" || h.HeldBy != "Helm release smoke-k8s/web" {
		t.Errorf("held = %+v, want kubernetes_config_map_v1 ConfigMap smoke-k8s/web-greeting held by Helm release smoke-k8s/web", h)
	}
	if res.OwnerSkipped != 1 {
		t.Errorf("owner-skipped = %d, want 1: a held object is counted with the rest of what the sweep set aside", res.OwnerSkipped)
	}
}

// TestKubernetesSweepStopsHoldingWhenReleaseSecretIsGone (GitHub issue
// #1625): the same object, the same annotation, but no release secret in
// the cluster at all - the release Helm named is gone. The sweep no longer
// holds it: it is an ordinary orphan, the same as the stray ConfigMap
// beside it, because helm's own field manager (not a control-plane one)
// wrote its content.
func TestKubernetesSweepStopsHoldingWhenReleaseSecretIsGone(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	secretGVR := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	stray := &unstructured.Unstructured{}
	stray.SetAPIVersion("v1")
	stray.SetKind("ConfigMap")
	stray.SetNamespace("smoke-k8s")
	stray.SetName("stray")
	stray.SetLabels(map[string]string{"tofu-estate": "smoke-k8s"})
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "ConfigMapList", secretGVR: "SecretList"},
		helmConfigMap("smoke-k8s", "web-greeting", "web"),
		// No helmReleaseSecret fixture: the release's history is gone.
		stray,
	)
	cm := kubesweep.Kind{GVR: gvr, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map_v1"}}
	req := Request{
		Estate:   "smoke-k8s",
		Sweepers: []Sweeper{KubernetesSweep{Client: fixedKindsClient{Client: kubesweep.NewWith(&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}, dyn), kinds: []kubesweep.Kind{cm}}, Types: []string{"kubernetes_config_map_v1"}}},
	}
	res := &Result{}
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	var orphans []string
	for _, o := range res.Orphans {
		orphans = append(orphans, o.ImportID)
	}
	sort.Strings(orphans)
	if len(orphans) != 2 || orphans[0] != "smoke-k8s/stray" || orphans[1] != "smoke-k8s/web-greeting" {
		t.Fatalf("orphans = %v, want smoke-k8s/stray and smoke-k8s/web-greeting: no release names the second one any more", orphans)
	}
	if len(res.ControllerHeld) != 0 {
		t.Errorf("held = %+v, want none: the annotation names a release that no longer exists", res.ControllerHeld)
	}
	if res.OwnerSkipped != 0 {
		t.Errorf("owner-skipped = %d, want 0", res.OwnerSkipped)
	}
}

// TestKubernetesSweepDeniedReleaseLookupIsAGapNotAnOrphan (GitHub issue
// #1738): the release check reads both of Helm's storage drivers, and a
// role that may list Secrets but not ConfigMaps cannot tell a release gone
// from one kept by HELM_DRIVER=configmap. The sweep reports that as a
// denied gap naming configmaps (#1582), and proposes nothing - not the
// release's object, and not the kind's other objects either, since the
// kind's listing did not complete.
func TestKubernetesSweepDeniedReleaseLookupIsAGapNotAnOrphan(t *testing.T) {
	svcGVR := schema.GroupVersionResource{Version: "v1", Resource: "services"}
	cmGVR := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	secretGVR := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	svc := helmConfigMap("smoke-k8s", "web-svc", "web")
	svc.SetKind("Service")
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{svcGVR: "ServiceList", cmGVR: "ConfigMapList", secretGVR: "SecretList"},
		svc,
	)
	dyn.PrependReactor("list", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "",
			errors.New(`User "sweeper" cannot list resource "configmaps" in API group "" in the namespace "smoke-k8s"`))
	})
	kind := kubesweep.Kind{GVR: svcGVR, Kind: "Service", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_service_v1"}}
	req := Request{
		Estate:   "smoke-k8s",
		Sweepers: []Sweeper{KubernetesSweep{Client: fixedKindsClient{Client: kubesweep.NewWith(&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}, dyn), kinds: []kubesweep.Kind{kind}}, Types: []string{"kubernetes_service_v1"}}},
	}
	res := &Result{}
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	if len(res.Orphans) != 0 {
		t.Errorf("orphans = %+v, want none: the release lookup was refused, not answered", res.Orphans)
	}
	if len(res.SweepGaps) != 1 || res.SweepGaps[0].TypeName != "kubernetes_service_v1" || res.SweepGaps[0].Reason != SweepGapListFailed {
		t.Errorf("gaps = %+v, want one LIST_FAILED gap for kubernetes_service_v1", res.SweepGaps)
	}
	if len(res.labelListDenied) != 1 || kubeGrantLine(res.labelListDenied[0]) != `list configmaps in namespace "smoke-k8s"` {
		t.Errorf("denials = %+v, want one naming list configmaps in namespace \"smoke-k8s\"", res.labelListDenied)
	}
}

// TestKubernetesSweepReportsHelmObjectNotInManifest (GitHub issue #1738
// item 4, ruled 2026-09-30): an object annotated with a live release whose
// manifest does not list it - here one the chart dropped under
// helm.sh/resource-policy: keep - is not controller-held and is not an
// orphan either. It is named in one warning, with its release, and the
// plan proposes nothing for it.
func TestKubernetesSweepReportsHelmObjectNotInManifest(t *testing.T) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	secretGVR := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	rls, err := json.Marshal(map[string]any{
		"name": "web", "namespace": "smoke-k8s", "version": 2, "info": map[string]any{"status": "deployed"},
		"manifest": "---\n# Source: web/templates/cm.yaml\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: web-greeting\ndata:\n  greeting: hello\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write(rls)
	_ = w.Close()
	record := helmReleaseSecret("smoke-k8s", "web")
	// The revision label is what picks the record to read; without one
	// the order is unknown, and an unknown order holds everything.
	record.SetLabels(map[string]string{"owner": "helm", "name": "web", "status": "deployed", "version": "2"})
	record.Object["data"] = map[string]any{"release": base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString(gz.Bytes())))}
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "ConfigMapList", secretGVR: "SecretList"},
		helmConfigMap("smoke-k8s", "web-greeting", "web"),
		helmConfigMap("smoke-k8s", "web-dropped", "web"),
		record,
	)
	cm := kubesweep.Kind{GVR: gvr, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map_v1"}}
	req := Request{
		Estate:   "smoke-k8s",
		Sweepers: []Sweeper{KubernetesSweep{Client: fixedKindsClient{Client: kubesweep.NewWith(&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}, dyn), kinds: []kubesweep.Kind{cm}}, Types: []string{"kubernetes_config_map_v1"}}},
	}
	res := &Result{}
	diags := sweepKubernetes(context.Background(), req, res)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	if len(res.Orphans) != 0 {
		t.Errorf("orphans = %+v, want none: an object annotated with a live release is never proposed for destroy", res.Orphans)
	}
	if len(res.ControllerHeld) != 1 || res.ControllerHeld[0].ImportID != "smoke-k8s/web-greeting" {
		t.Errorf("held = %+v, want smoke-k8s/web-greeting alone", res.ControllerHeld)
	}
	var found bool
	for _, d := range diags {
		if d.Description().Summary != SummaryHelmNotInManifest {
			continue
		}
		found = true
		if d.Severity() != tfdiags.Warning {
			t.Errorf("severity = %v, want a warning", d.Severity())
		}
		want := "ConfigMap smoke-k8s/web-dropped: annotated with live Helm release smoke-k8s/web, not in its manifest"
		if detail := d.Description().Detail; !strings.Contains(detail, want) || strings.Contains(detail, "web-greeting") {
			t.Errorf("detail = %q, want it to name web-dropped alone: %q", detail, want)
		}
	}
	if !found {
		t.Errorf("no %q warning in %v", SummaryHelmNotInManifest, diags)
	}
}
