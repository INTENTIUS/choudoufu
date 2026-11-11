// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// These tests are #2256 U4 (#2340): the new runtime (internal/engine) calls
// the Context's ConfigValueAdjuster when it plans, and again when the apply
// engine re-plans each object from freshly evaluated configuration.
//
// G's guard (#2339, context_live_runtime_guard.go) refuses Plan and Apply on
// the new runtime whenever a ConfigValueAdjuster is set, until U6. These
// tests sit below the guard: they call newEnginePlan and newEngineApply, the
// functions Plan and Apply route to once the guard has passed, so they run
// the same engine code a live run will reach after U6 without a bypass in
// the guard itself.

// newRuntimeAdjustProvider is a provider that plans the proposed new value
// and applies the planned value, so whatever the adjuster wrote reaches the
// applied object only if both plan and apply carried it.
func newRuntimeAdjustProvider() *MockProvider {
	p := simpleMockProvider()
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
	}
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
	}
	return p
}

// newRuntimePlanAndApply plans and applies m on the new runtime with adjuster
// set, failing the test on any error diagnostic, and returns the applied
// test_map of addr.
func newRuntimePlanAndApply(t *testing.T, m *configs.Config, prior *states.State, p *MockProvider, adjuster ConfigValueAdjuster, addr addrs.AbsResourceInstance) map[string]cty.Value {
	t.Helper()
	forceExperimentalRuntimeForTesting(t)

	c := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ConfigValueAdjuster: adjuster,
	})

	plan, diags := c.newEnginePlan(context.Background(), m, prior, DefaultPlanOpts)
	assertNoErrors(t, diags)

	newState, diags := c.newEngineApply(context.Background(), m, plan, InputValues{})
	assertNoErrors(t, diags)

	rs := newState.ResourceInstance(addr)
	if rs == nil || rs.Current == nil {
		t.Fatalf("no applied object for %s in the new state:\n%s", addr, newState)
	}
	obj, err := rs.Current.Decode(simpleTestSchema().ImpliedType())
	if err != nil {
		t.Fatalf("decoding the applied object: %s", err)
	}
	got := obj.Value.GetAttr("test_map")
	if got.IsNull() || !got.IsKnown() {
		t.Fatalf("applied test_map is null or unknown: %#v", got)
	}
	return got.AsValueMap()
}

// TestNewRuntimeAdjusterMarksAppliedCreate is the done-when of #2340: a create
// applied on the new runtime carries the marker the adjuster wrote, in the
// applied object. Without the planning-side call the plan has no marker and
// the assertion below fails; without the applying-side call ManagedFinalPlan
// re-plans without it and ValidateFinalPlan reports an inconsistent final
// plan, so newEngineApply returns an error.
func TestNewRuntimeAdjusterMarksAppliedCreate(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")
	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
  test_map = {
    other = "from config"
  }
}
`,
	})
	p := newRuntimeAdjustProvider()
	adjuster := &stubConfigValueAdjuster{attr: "test_map", key: "marker_key", value: "adjuster-value"}

	got := newRuntimePlanAndApply(t, m, states.NewState(), p, adjuster, addr)

	if v, ok := got["marker_key"]; !ok || !v.RawEquals(cty.StringVal("adjuster-value")) {
		t.Errorf("applied test_map has no marker: %#v", got)
	}
	if v, ok := got["other"]; !ok || !v.RawEquals(cty.StringVal("from config")) {
		t.Errorf("applied test_map lost the configured key: %#v", got)
	}
	// Once at plan, once when the apply engine re-plans the create.
	if len(adjuster.calls) < 2 {
		t.Errorf("expected the adjuster to be asked at plan and at final plan, got %d calls: %v", len(adjuster.calls), adjuster.calls)
	}
	for _, call := range adjuster.calls {
		if !call.Equal(addr) {
			t.Errorf("adjuster asked for %s, want %s", call, addr)
		}
	}
	sent := p.ApplyResourceChangeRequest.PlannedState.GetAttr("test_map").AsValueMap()
	if v, ok := sent["marker_key"]; !ok || !v.RawEquals(cty.StringVal("adjuster-value")) {
		t.Errorf("ApplyResourceChange was not sent the marker: %#v", sent)
	}
}

// createDistinguishingAdjuster writes a different marker value through each
// entry point, so a test can tell which one produced the applied object.
type createDistinguishingAdjuster struct {
	updates, creates int
}

func (a *createDistinguishingAdjuster) set(config cty.Value, value string) cty.Value {
	elems := config.AsValueMap()
	m := map[string]cty.Value{}
	if v := elems["test_map"]; !v.IsNull() {
		for it := v.ElementIterator(); it.Next(); {
			k, e := it.Element()
			m[k.AsString()] = e
		}
	}
	m["marker_key"] = cty.StringVal(value)
	elems["test_map"] = cty.MapVal(m)
	return cty.ObjectVal(elems)
}

func (a *createDistinguishingAdjuster) AdjustConfigValue(_ context.Context, _ addrs.AbsResourceInstance, config cty.Value, _ providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	a.updates++
	return a.set(config, "update-value"), nil
}

func (a *createDistinguishingAdjuster) AdjustCreateConfigValue(_ context.Context, _ addrs.AbsResourceInstance, config cty.Value, _ providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	a.creates++
	return a.set(config, "create-value"), nil
}

var _ CreateConfigValueAdjuster = (*createDistinguishingAdjuster)(nil)

// TestNewRuntimeAdjusterCreateEntryPoint: an adjuster implementing
// CreateConfigValueAdjuster is asked through AdjustCreateConfigValue for an
// instance with no prior object, at plan and at the apply engine's final
// plan, and the applied object carries that entry point's value.
func TestNewRuntimeAdjusterCreateEntryPoint(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")
	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	})
	p := newRuntimeAdjustProvider()
	adjuster := &createDistinguishingAdjuster{}

	got := newRuntimePlanAndApply(t, m, states.NewState(), p, adjuster, addr)

	if v, ok := got["marker_key"]; !ok || !v.RawEquals(cty.StringVal("create-value")) {
		t.Errorf("applied test_map should carry the create entry point's marker: %#v", got)
	}
	if adjuster.creates < 2 || adjuster.updates != 0 {
		t.Errorf("expected create calls at plan and final plan and no update calls, got %d and %d", adjuster.creates, adjuster.updates)
	}
}

// TestNewRuntimeAdjusterMarksAppliedUpdate: an existing object without the
// marker is updated to carry it, through the update entry point, and the
// applied object has it.
func TestNewRuntimeAdjusterMarksAppliedUpdate(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")
	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
  test_map = {
    other = "from config"
  }
}
`,
	})
	prior := states.BuildState(func(ss *states.SyncState) {
		ss.SetResourceInstanceCurrent(
			addr,
			&states.ResourceInstanceObjectSrc{
				Status:    states.ObjectReady,
				AttrsJSON: []byte(`{"test_string":"foo","test_map":{"other":"from config"}}`),
			},
			addrs.AbsProviderConfig{
				Provider: addrs.NewDefaultProvider("test"),
				Module:   addrs.RootModule,
			},
			addrs.NoKey,
		)
	})
	p := newRuntimeAdjustProvider()
	adjuster := &createDistinguishingAdjuster{}

	got := newRuntimePlanAndApply(t, m, prior, p, adjuster, addr)

	if v, ok := got["marker_key"]; !ok || !v.RawEquals(cty.StringVal("update-value")) {
		t.Errorf("applied test_map should carry the update entry point's marker: %#v", got)
	}
	if !p.ApplyResourceChangeCalled {
		t.Errorf("expected an update to be applied")
	}
	if adjuster.updates < 2 {
		t.Errorf("expected update calls at plan and final plan, got %d", adjuster.updates)
	}
}
