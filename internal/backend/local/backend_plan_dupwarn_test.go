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
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statemgr"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// A provider's resource-level warning - the kubernetes provider's
// "Deprecated Resource" on its unversioned aliases is the case that found
// this (epic #1885) - comes back from ValidateResourceConfig twice in one
// plan: once from the validate walk localRun runs first, and once from the
// plan walk's re-validation of the final config.
//
// With no live block the fork is stock OpenTofu, and stock OpenTofu 1.13.0
// prints it twice: four deprecated blocks read "(and 7 more similar
// warnings elsewhere)" in k8s-stock-when-you-need-it's oracle, and the
// fork printing "(and 3 more ...)" there is #1925. Under a live block the
// fork merges with stock terraform's AppendWithoutDuplicates (its
// backend_plan.go), once per resource, which reference-k8s-workloads'
// test_plan compares against.
func TestLocal_planProviderValidateWarningCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		live bool
		want int
	}{
		{"no live block prints it as stock OpenTofu does, twice", false, 2},
		{"a live run prints it once, as stock terraform does", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := TestLocal(t)
			b.OpValidation = true
			p := TestLocalProvider(t, b, "test", planFixtureSchema())
			if tc.live {
				prior := states.NewState()
				b.LiveRun = &replaceRecordingLiveRun{
					mgr:   statemgr.NewFullFake(statemgr.NewTransientInMemory(nil), prior.DeepCopy()),
					prior: prior,
				}
			}
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
			if got := strings.Count(all, "Deprecated Resource"); got != tc.want {
				t.Fatalf("the provider's warning for one resource printed %d time(s), want %d\n%s", got, tc.want, all)
			}
		})
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
