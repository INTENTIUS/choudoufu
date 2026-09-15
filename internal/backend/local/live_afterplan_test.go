// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statemgr"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// afterPlanRecordingStateless is [replaceRecordingStateless] with an
// AfterPlan that records what it was handed and refuses when told to.
type afterPlanRecordingStateless struct {
	replaceRecordingStateless
	refuse bool

	apMu          sync.Mutex
	afterPlanSeen *plans.Plan
	schemasSeen   bool
}

const afterPlanRefusalSummary = "Kubernetes API server rejected the planned object"

func (s *afterPlanRecordingStateless) AfterPlan(_ context.Context, _ *configs.Config, plan *plans.Plan, schemas *tofu.Schemas) tfdiags.Diagnostics {
	s.apMu.Lock()
	defer s.apMu.Unlock()
	s.afterPlanSeen = plan
	s.schemasSeen = schemas != nil
	if s.refuse {
		return tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, afterPlanRefusalSummary, "the server said no"))
	}
	return nil
}

func afterPlanFixture(t *testing.T, refuse bool) (*Local, *afterPlanRecordingStateless, *int) {
	t.Helper()
	b := TestLocal(t)
	p := TestLocalProvider(t, b, "test", applyFixtureSchema())
	applied := new(int)
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		*applied++
		return providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"id":  cty.StringVal("new"),
			"ami": req.PlannedState.GetAttr("ami"),
		})}
	}
	prior := states.NewState()
	stateless := &afterPlanRecordingStateless{
		replaceRecordingStateless: replaceRecordingStateless{
			mgr:   statemgr.NewFullFake(statemgr.NewTransientInMemory(nil), prior.DeepCopy()),
			prior: prior,
		},
		refuse: refuse,
	}
	b.Stateless = stateless
	return b, stateless, applied
}

// TestAfterPlanRefusalStopsTheApply (GitHub issue #1081, item 3): an
// AfterPlan error on an apply is asked once the plan exists, before the
// plan is rendered, and stops the operation with nothing applied and no
// WriteBack - the live system has already refused what the plan proposes.
func TestAfterPlanRefusalStopsTheApply(t *testing.T) {
	b, stateless, applied := afterPlanFixture(t, true)

	op, done := testOperationApply(t, "./testdata/apply")
	op.PlanRefresh = false
	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("starting the apply: %s", err)
	}
	<-run.Done()
	output := done(t)

	if run.Result != backend.OperationFailure {
		t.Fatalf("the apply succeeded past an AfterPlan refusal:\nstdout:\n%s", output.Stdout())
	}
	stateless.apMu.Lock()
	seen, schemasSeen := stateless.afterPlanSeen, stateless.schemasSeen
	stateless.apMu.Unlock()
	if seen == nil || !schemasSeen {
		t.Fatal("AfterPlan was not handed the plan and the schemas")
	}
	if len(seen.Changes.Resources) == 0 {
		t.Fatal("AfterPlan was handed a plan with no changes; the fixture plans one create")
	}
	if *applied != 0 {
		t.Errorf("the provider applied %d changes after the refusal; nothing may be applied", *applied)
	}
	stateless.mu.Lock()
	wb := stateless.writeBackCalled
	stateless.mu.Unlock()
	if wb {
		t.Error("WriteBack ran after an AfterPlan refusal")
	}
	if !strings.Contains(output.Stderr(), afterPlanRefusalSummary) {
		t.Errorf("the refusal was not reported:\nstderr:\n%s", output.Stderr())
	}
	if strings.Contains(output.Stdout(), "will be created") {
		t.Errorf("the plan was rendered despite the refusal:\n%s", output.Stdout())
	}
}

// TestAfterPlanRefusalStopsThePlan: the same on a plan-only operation -
// the plan is not rendered and the operation fails.
func TestAfterPlanRefusalStopsThePlan(t *testing.T) {
	b, stateless, _ := afterPlanFixture(t, true)

	op, done := testOperationPlan(t, "./testdata/plan")
	op.PlanRefresh = false
	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("starting the plan: %s", err)
	}
	<-run.Done()
	output := done(t)

	if run.Result != backend.OperationFailure {
		t.Fatalf("the plan succeeded past an AfterPlan refusal:\nstdout:\n%s", output.Stdout())
	}
	stateless.apMu.Lock()
	seen := stateless.afterPlanSeen
	stateless.apMu.Unlock()
	if seen == nil {
		t.Fatal("AfterPlan was never asked")
	}
	if !strings.Contains(output.Stderr(), afterPlanRefusalSummary) {
		t.Errorf("the refusal was not reported:\nstderr:\n%s", output.Stderr())
	}
	if strings.Contains(output.Stdout(), "will be created") {
		t.Errorf("the plan was rendered despite the refusal:\n%s", output.Stdout())
	}
}

// TestAfterPlanWithoutRefusalIsInert: AfterPlan returning nothing changes
// nothing about the apply, which still applies and still writes back.
func TestAfterPlanWithoutRefusalIsInert(t *testing.T) {
	b, stateless, applied := afterPlanFixture(t, false)

	op, done := testOperationApply(t, "./testdata/apply")
	op.PlanRefresh = false
	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("starting the apply: %s", err)
	}
	<-run.Done()
	output := done(t)
	if run.Result != backend.OperationSuccess {
		t.Fatalf("the apply failed:\nstdout:\n%s\nstderr:\n%s", output.Stdout(), output.Stderr())
	}
	if *applied == 0 {
		t.Error("nothing was applied")
	}
	stateless.mu.Lock()
	wb := stateless.writeBackCalled
	stateless.mu.Unlock()
	if !wb {
		t.Error("WriteBack did not run")
	}
}
