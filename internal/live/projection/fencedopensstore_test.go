// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/retry"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// GitHub issue #1448, section C. The store-open half: the identity the estate
// boundary policy fences used to open the store green, because its 403 was
// read as #1370's reader tolerance and an earlier run's sentinel was there to
// find. Everything here drives openBuiltStore, the production sequence, for
// the reason readeropensstore_test.go's own tests do.

// fenceRefused is the error the Kubernetes store returns when the estate
// boundary policy refuses a write.
//
// The 403 underneath is the real thing, transcribed from kind, and that is the
// point: with a hand-made cause this would pass however
// [staterecord.IsAccessDenied] reads, because there would be no 403 in it to
// recognise. It is only because this one IS a Forbidden StatusError that
// dropping that function's admission leg turns these tests red.
func fenceRefused(key string) error {
	denial := fmt.Sprintf(
		"ValidatingAdmissionPolicy '%s' with binding '%s' denied request: tofu-estate=prod would move this object into estate prod and system:serviceaccount:ci:fenced is not bound to it (no \"use\" on estates.choudoufu.intentius.io named prod)",
		staterecord.EstateBoundaryPolicyName, staterecord.EstateBoundaryPolicyName)
	return &staterecord.AdmissionDeniedError{
		Namespace: "tofu-records-prod",
		Key:       key,
		Estate:    "prod",
		Verb:      "create",
		Policy:    staterecord.EstateBoundaryPolicyName,
		Err:       forbidden(fmt.Sprintf("secrets %q is forbidden: %s", "tofu-record-abc", denial)),
	}
}

// forbidden is the API server's 403, the shape client-go hands back.
func forbidden(message string) error {
	return &k8serrors.StatusError{ErrStatus: metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    http.StatusForbidden,
		Reason:  metav1.StatusReasonForbidden,
		Message: message,
	}}
}

// TestAFencedIdentityIsRefusedThoughTheSentinelIsThere is the defect, at the
// layer that let it through. An earlier granted run left the sentinel, so the
// handshake's List finds it and the #1370 tolerance had nothing to object to.
// What makes this run different from a reader is that its writes reach the
// policy and the policy refuses them, so it must not get past open.
func TestAFencedIdentityIsRefusedThoughTheSentinelIsThere(t *testing.T) {
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "kubernetes"}
	inner := newTestLocalStore(t)
	seedSentinel(t, inner, rs, estate)
	store := &writeRefusingStore{Store: inner, denial: fenceRefused}

	opened, err := openBuiltStore(context.Background(), store, rs, estate)
	if err == nil {
		t.Fatalf("an identity the estate boundary policy refuses opened the store; the contract is skipped, the plan is built, and the apply is refused on its first record write (store=%T)", opened)
	}
	if !IsStoreRefusal(err) {
		t.Errorf("the fence's refusal is reported as an outage, so live-plan and live-mv warn and carry on against a store that refuses every write (#1376): %v", err)
	}
	for _, want := range []string{
		staterecord.EstateBoundaryPolicyName,
		"estates.choudoufu.intentius.io/prod",
		"live/kubernetes/estate-grant.yaml",
		"system:serviceaccount:ci:fenced",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, err)
		}
	}
	// #1370's sentence is about a role that may read and not write, and
	// saying it here would send the operator to the wrong grant.
	for _, unwanted := range []string{"may not write one", "read-only role"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("the refusal reads as #1370's read-only identity (%q):\n%s", unwanted, err)
		}
	}
	if store.checks != 0 {
		t.Errorf("the contract was read %d time(s) on a run that created no sentinel", store.checks)
	}
}

// TestAFencedIdentityWithNoSentinelSaysTheFenceRefusedIt is the other arm. The
// store holds no sentinel, so #1370's refusal is reached either way; what it
// must not do is explain the refusal as a read-only role when the policy is
// what said no.
func TestAFencedIdentityWithNoSentinelSaysTheFenceRefusedIt(t *testing.T) {
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "kubernetes"}
	store := &writeRefusingStore{Store: newTestLocalStore(t), denial: fenceRefused}

	_, err := openBuiltStore(context.Background(), store, rs, estate)
	if err == nil {
		t.Fatal("a store with no sentinel opened for an identity the estate boundary refuses")
	}
	if !IsStoreRefusal(err) {
		t.Errorf("the refusal is reported as an outage: %v", err)
	}
	if !strings.Contains(err.Error(), staterecord.EstateBoundaryPolicyName) {
		t.Errorf("the refusal does not name the policy that refused the write:\n%s", err)
	}
	if strings.Contains(err.Error(), "may not write one") {
		t.Errorf("the refusal explains the fence as a read-only role:\n%s", err)
	}
}

// TestAnUnidentifiedAdmissionDenialIsRefusedToo. Reader tolerance is for a
// denial positively identified as the authorizer's, so a 403 some other
// webhook or policy raised stops the run as well, in that policy's own words.
func TestAnUnidentifiedAdmissionDenialIsRefusedToo(t *testing.T) {
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "kubernetes"}
	inner := newTestLocalStore(t)
	seedSentinel(t, inner, rs, estate)
	store := &writeRefusingStore{Store: inner, denial: func(key string) error {
		return &staterecord.AdmissionDeniedError{
			Namespace: "tofu-records-prod", Key: key, Estate: estate, Verb: "create",
			Err: fmt.Errorf("admission webhook %q denied the request: no", "probe.example.com"),
		}
	}}

	_, err := openBuiltStore(context.Background(), store, rs, estate)
	if err == nil {
		t.Fatal("a 403 raised by an admission controller this fork does not know opened the store")
	}
	if !IsStoreRefusal(err) {
		t.Errorf("the refusal is reported as an outage: %v", err)
	}
	if !strings.Contains(err.Error(), "probe.example.com") {
		t.Errorf("the refusal does not carry what the API server said:\n%s", err)
	}
}

// TestTheOpenRefusalIsNotCalledAnOutageByIsAccessDenied pins the one line the
// whole design rests on. If [staterecord.IsAccessDenied] answers true for an
// admission denial, provisionStoreSentinel's tolerance leg takes it and the
// two tests above pass for the wrong reason - the sentinel is there, so the
// store opens.
func TestTheOpenRefusalIsNotCalledAnOutageByIsAccessDenied(t *testing.T) {
	if staterecord.IsAccessDenied(fenceRefused("tofu-records/prod/.store-sentinel")) {
		t.Error("the estate boundary policy's refusal is read as this identity being allowed to read and not write")
	}
}

// TestWriteBackNamesTheFenceRatherThanTheStore is item 3: a record write the
// policy refuses mid-apply used to read "Cannot persist a record: ...
// forbidden", which sends an operator to the record store. The remedy is one
// RBAC grant and nothing about the store is wrong.
func TestWriteBackNamesTheFenceRatherThanTheStore(t *testing.T) {
	addr := addrs.Resource{
		Mode: addrs.ManagedResourceMode, Type: "terraform_data", Name: "x",
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)

	diags := writeBackConflictDiag(addr, "Writing", fenceRefused("tofu-records/prod/terraform_data/x"), "kubernetes", retry.Config{})
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(diags))
	}
	desc := diags[0].Description()
	if desc.Summary != SummaryEstateBoundaryRefusedTheWrite {
		t.Errorf("the headline is %q", desc.Summary)
	}
	for _, want := range []string{
		addr.String(),
		staterecord.EstateBoundaryPolicyName,
		"live/kubernetes/estate-grant.yaml",
	} {
		if !strings.Contains(desc.Detail, want) {
			t.Errorf("the diagnostic does not say %q:\n%s", want, desc.Detail)
		}
	}
	if strings.Contains(desc.Summary, "Cannot persist a record") {
		t.Errorf("the headline still sends the reader to the store: %q", desc.Summary)
	}
}

// TestWriteBackStillSaysCannotPersistARecordForEverythingElse: the branch
// above must not swallow the ordinary write failure.
func TestWriteBackStillSaysCannotPersistARecordForEverythingElse(t *testing.T) {
	addr := addrs.Resource{
		Mode: addrs.ManagedResourceMode, Type: "terraform_data", Name: "x",
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)

	diags := writeBackConflictDiag(addr, "Writing", fmt.Errorf("the connection went away"), "kubernetes", retry.Config{})
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(diags))
	}
	if got := diags[0].Description().Summary; got != "Cannot persist a record" {
		t.Errorf("an ordinary write failure now reads %q", got)
	}
}

// TestAFencedApplyIsRefusedOnceAndNotTwice is the agreement between this
// change and PR #1452, measured on one cluster rather than reasoned about.
//
// #1452's cluster contract asks the API server whether this identity holds
// `use` on its estate and fails `estate_boundary` when it does not. That check
// runs in internal/command's BeforeApply, on every apply. This change refuses
// when the store is OPENED, which happens in the stateless runner's PriorState
// - before the plan graph exists, and so before BeforeApply is reached at all.
//
// So an apply by a fenced identity has two refusals available to it and prints
// one. This test shows both halves on the same fake cluster: the open path
// refuses in the fence's own words, and the contract, asked directly of that
// same cluster, does fail estate_boundary - which the run never gets far
// enough to hear.
func TestAFencedApplyIsRefusedOnceAndNotTwice(t *testing.T) {
	cs := newClusterFake(t)
	rs := kubernetesRecordStore()
	ctx := context.Background()

	// An earlier granted run, so the sentinel is there and the fenced run
	// below is not a first contact. That is the arrangement the audit found:
	// with the sentinel present, nothing else on the open path objected.
	if err := openCluster(t, cs, rs); err != nil {
		t.Fatalf("the granted run could not open the store: %v", err)
	}

	// Now the fence. The authorizer still allows create on Secrets in the
	// records namespace, which is what makes the 403 admission's rather than
	// its own; the estate grant is withheld, which is what #1452 asks for.
	cs.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, estateBoundaryDenial()
	})
	cs.denyVerb(staterecord.EstateGrantVerb)

	openErr := openCluster(t, cs, rs)
	if openErr == nil {
		t.Fatal("the store opened for a fenced identity, so the run would reach BeforeApply and be refused there instead, after the plan was built")
	}
	if !IsStoreRefusal(openErr) {
		t.Errorf("the open refusal is reported as an outage: %v", openErr)
	}
	var denied *staterecord.AdmissionDeniedError
	if !errors.As(openErr, &denied) {
		t.Fatalf("the open refusal is not the fence's: %v (%T)", openErr, openErr)
	}

	// The other refusal exists on this same cluster. It is the one #1452
	// raises, and the run never hears it because the store never opened.
	store, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(contractNamespace),
		Clientset: cs,
		Namespace: contractNamespace,
		Estate:    contractEstate,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	findings, checker, err := ContractFindings(ctx, store, rs, contractEstate)
	if err != nil {
		t.Fatalf("reading the cluster contract: %v", err)
	}
	if checker == nil {
		t.Fatal("the cluster store has no contract checker")
	}
	var boundary *staterecord.Finding
	for i := range findings {
		if findings[i].Setting == staterecord.ClusterEstateBoundary {
			boundary = &findings[i]
		}
	}
	if boundary == nil {
		t.Fatal("the contract reports no estate_boundary finding at all")
	}
	if boundary.Outcome != staterecord.Failed {
		t.Fatalf("estate_boundary is %v on a cluster whose policy refuses this identity every record write; this test then proves nothing about two refusals: %s", boundary.Outcome, boundary.Found)
	}

	// One refusal reaches the operator, and it is this one. The two say the
	// same thing - the identity holds no `use` on its estate - and send them
	// to the same file.
	for _, want := range []string{staterecord.EstateGrantVerb, "live/kubernetes/estate-grant.yaml"} {
		if !strings.Contains(openErr.Error(), want) {
			t.Errorf("the refusal the operator sees does not say %q:\n%s", want, openErr)
		}
		if !strings.Contains(boundary.Found, want) {
			t.Errorf("the refusal the operator does NOT see says %q and the one they do see should agree:\n%s", want, boundary.Found)
		}
	}
}

// estateBoundaryDenial is the API server's 403 when the estate boundary policy
// refuses a write: Details.Name set and one Details.Causes entry carrying the
// policy's own message. Transcribed from kind; see
// [staterecord.AdmissionDeniedError].
func estateBoundaryDenial() error {
	denial := fmt.Sprintf(
		"ValidatingAdmissionPolicy '%s' with binding '%s' denied request: tofu-estate=%s would move this object into estate %s and system:serviceaccount:ci:fenced is not bound to it",
		staterecord.EstateBoundaryPolicyName, staterecord.EstateBoundaryPolicyName, contractEstate, contractEstate)
	return &k8serrors.StatusError{ErrStatus: metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    http.StatusForbidden,
		Reason:  metav1.StatusReasonForbidden,
		Message: "secrets is forbidden: " + denial,
		Details: &metav1.StatusDetails{
			Name:   "tofu-record-abc",
			Kind:   "secrets",
			Causes: []metav1.StatusCause{{Message: denial}},
		},
	}}
}
