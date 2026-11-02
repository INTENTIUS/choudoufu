// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// GitHub issue #1448, section C: an admission denial is a 403, and
// IsAccessDenied read every 403 as #1370's reader tolerance.
//
// The four 403 bodies below are transcribed from a kind v1.36.1 API server,
// one per denial this store can meet, and they are what makes these tests
// worth anything: a hand-made "forbidden" would pass however the
// classification reads, because there would be nothing in it to recognise.
// What was measured, and the run that measured it, is in the pull request for
// this change; [KubernetesStore.admissionFault] carries the summary.

const (
	probeUser   = "system:serviceaccount:default:fenced"
	probeReader = "system:serviceaccount:default:reader"
)

// estateBoundaryForbidden is the estate boundary policy refusing a write:
// reason Forbidden, Details.Name set, and one Details.Causes entry carrying
// the policy's message. Measured.
func estateBoundaryForbidden(secretName, estate, user string) error {
	denial := fmt.Sprintf(
		"ValidatingAdmissionPolicy '%s' with binding '%s' denied request: tofu-estate=%s would move this object into estate %s and %s is not bound to it (no \"use\" on estates.choudoufu.intentius.io named %s)",
		EstateBoundaryPolicyName, EstateBoundaryPolicyName, estate, estate, user, estate)
	return &k8serrors.StatusError{ErrStatus: metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    http.StatusForbidden,
		Reason:  metav1.StatusReasonForbidden,
		Message: fmt.Sprintf("secrets %q is forbidden: %s", secretName, denial),
		Details: &metav1.StatusDetails{
			Name:   secretName,
			Kind:   "secrets",
			Causes: []metav1.StatusCause{{Message: denial}},
		},
	}}
}

// rbacForbidden is the authorizer's own refusal: no Details.Name, no Causes.
// This is #1370's plan identity, and it must keep being tolerated. Measured.
func rbacForbidden(user, namespace, verb string) error {
	return &k8serrors.StatusError{ErrStatus: metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    http.StatusForbidden,
		Reason:  metav1.StatusReasonForbidden,
		Message: fmt.Sprintf("secrets is forbidden: User %q cannot %s resource \"secrets\" in API group \"\" in the namespace %q", user, verb, namespace),
		Details: &metav1.StatusDetails{Kind: "secrets"},
	}}
}

// webhookForbidden is a validating webhook's denial, which carries no Details
// at all and so is indistinguishable from RBAC's by shape. Measured against a
// throwaway always-deny webhook.
func webhookForbidden(hook string) error {
	return &k8serrors.StatusError{ErrStatus: metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    http.StatusForbidden,
		Reason:  metav1.StatusReasonForbidden,
		Message: fmt.Sprintf("admission webhook %q denied the request: this identity is not bound to estate alice", hook),
	}}
}

// quotaForbidden is a built-in admission plugin's denial: Details.Name set,
// no Causes. Measured against a ResourceQuota of count/secrets=0.
func quotaForbidden(secretName string) error {
	return &k8serrors.StatusError{ErrStatus: metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    http.StatusForbidden,
		Reason:  metav1.StatusReasonForbidden,
		Message: fmt.Sprintf("secrets %q is forbidden: exceeded quota: no-secrets, requested: count/secrets=1, used: count/secrets=0, limited: count/secrets=0", secretName),
		Details: &metav1.StatusDetails{Name: secretName, Kind: "secrets"},
	}}
}

// authorizer is the API server's own answer to a SelfSubjectAccessReview,
// recorded so a test can hold the store to asking the right question.
type authorizer struct {
	allowed bool
	denied  bool
	// fail makes the review itself fail, the "cannot ask" case.
	fail bool

	mu    sync.Mutex
	asked []authzv1.ResourceAttributes
}

func (a *authorizer) reviews() []authzv1.ResourceAttributes {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]authzv1.ResourceAttributes(nil), a.asked...)
}

// fencedStore builds a store whose Secret writes fail with denial and whose
// authorizer answers az. Both go through one fake clientset, so the store's
// review reaches the same reactor set the write does.
func fencedStore(t *testing.T, estate string, az *authorizer, verb string, denial error) *KubernetesStore {
	t.Helper()
	cs := fake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fakeRecordNamespace}})
	cs.PrependReactor(verb, "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, denial
	})
	if az != nil {
		cs.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
			review := action.(k8stesting.CreateAction).GetObject().(*authzv1.SelfSubjectAccessReview)
			a := review.Spec.ResourceAttributes
			az.mu.Lock()
			if a != nil {
				az.asked = append(az.asked, *a)
			}
			az.mu.Unlock()
			if az.fail {
				return true, nil, errors.New("the API server did not answer the review")
			}
			review.Status = authzv1.SubjectAccessReviewStatus{Allowed: az.allowed, Denied: az.denied}
			return true, review, nil
		})
	}
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

const fencedKey = "tofu-records/alice/.store-sentinel"

// TestTheFenceIsNotReaderTolerance is the defect. A ServiceAccount with full
// RBAC on Secrets in the records namespace and no `use` grant on its estate
// sends the provisioning sentinel, the estate boundary policy refuses it with
// a 403, and IsAccessDenied said yes - so internal/live/projection tolerated
// it, found an earlier run's sentinel through List, and opened the store
// green. The apply that followed was refused by the same policy on every
// record it wrote.
func TestTheFenceIsNotReaderTolerance(t *testing.T) {
	az := &authorizer{allowed: true}
	store := fencedStore(t, "alice", az, "create",
		estateBoundaryForbidden("tofu-record-abc", "alice", probeUser))

	_, err := store.PutIfAbsent(context.Background(), fencedKey, []byte("x"))
	if err == nil {
		t.Fatal("a write the estate boundary policy refused reported success")
	}
	var denied *AdmissionDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("the policy's refusal is not an *AdmissionDeniedError, so nothing downstream can tell it from a read-only identity: %v (%T)", err, err)
	}
	if IsAccessDenied(err) {
		t.Error("the fence's own refusal is read as this identity being allowed to read and not write, which is #1370's tolerance; a plan carries on and the apply is refused mid-write")
	}
	if denied.Policy != EstateBoundaryPolicyName {
		t.Errorf("the refusal names policy %q, want %q", denied.Policy, EstateBoundaryPolicyName)
	}
	if denied.Verb != "create" {
		t.Errorf("the refusal names verb %q, want create", denied.Verb)
	}
	text := denied.Error()
	// The whole grant sentence, not its pieces: it is built from
	// EstateGrantVerb, EstateGrantResource and EstateGrantGroup, which
	// PR #1452 also builds its own estate_boundary finding from, and a
	// change to any of the three has to be a deliberate change to what an
	// operator reads here.
	if want := "holds no `use` on estates.choudoufu.intentius.io/alice and every record this run writes is refused the same way"; !strings.Contains(text, want) {
		t.Errorf("the refusal no longer reads %q:\n%s", want, text)
	}
	for _, want := range []string{
		EstateBoundaryPolicyName,
		"estates.choudoufu.intentius.io/alice",
		"live/kubernetes/estate-grant.yaml",
		"kubectl apply -f -",
		probeUser,
		fakeRecordNamespace,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, text)
		}
	}
}

// TestTheAuthorizersOwnRefusalStaysTolerated is #1370, unchanged. The reader
// holds get and list and no create, the authorizer refuses the write before
// admission ever sees it, and the run has to be able to carry on against a
// store an earlier run provisioned.
func TestTheAuthorizersOwnRefusalStaysTolerated(t *testing.T) {
	az := &authorizer{allowed: false}
	store := fencedStore(t, "alice", az, "create",
		rbacForbidden(probeReader, fakeRecordNamespace, "create"))

	_, err := store.PutIfAbsent(context.Background(), fencedKey, []byte("x"))
	var denied *AdmissionDeniedError
	if errors.As(err, &denied) {
		t.Fatalf("the authorizer's own refusal was read as an admission denial, so #1370's read-only plan stops: %v", err)
	}
	if !IsAccessDenied(err) {
		t.Fatalf("a read-only identity's 403 is no longer a denial, so its plan is refused as an outage: %v", err)
	}
}

// TestAnExplicitDenyIsTheAuthorizersRefusal: an authorizer can answer Allowed
// and Denied together, and reading only Allowed would turn its refusal into an
// admission one. The same mutation survived in the cluster contract (#1448,
// section F).
func TestAnExplicitDenyIsTheAuthorizersRefusal(t *testing.T) {
	az := &authorizer{allowed: true, denied: true}
	store := fencedStore(t, "alice", az, "create",
		rbacForbidden(probeReader, fakeRecordNamespace, "create"))

	_, err := store.PutIfAbsent(context.Background(), fencedKey, []byte("x"))
	var denied *AdmissionDeniedError
	if errors.As(err, &denied) {
		t.Fatalf("an explicit deny from the authorizer was read as an admission denial: %v", err)
	}
	if !IsAccessDenied(err) {
		t.Fatalf("an explicit deny is not a denial: %v", err)
	}
}

// TestAnUnknownAdmissionDenialIsNotToleratedEither. Reader tolerance is for a
// denial positively identified as the authorizer's. A validating webhook's
// 403 carries no Details at all and reads like RBAC's by shape, and a
// ResourceQuota's carries a name and no causes; neither names a policy this
// fork knows. The review is what settles both.
func TestAnUnknownAdmissionDenialIsNotToleratedEither(t *testing.T) {
	for _, tc := range []struct {
		name   string
		denial error
	}{
		{"a validating webhook", webhookForbidden("probe.example.com")},
		{"a built-in admission plugin", quotaForbidden("tofu-record-abc")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			az := &authorizer{allowed: true}
			store := fencedStore(t, "alice", az, "create", tc.denial)

			_, err := store.PutIfAbsent(context.Background(), fencedKey, []byte("x"))
			var denied *AdmissionDeniedError
			if !errors.As(err, &denied) {
				t.Fatalf("a 403 the authorizer would have allowed was tolerated as a read-only identity: %v (%T)", err, err)
			}
			if IsAccessDenied(err) {
				t.Error("an unidentified admission denial is read as #1370's reader tolerance")
			}
			if denied.Policy != "" {
				t.Errorf("the refusal names policy %q; nothing here named this fork's own", denied.Policy)
			}
			if !strings.Contains(denied.Error(), "an admission policy on this cluster") {
				t.Errorf("the refusal reads as the estate boundary's when the API server named no policy:\n%s", denied.Error())
			}
		})
	}
}

// TestTheReviewAsksTheQuestionTheWriteAsked. A review about the wrong verb,
// the wrong resource or the wrong object name answers a question nobody
// asked. The name matters for update and delete, where an RBAC resourceNames
// rule applies; a create is authorized with no name, because the name is in
// the body and not the path.
func TestTheReviewAsksTheQuestionTheWriteAsked(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		reactor  string
		wantVerb string
		wantName func(s *KubernetesStore) string
		run      func(s *KubernetesStore) error
	}{
		{
			name: "create", reactor: "create", wantVerb: "create",
			wantName: func(*KubernetesStore) string { return "" },
			run: func(s *KubernetesStore) error {
				_, err := s.PutIfAbsent(ctx, fencedKey, []byte("x"))
				return err
			},
		},
		{
			name: "update", reactor: "update", wantVerb: "update",
			wantName: func(s *KubernetesStore) string { return s.SecretName(fencedKey) },
			run: func(s *KubernetesStore) error {
				_, err := s.PutIfVersion(ctx, fencedKey, []byte("x"), "77")
				return err
			},
		},
		{
			name: "delete", reactor: "delete", wantVerb: "delete",
			wantName: func(s *KubernetesStore) string { return s.SecretName(fencedKey) },
			run:      func(s *KubernetesStore) error { return s.Delete(ctx, fencedKey, "77") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			az := &authorizer{allowed: true}
			store := fencedStore(t, "alice", az, tc.reactor,
				estateBoundaryForbidden("tofu-record-abc", "alice", probeUser))

			err := tc.run(store)
			var denied *AdmissionDeniedError
			if !errors.As(err, &denied) {
				t.Fatalf("the refused %s is not an admission denial: %v (%T)", tc.name, err, err)
			}
			if denied.Verb != tc.wantVerb {
				t.Errorf("the refusal names verb %q, want %q", denied.Verb, tc.wantVerb)
			}
			reviews := az.reviews()
			if len(reviews) != 1 {
				t.Fatalf("the store made %d reviews, want 1: %+v", len(reviews), reviews)
			}
			got := reviews[0]
			if got.Verb != tc.wantVerb {
				t.Errorf("the review asks about verb %q, want %q", got.Verb, tc.wantVerb)
			}
			if got.Group != "" || got.Version != "v1" || got.Resource != "secrets" {
				t.Errorf("the review asks about %q/%q/%q, want core/v1 secrets", got.Group, got.Version, got.Resource)
			}
			if got.Namespace != fakeRecordNamespace {
				t.Errorf("the review asks about namespace %q, want %q", got.Namespace, fakeRecordNamespace)
			}
			if want := tc.wantName(store); got.Name != want {
				t.Errorf("the review asks about name %q, want %q: a resourceNames rule answers a named request and a nameless one differently", got.Name, want)
			}
		})
	}
}

// TestTheReviewIsAskedOncePerRun. A write-back whose every write is refused
// would otherwise put one review in front of every record. The answer does not
// change inside a run, which is why [KubernetesStore.namespaceFault] caches
// its own.
func TestTheReviewIsAskedOncePerRun(t *testing.T) {
	az := &authorizer{allowed: true}
	store := fencedStore(t, "alice", az, "create",
		estateBoundaryForbidden("tofu-record-abc", "alice", probeUser))

	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if _, err := store.PutIfAbsent(ctx, fmt.Sprintf("tofu-records/alice/aws_thing/r%d", i), []byte("x")); err == nil {
			t.Fatal("a refused write reported success")
		}
	}
	if n := len(az.reviews()); n != 1 {
		t.Errorf("four refused creates made %d reviews, want 1", n)
	}
}

// TestAnUnaskableAuthorizerFallsBackToThePolicyName. A store built from a bare
// SecretInterface has no clientset, and the review can fail. Then the only
// signal left is the policy's own name inside the refusal: this fork's fence
// is still recognised, and a 403 naming nothing is left to the authorizer's
// side so that #1370 is not broken for a store that cannot ask.
func TestAnUnaskableAuthorizerFallsBackToThePolicyName(t *testing.T) {
	for _, tc := range []struct {
		name        string
		az          *authorizer
		denial      error
		wantRefusal bool
	}{
		{"the review fails and the fence is named", &authorizer{fail: true}, estateBoundaryForbidden("tofu-record-abc", "alice", probeUser), true},
		{"the review fails and nothing is named", &authorizer{fail: true}, rbacForbidden(probeReader, fakeRecordNamespace, "create"), false},
		{"there is no clientset and the fence is named", nil, estateBoundaryForbidden("tofu-record-abc", "alice", probeUser), true},
		{"there is no clientset and nothing is named", nil, rbacForbidden(probeReader, fakeRecordNamespace, "create"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var store *KubernetesStore
			if tc.az == nil {
				cs := fake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fakeRecordNamespace}})
				cs.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, tc.denial
				})
				var err error
				store, err = NewKubernetesStore(KubernetesConfig{
					Secrets:   cs.CoreV1().Secrets(fakeRecordNamespace),
					Namespace: fakeRecordNamespace,
					Estate:    "alice",
				})
				if err != nil {
					t.Fatalf("NewKubernetesStore: %v", err)
				}
			} else {
				store = fencedStore(t, "alice", tc.az, "create", tc.denial)
			}

			_, err := store.PutIfAbsent(context.Background(), fencedKey, []byte("x"))
			var denied *AdmissionDeniedError
			if got := errors.As(err, &denied); got != tc.wantRefusal {
				t.Fatalf("admission denial = %v, want %v: %v", got, tc.wantRefusal, err)
			}
			if IsAccessDenied(err) == tc.wantRefusal {
				t.Errorf("IsAccessDenied = %v beside an admission refusal of %v", IsAccessDenied(err), tc.wantRefusal)
			}
		})
	}
}

// TestATerminatingNamespaceIsStillItsOwnRefusal. A write into a namespace
// being deleted is a 403 with a NamespaceTerminating cause, and PR #1450 carved
// it out of reader tolerance first. It is not about this identity at all, so
// the admission leg has to leave it alone.
func TestATerminatingNamespaceIsStillItsOwnRefusal(t *testing.T) {
	az := &authorizer{allowed: true}
	terminating := &k8serrors.StatusError{ErrStatus: metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    http.StatusForbidden,
		Reason:  metav1.StatusReasonForbidden,
		Message: fmt.Sprintf("unable to create new content in namespace %s because it is being terminated", fakeRecordNamespace),
		Details: &metav1.StatusDetails{
			Name: fakeRecordNamespace, Kind: "secrets",
			Causes: []metav1.StatusCause{{Type: corev1.NamespaceTerminatingCause, Message: "namespace is being terminated"}},
		},
	}}
	store := fencedStore(t, "alice", az, "create", terminating)

	_, err := store.PutIfAbsent(context.Background(), fencedKey, []byte("x"))
	var nsErr *NamespaceTerminatingError
	if !errors.As(err, &nsErr) {
		t.Fatalf("a write into a terminating namespace is no longer its own refusal: %v (%T)", err, err)
	}
	var denied *AdmissionDeniedError
	if errors.As(err, &denied) {
		t.Error("a terminating namespace was reported as an estate boundary refusal, which sends the operator to an RBAC grant that is not the problem")
	}
}

// TestAReadIs403IsTheAuthorizersWithoutAsking. Admission never sees a get or a
// list, so a 403 on a read is the authorizer's whatever else the cluster runs,
// and the store must not spend a review on it.
func TestAReadIs403IsTheAuthorizersWithoutAsking(t *testing.T) {
	az := &authorizer{allowed: true}
	store := fencedStore(t, "alice", az, "get",
		rbacForbidden(probeReader, fakeRecordNamespace, "get"))

	_, _, _, err := store.Get(context.Background(), fencedKey)
	var denied *AdmissionDeniedError
	if errors.As(err, &denied) {
		t.Fatalf("a refused read was reported as an admission denial: %v", err)
	}
	if n := len(az.reviews()); n != 0 {
		t.Errorf("a refused read made %d reviews, want 0", n)
	}
}
