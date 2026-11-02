// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// The four ways this store answered "absent" or "empty" with a nil error,
// each measured against a real API server. GitHub issue #1448 section A.
//
// They are here rather than beside the fake-clientset tests because the fake
// answers none of them the way the API server does: it has no namespaces to
// be missing or terminating, it applies no label selector to a LIST unless
// asked, and it assigns no resourceVersion. Every one of these cases was
// first reproduced on kind and the refusals below were written against what
// the cluster actually said.

// TestKubernetesListRefusesARecordThatLostItsLabels is A1. The listing used
// to be a label selector, so a record Secret that lost tofu-estate or
// app.kubernetes.io/managed-by - a `kubectl label ... tofu-estate-`, a restore
// that dropped labels - was in neither List nor GetAll, with a nil error,
// while a Get by name still served it. A plan built from that listing proposes
// creating the resource it records a second time.
func TestKubernetesListRefusesARecordThatLostItsLabels(t *testing.T) {
	secrets, clientset, ns := liveKubernetesSecrets(t)
	prefix := "unlabelled-" + randomKeySegment(t)
	t.Cleanup(func() { deleteEveryRecordUnder(t, secrets, prefix) })
	store := liveStore(t, secrets, clientset, ns, prefix, "alice")
	ctx := context.Background()

	const kept = "tofu-records/alice/aws_thing/kept"
	if _, err := store.PutIfAbsent(ctx, kept, []byte("kept")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	for _, tc := range []struct {
		name  string
		key   string
		strip string
	}{
		{"the estate label", "tofu-records/alice/aws_thing/lost-estate", KubernetesEstateLabel},
		{"the managed-by label", "tofu-records/alice/aws_thing/lost-managed-by", KubernetesManagedByLabel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.PutIfAbsent(ctx, tc.key, []byte("v")); err != nil {
				t.Fatalf("PutIfAbsent: %v", err)
			}
			name := store.SecretName(tc.key)
			stripLabel(t, secrets, name, tc.strip)

			// The record is still there and a Get by name still serves it,
			// which is what made the omission silent.
			if _, _, exists, err := store.Get(ctx, tc.key); err != nil || !exists {
				t.Fatalf("Get after the label was stripped: exists=%v err=%v, want the record still served", exists, err)
			}

			keys, err := store.List(ctx, "tofu-records/alice/")
			var unlabelled *UnlabelledRecordError
			if !errors.As(err, &unlabelled) {
				t.Fatalf("List over a record that lost %s: keys=%v err=%v (%T), want *UnlabelledRecordError", tc.strip, keys, err, err)
			}
			if keys != nil {
				t.Errorf("List returned %v alongside the refusal, want no listing at all", keys)
			}
			if unlabelled.SecretName != name {
				t.Errorf("the refusal names Secret %q, want %q", unlabelled.SecretName, name)
			}
			if unlabelled.Key != prefix+"/"+tc.key {
				t.Errorf("the refusal names key %q, want %q", unlabelled.Key, prefix+"/"+tc.key)
			}
			text := unlabelled.Error()
			if !strings.Contains(text, tc.strip) {
				t.Errorf("the refusal does not name the label that is missing: %s", text)
			}
			if want := fmt.Sprintf("kubectl -n %s label secret %s --overwrite", ns, name); !strings.Contains(text, want) {
				t.Errorf("the refusal does not carry the kubectl line that puts the labels back (%q): %s", want, text)
			}

			if _, err := store.GetAll(ctx, "tofu-records/alice/"); !errors.As(err, &unlabelled) {
				t.Errorf("GetAll over the same namespace: %v (%T), want *UnlabelledRecordError", err, err)
			}

			// Relabelled, the refusal goes away: this cannot be a check that
			// refuses every listing.
			relabel(t, secrets, name, tc.strip, map[string]string{
				KubernetesEstateLabel:    "alice",
				KubernetesManagedByLabel: KubernetesManagedByValue,
			}[tc.strip])
			keys, err = store.List(ctx, "tofu-records/alice/")
			if err != nil {
				t.Fatalf("List after the label was put back: %v", err)
			}
			var listed bool
			for _, k := range keys {
				listed = listed || k == tc.key
			}
			if !listed {
				t.Errorf("List = %v, want the relabelled record %q among them", keys, tc.key)
			}
		})
	}

	// And a correctly labelled record of ANOTHER estate in the same namespace
	// is skipped, not refused: a shared namespace is a supported arrangement.
	other := liveStore(t, secrets, clientset, ns, prefix, "bob")
	if _, err := other.PutIfAbsent(ctx, "tofu-records/bob/aws_thing/theirs", []byte("v")); err != nil {
		t.Fatalf("the other estate's PutIfAbsent: %v", err)
	}
	keys, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List with another estate's record in the namespace: %v", err)
	}
	for _, k := range keys {
		if strings.Contains(k, "/bob/") {
			t.Errorf("List = %v, want none of the other estate's records", keys)
		}
	}
}

// TestKubernetesRefusesAMissingNamespaceOnEveryRead is A2's first half.
// notFoundIsNamespace keys on Status.Details.Kind == "namespaces", which the
// API server sends only for a CREATE: measured on kind, a GET of a Secret in
// a missing namespace is `404 secrets "x" not found` and a LIST is
// `200 {"items":[]}`. So Get answered no record, List and GetAll answered
// empty, and Delete(key, "") answered success, all with a nil error.
func TestKubernetesRefusesAMissingNamespaceOnEveryRead(t *testing.T) {
	_, clientset, _ := liveKubernetesSecrets(t)
	missing := "tofu-records-never-created-" + randomKeySegment(t)
	store := liveStore(t, clientset.CoreV1().Secrets(missing), clientset, missing, "", "alice")
	ctx := context.Background()
	const key = "tofu-records/alice/aws_thing/one"

	payload, version, exists, err := store.Get(ctx, key)
	var absent *NamespaceMissingError
	if !errors.As(err, &absent) {
		t.Errorf("Get in a missing namespace: payload=%q version=%q exists=%v err=%v (%T), want *NamespaceMissingError", payload, version, exists, err, err)
	}
	keys, err := store.List(ctx, "tofu-records/alice/")
	if !errors.As(err, &absent) {
		t.Errorf("List in a missing namespace: keys=%v err=%v (%T), want *NamespaceMissingError", keys, err, err)
	}
	records, err := store.GetAll(ctx, "tofu-records/alice/")
	if !errors.As(err, &absent) {
		t.Errorf("GetAll in a missing namespace: records=%v err=%v (%T), want *NamespaceMissingError", records, err, err)
	}
	if err := store.Delete(ctx, key, ""); !errors.As(err, &absent) {
		t.Errorf("Delete(key, \"\") in a missing namespace: err=%v (%T), want *NamespaceMissingError", err, err)
	}
	if err := store.Delete(ctx, key, ""); errors.As(err, &absent) && !strings.Contains(err.Error(), "kubectl create namespace "+missing) {
		t.Errorf("the refusal does not carry the kubectl line that creates the namespace: %v", err)
	}
}

// TestKubernetesRefusesATerminatingNamespace is A2's second half. A namespace
// held in Terminating by a finalizer still answers a LIST with 200 and an
// empty list once its contents are gone, so an estate whose namespace is being
// deleted reads as an estate with no records at all.
func TestKubernetesRefusesATerminatingNamespace(t *testing.T) {
	_, clientset, _ := liveKubernetesSecrets(t)
	ctx := context.Background()
	ns := "tofu-records-terminating-" + randomKeySegment(t)
	createNamespace(t, clientset, ns)
	holdNamespaceTerminating(t, clientset, ns)

	store := liveStore(t, clientset.CoreV1().Secrets(ns), clientset, ns, "", "alice")
	const key = "tofu-records/alice/aws_thing/one"
	if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent before the namespace is deleted: %v", err)
	}

	if err := clientset.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("deleting namespace %q: %v", ns, err)
	}
	waitForTerminating(t, clientset, ns)

	var terminating *NamespaceTerminatingError
	keys, err := store.List(ctx, "tofu-records/alice/")
	if !errors.As(err, &terminating) {
		t.Errorf("List in a terminating namespace: keys=%v err=%v (%T), want *NamespaceTerminatingError", keys, err, err)
	}
	if _, err := store.GetAll(ctx, "tofu-records/alice/"); !errors.As(err, &terminating) {
		t.Errorf("GetAll in a terminating namespace: %v (%T), want *NamespaceTerminatingError", err, err)
	}
	if _, _, _, err := store.Get(ctx, "tofu-records/alice/aws_thing/never-written"); !errors.As(err, &terminating) {
		t.Errorf("Get of an absent key in a terminating namespace: %v (%T), want *NamespaceTerminatingError", err, err)
	}
	if err := store.Delete(ctx, "tofu-records/alice/aws_thing/never-written", ""); !errors.As(err, &terminating) {
		t.Errorf("Delete(key, \"\") in a terminating namespace: %v (%T), want *NamespaceTerminatingError", err, err)
	}
	// The write path too: the API server answers a create with 403 and a
	// NamespaceTerminating cause, which is not this run's identity being
	// refused permission.
	_, err = store.PutIfAbsent(ctx, "tofu-records/alice/aws_thing/two", []byte("v"))
	if !errors.As(err, &terminating) {
		t.Errorf("PutIfAbsent in a terminating namespace: %v (%T), want *NamespaceTerminatingError", err, err)
	} else if !strings.Contains(terminating.Error(), ns) {
		t.Errorf("the refusal does not name the namespace: %v", terminating)
	}
}

// TestKubernetesRefusesARenamedRecordSecret is A3. The list loop took the key
// from the annotation and never checked the name, so List and GetAll returned
// a key that Get denies and every conditional write of it conflicts forever.
func TestKubernetesRefusesARenamedRecordSecret(t *testing.T) {
	secrets, clientset, ns := liveKubernetesSecrets(t)
	prefix := "renamed-" + randomKeySegment(t)
	t.Cleanup(func() { deleteEveryRecordUnder(t, secrets, prefix) })
	store := liveStore(t, secrets, clientset, ns, prefix, "alice")
	ctx := context.Background()

	const key = "tofu-records/alice/aws_thing/one"
	if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	want := store.SecretName(key)
	renamed := "tofu-record-renamed-by-hand-" + randomKeySegment(t)
	copySecret(t, secrets, want, renamed)
	if err := secrets.Delete(ctx, want, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("deleting the original: %v", err)
	}

	// The contradiction the refusal is about: the key is in the listing and
	// the Get of it says there is no record.
	if _, _, exists, err := store.Get(ctx, key); err != nil || exists {
		t.Fatalf("Get of the renamed record: exists=%v err=%v, want no record (the name is what a Get reads)", exists, err)
	}

	keys, err := store.List(ctx, "tofu-records/alice/")
	var misnamed *MisnamedRecordError
	if !errors.As(err, &misnamed) {
		t.Fatalf("List over a renamed record Secret: keys=%v err=%v (%T), want *MisnamedRecordError", keys, err, err)
	}
	if misnamed.SecretName != renamed || misnamed.WantName != want {
		t.Errorf("the refusal names Secret %q and the name %q, want %q and %q", misnamed.SecretName, misnamed.WantName, renamed, want)
	}
	if misnamed.Key != prefix+"/"+key {
		t.Errorf("the refusal names key %q, want %q", misnamed.Key, prefix+"/"+key)
	}
	if _, err := store.GetAll(ctx, "tofu-records/alice/"); !errors.As(err, &misnamed) {
		t.Errorf("GetAll over the same namespace: %v (%T), want *MisnamedRecordError", err, err)
	}
}

// TestKubernetesRefusesTwoSecretsHoldingOneKey is A4. List returned the key
// twice and GetAll kept whichever listed last. With A3's name check in place
// the duplicate can only be the mis-named one, which the refusal says: it
// names both Secrets and the one name the key hashes to.
func TestKubernetesRefusesTwoSecretsHoldingOneKey(t *testing.T) {
	secrets, clientset, ns := liveKubernetesSecrets(t)
	prefix := "duplicate-" + randomKeySegment(t)
	t.Cleanup(func() { deleteEveryRecordUnder(t, secrets, prefix) })
	store := liveStore(t, secrets, clientset, ns, prefix, "alice")
	ctx := context.Background()

	const key = "tofu-records/alice/aws_thing/one"
	if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	want := store.SecretName(key)
	copyName := "tofu-record-copied-by-hand-" + randomKeySegment(t)
	copySecret(t, secrets, want, copyName)

	keys, err := store.List(ctx, "tofu-records/alice/")
	var duplicate *DuplicateRecordKeyError
	if !errors.As(err, &duplicate) {
		t.Fatalf("List over two Secrets holding one key: keys=%v err=%v (%T), want *DuplicateRecordKeyError", keys, err, err)
	}
	if keys != nil {
		t.Errorf("List returned %v alongside the refusal, want no listing at all", keys)
	}
	if duplicate.Key != prefix+"/"+key {
		t.Errorf("the refusal names key %q, want %q", duplicate.Key, prefix+"/"+key)
	}
	if len(duplicate.SecretNames) != 2 {
		t.Fatalf("the refusal names %v, want both Secrets", duplicate.SecretNames)
	}
	for _, name := range []string{want, copyName} {
		found := false
		for _, got := range duplicate.SecretNames {
			if got == name {
				found = true
			}
		}
		if !found {
			t.Errorf("the refusal names %v, want %q among them", duplicate.SecretNames, name)
		}
	}
	// The reasoning A4 rests on, measured rather than argued: of the two, the
	// one the key hashes to is the record and the other is the copy.
	if duplicate.WantName != want {
		t.Errorf("the refusal says the key hashes to %q, want %q", duplicate.WantName, want)
	}
	if _, err := store.GetAll(ctx, "tofu-records/alice/"); !errors.As(err, &duplicate) {
		t.Errorf("GetAll over the same namespace: %v (%T), want *DuplicateRecordKeyError", err, err)
	}
}

// liveStore builds a store carrying the Clientset, which is what the
// namespace probe in [KubernetesStore.Get] and [KubernetesStore.List] needs
// and what internal/live/projection always passes.
func liveStore(t *testing.T, secrets corev1client.SecretInterface, clientset kubernetes.Interface, ns, prefix, estate string) *KubernetesStore {
	t.Helper()
	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:   secrets,
		Clientset: clientset,
		Namespace: ns,
		KeyPrefix: prefix,
		Estate:    estate,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	return store
}

// stripLabel removes one label from a Secret, the way
// `kubectl label secret NAME key-` does.
func stripLabel(t *testing.T, secrets corev1client.SecretInterface, name, label string) {
	t.Helper()
	ctx := context.Background()
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading Secret %q: %v", name, err)
	}
	delete(secret.Labels, label)
	if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("stripping %s from Secret %q: %v", label, name, err)
	}
}

func relabel(t *testing.T, secrets corev1client.SecretInterface, name, label, value string) {
	t.Helper()
	ctx := context.Background()
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading Secret %q: %v", name, err)
	}
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels[label] = value
	if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("labelling Secret %q: %v", name, err)
	}
}

// copySecret is the hand edit A3 and A4 are about: the same record object
// under a second name, labels and annotations and all.
func copySecret(t *testing.T, secrets corev1client.SecretInterface, from, to string) {
	t.Helper()
	ctx := context.Background()
	secret, err := secrets.Get(ctx, from, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading Secret %q: %v", from, err)
	}
	copied := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        to,
			Namespace:   secret.Namespace,
			Labels:      secret.Labels,
			Annotations: secret.Annotations,
		},
		Data: secret.Data,
	}
	if _, err := secrets.Create(ctx, copied, metav1.CreateOptions{}); err != nil {
		t.Fatalf("copying Secret %q to %q: %v", from, to, err)
	}
	t.Cleanup(func() {
		if err := secrets.Delete(context.Background(), to, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
			t.Logf("cleanup: deleting %s: %v", to, err)
		}
	})
}

func createNamespace(t *testing.T, clientset kubernetes.Interface, ns string) {
	t.Helper()
	ctx := context.Background()
	if _, err := clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating namespace %q: %v", ns, err)
	}
}

// holdNamespaceTerminating puts a finalizer on an object inside ns, so that a
// delete of the namespace stops at Terminating instead of completing. The
// cleanup takes the finalizer off again, which lets the delete finish; without
// it the namespace would sit in Terminating on this cluster forever.
func holdNamespaceTerminating(t *testing.T, clientset kubernetes.Interface, ns string) {
	t.Helper()
	ctx := context.Background()
	const holder = "namespace-holder"
	if _, err := clientset.CoreV1().ConfigMaps(ns).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:       holder,
			Namespace:  ns,
			Finalizers: []string{"choudoufu.intentius.io/test-hold"},
		},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating the finalizer holder in %q: %v", ns, err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		cm, err := clientset.CoreV1().ConfigMaps(ns).Get(ctx, holder, metav1.GetOptions{})
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				t.Logf("cleanup: reading the finalizer holder in %q: %v", ns, err)
			}
			return
		}
		cm.Finalizers = nil
		if _, err := clientset.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
			t.Logf("cleanup: releasing the finalizer in %q: %v (the namespace stays in Terminating until it is released)", ns, err)
		}
		if err := clientset.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
			t.Logf("cleanup: deleting namespace %q: %v", ns, err)
		}
	})
}

// waitForTerminating blocks until ns reports phase Terminating, bounded, so a
// cluster that never gets there fails the test rather than hanging it.
func waitForTerminating(t *testing.T, clientset kubernetes.Interface, ns string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, err := clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("reading namespace %q while waiting for Terminating: %v", ns, err)
		}
		if got.Status.Phase == corev1.NamespaceTerminating {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("namespace %q is still %s after 30s, want Terminating", ns, got.Status.Phase)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
