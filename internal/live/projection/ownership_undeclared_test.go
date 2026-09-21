// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1226. [builder.checkOwnership] asked the policy for a verb
// with declared hardcoded to true, so an UNDECLARED instance that reached the
// tag read was governed by whichever declared quadrant its tags put it in.
// The own.verified short-circuit already guards the marker-verified half
// (TestOwnershipPolicy_ReconcileCandidateIsNotDeclaredTagged); these tests
// are the same guard for an undeclared instance that is not in
// Ownership.Verified and so gets as far as the verb.
//
// The unmarked half is the dangerous one. An undeclared instance in the
// prior state has no resource block, so the only thing a plan can propose
// for it is a destroy. declared_untagged = "adopt" admitting it therefore
// reads an operator's "adopt what I declared" as "destroy what nobody
// declared and nobody marked", which live/MARKERS.md's "Ownership semantics"
// rules out by name: such a resource is foreign, "reported, protected, and
// never auto-deleted".

// TestOwnershipPolicy_UndeclaredUnmarkedIsNotDeclaredUntagged is the issue's
// BuildWith reproduction. aws_cloudwatch_log_group.nowhere is not in
// testdata/named, and the live object carries no marker at all.
func TestOwnershipPolicy_UndeclaredUnmarkedIsNotDeclaredUntagged(t *testing.T) {
	for _, verb := range []string{"adopt", "converge"} {
		t.Run(verb, func(t *testing.T) {
			cfg := loadConfig(t, "testdata/named")
			nowhere := mustAddr(t, `aws_cloudwatch_log_group.nowhere`)

			cloud := newFakeCloud()
			cloud.putTagged("aws_cloudwatch_log_group", "/orphan/logs", map[string]string{
				"id": "/orphan/logs", "name": "/orphan/logs",
			}, map[string]string{"Team": "platform"})

			res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
				{Addr: nowhere, Class: identity.ClassConcrete, ImportID: "/orphan/logs", Undeclared: true},
			}, cloud.providers(t), Options{Ownership: &Ownership{Estate: policyEstate, Policy: buildPolicy(t, "", verb)}})

			assertNoErrors(t, diags)
			if res.Has(nowhere) {
				t.Errorf("declared_untagged = %q materialized an undeclared, unmarked live object into the prior state, where the only thing a plan can propose for it is a destroy:\n%s", verb, res)
			}
			if len(res.Policy) != 0 {
				t.Errorf("declared-quadrant outcome for an undeclared instance: %+v", res.Policy)
			}
			if len(res.Unowned) != 1 {
				t.Fatalf("Unowned = %+v, want the one foreign object reported", res.Unowned)
			}
			if !hasDiag(diags, SummaryOutsideEstate, "/orphan/logs") {
				t.Errorf("a foreign object is reported, not dropped in silence:\n%s", renderDiags(diags))
			}
		})
	}
}

// TestOwnershipPolicy_UndeclaredUnmarkedDefaultIsUnchanged pins what this
// issue must not move: with no policy block, and with one naming only
// defaults, an undeclared unmarked object was already left out and reported
// under the same summary. It passes before and after the fix.
func TestOwnershipPolicy_UndeclaredUnmarkedDefaultIsUnchanged(t *testing.T) {
	for name, pol := range map[string]*Ownership{
		"no policy":       {Estate: policyEstate},
		"default verbs":   {Estate: policyEstate, Policy: buildPolicy(t, "converge", "refuse")},
		"untagged report": {Estate: policyEstate, Policy: buildPolicy(t, "", "report")},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := loadConfig(t, "testdata/named")
			nowhere := mustAddr(t, `aws_cloudwatch_log_group.nowhere`)

			cloud := newFakeCloud()
			cloud.putTagged("aws_cloudwatch_log_group", "/orphan/logs", map[string]string{
				"id": "/orphan/logs", "name": "/orphan/logs",
			}, nil)

			res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
				{Addr: nowhere, Class: identity.ClassConcrete, ImportID: "/orphan/logs", Undeclared: true},
			}, cloud.providers(t), Options{Ownership: pol})

			assertNoErrors(t, diags)
			if res.Has(nowhere) {
				t.Fatalf("an undeclared, unmarked object was admitted:\n%s", res)
			}
			if len(res.Unowned) != 1 {
				t.Errorf("Unowned = %+v, want one", res.Unowned)
			}
			if !hasDiag(diags, SummaryOutsideEstate, "/orphan/logs") {
				t.Errorf("the refusal lost its warning:\n%s", renderDiags(diags))
			}
		})
	}
}

// TestOwnershipPolicy_UndeclaredTaggedOnTheTagReadIsNotDeclaredTagged is the
// tagged half of the same literal. An undeclared instance carrying this
// estate's marker, and its own address, is admitted - that is what makes it
// destroyable, exactly as the verified short-circuit admits one - but
// declared_tagged = "untag" was never assigned to it, and a PolicyOutcome
// for it would feed stamp's PolicyUntag an address no block declares.
func TestOwnershipPolicy_UndeclaredTaggedOnTheTagReadIsNotDeclaredTagged(t *testing.T) {
	for _, verb := range []string{"untag", "keep", "report"} {
		t.Run(verb, func(t *testing.T) {
			cfg := loadConfig(t, "testdata/named")
			nowhere := mustAddr(t, `aws_cloudwatch_log_group.nowhere`)

			cloud := newFakeCloud()
			cloud.putTagged("aws_cloudwatch_log_group", "/ours/old-logs", map[string]string{
				"id": "/ours/old-logs", "name": "/ours/old-logs",
			}, map[string]string{
				markers.TagEstate:  policyEstate,
				markers.TagAddress: "aws_cloudwatch_log_group.nowhere",
			})

			res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
				{Addr: nowhere, Class: identity.ClassConcrete, ImportID: "/ours/old-logs", Undeclared: true},
			}, cloud.providers(t), Options{Ownership: &Ownership{Estate: policyEstate, Policy: buildPolicy(t, verb, "")}})

			assertNoErrors(t, diags)
			if !res.Has(nowhere) {
				t.Fatalf("an undeclared instance carrying this estate's own marker must still be admitted:\n%s", res)
			}
			if len(res.Policy) != 0 {
				t.Errorf("declared-quadrant outcome for an undeclared instance: %+v", res.Policy)
			}
		})
	}
}

// TestCheckOwnership_undeclaredUnmarkedGetsNoDeclaredVerb is the unit-level
// form from the issue, on the label surface, with declared=false. It also
// pins the two things the verdict alone does not show: "keep" is a
// declared_untagged affordance and must not quieten a finding about an
// object outside that quadrant, and the refusal must not tell the operator
// the plan "proposes creating the resource this configuration declares" or
// offer them declared_untagged = "adopt", neither of which is true of this
// object.
func TestCheckOwnership_undeclaredUnmarkedGetsNoDeclaredVerb(t *testing.T) {
	for _, verb := range []string{"adopt", "converge", "keep", "report"} {
		t.Run(verb, func(t *testing.T) {
			b := k8sOwnershipBuilder(&Ownership{Estate: policyEstate, Policy: buildPolicy(t, "", verb)})
			addr := k8sConfigMapAddr(t)

			verdict := b.checkOwnership(addr, configMapTestType, "smoke-k8s/app-config",
				configMapTypeSchema(), k8sLiveConfigMap(nil), false, false, false)

			if verdict != ownershipUnowned {
				t.Errorf("checkOwnership = %d, want ownershipUnowned (%d): declared_untagged = %q governed an instance nothing declares", verdict, ownershipUnowned, verb)
			}
			if len(b.policyList) != 0 {
				t.Errorf("declared-quadrant outcome for an undeclared instance: %+v", b.policyList)
			}
			if len(b.unownedList) != 1 {
				t.Fatalf("Unowned entries = %+v, want exactly one", b.unownedList)
			}
			if !hasDiag(b.diags, SummaryOutsideEstate, "smoke-k8s/app-config") {
				t.Errorf("declared_untagged = %q quietened a finding about an object outside its quadrant:\n%s", verb, renderDiags(b.diags))
			}
			detail := b.unownedList[0].Detail
			for _, wrong := range []string{"proposes creating the resource this configuration declares", `declared_untagged = "adopt"`} {
				if strings.Contains(detail, wrong) {
					t.Errorf("the refusal says %q about an instance nothing declares:\n%s", wrong, detail)
				}
			}
		})
	}
}
