// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"
)

// TestListTruncationDoesNotSilentlyProposeCreate is GitHub issue #1046's
// mechanism, isolated from real AWS and from the emulator.
//
// #1046's own hypothesis was that IAM's ListPolicies pages at 100 and
// something reads only the first page. That is not what terraform-provider-
// aws v6.59.0 does: internal/service/iam/policy_list.go's listPolicies (and
// role_list.go's listNonServiceLinkedRoles, and ecs/service_list.go's
// listServices) all walk their AWS SDK paginator to HasMorePages() ==
// false, so an ordinary multi-page list is not the defect - see this unit's
// own report for the verified source.
//
// The real defect is one page (or one per-object read, such as
// GetPolicyVersion or GetPolicyVersion's ECS/IAM-role analogues) FAILING
// mid-enumeration for any reason other than NotFound - a throttle is the
// likely real-world trigger, and this fixture models any such cause the
// same way, since the shape at the wire is identical regardless of cause.
// aws_iam_policy's and aws_iam_role's list resources respond to that by
// yielding fwdiag.NewListResultErrorDiagnostic(err) - a bare list.ListResult
// carrying only an error diagnostic, no identity, no resource - and then
// returning, which ends the Go range loop over the paginator (and so the
// whole gRPC ListResource stream) for good. Every object that a later page
// would have produced is simply never sent; nothing in the wire protocol
// distinguishes that from "the account genuinely has no more policies."
// (aws_ecs_service's list resource does NOT share this shape: its
// per-object read failure path is a plain `continue`, not yield+return -
// see the report.)
//
// fakeCloud.truncateAfter reproduces the wire shape exactly: after N
// matching objects, emit ONE event carrying only an error diagnostic, then
// stop - see its own doc comment on fakeCloud.truncateAt.
func TestListTruncationDoesNotSilentlyProposeCreate(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")

	// "before" sits ahead of the simulated failure and is found normally.
	cloud.own("aws_iam_policy", "before-policy", `aws_iam_policy.before`)
	// Filler standing in for the rest of a large account's unrelated
	// policies - real accounts hold well over 100 (issue #1046's own
	// account did), a small fixture-friendly number here stands in for the
	// same shape: objects the estate does not own, sitting between the
	// truncation point and "beyond".
	cloud.obj("aws_iam_policy", "filler-policy", nil)
	// "beyond" sits AFTER the simulated failure and is this test's whole
	// point: a real, tagged, estate-owned policy the plan must not treat as
	// absent.
	cloud.own("aws_iam_policy", "beyond-policy", `aws_iam_policy.beyond`)

	// Truncate after the first matching object ("before-policy") - "filler-
	// policy" and "beyond-policy" are never sent.
	cloud.truncateAfter("aws_iam_policy", 1)

	cfg := loadConfig(t, "testdata/iam-policy-pagination")
	req := Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
	}

	res, diags := Discover(context.Background(), req)

	beforeBinding, beforeOK := res.BindingFor(mustAddr(t, "aws_iam_policy.before"))
	if !beforeOK || beforeBinding.ImportID != "before-policy" {
		t.Fatalf("aws_iam_policy.before (ahead of the simulated failure) did not bind to before-policy: ok=%v binding=%+v\n%s", beforeOK, beforeBinding, res)
	}

	beyondBinding, beyondOK := res.BindingFor(mustAddr(t, "aws_iam_policy.beyond"))
	t.Logf("diags.HasErrors()=%v\n%s", diags.HasErrors(), renderDiags(diags))
	t.Logf("res.Unbound=%v", res.Unbound)

	if beyondOK {
		t.Fatalf("aws_iam_policy.beyond bound to %+v even though the fixture's list stream never sent it past the simulated failure - the test fixture itself is wrong if this passes", beyondBinding)
	}

	if !diags.HasErrors() {
		t.Errorf("RED: Discover reported no errors even though aws_iam_policy's listing stopped mid-account with a bare error placeholder and never reached aws_iam_policy.beyond's live object. "+
			"aws_iam_policy.beyond is now unbound (res.Unbound=%v) with nothing telling the caller the type's listing was INCOMPLETE rather than exhaustive - a live-plan built on this Result proposes creating a policy that already exists and already carries this estate's markers.", res.Unbound)
	}
}

// TestSilentListTruncationProposesCreate is
// [TestListTruncationDoesNotSilentlyProposeCreate]'s companion: what happens
// when the stream truncates with NO diagnostic of any kind, not even the
// bare error placeholder terraform-provider-aws v6.59.0 actually sends.
// Nothing in that provider's source (verified for aws_iam_policy,
// aws_iam_role and aws_ecs_service - see this unit's report) produces this
// shape; it is here to answer a narrower question than #1046 itself asks -
// does discovery have ANY defense left if a provider (this one, a future
// version of it, or a different one) ever gives no signal at all - and to
// keep that question answered by a value rather than by argument.
func TestSilentListTruncationProposesCreate(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")
	cloud.own("aws_iam_policy", "before-policy", `aws_iam_policy.before`)
	cloud.obj("aws_iam_policy", "filler-policy", nil)
	cloud.own("aws_iam_policy", "beyond-policy", `aws_iam_policy.beyond`)
	cloud.truncateAfterSilently("aws_iam_policy", 1)

	cfg := loadConfig(t, "testdata/iam-policy-pagination")
	req := Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
	}

	res, diags := Discover(context.Background(), req)

	_, beyondOK := res.BindingFor(mustAddr(t, "aws_iam_policy.beyond"))
	t.Logf("diags.HasErrors()=%v render=%s", diags.HasErrors(), renderDiags(diags))
	t.Logf("res.Unbound=%v res.Problems=%v", res.Unbound, res.Problems)

	if beyondOK {
		t.Fatalf("aws_iam_policy.beyond bound even though the fixture's stream never sent it - the fixture itself is wrong if this passes")
	}
	if diags.HasErrors() {
		t.Skipf("discovery already refuses a silent truncation with no per-object signal at all (diags.HasErrors()=true) - no gap to document here; this run's diagnostics: %s", renderDiags(diags))
	}
	t.Logf("CONFIRMED GAP (out of #1046's scope: terraform-provider-aws v6.59.0 never produces this shape): a completely silent list truncation is indistinguishable from an exhaustive one. aws_iam_policy.beyond is unbound with no error and no warning; a live-plan built on this Result silently proposes creating it. This is a live provider contract to document, not a fix to make here - see the report.")
}
