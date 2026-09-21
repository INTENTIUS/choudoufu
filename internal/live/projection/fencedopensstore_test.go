// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
