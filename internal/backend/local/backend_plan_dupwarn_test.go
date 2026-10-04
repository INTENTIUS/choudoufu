// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// A provider's resource-level warning - the kubernetes provider's
// "Deprecated Resource" on kubernetes_daemonset, kubernetes_role and
// kubernetes_network_policy is the case that found this (epic #1885,
// reference-k8s-workloads' test_plan) - comes back from
// ValidateResourceConfig twice in one plan: once from the validate walk
// localRun runs first, and once from the plan walk's re-validation of the
// final config. Stock terraform prints it once per resource (its
// backend_plan.go merges the plan's diagnostics with
// AppendWithoutDuplicates); before this fix the fork printed it twice, so
// three deprecated blocks read "(and 5 more similar warnings elsewhere)"
// where terraform reads "(and 2 more ...)".
func TestLocal_planProviderValidateWarningPrintedOnce(t *testing.T) {
	b := TestLocal(t)
	b.OpValidation = true
	p := TestLocalProvider(t, b, "test", planFixtureSchema())
	calls := 0
	p.ValidateResourceConfigFn = func(req providers.ValidateResourceConfigRequest) providers.ValidateResourceConfigResponse {
		calls++
		var diags tfdiags.Diagnostics
		diags = diags.Append(tfdiags.WholeContainingBody(tfdiags.Warning, "Deprecated Resource", "use test_instance_v1"))
		return providers.ValidateResourceConfigResponse{Diagnostics: diags}
	}

	op, done := testOperationPlan(t, "./testdata/plan")
	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	<-run.Done()
	if run.Result != backend.OperationSuccess {
		t.Fatalf("plan operation failed")
	}
	out := done(t)
	all := out.Stdout() + out.Stderr()

	// The premise: the provider really was asked twice for the one block.
	if calls != 2 {
		t.Fatalf("ValidateResourceConfig called %d time(s), want 2 (validate walk + plan walk); the premise of this test no longer holds", calls)
	}
	if got := strings.Count(all, "Deprecated Resource"); got != 1 {
		t.Fatalf("the provider's warning for one resource printed %d time(s), want 1\n%s", got, all)
	}
}
