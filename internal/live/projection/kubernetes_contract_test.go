// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

const (
	contractEstate    = "k8scontract"
	contractNamespace = "tofu-records-" + contractEstate
)

// clusterFake is a fake clientset standing in for a cluster that satisfies
// the whole contract, with two counters over the calls the contract makes.
//
// It has one deliberate departure from client-go's default fake, and the
// departure is the point of the whole file: the fake's object tracker assigns
// no metadata.resourceVersion at all, so a Create answers with "" and
// [staterecord.KubernetesStore.PutIfAbsent] returns "" as the created
// version. That "" is exactly the signal [openBuiltStore] reads as "this run
// did not create the sentinel", so without the reactor below the first-contact
// assertion would never fire and every case here would pass vacuously. The
// conformance suite says the same thing about versions and runs against a
// real API server for it (kubernetes_live_test.go).
type clusterFake struct {
	*fake.Clientset

	mu      sync.Mutex
	reviews int
}

func newClusterFake(t *testing.T, objects ...runtime.Object) *clusterFake {
	t.Helper()
	base := []runtime.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: contractNamespace}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-apiserver-control-plane",
				Namespace: "kube-system",
				Labels:    map[string]string{"component": "kube-apiserver"},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:    "kube-apiserver",
				Command: []string{"kube-apiserver", "--encryption-provider-config=/etc/kubernetes/enc/enc.yaml"},
			}}},
		},
		&admissionv1.ValidatingAdmissionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: staterecord.EstateBoundaryPolicyName, Generation: 1},
			Status:     admissionv1.ValidatingAdmissionPolicyStatus{ObservedGeneration: 1},
		},
		&admissionv1.ValidatingAdmissionPolicyBinding{
			ObjectMeta: metav1.ObjectMeta{Name: staterecord.EstateBoundaryPolicyName},
			Spec: admissionv1.ValidatingAdmissionPolicyBindingSpec{
				PolicyName:        staterecord.EstateBoundaryPolicyName,
				ValidationActions: []admissionv1.ValidationAction{admissionv1.Deny},
			},
		},
	}
	cs := &clusterFake{Clientset: fake.NewClientset(append(base, objects...)...)}

	// The resourceVersion the tracker will not assign. See the type comment.
	var rv int
	cs.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		secret, ok := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret)
		if !ok {
			return false, nil, nil
		}
		cs.mu.Lock()
		rv++
		secret.ResourceVersion = strconv.Itoa(rv)
		cs.mu.Unlock()
		return false, nil, nil
	})

	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		review, ok := action.(k8stesting.CreateAction).GetObject().(*authzv1.SelfSubjectAccessReview)
		if !ok {
			return false, nil, nil
		}
		cs.mu.Lock()
		cs.reviews++
		cs.mu.Unlock()
		out := review.DeepCopy()
		// Scoped to its own records namespace, which is what the docs
		// recommend and what read_isolation wants to see.
		out.Status.Allowed = review.Spec.ResourceAttributes.Namespace == contractNamespace
		return true, out, nil
	})
	return cs
}

func (c *clusterFake) reviewCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reviews
}

// openCluster runs the production open sequence over cs, the way
// NewRecordStore does for a real cluster: the store, then the trip counter,
// the sentinel handshake, the first-contact contract and the run cache.
func openCluster(t *testing.T, cs *clusterFake, rs *configs.LiveRecordStore) error {
	t.Helper()
	store, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(contractNamespace),
		Clientset: cs,
		Namespace: contractNamespace,
		Estate:    contractEstate,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	_, err = openBuiltStore(context.Background(), store, rs, contractEstate)
	return err
}

func kubernetesRecordStore(waived ...string) *configs.LiveRecordStore {
	return &configs.LiveRecordStore{
		Type:          "kubernetes",
		Namespace:     contractNamespace,
		NamespaceSet:  true,
		AllowInsecure: waived,
	}
}

// TestClusterContractRunsOnFirstContactAndNotOnEveryPlan is GitHub issue
// #1393's second Accept item, measured rather than asserted: the count of
// SelfSubjectAccessReviews the run makes.
//
// The first open creates the sentinel and is the estate's first contact with
// this cluster, so the contract runs. Every later open finds the sentinel
// already there, which is the signal that some earlier run already asked, so
// the contract does not run and the count does not move. That is #1339's
// ruling carried onto this store: the four properties are facts about the
// cluster and do not change between two plans.
func TestClusterContractRunsOnFirstContactAndNotOnEveryPlan(t *testing.T) {
	cs := newClusterFake(t)
	rs := kubernetesRecordStore()

	if err := openCluster(t, cs, rs); err != nil {
		t.Fatalf("first contact: %v", err)
	}
	first := cs.reviewCount()
	if first == 0 {
		t.Fatal("first contact made no SelfSubjectAccessReview, so the contract did not run and every count below would be meaningless")
	}

	for i := 2; i <= 3; i++ {
		if err := openCluster(t, cs, rs); err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if got := cs.reviewCount(); got != first {
			t.Fatalf("open %d made %d reviews in total, first contact alone made %d; the contract is running on every plan", i, got, first)
		}
	}
}

// TestClusterContractRefusalTakesTheSentinelBackOut is the other half of the
// first-contact rule. A refused first contact must leave the store as it
// found it, or the second plan proceeds against the cluster the first one
// refused - and it will never be a first contact again.
func TestClusterContractRefusalTakesTheSentinelBackOut(t *testing.T) {
	cs := newClusterFake(t)
	// Take the estate boundary policy away, which is a failure and not a
	// permission problem.
	if err := cs.AdmissionregistrationV1().ValidatingAdmissionPolicies().Delete(context.Background(), staterecord.EstateBoundaryPolicyName, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("removing the policy: %v", err)
	}
	rs := kubernetesRecordStore()

	err := openCluster(t, cs, rs)
	if err == nil {
		t.Fatal("first contact with a cluster that has no estate boundary policy was not refused")
	}
	if !IsStoreRefusal(err) {
		t.Errorf("the refusal is an outage, so live-plan would carry on past it (#1376): %v", err)
	}
	if !strings.Contains(err.Error(), "estate_boundary") {
		t.Errorf("the refusal does not name the assertion: %v", err)
	}
	if !strings.Contains(err.Error(), contractNamespace) {
		t.Errorf("the refusal does not name the records namespace: %v", err)
	}

	secrets, listErr := cs.CoreV1().Secrets(contractNamespace).List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("listing the records namespace: %v", listErr)
	}
	if len(secrets.Items) != 0 {
		t.Errorf("the refused first contact left %d Secret(s) behind; the next run would not be a first contact and would proceed against the cluster this one refused", len(secrets.Items))
	}

	// And the next run refuses again, for the same reason.
	if err := openCluster(t, cs, rs); err == nil {
		t.Error("the run after a refused first contact was not refused")
	}
}

// TestClusterContractWaiverLetsTheRunProceed is #1340's shape on this
// contract: a waiver reaches exactly what it names, and the run goes on.
func TestClusterContractWaiverLetsTheRunProceed(t *testing.T) {
	cs := newClusterFake(t)
	if err := cs.AdmissionregistrationV1().ValidatingAdmissionPolicies().Delete(context.Background(), staterecord.EstateBoundaryPolicyName, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("removing the policy: %v", err)
	}

	if err := openCluster(t, cs, kubernetesRecordStore("estate_boundary")); err != nil {
		t.Fatalf("a run that waives the failing assertion was still refused: %v", err)
	}

	// A waiver of something else does not reach it.
	cs2 := newClusterFake(t)
	if err := cs2.AdmissionregistrationV1().ValidatingAdmissionPolicies().Delete(context.Background(), staterecord.EstateBoundaryPolicyName, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("removing the policy: %v", err)
	}
	if err := openCluster(t, cs2, kubernetesRecordStore("encryption_at_rest")); err == nil {
		t.Error("waiving encryption_at_rest also waived estate_boundary")
	}
}

// TestAPlanIsNotRefusedForLackingWriteVerbs is GitHub issue #1393's third
// Accept item and #1370's lesson on this store, driven through the
// production open path.
//
// The identity may get and list Secrets and may not create one. An earlier
// writing run left the sentinel behind, which is what makes this survivable:
// the sentinel's presence is #693's whole proof, and nothing about this run
// being unable to repeat it makes the store less sound. The run goes on,
// read-only in effect, and - measured here - the contract does NOT run,
// because createdVersion is "" and this is not a first contact.
func TestAPlanIsNotRefusedForLackingWriteVerbs(t *testing.T) {
	cs := newClusterFake(t)
	rs := kubernetesRecordStore()

	// A writing run first, so the sentinel is there.
	if err := openCluster(t, cs, rs); err != nil {
		t.Fatalf("the writing run: %v", err)
	}
	afterWrite := cs.reviewCount()

	// Now the plan identity: create is forbidden, everything else stands.
	cs.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, k8serrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "",
			nil)
	})

	if err := openCluster(t, cs, rs); err != nil {
		t.Fatalf("a plan under a get/list-only identity was refused: %v", err)
	}
	if got := cs.reviewCount(); got != afterWrite {
		t.Errorf("the plan made %d reviews (was %d); a run that did not create the sentinel is not a first contact and must not assert", got-afterWrite, afterWrite)
	}
}

// TestAPlanWithNoSentinelIsStillRefused is the boundary of the tolerance
// above, and the thing #1370 must not undo. A store with no sentinel and an
// identity that cannot provision one reads exactly like an empty estate,
// which is #693's failure whole: the plan would propose creating an estate
// that already exists.
func TestAPlanWithNoSentinelIsStillRefused(t *testing.T) {
	cs := newClusterFake(t)
	cs.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, k8serrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", nil)
	})

	err := openCluster(t, cs, kubernetesRecordStore())
	if err == nil {
		t.Fatal("a store with no sentinel was planned against by an identity that cannot write one")
	}
	if !IsStoreRefusal(err) {
		t.Errorf("that is a refusal and not an outage, so no retry gets past it (#1376): %v", err)
	}
	if !strings.Contains(err.Error(), "holds no sentinel") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
}

// TestClusterContractFindingsSeesThroughTheWrappers pins that the contract is
// reachable from the store openBuiltStore hands back, which is the run cache
// over the trip counter over the store.
func TestClusterContractFindingsSeesThroughTheWrappers(t *testing.T) {
	cs := newClusterFake(t)
	store, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(contractNamespace),
		Clientset: cs,
		Namespace: contractNamespace,
		Estate:    contractEstate,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	opened, err := openBuiltStore(context.Background(), store, kubernetesRecordStore(), contractEstate)
	if err != nil {
		t.Fatalf("openBuiltStore: %v", err)
	}

	findings, ok, err := ClusterContractFindings(context.Background(), opened, staterecord.KubernetesRecordVerbs)
	if !ok {
		t.Fatal("the opened store does not answer the cluster contract")
	}
	if err != nil {
		t.Fatalf("ClusterContractFindings: %v", err)
	}
	for _, f := range findings {
		if !f.OK {
			t.Errorf("%s failed on a cluster built to satisfy everything: %s", f.Setting, f.Found)
		}
	}

	// A bucket store answers the bucket contract and not this one, and a
	// local store answers neither.
	local, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	if _, ok, _ := ClusterContractFindings(context.Background(), local, nil); ok {
		t.Error("a local store claims a cluster contract")
	}
}

// TestRecordNamespaceNamesTheStoresOwn pins the one string every cluster
// refusal has to carry. A refusal that named the wrong namespace would send
// an operator to look at a namespace that is fine.
func TestRecordNamespaceNamesTheStoresOwn(t *testing.T) {
	if got := RecordNamespace(kubernetesRecordStore(), contractEstate); got != contractNamespace {
		t.Errorf("RecordNamespace = %q, want %q", got, contractNamespace)
	}
	derived := &configs.LiveRecordStore{Type: "kubernetes"}
	if got := RecordNamespace(derived, "alice"); got != "tofu-records-alice" {
		t.Errorf("a derived namespace is %q, want tofu-records-alice", got)
	}
	if got := RecordNamespace(&configs.LiveRecordStore{Type: "s3", Bucket: "b"}, "alice"); got != "" {
		t.Errorf("a bucket store has a records namespace %q", got)
	}
	if got := RecordNamespace(nil, "alice"); got != "" {
		t.Errorf("no record store at all has a records namespace %q", got)
	}
}
