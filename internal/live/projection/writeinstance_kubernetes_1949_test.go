// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// GitHub issue #1949: #1946's version chain on record_store "kubernetes".
//
// client-go's fake clientset assigns no metadata.resourceVersion and checks
// none, so a [staterecord.KubernetesStore] over a bare fake proves nothing
// about versions. apiserverFake puts back the part of the API server's
// behaviour the chain depends on, for Secrets, in one reactor that handles
// every create, update and delete itself under one lock:
//
//   - Every write that changes an object stamps a resourceVersion from one
//     counter, so no two writes ever share one.
//   - Create of an existing name answers 409 AlreadyExists.
//   - Update of an absent name answers 404 NotFound. An update carrying a
//     resourceVersion other than the stored one answers 409 Conflict (the
//     apiserver's optimistic concurrency check in GuaranteedUpdate). An
//     update whose object is semantically equal to the stored one is a
//     no-op and returns the stored object at its UNCHANGED resourceVersion,
//     which is what the apiserver does when the new bytes equal the old.
//   - Delete with Preconditions.ResourceVersion other than the stored one
//     answers 409 Conflict; of an absent name, 404 NotFound.
//
// TestApiserverFakeEnforcesConcurrency proves each of those, so the tests
// below cannot pass against a fake that silently accepts a stale write.
type apiserverFake struct {
	cs *fake.Clientset

	mu     sync.Mutex
	nextRV int
	writes []k8stesting.Action // creates, updates and deletes that reached the store, in order
}

const fake1949Namespace = "tofu-records-1949"

func newApiserverFake(t *testing.T) *apiserverFake {
	t.Helper()
	f := &apiserverFake{cs: fake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fake1949Namespace}})}
	tracker := f.cs.Tracker()
	secrets := corev1.SchemeGroupVersion.WithResource("secrets")

	current := func(ns, name string) (*corev1.Secret, error) {
		obj, err := tracker.Get(secrets, ns, name)
		if err != nil {
			return nil, err
		}
		return obj.(*corev1.Secret), nil
	}
	stamp := func(s *corev1.Secret) {
		f.nextRV++
		s.ResourceVersion = strconv.Itoa(f.nextRV)
	}

	f.cs.PrependReactor("*", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		gr := secrets.GroupResource()
		ns := action.GetNamespace()
		// On the verb, not the action's type: an UpdateActionImpl satisfies
		// k8stesting.CreateAction too, since both are Action plus GetObject.
		switch action.GetVerb() {
		case "create":
			in := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret).DeepCopy()
			if _, err := current(ns, in.Name); err == nil {
				return true, nil, k8serrors.NewAlreadyExists(gr, in.Name)
			}
			stamp(in)
			if err := tracker.Create(secrets, in, ns); err != nil {
				return true, nil, err
			}
			f.writes = append(f.writes, action)
			return true, in.DeepCopy(), nil
		case "update":
			in := action.(k8stesting.UpdateAction).GetObject().(*corev1.Secret).DeepCopy()
			cur, err := current(ns, in.Name)
			if err != nil {
				return true, nil, err // the tracker's own 404
			}
			if in.ResourceVersion != "" && in.ResourceVersion != cur.ResourceVersion {
				return true, nil, k8serrors.NewConflict(gr, in.Name, errors.New("the object has been modified; please apply your changes to the latest version and try again"))
			}
			if apiequality.Semantic.DeepEqual(in.Labels, cur.Labels) &&
				apiequality.Semantic.DeepEqual(in.Annotations, cur.Annotations) &&
				apiequality.Semantic.DeepEqual(in.Data, cur.Data) &&
				in.Type == cur.Type {
				return true, cur.DeepCopy(), nil
			}
			stamp(in)
			if err := tracker.Update(secrets, in, ns); err != nil {
				return true, nil, err
			}
			f.writes = append(f.writes, action)
			return true, in.DeepCopy(), nil
		case "delete":
			a := action.(k8stesting.DeleteAction)
			cur, err := current(ns, a.GetName())
			if err != nil {
				return true, nil, err
			}
			if pre := a.GetDeleteOptions().Preconditions; pre != nil && pre.ResourceVersion != nil && *pre.ResourceVersion != cur.ResourceVersion {
				return true, nil, k8serrors.NewConflict(gr, a.GetName(), errors.New("Precondition failed: ResourceVersion in precondition does not match"))
			}
			if err := tracker.Delete(secrets, ns, a.GetName()); err != nil {
				return true, nil, err
			}
			f.writes = append(f.writes, action)
			return true, nil, nil
		}
		return false, nil, nil // get and list fall through to the tracker
	})
	return f
}

func (f *apiserverFake) store(t *testing.T) *staterecord.KubernetesStore {
	t.Helper()
	s, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:   f.cs.CoreV1().Secrets(fake1949Namespace),
		Namespace: fake1949Namespace,
		Estate:    "estate-1949",
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %s", err)
	}
	return s
}

// writesTo is every write that reached the Secret named name since reset.
func (f *apiserverFake) writesTo(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.writes {
		var n string
		if a.GetVerb() == "delete" {
			n = a.(k8stesting.DeleteAction).GetName()
		} else {
			n = a.(k8stesting.CreateAction).GetObject().(*corev1.Secret).Name
		}
		if n == name {
			out = append(out, a.GetVerb())
		}
	}
	return out
}

func (f *apiserverFake) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = nil
}

// TestApiserverFakeEnforcesConcurrency proves the fake refuses what the
// API server refuses. Without it the chain tests below could pass against a
// fake that accepts any version.
func TestApiserverFakeEnforcesConcurrency(t *testing.T) {
	ctx := context.Background()
	s := newApiserverFake(t).store(t)

	v1, err := s.PutIfAbsent(ctx, "k", []byte("a"))
	if err != nil || v1 == "" {
		t.Fatalf("create: version %q err %v, want a resourceVersion", v1, err)
	}
	var conflict *staterecord.VersionConflictError
	if _, err := s.PutIfAbsent(ctx, "k", []byte("b")); !errors.As(err, &conflict) {
		t.Errorf("a second create of k answered %v, want a version conflict", err)
	}
	v2, err := s.PutIfVersion(ctx, "k", []byte("b"), v1)
	if err != nil || v2 == v1 {
		t.Fatalf("update at the current version: %q -> %q err %v, want a new resourceVersion", v1, v2, err)
	}
	if _, err := s.PutIfVersion(ctx, "k", []byte("c"), v1); !errors.As(err, &conflict) || conflict.ActualVersion != v2 {
		t.Errorf("update at stale %s answered %v, want a conflict naming %s", v1, err, v2)
	}
	if v, err := s.PutIfVersion(ctx, "k", []byte("b"), v2); err != nil || v != v2 {
		t.Errorf("a no-op update at %s answered %q err %v, want the unchanged %s", v2, v, err, v2)
	}
	if err := s.Delete(ctx, "k", v1); !errors.As(err, &conflict) {
		t.Errorf("delete at stale %s answered %v, want a version conflict", v1, err)
	}
	if err := s.Delete(ctx, "k", v2); err != nil {
		t.Fatalf("delete at the current version: %v", err)
	}
	if _, err := s.PutIfVersion(ctx, "k", []byte("d"), v2); !errors.As(err, &conflict) {
		t.Errorf("update of a deleted record answered %v, want a version conflict", err)
	}
}

// TestKubernetesMidApplyWriteIsTheOnlyWrite is #1949's first half, read off
// the API calls rather than off the Store interface: the mid-apply write is
// the one write that reaches left's Secret, and the final pass - which
// passes the plan's resourceVersion - sends nothing and leaves the record at
// the mid-apply write's resourceVersion.
func TestKubernetesMidApplyWriteIsTheOnlyWrite(t *testing.T) {
	f := newApiserverFake(t)
	store := f.store(t)
	e := newEstate1938(t, store)
	e.seed("left-0", "right-0")
	_, planned := e.recorded(e.left)
	r := e.plan()
	f.reset()

	final := e.finalState("left-1", "right-0")
	assertNoErrors(t, e.writeInstance(r, e.left, only(final, e.left)))
	_, mid := e.recorded(e.left)
	if mid == planned {
		t.Fatalf("the mid-apply write left the resourceVersion at the plan's %s", planned)
	}
	assertNoErrors(t, e.writeBack(r, final))

	name := store.SecretName(RecordKey(estate1938Prefix, e.left))
	if got := f.writesTo(name); len(got) != 1 || got[0] != "update" {
		t.Errorf("writes to left's Secret %s across the mid-apply write and the final pass: %v, want exactly [update]", name, got)
	}
	if got := f.writesTo(store.SecretName(RecordKey(estate1938Prefix, e.right))); len(got) != 0 {
		t.Errorf("right was unchanged and its Secret saw writes %v", got)
	}
	if id, v := e.recorded(e.left); id != "left-1" || v != mid {
		t.Errorf("left's record is %s at resourceVersion %s, want left-1 at the mid-apply write's %s", id, v, mid)
	}
	list, err := f.cs.CoreV1().Secrets(fake1949Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range list.Items {
		if s.Name == name {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d Secrets named %s, want one", n, name)
	}
}

// TestKubernetesMidApplyStillConflictsWithAnotherRun is #1949's second
// half: the chain follows only resourceVersions this run produced. Another
// run's write after this run's mid-apply write makes this run's final pass
// lose by name, and the Secret keeps the other run's record and version.
func TestKubernetesMidApplyStillConflictsWithAnotherRun(t *testing.T) {
	f := newApiserverFake(t)
	e := newEstate1938(t, f.store(t))
	e.seed("left-0", "right-0")
	a := e.plan()
	assertNoErrors(t, e.writeInstance(a, e.left, only(e.finalState("left-a1", "right-0"), e.left)))

	b := e.plan()
	assertNoErrors(t, e.writeBack(b, e.finalState("left-b", "right-0")))
	_, bVersion := e.recorded(e.left)

	diags := e.writeBack(a, e.finalState("left-a2", "right-0"))
	if got := conflictCount(diags); got != 1 {
		t.Fatalf("a's final pass reported %d write conflicts, want 1:\n%s", got, renderDiags(diags))
	}
	if id, v := e.recorded(e.left); id != "left-b" || v != bVersion {
		t.Errorf("left's record is %s at %s, want left-b at %s: the losing run overwrote it", id, v, bVersion)
	}
}
