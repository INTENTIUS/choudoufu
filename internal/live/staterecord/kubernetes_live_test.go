// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

// kubeconfigEnvVar names the kubeconfig [TestKubernetesStoreConformance] runs
// against. It is a real API server and nothing else will do: client-go's fake
// clientset assigns no metadata.resourceVersion at all, so every version
// assertion in the conformance suite - "" means absent, an update moves it, a
// stale one is refused with 409 - is vacuous against a fake.
//
// # Where this runs for real
//
// `just smoke k8s-records-in-the-cluster` (live/smoke/scenarios/), which
// creates a kind cluster, creates the records namespace, sets this variable
// and runs `go test -run TestKubernetesStore`. That scenario is claim 39 in
// live/smoke/claims.json and is what the claim's evidence is read from.
//
// A skip here is not a pass. A `go test ./internal/live/staterecord/` with no
// cluster measures the unit tests over the fake clientset and nothing about
// conditional writes; the line this test prints when it skips says so, and
// nothing in CI reads that line as evidence.
const kubeconfigEnvVar = "CHOUDOUFU_K8S_RECORD_KUBECONFIG"

// namespaceEnvVar names the Kubernetes namespace the suite writes its Secrets
// into. The store never creates one (see [NamespaceMissingError]), so the
// scenario does, the way a cluster admin would.
const namespaceEnvVar = "CHOUDOUFU_K8S_RECORD_NAMESPACE"

// liveKubernetesSecrets builds the Secret client the live tests use, or skips.
func liveKubernetesSecrets(t *testing.T) (corev1client.SecretInterface, kubernetes.Interface, string) {
	t.Helper()
	path := strings.TrimSpace(os.Getenv(kubeconfigEnvVar))
	if path == "" {
		t.Skipf("%s is not set. This test needs a real Kubernetes API server, because client-go's fake clientset assigns no resourceVersion and every version assertion here would be vacuous against it. `just smoke k8s-records-in-the-cluster` creates a kind cluster and runs it. A skip here is not a pass.", kubeconfigEnvVar)
	}
	ns := strings.TrimSpace(os.Getenv(namespaceEnvVar))
	if ns == "" {
		t.Fatalf("%s is set and %s is not. The store never creates a namespace, so the caller has to name one it created.", kubeconfigEnvVar, namespaceEnvVar)
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatalf("reading the kubeconfig at %s: %v", path, err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("building a client: %v", err)
	}
	if _, err := clientset.CoreV1().Namespaces().Get(context.Background(), ns, metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace %q: %v. The scenario creates it; this test does not, because the store does not either.", ns, err)
	}
	return clientset.CoreV1().Secrets(ns), clientset, ns
}

// TestKubernetesStoreConformance runs the whole [Store] contract against a
// real API server: the conditional create, the conditional update, the
// conditional delete, the absent-key answers and the listing rules, identical
// to what [LocalStore] and [S3Store] are held to.
//
// Each subtest gets its own key prefix inside one namespace, the way the
// SSE suite gives each of its own object-key prefix, so unrelated cases never
// collide on a key like "k1".
func TestKubernetesStoreConformance(t *testing.T) {
	secrets, _, ns := liveKubernetesSecrets(t)
	prefix := "conformance-" + randomKeySegment(t)
	t.Cleanup(func() { deleteEveryRecordUnder(t, secrets, prefix) })

	n := 0
	runConformance(t, func(t *testing.T) Store {
		t.Helper()
		n++
		store, err := NewKubernetesStore(KubernetesConfig{
			Secrets:   secrets,
			Namespace: ns,
			KeyPrefix: fmt.Sprintf("%s/case-%03d", prefix, n),
			Estate:    "conformance",
		})
		if err != nil {
			t.Fatalf("NewKubernetesStore: %v", err)
		}
		return store
	})
}

// TestKubernetesStoreVersionIsTheResourceVersion is the mapping the ruling
// names, measured rather than argued: the version a caller holds is exactly
// metadata.resourceVersion, and a conditional update sends the version the
// CALLER got from Get rather than one re-read inside the call.
//
// The second half is what separates this store from the stock backend, whose
// Put reads the Secret and updates what it read (client.go:86). A store that
// did that would pass every "the write lands" case and lose the race this
// interface exists to win, so the losing writer's payload is checked to have
// never landed.
func TestKubernetesStoreVersionIsTheResourceVersion(t *testing.T) {
	secrets, _, ns := liveKubernetesSecrets(t)
	prefix := "version-" + randomKeySegment(t)
	t.Cleanup(func() { deleteEveryRecordUnder(t, secrets, prefix) })

	store, err := NewKubernetesStore(KubernetesConfig{Secrets: secrets, Namespace: ns, KeyPrefix: prefix, Estate: "conformance"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const key = "tofu-records/conformance/aws_thing/one"

	v1, err := store.PutIfAbsent(ctx, key, []byte("first"))
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	secret, err := secrets.Get(ctx, store.SecretName(key), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the Secret back: %v", err)
	}
	if secret.ResourceVersion != v1 {
		t.Errorf("the store's version is %q and metadata.resourceVersion is %q; the ruling is that they are the same string", v1, secret.ResourceVersion)
	}
	if v1 == "" {
		t.Error("a live record's version is empty, and \"\" is the interface's absent sentinel")
	}

	// Two writers, both holding v1. One wins; the other must be told, and its
	// payload must never be on the object.
	v2, err := store.PutIfVersion(ctx, key, []byte("winner"), v1)
	if err != nil {
		t.Fatalf("the first writer's update: %v", err)
	}
	_, err = store.PutIfVersion(ctx, key, []byte("loser"), v1)
	var conflict *VersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("the second writer's update: got %v (%T), want *VersionConflictError", err, err)
	}
	if conflict.ExpectedVersion != v1 || conflict.ActualVersion != v2 {
		t.Errorf("conflict names expected %q actual %q, want %q and %q", conflict.ExpectedVersion, conflict.ActualVersion, v1, v2)
	}
	payload, _, _, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get after the conflict: %v", err)
	}
	if string(payload) != "winner" {
		t.Errorf("payload = %q, want %q: the losing write landed, so the conditional update read the version itself instead of sending the caller's", payload, "winner")
	}
}

// TestKubernetesStoreRefusesAMissingNamespaceByName is decision 2's other
// half. A LIST in a namespace that does not exist comes back EMPTY rather than
// failing, and an empty listing reads as an empty estate - #688's failure
// arriving through a different door. The write says so instead, by name and
// with the kubectl line.
func TestKubernetesStoreRefusesAMissingNamespaceByName(t *testing.T) {
	_, clientset, _ := liveKubernetesSecrets(t)
	missing := "tofu-records-never-created-" + randomKeySegment(t)
	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:   clientset.CoreV1().Secrets(missing),
		Namespace: missing,
		Estate:    "conformance",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// The hazard first, so the refusal below cannot be read as the cluster
	// simply failing everything: a LIST against the absent namespace is an
	// empty, successful answer.
	keys, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List against an absent namespace: %v, want an empty answer - if this now errors, the refusal below is no longer the only thing standing between an absent namespace and a plan that reads it as an empty estate", err)
	}
	if len(keys) != 0 {
		t.Fatalf("List against an absent namespace returned %v", keys)
	}

	_, err = store.PutIfAbsent(ctx, "tofu-records/conformance/aws_thing/one", []byte("v"))
	var absent *NamespaceMissingError
	if !errors.As(err, &absent) {
		t.Fatalf("PutIfAbsent into an absent namespace: got %v (%T), want *NamespaceMissingError", err, err)
	}
	if !strings.Contains(absent.Error(), "kubectl create namespace "+missing) {
		t.Errorf("the refusal does not carry the kubectl line that creates the namespace: %v", absent)
	}
}

// TestKubernetesStoreWritesTheEstateLabelOnARealCluster re-reads decision 3's
// labels and annotations off an object the API server actually persisted,
// rather than off the fake's in-memory copy. live/kubernetes/estate-boundary.yaml
// selects on tofu-estate existing, so a Secret that reaches etcd without it is
// outside the fence whatever the unit test over the fake said.
func TestKubernetesStoreWritesTheEstateLabelOnARealCluster(t *testing.T) {
	secrets, _, ns := liveKubernetesSecrets(t)
	prefix := "labels-" + randomKeySegment(t)
	t.Cleanup(func() { deleteEveryRecordUnder(t, secrets, prefix) })

	store, err := NewKubernetesStore(KubernetesConfig{Secrets: secrets, Namespace: ns, KeyPrefix: prefix, Estate: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	const addr = "module.net.aws_vpc.this"
	ctx := WithObjectTags(context.Background(), map[string]string{"tofu-address": addr})
	const key = "tofu-records/alice/aws_vpc/one"
	if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	secret, err := secrets.Get(context.Background(), store.SecretName(key), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the Secret back: %v", err)
	}
	if got := secret.Labels[KubernetesEstateLabel]; got != "alice" {
		t.Errorf("%s label = %q, want %q", KubernetesEstateLabel, got, "alice")
	}
	if got := secret.Annotations["tofu-address"]; got != addr {
		t.Errorf("tofu-address annotation = %q, want %q", got, addr)
	}
	if got := secret.Annotations[KubernetesRecordKeyAnnotation]; got != prefix+"/"+key {
		t.Errorf("%s annotation = %q, want %q", KubernetesRecordKeyAnnotation, got, prefix+"/"+key)
	}
}

// deleteEveryRecordUnder removes what a test wrote, so one namespace can host
// run after run without the listings growing.
func deleteEveryRecordUnder(t *testing.T, secrets corev1client.SecretInterface, prefix string) {
	t.Helper()
	ctx := context.Background()
	list, err := secrets.List(ctx, metav1.ListOptions{LabelSelector: KubernetesManagedByLabel + "=" + KubernetesManagedByValue})
	if err != nil {
		t.Logf("cleanup: listing %s: %v", prefix, err)
		return
	}
	for i := range list.Items {
		secret := &list.Items[i]
		if !strings.HasPrefix(secret.Annotations[KubernetesRecordKeyAnnotation], prefix) {
			continue
		}
		if err := secrets.Delete(ctx, secret.Name, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
			t.Logf("cleanup: deleting %s: %v", secret.Name, err)
		}
	}
}
