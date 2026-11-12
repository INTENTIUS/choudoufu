// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/cancel"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

// stopAfterCreateHook stands in for internal/command's e2e crash hook
// (apply_e2etesting_crash.go): the instant the create half of a replace
// returns, it stops the run the way that hook's self-sent SIGTERM does
// through cmd/choudoufu's makeShutdownCh, and returns only once the stop
// has landed. It also records each PreApply it sees, which is where the
// CLI prints "Destroying...".
type stopAfterCreateHook struct {
	NilHook
	ctx     *Context
	once    sync.Once
	stopped bool

	mu         sync.Mutex
	preApplies []string
}

func (h *stopAfterCreateHook) PreApply(addr addrs.AbsResourceInstance, gen states.Generation, action plans.Action, _, _ cty.Value) (HookAction, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	g := "current"
	if gen != addrs.CurrentResourceInstanceObjectGeneration {
		g = fmt.Sprintf("deposed %v", gen)
	}
	h.preApplies = append(h.preApplies, fmt.Sprintf("%s %s %s", addr, g, action))
	return HookActionContinue, nil
}

func (h *stopAfterCreateHook) PostApply(addr addrs.AbsResourceInstance, gen states.Generation, newState cty.Value, applyErr error) (HookAction, error) {
	if gen != addrs.CurrentResourceInstanceObjectGeneration || applyErr != nil || newState.IsNull() {
		return HookActionContinue, nil
	}
	h.once.Do(func() {
		go h.ctx.Stop()
		deadline := time.Now().Add(5 * time.Second)
		for !h.ctx.sh.Stopped() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		h.stopped = h.ctx.sh.Stopped()
	})
	return HookActionContinue, nil
}

type stoppedReplaceRun struct {
	applies    []string
	preApplies []string
	state      *states.State
	halted     bool
	stopped    bool
}

// newRuntimeStoppedReplace applies, on the new runtime, a
// create_before_destroy replace of test_object.a that is stopped as soon as
// the create half returns.
func newRuntimeStoppedReplace(t *testing.T) *stoppedReplaceRun {
	t.Helper()
	forceExperimentalRuntimeForTesting(t)

	addr := mustResourceInstanceAddr("test_object.a")
	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_object" "a" {
  test_string = "new"
  lifecycle {
    create_before_destroy = true
  }
}
`})
	prior := states.BuildState(func(ss *states.SyncState) {
		ss.SetResourceInstanceCurrent(
			addr,
			&states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{"test_string":"old"}`)},
			addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule},
			addrs.NoKey,
		)
	})

	run := &stoppedReplaceRun{}
	var mu sync.Mutex
	p := simpleMockProvider()
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		resp := providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
		if !req.PriorState.IsNull() && !req.ProposedNewState.IsNull() &&
			!req.PriorState.GetAttr("test_string").RawEquals(req.ProposedNewState.GetAttr("test_string")) {
			resp.RequiresReplace = []cty.Path{cty.GetAttrPath("test_string")}
		}
		return resp
	}
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case req.PriorState.IsNull():
			run.applies = append(run.applies, "create")
		case req.PlannedState.IsNull():
			run.applies = append(run.applies, "delete")
		default:
			run.applies = append(run.applies, "update")
		}
		return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
	}

	hook := &stopAfterCreateHook{}
	ctx := testContext2(t, &ContextOpts{
		Hooks: []Hook{hook},
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
	})
	hook.ctx = ctx

	plan, diags := ctx.Plan(context.Background(), m, prior, DefaultPlanOpts)
	assertNoErrors(t, diags)
	if rc := plan.Changes.ResourceInstance(addr); rc == nil || rc.Action != plans.CreateThenDelete {
		t.Fatalf("planned change for %s is %#v, want %s; this test proves nothing without the create_before_destroy replace", addr, rc, plans.CreateThenDelete)
	}

	state, diags := ctx.Apply(context.Background(), plan, m, nil)
	for _, d := range diags {
		if d.Description().Summary == "execution halted" {
			run.halted = true
		}
	}
	run.state = state
	hook.mu.Lock()
	run.preApplies = append(run.preApplies, hook.preApplies...)
	hook.mu.Unlock()
	run.stopped = hook.stopped
	if !run.stopped {
		t.Fatal("the hook never stopped the run after the create half returned; this test proves nothing without the stop")
	}
	return run
}

// TestNewRuntimeStopBetweenCreateAndDeposedDestroy is the new runtime's half
// of the gauntlet's day2_crash window (#2256): a stop that lands after a
// create_before_destroy replace's create has returned and before the
// deposed object's destroy has started leaves the destroy undispatched, as
// the default runtime's stop hook does. The new object is current and the
// old one is deposed in the returned state, which is what the next plan's
// one deposed-object destroy recovers from.
func TestNewRuntimeStopBetweenCreateAndDeposedDestroy(t *testing.T) {
	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(cancel.Name), "")
	run := newRuntimeStoppedReplace(t)

	if got, want := fmt.Sprint(run.applies), "[create]"; got != want {
		t.Fatalf("ApplyResourceChange calls were %s, want %s: the deposed object's destroy was dispatched after the stop", got, want)
	}
	for _, pa := range run.preApplies {
		if pa != "test_object.a current Create" {
			t.Errorf("PreApply fired for %q after the stop; the CLI would print Destroying for it", pa)
		}
	}
	if !run.halted {
		t.Error("the stopped apply did not report execution halted")
	}

	rs := run.state.ResourceInstance(mustResourceInstanceAddr("test_object.a"))
	if rs == nil || rs.Current == nil {
		t.Fatalf("no current object for test_object.a after the stopped apply:\n%s", run.state)
	}
	cur, err := rs.Current.Decode(simpleTestSchema().ImpliedType())
	if err != nil {
		t.Fatal(err)
	}
	if got := cur.Value.GetAttr("test_string"); !got.RawEquals(cty.StringVal("new")) {
		t.Errorf("the current object is not the new one: test_string = %#v", got)
	}
	if len(rs.Deposed) != 1 {
		t.Fatalf("the stopped apply left %d deposed objects, want the old one:\n%s", len(rs.Deposed), run.state)
	}
	for _, d := range rs.Deposed {
		obj, err := d.Decode(simpleTestSchema().ImpliedType())
		if err != nil {
			t.Fatal(err)
		}
		if got := obj.Value.GetAttr("test_string"); !got.RawEquals(cty.StringVal("old")) {
			t.Errorf("the deposed object is not the old one: test_string = %#v", got)
		}
	}
}

// TestNewRuntimeStopBetweenCreateAndDeposedDestroyPortOff holds the switch:
// with the cancel port off the engine does not look at the stop between
// operations, so the destroy still runs, as before the port.
func TestNewRuntimeStopBetweenCreateAndDeposedDestroyPortOff(t *testing.T) {
	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(cancel.Name), "1")
	run := newRuntimeStoppedReplace(t)
	if got, want := fmt.Sprint(run.applies), "[create delete]"; got != want {
		t.Fatalf("with the cancel port off ApplyResourceChange calls were %s, want %s", got, want)
	}
}
