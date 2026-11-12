// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/datasources"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// choudoufu fork port (#2414): remove when upstream #4058 and #4740 land.

// recordingAdjuster is a ConfigValueAdjuster that changes nothing and
// records each address it is asked about.
type recordingAdjuster struct {
	mu    sync.Mutex
	calls []string
}

func (a *recordingAdjuster) AdjustConfigValue(_ context.Context, addr addrs.AbsResourceInstance, config cty.Value, _ providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, addr.String())
	return config, nil
}

// TestNewRuntimeDataSourcesLeftAloneByLiveSeams: on the new runtime with the
// datasources port, data sources reach none of live mode's seams, as on the
// default runtime: the marker adjuster, the identity resolver and the
// per-instance record hook see only the managed resource, and the in-run
// estates port leaves an ordinary data block to the datasources port. One
// data block is read during the plan and one, whose configuration uses a
// value known only after the apply, is deferred to it; both end in the
// state.
func TestNewRuntimeDataSourcesLeftAloneByLiveSeams(t *testing.T) {
	showNewRuntimeWarningForTesting(t)
	forceExperimentalRuntimeForTesting(t)
	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(datasources.Name), "")

	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_instance" "a" {
  ami = "x"
}

data "test_data_source" "known" {
  foo = "k"
}

data "test_data_source" "deferred" {
  foo = test_instance.a.id
}
`})
	p := testProvider("test")
	p.PlanResourceChangeFn = testDiffFn
	p.ApplyResourceChangeFn = testApplyFn
	p.ReadDataSourceFn = func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
		v := req.Config.AsValueMap()
		v["id"] = cty.StringVal("read-" + v["foo"].AsString())
		return providers.ReadDataSourceResponse{State: cty.ObjectVal(v)}
	}
	adjuster := &recordingAdjuster{}
	resolver := &stubResourceIdentityResolver{}
	hook := &instanceStateRecorder{}
	newCtx := func() *Context {
		return testContext2(t, &ContextOpts{
			Hooks: []Hook{hook},
			Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
				addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
			}, nil),
			ConfigValueAdjuster:      adjuster,
			ResourceIdentityResolver: resolver,
			InRunEstates:             map[string]addrs.Module{"net": {"net"}},
		})
	}

	plan, diags := newCtx().Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	assertNoErrors(t, diags)
	if !hasNewRuntimeWarning(diags) {
		t.Fatalf("the plan did not run on the new runtime; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	known := mustResourceInstanceAddr("data.test_data_source.known")
	deferred := mustResourceInstanceAddr("data.test_data_source.deferred")
	if rc := plan.Changes.ResourceInstance(known); rc != nil {
		t.Errorf("%s has a planned %s; it is read during the plan", known, rc.Action)
	}
	if plan.PriorState.ResourceInstance(known) == nil {
		t.Errorf("%s is not in the plan's refreshed state", known)
	}
	if rc := plan.Changes.ResourceInstance(deferred); rc == nil || rc.Action != plans.Read || rc.ActionReason != plans.ResourceInstanceReadBecauseConfigUnknown {
		t.Fatalf("%s is not deferred to the apply as a Read because its configuration is unknown: %#v", deferred, rc)
	}

	hook.reset()
	state, diags := newCtx().Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)
	if !hasNewRuntimeWarning(diags) {
		t.Fatalf("the apply did not run on the new runtime; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	for _, addr := range []addrs.AbsResourceInstance{known, deferred} {
		if rs := state.ResourceInstance(addr); rs == nil || rs.Current == nil {
			t.Errorf("%s is not in the applied state:\n%s", addr, state)
		}
	}

	for _, call := range adjuster.calls {
		if call != "test_instance.a" {
			t.Errorf("the marker adjuster was asked about %s; data sources carry no marker", call)
		}
	}
	if len(adjuster.calls) == 0 {
		t.Errorf("the marker adjuster was never called; the test proves nothing")
	}
	for _, call := range resolver.calls {
		if call.Resource.Resource.Mode != addrs.ManagedResourceMode {
			t.Errorf("the identity resolver was asked about %s; data sources have no identity to resolve", call)
		}
	}
	if len(resolver.calls) == 0 {
		t.Errorf("the identity resolver was never called; the test proves nothing")
	}
	for addr := range hook.calls {
		if addr != "test_instance.a" {
			t.Errorf("the per-instance record hook fired for %s; data sources carry no record", addr)
		}
	}
	if len(hook.calls) == 0 {
		t.Errorf("the per-instance record hook never fired; the test proves nothing")
	}
}
