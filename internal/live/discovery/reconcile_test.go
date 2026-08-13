// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/policy"
)

// scopedPolicy is a Policy whose undeclared_untagged is "delete", scoped to
// typeName, for [Reconcile]'s tests.
func scopedPolicy(typeName string, threshold int) *policy.Policy {
	raw := &policy.Raw{
		UndeclaredUntagged: "delete", UndeclaredUntaggedSet: true,
		Scope: &policy.RawScope{Types: []string{typeName}},
	}
	if threshold > 0 {
		raw.Threshold = threshold
		raw.ThresholdSet = true
	}
	return policy.Build(raw, estateName)
}

// TestReconcileFindsUntaggedCandidate: the mainline case - a live resource
// of a scoped, admitted, enumerable type with no estate marker at all is a
// deletion candidate, rendered with its identity.
func TestReconcileFindsUntaggedCandidate(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_security_group")
	cloud.obj("aws_security_group", "sg-stray", nil)

	res, diags := Reconcile(context.Background(), ReconcileRequest{
		Estate:   estateName,
		Provider: cloud,
		Policy:   scopedPolicy("aws_security_group", 10),
	})
	assertNoErrors(t, diags)

	if len(res.Roster) != 1 {
		t.Fatalf("want 1 candidate, got %d: %+v", len(res.Roster), res.Roster)
	}
	c := res.Roster[0]
	if c.TypeName != "aws_security_group" || c.ImportID != "sg-stray" {
		t.Errorf("candidate = %+v, want aws_security_group/sg-stray", c)
	}
	if res.ThresholdExceeded {
		t.Error("one candidate under a threshold of 10 must not exceed it")
	}
}

// TestReconcileProtectsAnyEstatesResources: a resource carrying ANY
// estate's marker is never a candidate, this estate's own included -
// account reconciliation reaches only for resources no estate has ever
// claimed.
func TestReconcileProtectsAnyEstatesResources(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_security_group")
	cloud.own("aws_security_group", "sg-mine", "aws_security_group.mine")
	cloud.obj("aws_security_group", "sg-theirs", map[string]string{TagEstate: "some-other-estate"})

	res, diags := Reconcile(context.Background(), ReconcileRequest{
		Estate:   estateName,
		Provider: cloud,
		Policy:   scopedPolicy("aws_security_group", 10),
	})
	assertNoErrors(t, diags)

	if len(res.Roster) != 0 {
		t.Errorf("estate-tagged resources must never be reconciliation candidates: %+v", res.Roster)
	}
}

// TestReconcileProtectsThePreservationTag is the maintainer's motivating
// example's other half: a resource carrying the policy's own tag_key=
// tag_value is protected, even though it carries no estate marker at all.
func TestReconcileProtectsThePreservationTag(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_security_group")
	cloud.obj("aws_security_group", "sg-protected", map[string]string{"keep-me": "yes"})
	cloud.obj("aws_security_group", "sg-stray", nil)

	pol := policy.Build(&policy.Raw{
		UndeclaredUntagged: "delete", UndeclaredUntaggedSet: true,
		TagKey: "keep-me", TagKeySet: true,
		TagValue: "yes", TagValueSet: true,
		Scope: &policy.RawScope{Types: []string{"aws_security_group"}},
	}, estateName)

	res, diags := Reconcile(context.Background(), ReconcileRequest{
		Estate:   estateName,
		Provider: cloud,
		Policy:   pol,
	})
	assertNoErrors(t, diags)

	if len(res.Roster) != 1 || res.Roster[0].ImportID != "sg-stray" {
		t.Fatalf("want exactly sg-stray on the roster (sg-protected carries the preservation tag), got %+v", res.Roster)
	}
}

// TestReconcileThresholdGuard: a roster over the threshold refuses with the
// roster still populated, so a caller can show what tripped it.
func TestReconcileThresholdGuard(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_security_group")
	cloud.obj("aws_security_group", "sg-1", nil)
	cloud.obj("aws_security_group", "sg-2", nil)

	res, diags := Reconcile(context.Background(), ReconcileRequest{
		Estate:   estateName,
		Provider: cloud,
		Policy:   scopedPolicy("aws_security_group", 1),
	})
	assertNoErrors(t, diags) // Reconcile itself does not raise the refusal.

	if !res.ThresholdExceeded {
		t.Fatal("2 candidates over a threshold of 1 must exceed it")
	}
	if len(res.Roster) != 2 {
		t.Errorf("the roster must stay populated on a threshold refusal so it can be reviewed, got %d", len(res.Roster))
	}
	if res.Threshold != 1 {
		t.Errorf("Threshold = %d, want 1", res.Threshold)
	}
}

// TestReconcileDefaultThreshold: a policy that never set threshold falls
// back to [policy.DefaultThreshold] - the "default modest" issue #67 asks
// for.
func TestReconcileDefaultThreshold(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_security_group")
	cloud.obj("aws_security_group", "sg-1", nil)

	res, diags := Reconcile(context.Background(), ReconcileRequest{
		Estate:   estateName,
		Provider: cloud,
		Policy:   scopedPolicy("aws_security_group", 0),
	})
	assertNoErrors(t, diags)

	if res.Threshold != policy.DefaultThreshold {
		t.Errorf("Threshold = %d, want the default %d", res.Threshold, policy.DefaultThreshold)
	}
}

// TestReconcileRefusesWithNoScope: defense in depth, independent of
// internal/live/lint - a delete quadrant with no scope is refused here too
// rather than falling back to an unscoped purge.
func TestReconcileRefusesWithNoScope(t *testing.T) {
	cloud := newFakeCloud()
	pol := policy.Build(&policy.Raw{
		UndeclaredUntagged: "delete", UndeclaredUntaggedSet: true,
	}, estateName)

	_, diags := Reconcile(context.Background(), ReconcileRequest{
		Estate:   estateName,
		Provider: cloud,
		Policy:   pol,
	})
	if !diags.HasErrors() {
		t.Fatal("an unscoped delete quadrant must be refused, not silently run unscoped")
	}
}

// TestReconcileGapsAreReportedNotSilent: a scope-selected type this pass
// cannot enumerate is a gap, not an empty roster - "delete" must never
// imply the account is clean.
func TestReconcileGapsAreReportedNotSilent(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_security_group")
	cloud.unlistable("aws_security_group")

	res, diags := Reconcile(context.Background(), ReconcileRequest{
		Estate:   estateName,
		Provider: cloud,
		Policy:   scopedPolicy("aws_security_group", 10),
	})
	assertNoErrors(t, diags)

	if len(res.Roster) != 0 {
		t.Errorf("an unlistable type must never produce a candidate: %+v", res.Roster)
	}
	if len(res.Gaps) != 1 || res.Gaps[0].TypeName != "aws_security_group" {
		t.Errorf("want one gap for aws_security_group, got %+v", res.Gaps)
	}
}

// TestReconcileOnlyAdmittedTypes: a scope naming a type outside the
// admission table finds nothing, rather than reaching for a type this fork
// has no identity or lint coverage for at all.
func TestReconcileOnlyAdmittedTypes(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("not_an_admitted_type")
	cloud.obj("not_an_admitted_type", "x-1", nil)

	res, diags := Reconcile(context.Background(), ReconcileRequest{
		Estate:   estateName,
		Provider: cloud,
		Policy:   scopedPolicy("not_an_admitted_type", 10),
	})
	assertNoErrors(t, diags)

	if len(res.Roster) != 0 || len(res.Gaps) != 0 {
		t.Errorf("a non-admitted type must be excluded from the type universe entirely, got roster=%+v gaps=%+v", res.Roster, res.Gaps)
	}
}
