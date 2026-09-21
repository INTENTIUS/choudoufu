// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// TestOpeningAStoreWithNoNamespaceIsARefusal drives the production open
// sequence against a cluster whose records namespace is not there, which is
// the shape GitHub issue #1448's A2 is about.
//
// The store raises [staterecord.NamespaceMissingError] by name, with the
// kubectl line that creates the namespace. provisionStoreSentinel wraps that
// in a plain error, and before #1448 [IsStoreRefusal] read the wrapping and
// not the error: `live-plan` and `live-mv` then treated a namespace that will
// never come back on its own as an outage and went on without the store, which
// is planning an estate whose records are all invisible.
func TestOpeningAStoreWithNoNamespaceIsARefusal(t *testing.T) {
	cs := fake.NewClientset()
	// What the API server answers a create in a namespace that does not
	// exist: a 404 naming the NAMESPACE. Measured on kind (#1448).
	cs.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, k8serrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, contractNamespace)
	})
	store, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(contractNamespace),
		Clientset: cs,
		Namespace: contractNamespace,
		Estate:    contractEstate,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}

	_, err = openBuiltStore(context.Background(), store, kubernetesRecordStore(), contractEstate)
	if err == nil {
		t.Fatal("opening a store whose namespace does not exist reported success")
	}
	var absent *staterecord.NamespaceMissingError
	if !errors.As(err, &absent) {
		t.Fatalf("opening the store: %v (%T), want the store's own *NamespaceMissingError", err, err)
	}
	if !IsStoreRefusal(err) {
		t.Errorf("a namespace that does not exist is read as an outage, so live-plan and live-mv go on without the store: %v", err)
	}
	if !strings.Contains(err.Error(), "kubectl create namespace "+contractNamespace) {
		t.Errorf("the refusal reaching the caller does not carry the kubectl line: %v", err)
	}
}

// TestOpeningAStoreWithATerminatingNamespaceIsARefusal is the other arm. A
// namespace being deleted refuses a write with 403, which [staterecord.IsAccessDenied]
// reads as this identity lacking permission - #1370's reader tolerance - and
// the run is then carried as far as the List. The List is what stops it: the
// namespace is asked about by phase, and the refusal says so by name.
func TestOpeningAStoreWithATerminatingNamespaceIsARefusal(t *testing.T) {
	cs := fake.NewClientset(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: contractNamespace},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating},
	})
	cs.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		err := k8serrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "tofu-record-x",
			errors.New("unable to create new content in namespace "+contractNamespace+" because it is being terminated"))
		err.ErrStatus.Details.Causes = []metav1.StatusCause{{
			Type:  corev1.NamespaceTerminatingCause,
			Field: "metadata.namespace",
		}}
		return true, nil, err
	})
	store, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(contractNamespace),
		Clientset: cs,
		Namespace: contractNamespace,
		Estate:    contractEstate,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}

	_, err = openBuiltStore(context.Background(), store, kubernetesRecordStore(), contractEstate)
	var terminating *staterecord.NamespaceTerminatingError
	if !errors.As(err, &terminating) {
		t.Fatalf("opening a store whose namespace is terminating: %v (%T), want *NamespaceTerminatingError", err, err)
	}
	if !IsStoreRefusal(err) {
		t.Errorf("a namespace that is being deleted is read as an outage: %v", err)
	}
}

// TestOpeningAStoreWithAnUnlabelledRecordIsARefusal is where A1 actually
// stops a run. The handshake's List runs under the trip counter and above the
// run cache, on every run and before any plan is built, so a record object the
// listing will not read past refuses the run there rather than after a plan
// has been shown.
func TestOpeningAStoreWithAnUnlabelledRecordIsARefusal(t *testing.T) {
	cs := fake.NewClientset(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: contractNamespace},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	})
	store, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(contractNamespace),
		Clientset: cs,
		Namespace: contractNamespace,
		Estate:    contractEstate,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	prefix := RecordStoreKeyPrefix(kubernetesRecordStore(), contractEstate)
	key := prefix + "aws_thing/one"
	if _, err := store.PutIfAbsent(context.Background(), key, []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	// The label goes, the way `kubectl label secret NAME tofu-estate-` takes
	// it: the record is still there and no listing used to carry it.
	secrets := cs.CoreV1().Secrets(contractNamespace)
	secret, err := secrets.Get(context.Background(), store.SecretName(key), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	delete(secret.Labels, staterecord.KubernetesEstateLabel)
	if _, err := secrets.Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	_, err = openBuiltStore(context.Background(), store, kubernetesRecordStore(), contractEstate)
	var unlabelled *staterecord.UnlabelledRecordError
	if !errors.As(err, &unlabelled) {
		t.Fatalf("opening a store holding a record that lost its label: %v (%T), want *UnlabelledRecordError", err, err)
	}
	if !IsStoreRefusal(err) {
		t.Errorf("that is read as an outage, so live-plan and live-mv plan the estate without the record: %v", err)
	}
}

// TestTheStoresOwnFaultsAreRefusals covers the rest of what the record store
// raises by name and by type. Each one is the store reporting something it
// found and will not read past, so each is a refusal wherever it was wrapped -
// none of them is fixed by retrying, and every one of them makes an estate
// read as having fewer records than it has.
func TestTheStoresOwnFaultsAreRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"a namespace that does not exist", &staterecord.NamespaceMissingError{Namespace: contractNamespace}},
		{"a namespace being deleted", &staterecord.NamespaceTerminatingError{Namespace: contractNamespace}},
		{"a Secret holding another key", &staterecord.KeyCollisionError{Key: "k", SecretName: "tofu-record-a", FoundKey: "other"}},
		{"a record that lost its labels", &staterecord.UnlabelledRecordError{Namespace: contractNamespace, SecretName: "tofu-record-a", Key: "k", Missing: []string{"tofu-estate=" + contractEstate}}},
		{"a record under the wrong name", &staterecord.MisnamedRecordError{Namespace: contractNamespace, SecretName: "renamed", Key: "k", WantName: "tofu-record-a"}},
		{"two Secrets holding one key", &staterecord.DuplicateRecordKeyError{Namespace: contractNamespace, Key: "k", SecretNames: []string{"a", "b"}, WantName: "a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Through the wrapping, because nothing reaches IsStoreRefusal
			// raw: provisionStoreSentinel wraps both the write and the List.
			wrapped := fmt.Errorf("record_store: reading the sentinel back through List: %w", tc.err)
			if !IsStoreRefusal(wrapped) {
				t.Errorf("%v is read as an outage, so live-plan and live-mv go on without the store", wrapped)
			}
		})
	}

	// And an outage stays one, so this cannot be a check that calls
	// everything a refusal: a cluster that could not be reached is exactly
	// what the two commands are allowed to carry on past.
	unreachable := fmt.Errorf("record_store: provisioning the sentinel at %q: %w",
		SentinelKey(RecordStoreKeyPrefix(kubernetesRecordStore(), contractEstate)),
		errors.New("dial tcp 10.0.0.1:6443: i/o timeout"))
	if IsStoreRefusal(unreachable) {
		t.Errorf("a cluster that could not be reached is read as a refusal: %v", unreachable)
	}
}
