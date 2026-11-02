// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// The cases from GitHub issue #1448 section A that can be measured without a
// cluster. The ones that cannot - a namespace that is really missing or really
// terminating, a LIST the API server really narrowed - are in
// kubernetes_absent_live_test.go and run on kind.

// fakeCluster is a fake clientset with the records namespace present and
// Active, so that a store built over it probes a namespace that is there and
// the case under test is the only thing answering.
func fakeCluster(t *testing.T, objects ...runtime.Object) *fake.Clientset {
	t.Helper()
	base := []runtime.Object{&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: fakeRecordNamespace},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}}
	return fake.NewClientset(append(base, objects...)...)
}

func fakeClusterStore(t *testing.T, cs *fake.Clientset, estate string) *KubernetesStore {
	t.Helper()
	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(fakeRecordNamespace),
		Clientset: cs,
		Namespace: fakeRecordNamespace,
		Estate:    estate,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	return store
}

// TestKubernetesGetReadsANamespaceNotFoundFromTheSecretRead pins mutation M2
// from the audit: deleting the notFoundIsNamespace leg from Get went unnoticed
// by every test there was.
//
// The namespace here EXISTS as far as the probe is concerned, so the probe
// cannot answer for the leg: the only thing that can turn this 404 into a
// refusal is Get reading what the API server named in Status.Details.Kind.
// That is also the shape a create answers with, which is where this 404 comes
// from in production - a Get racing a namespace delete on a cluster that
// answers the older way.
func TestKubernetesGetReadsANamespaceNotFoundFromTheSecretRead(t *testing.T) {
	cs := fakeCluster(t)
	cs.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, fakeRecordNamespace)
	})
	store := fakeClusterStore(t, cs, "alice")

	_, _, exists, err := store.Get(context.Background(), "tofu-records/alice/aws_thing/one")
	var absent *NamespaceMissingError
	if !errors.As(err, &absent) {
		t.Fatalf("Get whose 404 named the namespace: exists=%v err=%v (%T), want *NamespaceMissingError", exists, err, err)
	}
	if absent.Namespace != fakeRecordNamespace {
		t.Errorf("the refusal names namespace %q, want %q", absent.Namespace, fakeRecordNamespace)
	}
}

// TestKubernetesGetRefusesWhenTheNamespaceIsGone is the read path's half of
// A2, over a cluster where the namespace is not there at all. The Secret's own
// 404 names the SECRET (measured on kind), so nothing about the answer says
// the namespace is missing and the store has to ask.
func TestKubernetesGetRefusesWhenTheNamespaceIsGone(t *testing.T) {
	cs := fake.NewClientset()
	store := fakeClusterStore(t, cs, "alice")
	ctx := context.Background()

	_, _, exists, err := store.Get(ctx, "tofu-records/alice/aws_thing/one")
	var absent *NamespaceMissingError
	if !errors.As(err, &absent) {
		t.Errorf("Get in a missing namespace: exists=%v err=%v (%T), want *NamespaceMissingError", exists, err, err)
	}
	if keys, err := store.List(ctx, "tofu-records/alice/"); !errors.As(err, &absent) {
		t.Errorf("List in a missing namespace: keys=%v err=%v (%T), want *NamespaceMissingError", keys, err, err)
	}
	if err := store.Delete(ctx, "tofu-records/alice/aws_thing/one", ""); !errors.As(err, &absent) {
		t.Errorf("Delete(key, \"\") in a missing namespace: %v (%T), want *NamespaceMissingError", err, err)
	}
}

// TestKubernetesRefusesATerminatingNamespaceByPhase is the other half of A2
// over a namespace that exists and is on its way out. A read of a terminating
// namespace succeeds and returns less and less, so the phase is the only thing
// that separates "this estate has no records" from "this estate's records are
// being deleted".
func TestKubernetesRefusesATerminatingNamespaceByPhase(t *testing.T) {
	cs := fake.NewClientset(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: fakeRecordNamespace},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating},
	})
	store := fakeClusterStore(t, cs, "alice")
	ctx := context.Background()

	var terminating *NamespaceTerminatingError
	if keys, err := store.List(ctx, "tofu-records/alice/"); !errors.As(err, &terminating) {
		t.Errorf("List in a terminating namespace: keys=%v err=%v (%T), want *NamespaceTerminatingError", keys, err, err)
	}
	if _, _, _, err := store.Get(ctx, "tofu-records/alice/aws_thing/one"); !errors.As(err, &terminating) {
		t.Errorf("Get in a terminating namespace: %v (%T), want *NamespaceTerminatingError", err, err)
	}
	if !strings.Contains(terminating.Error(), "kubectl create namespace "+fakeRecordNamespace) {
		t.Errorf("the refusal does not say how the namespace comes back: %v", terminating)
	}
}

// TestKubernetesTerminatingWriteIsNotADenial pins the classification a
// terminating namespace's 403 gets. The API server refuses a create into one
// with `403 ... is forbidden: unable to create new content in namespace X
// because it is being terminated`, and [IsAccessDenied] reads a bare 403 as
// this run's identity being refused - which is #1370's reader tolerance, and
// carrying a run past a namespace whose records are being deleted is not what
// that tolerance is for.
func TestKubernetesTerminatingWriteIsNotADenial(t *testing.T) {
	cs := fakeCluster(t)
	cs.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		err := k8serrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "tofu-record-x",
			errors.New("unable to create new content in namespace "+fakeRecordNamespace+" because it is being terminated"))
		err.ErrStatus.Details.Causes = []metav1.StatusCause{{
			Type:    corev1.NamespaceTerminatingCause,
			Message: "namespace " + fakeRecordNamespace + " is being terminated",
			Field:   "metadata.namespace",
		}}
		return true, nil, err
	})
	store := fakeClusterStore(t, cs, "alice")

	_, err := store.PutIfAbsent(context.Background(), "tofu-records/alice/aws_thing/one", []byte("v"))
	var terminating *NamespaceTerminatingError
	if !errors.As(err, &terminating) {
		t.Fatalf("PutIfAbsent into a terminating namespace: %v (%T), want *NamespaceTerminatingError", err, err)
	}
}

// TestKubernetesAsksAboutTheNamespaceOnceWhenItMayNot is the honest gap. The
// Role this fork recommends holds no cluster-scoped get on namespaces, so the
// question a read needs answered cannot always be asked. When it cannot, the
// absent answer stands - and the question is asked once per store, not once
// per read.
func TestKubernetesAsksAboutTheNamespaceOnceWhenItMayNot(t *testing.T) {
	cs := fakeCluster(t)
	var asked atomic.Int64
	cs.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		asked.Add(1)
		return true, nil, k8serrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, fakeRecordNamespace,
			errors.New(`User "system:serviceaccount:default:planner" cannot get resource "namespaces" in API group "" at the cluster scope`))
	})
	store := fakeClusterStore(t, cs, "alice")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, _, exists, err := store.Get(ctx, "tofu-records/alice/aws_thing/one"); err != nil || exists {
			t.Fatalf("Get when the namespace may not be asked about: exists=%v err=%v, want the absent answer to stand", exists, err)
		}
		if keys, err := store.List(ctx, "tofu-records/alice/"); err != nil || len(keys) != 0 {
			t.Fatalf("List when the namespace may not be asked about: keys=%v err=%v", keys, err)
		}
	}
	if got := asked.Load(); got != 1 {
		t.Errorf("the namespace was asked about %d times, want exactly 1: a permission does not change inside a run", got)
	}
}

// TestKubernetesListRefusesARecordMissingItsLabels is A1 without a cluster.
// The live test is the one that proves the listing itself no longer narrows
// server-side; this one pins what each combination of labels produces, which
// is cheaper to cover here.
func TestKubernetesListRefusesARecordMissingItsLabels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "no labels at all",
			labels: map[string]string{},
			want:   "app.kubernetes.io/managed-by and tofu-estate",
		},
		{
			name:   "the estate label stripped",
			labels: map[string]string{KubernetesManagedByLabel: KubernetesManagedByValue},
			want:   "tofu-estate",
		},
		{
			name:   "the managed-by label stripped",
			labels: map[string]string{KubernetesEstateLabel: "alice"},
			want:   "app.kubernetes.io/managed-by",
		},
		{
			name:   "managed-by holding another value",
			labels: map[string]string{KubernetesManagedByLabel: "helm", KubernetesEstateLabel: "alice"},
			want:   "app.kubernetes.io/managed-by",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := fakeCluster(t)
			store := fakeClusterStore(t, cs, "alice")
			ctx := context.Background()
			const key = "tofu-records/alice/aws_thing/one"
			if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
				t.Fatalf("PutIfAbsent: %v", err)
			}
			setLabels(t, cs, store.SecretName(key), tc.labels)

			keys, err := store.List(ctx, "tofu-records/alice/")
			var unlabelled *UnlabelledRecordError
			if !errors.As(err, &unlabelled) {
				t.Fatalf("List: keys=%v err=%v (%T), want *UnlabelledRecordError", keys, err, err)
			}
			if keys != nil {
				t.Errorf("List returned %v alongside the refusal, want no listing at all", keys)
			}
			if !strings.Contains(unlabelled.Error(), tc.want) {
				t.Errorf("the refusal does not name %q: %v", tc.want, unlabelled)
			}
			if !strings.Contains(unlabelled.Error(), "kubectl -n "+fakeRecordNamespace+" label secret "+store.SecretName(key)+" --overwrite") {
				t.Errorf("the refusal does not carry the kubectl line that puts the labels back: %v", unlabelled)
			}
			if _, err := store.GetAll(ctx, "tofu-records/alice/"); !errors.As(err, &unlabelled) {
				t.Errorf("GetAll: %v (%T), want the same refusal", err, err)
			}
		})
	}
}

// TestKubernetesListSkipsWhatIsNotThisStoresRecord is the other side of A1:
// the refusal must not swallow the namespace. A listing with no label selector
// sees everything in it, and what is not this estate's record is skipped as it
// was before - including an object that carries the key annotation and neither
// this store's labels nor a record's name, which nothing here can tell from
// someone else's Secret.
func TestKubernetesListSkipsWhatIsNotThisStoresRecord(t *testing.T) {
	cs := fakeCluster(t)
	store := fakeClusterStore(t, cs, "alice")
	other := fakeClusterStore(t, cs, "bob")
	ctx := context.Background()

	if _, err := store.PutIfAbsent(ctx, "tofu-records/alice/aws_thing/mine", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if _, err := other.PutIfAbsent(ctx, "tofu-records/bob/aws_thing/theirs", []byte("v")); err != nil {
		t.Fatal(err)
	}
	secrets := cs.CoreV1().Secrets(fakeRecordNamespace)
	for _, secret := range []*corev1.Secret{
		{ObjectMeta: metav1.ObjectMeta{Name: "someone-elses-secret", Namespace: fakeRecordNamespace}},
		{ObjectMeta: metav1.ObjectMeta{
			Name:        "not-named-like-a-record",
			Namespace:   fakeRecordNamespace,
			Annotations: map[string]string{KubernetesRecordKeyAnnotation: "tofu-records/alice/aws_thing/claimed"},
		}},
		{ObjectMeta: metav1.ObjectMeta{
			Name:      "tofu-record-labelled-but-no-key",
			Namespace: fakeRecordNamespace,
			Labels: map[string]string{
				KubernetesManagedByLabel: KubernetesManagedByValue,
				KubernetesEstateLabel:    "alice",
			},
		}},
	} {
		if _, err := secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	keys, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0] != "tofu-records/alice/aws_thing/mine" {
		t.Errorf("List = %v, want only this estate's one record", keys)
	}
}

// TestKubernetesListRefusesTwoSecretsHoldingOneKey is A4 without a cluster.
// The refusal names both objects and which of them the key hashes to, because
// the copy is the one to delete and a listing that picked one silently is how
// a run ends up using whichever payload the cluster returned last.
func TestKubernetesListRefusesTwoSecretsHoldingOneKey(t *testing.T) {
	cs := fakeCluster(t)
	store := fakeClusterStore(t, cs, "alice")
	ctx := context.Background()
	const key = "tofu-records/alice/aws_thing/one"
	if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	want := store.SecretName(key)
	secrets := cs.CoreV1().Secrets(fakeRecordNamespace)
	original, err := secrets.Get(ctx, want, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const copyName = "tofu-record-copied-by-hand"
	if _, err := secrets.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        copyName,
			Namespace:   fakeRecordNamespace,
			Labels:      original.Labels,
			Annotations: original.Annotations,
		},
		Data: original.Data,
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	keys, err := store.List(ctx, "tofu-records/alice/")
	var duplicate *DuplicateRecordKeyError
	if !errors.As(err, &duplicate) {
		t.Fatalf("List: keys=%v err=%v (%T), want *DuplicateRecordKeyError", keys, err, err)
	}
	if duplicate.WantName != want {
		t.Errorf("the refusal says the key hashes to %q, want %q", duplicate.WantName, want)
	}
	text := duplicate.Error()
	if !strings.Contains(text, want) || !strings.Contains(text, copyName) {
		t.Errorf("the refusal does not name both Secrets: %s", text)
	}
	if !strings.Contains(text, "kubectl -n "+fakeRecordNamespace+" delete secret "+copyName) {
		t.Errorf("the refusal does not say how to delete the copy, or offers to delete the record: %s", text)
	}
}

// TestMisnamedRecordErrorSendsTheLongCommandToTheDocs pins where the A3
// refusal stops. Moving a record Secret to the name its key hashes to is a
// get, an edit of three metadata fields and a create, which is a jq pipeline;
// a reader meeting it in an error message has to parse it under pressure and
// may not have jq installed at all. So the message carries the one command
// that is short and total - delete the copy - and sends the other to
// live/STORAGE.md, where it can be read beside what it does.
func TestMisnamedRecordErrorSendsTheLongCommandToTheDocs(t *testing.T) {
	err := &MisnamedRecordError{
		Namespace:  fakeRecordNamespace,
		SecretName: "tofu-record-copied-by-hand",
		Key:        "tofu-records/alice/aws_thing/one",
		WantName:   "tofu-record-69111df5",
	}
	got := err.Error()
	if strings.Contains(got, "jq") {
		t.Errorf("the refusal carries a jq pipeline, which belongs in live/STORAGE.md: %s", got)
	}
	if !strings.Contains(got, "live/STORAGE.md") {
		t.Errorf("the refusal does not say where the command that moves the record is: %s", got)
	}
	want := "If it is a copy, delete it with `kubectl -n " + fakeRecordNamespace + " delete secret tofu-record-copied-by-hand`."
	if !strings.Contains(got, want) {
		t.Errorf("the refusal does not carry %q: %s", want, got)
	}
}

// setLabels replaces a Secret's labels wholesale, which is what a restore that
// dropped them or a `kubectl label secret NAME key-` leaves behind.
func setLabels(t *testing.T, cs *fake.Clientset, name string, labels map[string]string) {
	t.Helper()
	ctx := context.Background()
	secrets := cs.CoreV1().Secrets(fakeRecordNamespace)
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading Secret %q: %v", name, err)
	}
	secret.Labels = labels
	if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("relabelling Secret %q: %v", name, err)
	}
}
