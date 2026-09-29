// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

// helmSweepKubeconfigEnvVar names the kubeconfig [TestListHoldsHelmReleaseOnACluster]
// runs against: a real cluster (kind is what it was written on) with the
// helm binary on PATH. The fake-clientset tests beside it pin the rule; this
// one pins the fact the rule rests on, that `helm install` writes
// meta.helm.sh/release-name onto what it installs and nothing else in the
// object says a release holds it. A skip here is not a pass.
const helmSweepKubeconfigEnvVar = "CHOUDOUFU_K8S_SWEEP_KUBECONFIG"

// TestListHoldsHelmReleaseOnACluster (GitHub issue #1607): a chart whose
// values put tofu-estate on a ConfigMap is installed with helm, and a
// ConfigMap carrying the same label is created by an ordinary client beside
// it. List returns the ordinary one and holds the release's, naming the
// release; before #1607 it returned both, and the sweep proposed destroying
// the release's object.
func TestListHoldsHelmReleaseOnACluster(t *testing.T) {
	path := strings.TrimSpace(os.Getenv(helmSweepKubeconfigEnvVar))
	if path == "" {
		t.Skipf("%s is not set. This test needs a real cluster and the helm binary; the fake-clientset tests pin the rule without one. A skip here is not a pass.", helmSweepKubeconfigEnvVar)
	}
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatalf("%s is set and helm is not on PATH: %v", helmSweepKubeconfigEnvVar, err)
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatalf("reading the kubeconfig at %s: %v", path, err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
	ns, estate, release := "helm-sweep-"+suffix, "helm-sweep-"+suffix, "web"

	nsGVR := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	nsObj := &unstructured.Unstructured{}
	nsObj.SetAPIVersion("v1")
	nsObj.SetKind("Namespace")
	nsObj.SetName(ns)
	if _, err := dyn.Resource(nsGVR).Create(ctx, nsObj, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = dyn.Resource(nsGVR).Delete(context.Background(), ns, metav1.DeleteOptions{})
	})

	chart := t.TempDir()
	writeFile(t, filepath.Join(chart, "Chart.yaml"), "apiVersion: v2\nname: web\nversion: 0.1.0\n")
	writeFile(t, filepath.Join(chart, "values.yaml"), "estate: \"\"\n")
	writeFile(t, filepath.Join(chart, "templates", "cm.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-greeting
  labels:
    tofu-estate: {{ .Values.estate | quote }}
data:
  greeting: hello
`)
	install := exec.Command(helm, "install", release, chart, "--namespace", ns, "--set", "estate="+estate, "--kubeconfig", path)
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("helm install: %v\n%s", err, out)
	}

	cmGVR := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	plain := &unstructured.Unstructured{}
	plain.SetAPIVersion("v1")
	plain.SetKind("ConfigMap")
	plain.SetNamespace(ns)
	plain.SetName("declared-elsewhere")
	plain.SetLabels(map[string]string{"tofu-estate": estate})
	if _, err := dyn.Resource(cmGVR).Namespace(ns).Create(ctx, plain, metav1.CreateOptions{FieldManager: "choudoufu-test"}); err != nil {
		t.Fatal(err)
	}

	c := NewWith(nil, dyn)
	got, skipped, err := c.List(ctx, Kind{GVR: cmGVR, Kind: "ConfigMap", Namespaced: true}, "tofu-estate", estate)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range got {
		names = append(names, o.Name)
	}
	t.Logf("listed %v; held %+v", names, skipped.Held)
	if len(got) != 1 || got[0].Name != "declared-elsewhere" {
		t.Errorf("listed %v, want only declared-elsewhere: the release's ConfigMap would be proposed for removal", names)
	}
	want := "Helm release " + ns + "/" + release
	if len(skipped.Held) != 1 || skipped.Held[0].Name != release+"-greeting" || skipped.Held[0].HeldBy != want {
		t.Errorf("held = %+v, want %s-greeting held by %s", skipped.Held, release, want)
	}
}

// TestListDropsHelmHoldWhenReleaseSecretIsGoneOnACluster (GitHub issue
// #1625, a #1607 follow-up): a chart installs a ConfigMap and Helm's
// release annotations
// land on it as usual. The release secret Helm keeps in the same namespace
// (sh.helm.release.v1.<name>.v1, labels owner=helm,name=<name>) is then
// deleted directly - the way a user moves an object off Helm without
// deleting the object itself, since `helm uninstall` would delete the
// object along with the release. Server-side apply leaves the annotations
// alone: they are not this deletion's field, so they persist on an object
// no release owns any more.
//
// Measured on kind on 2026-09-26: after `kubectl delete secret
// sh.helm.release.v1.web.v1`, `helm status web` answers "release: not
// found" and `helm list` shows nothing, but the ConfigMap keeps both
// meta.helm.sh/release-name and meta.helm.sh/release-namespace verbatim.
// Before this fix, List still reported it held by "Helm release
// NAMESPACE/web" and never proposed it for adoption or removal.
func TestListDropsHelmHoldWhenReleaseSecretIsGoneOnACluster(t *testing.T) {
	path := strings.TrimSpace(os.Getenv(helmSweepKubeconfigEnvVar))
	if path == "" {
		t.Skipf("%s is not set. This test needs a real cluster and the helm binary. A skip here is not a pass.", helmSweepKubeconfigEnvVar)
	}
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatalf("%s is set and helm is not on PATH: %v", helmSweepKubeconfigEnvVar, err)
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatalf("reading the kubeconfig at %s: %v", path, err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
	ns, estate, release := "helm-gone-"+suffix, "helm-gone-"+suffix, "web"

	nsGVR := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	nsObj := &unstructured.Unstructured{}
	nsObj.SetAPIVersion("v1")
	nsObj.SetKind("Namespace")
	nsObj.SetName(ns)
	if _, err := dyn.Resource(nsGVR).Create(ctx, nsObj, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = dyn.Resource(nsGVR).Delete(context.Background(), ns, metav1.DeleteOptions{})
	})

	chart := t.TempDir()
	writeFile(t, filepath.Join(chart, "Chart.yaml"), "apiVersion: v2\nname: web\nversion: 0.1.0\n")
	writeFile(t, filepath.Join(chart, "values.yaml"), "estate: \"\"\n")
	writeFile(t, filepath.Join(chart, "templates", "cm.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-greeting
  labels:
    tofu-estate: {{ .Values.estate | quote }}
data:
  greeting: hello
`)
	install := exec.Command(helm, "install", release, chart, "--namespace", ns, "--set", "estate="+estate, "--kubeconfig", path)
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("helm install: %v\n%s", err, out)
	}

	secretGVR := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	secrets, err := dyn.Resource(secretGVR).Namespace(ns).List(ctx, metav1.ListOptions{LabelSelector: "owner=helm,name=" + release})
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets.Items) == 0 {
		t.Fatal("helm wrote no release secret; the test fixture is wrong")
	}
	for _, s := range secrets.Items {
		if err := dyn.Resource(secretGVR).Namespace(ns).Delete(ctx, s.GetName(), metav1.DeleteOptions{}); err != nil {
			t.Fatalf("deleting release secret %s: %v", s.GetName(), err)
		}
	}

	status := exec.Command(helm, "status", release, "--namespace", ns, "--kubeconfig", path)
	if out, err := status.CombinedOutput(); err == nil {
		t.Fatalf("helm status still finds the release after its secret was deleted: %s", out)
	}

	cmGVR := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	cm, err := dyn.Resource(cmGVR).Namespace(ns).Get(ctx, release+"-greeting", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ann := cm.GetAnnotations()
	if ann[HelmReleaseNameAnnotation] != release {
		t.Fatalf("the ConfigMap's Helm annotation did not survive the secret's deletion; the fixture proves nothing: %+v", ann)
	}

	c := NewWith(nil, dyn)
	got, skipped, err := c.List(ctx, Kind{GVR: cmGVR, Kind: "ConfigMap", Namespaced: true}, "tofu-estate", estate)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range got {
		names = append(names, o.Name)
	}
	t.Logf("listed %v; held %+v", names, skipped.Held)
	if len(got) != 1 || got[0].Name != release+"-greeting" {
		t.Errorf("listed %v, want [%s]: no release names it any more, so it is an ordinary estate-labelled object", names, release+"-greeting")
	}
	if len(skipped.Held) != 0 {
		t.Errorf("held = %+v, want none: the annotation names a release that no longer exists", skipped.Held)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
