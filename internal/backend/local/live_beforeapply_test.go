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
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statemgr"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// beforeApplyRecordingStateless is [replaceRecordingStateless] with a
// BeforeApply that counts its calls and refuses when told to.
type beforeApplyRecordingStateless struct {
	replaceRecordingStateless
	refuse bool

	baMu  sync.Mutex
	calls int
}

const beforeApplyRefusalSummary = "The record store bucket fails its versioning assertion"

func (s *beforeApplyRecordingStateless) BeforeApply(context.Context) tfdiags.Diagnostics {
	s.baMu.Lock()
	defer s.baMu.Unlock()
	s.calls++
	if s.refuse {
		return tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, beforeApplyRefusalSummary, "versioning has never been enabled"))
	}
	return nil
}

func beforeApplyFixture(t *testing.T, refuse bool) (*Local, *beforeApplyRecordingStateless, *int) {
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
	stateless := &beforeApplyRecordingStateless{
		replaceRecordingStateless: replaceRecordingStateless{
			mgr:   statemgr.NewFullFake(statemgr.NewTransientInMemory(nil), prior.DeepCopy()),
			prior: prior,
		},
		refuse: refuse,
	}
	b.Stateless = stateless
	return b, stateless, applied
}

// TestBeforeApplyRefusalStopsTheApply (GitHub issue #1339): a BeforeApply
// error lands after the plan and before the first change, so the provider
// applies nothing and no record is written back.
func TestBeforeApplyRefusalStopsTheApply(t *testing.T) {
	b, stateless, applied := beforeApplyFixture(t, true)

	op, done := testOperationApply(t, "./testdata/apply")
	op.PlanRefresh = false
	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("starting the apply: %s", err)
	}
	<-run.Done()
	output := done(t)

	if run.Result != backend.OperationFailure {
		t.Fatalf("the apply succeeded past a BeforeApply refusal:\nstdout:\n%s", output.Stdout())
	}
	if stateless.calls != 1 {
		t.Errorf("BeforeApply was called %d times, want 1", stateless.calls)
	}
	if *applied != 0 {
		t.Errorf("the provider applied %d changes after the refusal; nothing may be applied", *applied)
	}
	stateless.mu.Lock()
	wb := stateless.writeBackCalled
	stateless.mu.Unlock()
	if wb {
		t.Error("WriteBack ran after a BeforeApply refusal")
	}
	if !strings.Contains(output.Stderr(), beforeApplyRefusalSummary) {
		t.Errorf("the refusal was not reported:\nstderr:\n%s", output.Stderr())
	}
}

// TestBeforeApplyAllowsTheApply is the control: asked exactly once, and with
// no refusal the apply goes through. Without it the test above would pass
// against a backend that failed every apply.
func TestBeforeApplyAllowsTheApply(t *testing.T) {
	b, stateless, applied := beforeApplyFixture(t, false)

	op, done := testOperationApply(t, "./testdata/apply")
	op.PlanRefresh = false
	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("starting the apply: %s", err)
	}
	<-run.Done()
	output := done(t)

	if run.Result != backend.OperationSuccess {
		t.Fatalf("the apply failed with nothing refusing it:\nstderr:\n%s", output.Stderr())
	}
	if stateless.calls != 1 {
		t.Errorf("BeforeApply was called %d times, want 1", stateless.calls)
	}
	if *applied == 0 {
		t.Error("nothing was applied")
	}
}

// TestBeforeApplyIsNotAskedOnAPlan is the ruling on #1339 held as a test:
// the bucket's assertions do not run on every plan.
func TestBeforeApplyIsNotAskedOnAPlan(t *testing.T) {
	b, stateless, _ := beforeApplyFixture(t, true)

	// The apply fixture, planned: testdata/plan carries a block this
	// fixture's schema does not, and the plan would fail on rendering it.
	op, done := testOperationPlan(t, "./testdata/apply")
	op.PlanRefresh = false
	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("starting the plan: %s", err)
	}
	<-run.Done()
	output := done(t)

	if run.Result != backend.OperationSuccess {
		t.Fatalf("a plan failed on a BeforeApply refusal it should never have asked for:\nstderr:\n%s", output.Stderr())
	}
	if stateless.calls != 0 {
		t.Errorf("BeforeApply was called %d times on a plan-only operation, want 0", stateless.calls)
	}
}
