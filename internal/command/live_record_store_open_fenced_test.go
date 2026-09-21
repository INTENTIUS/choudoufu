// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// TestRecordStoreOpenDiagNamesTheEstateBoundary is GitHub issue #1448 section
// C, at the layer an operator reads. The estate boundary policy refusing this
// run is settled by one RBAC grant, and nothing about the record store is
// wrong, so "Cannot open the record store" sends the reader to a namespace,
// a Role and a set of Secrets that are all correct.
func TestRecordStoreOpenDiagNamesTheEstateBoundary(t *testing.T) {
	denied := &staterecord.AdmissionDeniedError{
		Namespace: "tofu-records-alice",
		Key:       "tofu-records/alice/.store-sentinel",
		Estate:    "alice",
		Verb:      "create",
		Policy:    staterecord.EstateBoundaryPolicyName,
		Err:       errors.New(`secrets "tofu-record-abc" is forbidden: ValidatingAdmissionPolicy 'choudoufu-estate-boundary' with binding 'choudoufu-estate-boundary' denied request: tofu-estate=alice would move this object into estate alice and system:serviceaccount:ci:fenced is not bound to it`),
	}
	// Wrapped the way provisionStoreSentinel wraps it.
	d := recordStoreOpenDiag("kubernetes", fmt.Errorf("record_store: provisioning the sentinel at %q: %w", denied.Key, denied))
	if got := d.Description().Summary; got != projection.SummaryEstateBoundaryRefusedTheWrite {
		t.Errorf("summary = %q", got)
	}
	for _, want := range []string{
		staterecord.EstateBoundaryPolicyName,
		"estates.choudoufu.intentius.io/alice",
		"live/kubernetes/estate-grant.yaml",
		"system:serviceaccount:ci:fenced",
	} {
		if !strings.Contains(d.Description().Detail, want) {
			t.Errorf("the detail does not say %q:\n%s", want, d.Description().Detail)
		}
	}
	// The refusal is rendered once. The KMS branches above learned this the
	// hard way: appending the whole wrapped error prints the remedy twice.
	if n := strings.Count(d.Description().Detail, "live/kubernetes/estate-grant.yaml"); n != 1 {
		t.Errorf("the grant line appears %d time(s), want 1:\n%s", n, d.Description().Detail)
	}

	other := recordStoreOpenDiag("kubernetes", errors.New("dial tcp: connection refused"))
	if got := other.Description().Summary; got != "Cannot open the record store" {
		t.Errorf("a store that could not be reached got the summary %q", got)
	}
}

// TestRecordStoreOpenDiagNamesAnUnidentifiedAdmissionDenial: the same headline
// must not blame the estate boundary when some other admission controller is
// what refused the write.
func TestRecordStoreOpenDiagNamesAnUnidentifiedAdmissionDenial(t *testing.T) {
	denied := &staterecord.AdmissionDeniedError{
		Namespace: "tofu-records-alice",
		Key:       "tofu-records/alice/.store-sentinel",
		Estate:    "alice",
		Verb:      "create",
		Err:       errors.New(`admission webhook "guard.example.com" denied the request: no`),
	}
	d := recordStoreOpenDiag("kubernetes", denied)
	if got := d.Description().Summary; got != projection.SummaryAdmissionRefusedTheWrite {
		t.Errorf("summary = %q", got)
	}
	if strings.Contains(d.Description().Detail, "estate-grant.yaml") {
		t.Errorf("the detail offers the estate grant for a policy that never asked for it:\n%s", d.Description().Detail)
	}
	if !strings.Contains(d.Description().Detail, "guard.example.com") {
		t.Errorf("the detail lost what the API server said:\n%s", d.Description().Detail)
	}
}
