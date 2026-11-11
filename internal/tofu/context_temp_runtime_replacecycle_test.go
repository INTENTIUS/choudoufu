// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

// choudoufu fork port (#2380): remove when upstream #4714 lands.

import (
	"context"
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/replacecycle"
	"github.com/intentius/choudoufu/internal/live/newruntime"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// newRuntime4714 plans upstream #4714's repro on the new runtime: r2
// depended on r1 in state, r1 has left the configuration, and r2 is replaced
// with create_before_destroy newly enabled. The old runtime reports a cycle
// here. applies records each ApplyResourceChange as "create <new>" or
// "delete <old>", by test_string.
func newRuntime4714(t *testing.T) (*Context, *configs.Config, func() (*plans.Plan, tfdiags.Diagnostics), *[]string) {
	t.Helper()
	forceExperimentalRuntimeForTesting(t)

	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_object" "r2" {
  test_string = "new"
  lifecycle {
    create_before_destroy = true
  }
}
`})
	provider := addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule}
	prior := states.BuildState(func(ss *states.SyncState) {
		ss.SetResourceInstanceCurrent(mustResourceInstanceAddr("test_object.r1"), &states.ResourceInstanceObjectSrc{
			Status:    states.ObjectReady,
			AttrsJSON: []byte(`{"test_string":"r1"}`),
		}, provider, addrs.NoKey)
		ss.SetResourceInstanceCurrent(mustResourceInstanceAddr("test_object.r2"), &states.ResourceInstanceObjectSrc{
			Status:       states.ObjectReady,
			AttrsJSON:    []byte(`{"test_string":"old"}`),
			Dependencies: []addrs.ConfigResource{mustConfigResourceAddr("test_object.r1")},
		}, provider, addrs.NoKey)
	})

	var applies []string
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
			applies = append(applies, "create "+req.PlannedState.GetAttr("test_string").AsString())
		case req.PlannedState.IsNull():
			applies = append(applies, "delete "+req.PriorState.GetAttr("test_string").AsString())
		default:
			applies = append(applies, "update "+req.PriorState.GetAttr("test_string").AsString())
		}
		return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
	}
	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
	})
	plan := func() (*plans.Plan, tfdiags.Diagnostics) {
		return ctx.Plan(context.Background(), m, prior, DefaultPlanOpts)
	}
	return ctx, m, plan, &applies
}

// TestNewRuntimeReplaceCycle4714 is the port's proof on upstream #4714's
// repro: with the port on, the new runtime plans it without a cycle, the
// post-plan check admits the replace, and the apply creates the new r2,
// then deletes the old r2, then r1, recording create_before_destroy on the
// new r2. With the port's switch set, the gap table's replace-cbd-cycle row
// refuses the plan again.
func TestNewRuntimeReplaceCycle4714(t *testing.T) {
	t.Run("port on", func(t *testing.T) {
		t.Setenv(forkport.AllOff, "")
		t.Setenv(forkport.Switch(replacecycle.Name), "")
		ctx, m, plan, applies := newRuntime4714(t)
		p, diags := plan()
		assertNoErrors(t, diags)
		for addr, want := range map[string]plans.Action{"test_object.r1": plans.Delete, "test_object.r2": plans.CreateThenDelete} {
			rc := p.Changes.ResourceInstance(mustResourceInstanceAddr(addr))
			if rc == nil || rc.Action != want {
				t.Fatalf("planned change for %s is %#v, want %s", addr, rc, want)
			}
		}
		state, diags := ctx.Apply(context.Background(), p, m, nil)
		assertNoErrors(t, diags)
		if got, want := fmt.Sprint(*applies), "[create new delete old delete r1]"; got != want {
			t.Errorf("applied %s, want %s", got, want)
		}
		if state.ResourceInstance(mustResourceInstanceAddr("test_object.r1")) != nil {
			t.Errorf("r1 is still in state:\n%s", state)
		}
		r2 := state.ResourceInstance(mustResourceInstanceAddr("test_object.r2"))
		if r2 == nil || r2.Current == nil || len(r2.Deposed) != 0 {
			t.Fatalf("r2 is not one current object:\n%s", state)
		}
		if !r2.Current.CreateBeforeDestroy {
			t.Errorf("the new r2 does not record create_before_destroy")
		}
	})
	t.Run("port off", func(t *testing.T) {
		t.Setenv(forkport.Switch(replacecycle.Name), "1")
		_, _, plan, _ := newRuntime4714(t)
		_, diags := plan()
		if !hasDiag(diags, tfdiags.Error, newruntime.RefusalSummary) {
			t.Fatalf("with %s=1 the replace was not refused; diagnostics:\n%s", forkport.Switch(replacecycle.Name), diags.ErrWithWarnings())
		}
	})
}
