// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"context"
	"errors"
	"fmt"
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
)

// GitHub issue #1738: the release-existence check behind a Helm hold
// (#1625) read only the Secrets storage driver, counted a release
// uninstalled with --keep-history as live, and cached one namespace's
// answer for every other namespace holding a release of the same name.

var (
	helmCMGVR     = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	helmSecretGVR = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	// servicesGVR is the kind these tests sweep: not ConfigMap, so a
	// reactor on the configmaps list refuses the release lookup alone and
	// never the sweep's own list.
	servicesGVR = schema.GroupVersionResource{Version: "v1", Resource: "services"}
)

// helmRecord is one revision of a Helm release as either storage driver
// keeps it (helm.sh/helm/v3 pkg/storage/driver, secrets.go and
// cfgmaps.go): named sh.helm.release.v1.<name>.v<revision> and labelled
// owner=helm, name, status and version.
func helmRecord(kind, ns, name string, revision int, status string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind(kind)
	u.SetNamespace(ns)
	u.SetName(fmt.Sprintf("sh.helm.release.v1.%s.v%d", name, revision))
	u.SetLabels(map[string]string{"owner": "helm", "name": name, "status": status, "version": fmt.Sprint(revision)})
	return u
}

// helmService is an estate-labelled Service carrying Helm's release
// annotations; releaseNS "" leaves the namespace annotation off.
func helmService(ns, name, release, releaseNS string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Service")
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

func helmSweep(t *testing.T, objs ...runtime.Object) *fakedynamic.FakeDynamicClient {
	t.Helper()
	return fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{servicesGVR: "ServiceList", helmSecretGVR: "SecretList", helmCMGVR: "ConfigMapList"},
		objs...)
}

func listServices(dyn *fakedynamic.FakeDynamicClient) ([]Object, Skipped, error) {
	c := NewWith(&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}, dyn)
	return c.List(context.Background(), Kind{GVR: servicesGVR, Kind: "Service", Namespaced: true}, "tofu-estate", "smoke-k8s")
}

func importIDs(objs []Object) []string {
	var ids []string
	for _, o := range objs {
		ids = append(ids, o.ImportID)
	}
	return ids
}

// TestHelmConfigMapDriverReleaseIsHeld: a release stored with
// HELM_DRIVER=configmap keeps its history in ConfigMaps, not Secrets. Its
// objects are held exactly as a Secrets-driver release's are; reading only
// Secrets called it gone and proposed its objects for destroy.
func TestHelmConfigMapDriverReleaseIsHeld(t *testing.T) {
	dyn := helmSweep(t,
		helmService("web", "web-svc", "web", "web"),
		helmRecord("ConfigMap", "web", "web", 1, "deployed"),
	)
	got, skipped, err := listServices(dyn)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("orphans = %v, want none: release web/web is live in the ConfigMap driver", importIDs(got))
	}
	if len(skipped.Held) != 1 || skipped.Held[0].HeldBy != "Helm release web/web" {
		t.Errorf("held = %+v, want web-svc held by Helm release web/web", skipped.Held)
	}
}

// TestHelmKeepHistoryUninstallIsGone: `helm uninstall --keep-history`
// deletes the release's objects but keeps its records, the latest one
// relabelled status=uninstalled (the ones before it stay superseded).
// Helm itself reports no such release (`helm list` omits it, `helm
// status` reads "uninstalled"), so an object still annotated with it is
// held by nothing.
func TestHelmKeepHistoryUninstallIsGone(t *testing.T) {
	for _, kind := range []string{"Secret", "ConfigMap"} {
		t.Run(kind, func(t *testing.T) {
			dyn := helmSweep(t,
				helmService("web", "web-svc", "web", "web"),
				helmRecord(kind, "web", "web", 1, "superseded"),
				helmRecord(kind, "web", "web", 2, "uninstalled"),
			)
			got, skipped, err := listServices(dyn)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].ImportID != "web/web-svc" {
				t.Errorf("orphans = %v, want [web/web-svc]: the release was uninstalled", importIDs(got))
			}
			if len(skipped.Held) != 0 {
				t.Errorf("held = %+v, want none", skipped.Held)
			}
		})
	}
}

// TestHelmReleaseStatusDecidesLiveness pins which statuses hold. Helm's
// status set is pkg/release/status.go: unknown, deployed, uninstalled,
// superseded, failed, uninstalling, pending-install, pending-upgrade,
// pending-rollback. Only a release whose latest record reads uninstalled
// is gone; every other status - a failed install included, whose objects
// `helm uninstall` still deletes - is a release Helm still answers for,
// and holding is the direction that never proposes a destroy.
func TestHelmReleaseStatusDecidesLiveness(t *testing.T) {
	for _, tc := range []struct {
		records []*unstructured.Unstructured
		held    bool
	}{
		{[]*unstructured.Unstructured{helmRecord("Secret", "web", "web", 1, "deployed")}, true},
		{[]*unstructured.Unstructured{helmRecord("Secret", "web", "web", 1, "failed")}, true},
		{[]*unstructured.Unstructured{helmRecord("Secret", "web", "web", 1, "pending-install")}, true},
		{[]*unstructured.Unstructured{helmRecord("Secret", "web", "web", 1, "uninstalling")}, true},
		{[]*unstructured.Unstructured{helmRecord("Secret", "web", "web", 1, "uninstalled")}, false},
		// A rollback after a keep-history uninstall: the latest revision
		// decides, not any older one.
		{[]*unstructured.Unstructured{helmRecord("Secret", "web", "web", 1, "uninstalled"), helmRecord("Secret", "web", "web", 2, "deployed")}, true},
		// Ordered by revision number, not by name: v10 is after v9.
		{[]*unstructured.Unstructured{helmRecord("Secret", "web", "web", 9, "deployed"), helmRecord("Secret", "web", "web", 10, "uninstalled")}, false},
	} {
		var desc []string
		objs := []runtime.Object{helmService("web", "web-svc", "web", "web")}
		for _, r := range tc.records {
			objs = append(objs, r)
			desc = append(desc, r.GetName()+"="+r.GetLabels()["status"])
		}
		_, skipped, err := listServices(helmSweep(t, objs...))
		if err != nil {
			t.Fatal(err)
		}
		if got := len(skipped.Held) == 1; got != tc.held {
			t.Errorf("records %v: held = %v, want %v", desc, got, tc.held)
		}
	}
}

// TestHelmReleaseSameNameTwoNamespaces: two objects with no
// release-namespace annotation, in two namespaces, both naming release
// "web". Each is looked up in its own namespace, and each answer is its
// own: the release exists in ns-a only.
func TestHelmReleaseSameNameTwoNamespaces(t *testing.T) {
	dyn := helmSweep(t,
		helmService("ns-a", "web-svc", "web", ""),
		helmService("ns-b", "web-svc", "web", ""),
		helmRecord("Secret", "ns-a", "web", 1, "deployed"),
	)
	got, skipped, err := listServices(dyn)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ImportID != "ns-b/web-svc" {
		t.Errorf("orphans = %v, want [ns-b/web-svc]: ns-b has no release web", importIDs(got))
	}
	if len(skipped.Held) != 1 || skipped.Held[0].Namespace != "ns-a" {
		t.Errorf("held = %+v, want ns-a/web-svc alone", skipped.Held)
	}
}

// forbidHelmList refuses the release lookup on resource, the way RBAC does
// for a role without list on it, and leaves every other list alone.
func forbidHelmList(dyn *fakedynamic.FakeDynamicClient, resource string) {
	dyn.PrependReactor("list", resource, func(action clienttesting.Action) (bool, runtime.Object, error) {
		la, ok := action.(clienttesting.ListAction)
		if !ok || !strings.Contains(la.GetListRestrictions().Labels.String(), "owner=helm") {
			return false, nil, nil
		}
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "",
			fmt.Errorf(`User "sweeper" cannot list resource %q in API group "" in the namespace %q`, resource, action.GetNamespace()))
	})
}

// TestHelmForbiddenConfigMapListIsNotAnOrphan: a least-privilege role
// that may list Secrets but not ConfigMaps cannot tell a release gone from
// a release stored in ConfigMaps. That is a denied gap naming configmaps
// (#1582's class), never an orphan.
func TestHelmForbiddenConfigMapListIsNotAnOrphan(t *testing.T) {
	dyn := helmSweep(t, helmService("web", "web-svc", "web", "web"))
	forbidHelmList(dyn, "configmaps")
	got, _, err := listServices(dyn)
	if err == nil {
		t.Fatalf("no error; orphans = %v, want the denied lookup reported: an unanswered release check is never 'gone'", importIDs(got))
	}
	d, denied := Forbidden(err)
	if !denied || d.Verb != "list" || d.Resource != "configmaps" || d.Namespace != "web" {
		t.Errorf("Forbidden(%v) = %+v, %v; want list configmaps in namespace web", err, d, denied)
	}
	if len(got) != 0 {
		t.Errorf("orphans = %v, want none", importIDs(got))
	}
}

// TestHelmOneDriverAnsweringLiveIsEnough: a live record in the store the
// role may read holds the object even when the other store is refused -
// the refusal could only have added a record, never removed one. The
// refusal still stops a "gone" answer when neither store shows one.
func TestHelmOneDriverAnsweringLiveIsEnough(t *testing.T) {
	for _, tc := range []struct {
		forbid, recordKind string
	}{{"configmaps", "Secret"}, {"secrets", "ConfigMap"}} {
		dyn := helmSweep(t,
			helmService("web", "web-svc", "web", "web"),
			helmRecord(tc.recordKind, "web", "web", 1, "deployed"),
		)
		forbidHelmList(dyn, tc.forbid)
		_, skipped, err := listServices(dyn)
		if err != nil {
			t.Errorf("%s forbidden, release in %ss: %v, want held", tc.forbid, tc.recordKind, err)
			continue
		}
		if len(skipped.Held) != 1 {
			t.Errorf("%s forbidden, release in %ss: held = %+v, want web-svc", tc.forbid, tc.recordKind, skipped.Held)
		}
	}
	dyn := helmSweep(t, helmService("web", "web-svc", "web", "web"))
	forbidHelmList(dyn, "secrets")
	if got, _, err := listServices(dyn); err == nil {
		t.Errorf("secrets forbidden, no record anywhere: no error, orphans = %v", importIDs(got))
	} else if d, denied := Forbidden(err); !denied || d.Resource != "secrets" {
		t.Errorf("Forbidden(%v) = %+v, %v; want secrets", err, d, denied)
	}
	// A non-RBAC failure is not "gone" either.
	dyn = helmSweep(t, helmService("web", "web-svc", "web", "web"))
	dyn.PrependReactor("list", "configmaps", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection reset by peer")
	})
	if got, _, err := listServices(dyn); err == nil {
		t.Errorf("configmaps list failed: no error, orphans = %v", importIDs(got))
	}
}
