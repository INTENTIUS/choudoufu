// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"

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

func TestAppendWithoutDuplicates(t *testing.T) {
	at := func(line int) *hcl.Diagnostic {
		return &hcl.Diagnostic{
			Severity: hcl.DiagWarning,
			Summary:  "Deprecated Resource",
			Detail:   "use the _v1 type",
			Subject:  &hcl.Range{Filename: "main.tf", Start: hcl.Pos{Line: line, Column: 1}, End: hcl.Pos{Line: line, Column: 9}},
		}
	}
	var validate tfdiags.Diagnostics
	validate = validate.Append(at(1), at(5))

	var plan tfdiags.Diagnostics
	plan = plan.Append(at(1), at(5), at(9))
	errAt1 := *at(1)
	errAt1.Severity = hcl.DiagError
	plan = plan.Append(&errAt1)

	got := appendWithoutDuplicates(validate, plan)
	if len(got) != 4 {
		t.Fatalf("got %d diagnostics, want 4 (two from validate, line 9's warning, and the error at line 1 whose severity differs)", len(got))
	}
	if got[2].Source().Subject.Start.Line != 9 || got[3].Severity() != tfdiags.Error {
		t.Fatalf("unexpected merge result: %#v", got)
	}

	// Duplicates within the appended list itself are not the merge's business.
	if got := appendWithoutDuplicates(nil, tfdiags.Diagnostics{plan[0], plan[0]}); len(got) != 2 {
		t.Fatalf("got %d, want 2: only diagnostics already held are deduplicated against", len(got))
	}
}
