// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/replacecycle"
	"github.com/intentius/choudoufu/internal/live/newruntime"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// These tests are #2256 U7 (#2343): markers on the create half of a replace
// on the new runtime, for both replace orderings.
//
// The new runtime's execution graph (internal/engine/planning,
// execgraph_managed.go) gives the create half's ManagedFinalPlan no prior
// object in both orderings: delete-then-create plans the create before the
// delete runs, and create-before-destroy plans it before the prior object is
// deposed. So the apply engine adjusts it through the create entry point,
// and the planning engine plans the replace's create half with the
// CreateConfig value configadjust.Adjust computed for the same purpose
// (U4). These tests hold the engine to that, end to end: the object that
// replaces the old one carries the create entry point's marker in the
// applied state.
//
// Like U4's tests they call newEnginePlan and newEngineApply, below G's
// guard (#2339), which refuses any run with an adjuster until U6.

// replaceRun is what newRuntimeReplace saw.
type replaceRun struct {
	plan     *plans.Plan
	state    *states.State
	adjuster *createDistinguishingAdjuster
	ctx      *Context
	// applies lists each ApplyResourceChange call in order, as "create"
	// (null prior) or "delete" (null planned), each with the test_map the
	// provider was sent.
	applies []string
	sent    []map[string]cty.Value
}

// newRuntimeReplace plans and applies, on the new runtime with a
// create-distinguishing adjuster, a change to test_string that the
// provider says requires replacement. cbd sets create_before_destroy.
func newRuntimeReplace(t *testing.T, cbd bool) *replaceRun {
	t.Helper()
	forceExperimentalRuntimeForTesting(t)

	addr := mustResourceInstanceAddr("test_object.a")
	lifecycle := ""
	if cbd {
		lifecycle = "\n  lifecycle {\n    create_before_destroy = true\n  }\n"
	}
	src := fmt.Sprintf(`
resource "test_object" "a" {
  test_string = "new"
  test_map = {
    other = "from config"
  }%s
}
`, lifecycle)
	m := testModuleInline(t, map[string]string{"main.tf": src})

	prior := states.BuildState(func(ss *states.SyncState) {
		ss.SetResourceInstanceCurrent(
			addr,
			&states.ResourceInstanceObjectSrc{
				Status:    states.ObjectReady,
				AttrsJSON: []byte(`{"test_string":"old","test_map":{"other":"from config"}}`),
			},
			addrs.AbsProviderConfig{
				Provider: addrs.NewDefaultProvider("test"),
				Module:   addrs.RootModule,
			},
			addrs.NoKey,
		)
	})

	run := &replaceRun{adjuster: &createDistinguishingAdjuster{}}
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
		switch {
		case req.PriorState.IsNull():
			run.applies = append(run.applies, "create")
		case req.PlannedState.IsNull():
			run.applies = append(run.applies, "delete")
		default:
			run.applies = append(run.applies, "update")
		}
		var sent map[string]cty.Value
		if !req.PlannedState.IsNull() {
			if tm := req.PlannedState.GetAttr("test_map"); !tm.IsNull() && tm.IsKnown() {
				sent = tm.AsValueMap()
			}
		}
		run.sent = append(run.sent, sent)
		return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
	}

	run.ctx = testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ConfigValueAdjuster: run.adjuster,
	})

	plan, diags := run.ctx.newEnginePlan(context.Background(), m, prior, DefaultPlanOpts)
	assertNoErrors(t, diags)
	run.plan = plan

	newState, diags := run.ctx.newEngineApply(context.Background(), m, plan, InputValues{})
	assertNoErrors(t, diags)
	run.state = newState
	return run
}

// TestNewRuntimeReplaceMarksNewObject is the done-when of #2343: for each
// replace ordering, the new runtime plans a replace with the expected
// action, applies the delete and the create in that order, and the object
// that ends up current carries the marker the create entry point wrote.
// The prior object carried no marker, and the update entry point writes a
// different value, so the assertion can only pass if the create half was
// planned and final-planned through AdjustCreateConfigValue. Had the plan
// used the update value for the create half, the final plan would disagree
// with it and newEngineApply would fail as an inconsistent final plan.
func TestNewRuntimeReplaceMarksNewObject(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")
	for _, tc := range []struct {
		name    string
		cbd     bool
		action  plans.Action
		applies []string
	}{
		{"delete_then_create", false, plans.DeleteThenCreate, []string{"delete", "create"}},
		{"create_before_destroy", true, plans.CreateThenDelete, []string{"create", "delete"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := newRuntimeReplace(t, tc.cbd)

			rc := run.plan.Changes.ResourceInstance(addr)
			if rc == nil || rc.Action != tc.action {
				t.Fatalf("planned change for %s is %#v, want action %s; this test proves nothing without the replace", addr, rc, tc.action)
			}

			if fmt.Sprint(run.applies) != fmt.Sprint(tc.applies) {
				t.Fatalf("ApplyResourceChange calls were %v, want %v", run.applies, tc.applies)
			}
			for i, kind := range run.applies {
				if kind != "create" {
					continue
				}
				if v, ok := run.sent[i]["marker_key"]; !ok || !v.RawEquals(cty.StringVal("create-value")) {
					t.Errorf("the create half was sent test_map %#v, want the create entry point's marker", run.sent[i])
				}
			}

			rs := run.state.ResourceInstance(addr)
			if rs == nil || rs.Current == nil {
				t.Fatalf("no current object for %s in the applied state:\n%s", addr, run.state)
			}
			if len(rs.Deposed) != 0 {
				t.Errorf("the applied state still holds deposed objects: %v", rs.Deposed)
			}
			obj, err := rs.Current.Decode(simpleTestSchema().ImpliedType())
			if err != nil {
				t.Fatalf("decoding the applied object: %s", err)
			}
			if got := obj.Value.GetAttr("test_string"); !got.RawEquals(cty.StringVal("new")) {
				t.Errorf("the current object is not the new one: test_string = %#v", got)
			}
			tm := obj.Value.GetAttr("test_map")
			if tm.IsNull() || !tm.IsKnown() {
				t.Fatalf("applied test_map is null or unknown: %#v", tm)
			}
			got := tm.AsValueMap()
			if v, ok := got["marker_key"]; !ok || !v.RawEquals(cty.StringVal("create-value")) {
				t.Errorf("the new object does not carry the create entry point's marker: %#v", got)
			}
			if v, ok := got["other"]; !ok || !v.RawEquals(cty.StringVal("from config")) {
				t.Errorf("the new object lost the configured key: %#v", got)
			}

			// Plan asks both entry points (the update value decides whether
			// to replace; the create value plans the create half). The apply
			// engine's final plan of the create half asks the create one
			// again, with no prior object.
			if run.adjuster.creates < 2 {
				t.Errorf("expected the create entry point at plan and at the create half's final plan, got %d calls", run.adjuster.creates)
			}
		})
	}
}

// TestNewRuntimeReplaceNotRefusedInLiveMode: U7 lifts the replace refusal in
// live mode only. The post-plan check Plan and Apply run on the new runtime
// passes a replace when an adjuster is set (live mode), in both orderings.
// Outside live mode the create_before_destroy cycle row (upstream `#4714`)
// refuses the same plan only while the replacecycle fork port (#2380) is
// switched off.
func TestNewRuntimeReplaceNotRefusedInLiveMode(t *testing.T) {
	for _, cbd := range []bool{false, true} {
		t.Run(fmt.Sprintf("create_before_destroy=%v", cbd), func(t *testing.T) {
			run := newRuntimeReplace(t, cbd)
			if diags := run.ctx.newRuntimePostPlan(context.Background(), run.plan, nil); diags.HasErrors() {
				t.Errorf("a replace was refused in live mode: %s", diags.ErrWithWarnings())
			}
			t.Setenv(forkport.AllOff, "")
			t.Setenv(forkport.Switch(replacecycle.Name), "")
			if diags := newruntime.PostPlan(run.plan, false); diags.HasErrors() {
				t.Errorf("with the replacecycle port on, a replace was refused outside live mode: %s", diags.ErrWithWarnings())
			}
			t.Setenv(forkport.Switch(replacecycle.Name), "1")
			if diags := newruntime.PostPlan(run.plan, false); !hasDiag(diags, tfdiags.Error, newruntime.RefusalSummary) {
				t.Errorf("with the replacecycle port off, a replace was not refused outside live mode; diagnostics:\n%s", diags.ErrWithWarnings())
			}
		})
	}
}
