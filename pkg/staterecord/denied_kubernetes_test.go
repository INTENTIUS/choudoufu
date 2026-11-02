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

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// TestIsAccessDeniedRecognisesAKubernetesForbidden is GitHub issue #1370's
// reader tolerance reaching this store, and it did not before #1393.
//
// The tolerance in internal/live/projection's provisionStoreSentinel is gated
// on [IsAccessDenied]. A Kubernetes 403 is a *k8serrors.StatusError and
// carries none of the smithy shapes or the fs.ErrPermission that function
// knew, so a CI plan identity with get and list on the records namespace and
// no create had its sentinel write read as an outage, and the plan stopped
// with "Cannot open the record store". Measured on kind before this test
// existed, and quoted in the pull request.
//
// The error has to be recognised THROUGH the store's own wrapping, which is
// what the second case is for: nothing reaches IsAccessDenied raw.
func TestIsAccessDeniedRecognisesAKubernetesForbidden(t *testing.T) {
	forbidden := k8serrors.NewForbidden(
		schema.GroupResource{Resource: "secrets"}, "tofu-record-abc",
		errors.New(`User "system:serviceaccount:default:planner" cannot create resource "secrets" in API group "" in the namespace "tofu-records-alice"`))

	if !IsAccessDenied(forbidden) {
		t.Error("a bare Kubernetes Forbidden is not recognised as this identity being refused permission")
	}

	store := newFakeKubernetesStore(t, "alice")
	wrapped := store.classify("creating", "tofu-records/alice/x", forbidden)
	if !IsAccessDenied(wrapped) {
		t.Errorf("a Forbidden wrapped by the store's own classify is not recognised: %v", wrapped)
	}
	if !strings.Contains(wrapped.Error(), "forbidden") {
		t.Errorf("the wrapping lost the API server's own words: %v", wrapped)
	}

	// The other refusals keep their meaning. A 404 is not a denial, and a
	// namespace that is not there is its own named refusal.
	notFound := k8serrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "tofu-record-abc")
	if IsAccessDenied(notFound) {
		t.Error("a Kubernetes NotFound is read as a permission denial")
	}
}

// TestKubernetesStoreSurfacesAForbiddenCreateAsADenial drives the whole path
// a plan takes: the store's own PutIfAbsent against a client that answers 403
// on create, and the error it produces has to satisfy IsAccessDenied. This is
// what internal/live/projection branches on, so a store that wrapped the 403
// in something opaque would put the plan back where it was.
func TestKubernetesStoreSurfacesAForbiddenCreateAsADenial(t *testing.T) {
	cs := fake.NewClientset()
	cs.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, k8serrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "",
			fmt.Errorf(`User "system:serviceaccount:default:planner" cannot create resource "secrets" in API group "" in the namespace %q`, fakeRecordNamespace))
	})
	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(fakeRecordNamespace),
		Clientset: cs,
		Namespace: fakeRecordNamespace,
		Estate:    "alice",
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}

	_, err = store.PutIfAbsent(context.Background(), "tofu-records/alice/.store-sentinel", []byte("x"))
	if err == nil {
		t.Fatal("a create the API server forbids reported success")
	}
	if !IsAccessDenied(err) {
		t.Fatalf("a forbidden create is not a denial from the caller's side, so a read-only plan cannot be carried past it: %v", err)
	}
	var conflict *VersionConflictError
	if errors.As(err, &conflict) {
		t.Error("a forbidden create was read as a version conflict; the sentinel would then look provisioned")
	}
}
