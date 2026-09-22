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
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// withholdingAdjuster is the shape internal/live/projection's NodeResolver
// has for a tag_on_create false type (GitHub issues #1084, #1512): the
// update path writes a marker key into test_map, the create path leaves the
// configuration as written, and the applied-marker writer records every
// call it is given.
type withholdingAdjuster struct {
	mu      sync.Mutex
	writes  []plans.Action
	creates int
}

func (a *withholdingAdjuster) AdjustConfigValue(_ context.Context, _ addrs.AbsResourceInstance, config cty.Value, _ providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	elems := config.AsValueMap()
	m := map[string]cty.Value{}
	if v := elems["test_map"]; !v.IsNull() {
		for it := v.ElementIterator(); it.Next(); {
			k, e := it.Element()
			m[k.AsString()] = e
		}
	}
	m["marker"] = cty.StringVal("stamped")
	elems["test_map"] = cty.MapVal(m)
	return cty.ObjectVal(elems), nil
}

func (a *withholdingAdjuster) AdjustCreateConfigValue(_ context.Context, _ addrs.AbsResourceInstance, config cty.Value, _ providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	a.mu.Lock()
	a.creates++
	a.mu.Unlock()
	return config, nil
}

func (a *withholdingAdjuster) WriteAppliedMarkers(_ context.Context, _ addrs.AbsResourceInstance, _ addrs.AbsProviderConfig, action plans.Action, applied cty.Value, _ providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	a.mu.Lock()
	a.writes = append(a.writes, action)
	a.mu.Unlock()
	return applied, nil
}

// The stub is found by type assertion at the call site, so a signature that
// drifts from the interface makes it silently absent rather than a build
// error: the writes assertion below then reads zero calls. These make the
// drift a compile error instead.
var (
	_ CreateConfigValueAdjuster = (*withholdingAdjuster)(nil)
	_ AppliedMarkerWriter       = (*withholdingAdjuster)(nil)
)

// TestContext2Apply_createConfigValueAdjusterOnReplace is GitHub issue
// #1512. A replace's create half must be planned with the value
// AdjustCreateConfigValue gives, because that is the value the apply-time
// re-plan uses once the prior object is gone (delete-then-create) or
// deposed (create_before_destroy); planned with the update's value, the two
// disagree and core fails the apply with "Provider produced inconsistent
// final plan" after the old object is already destroyed. The markers the
// create withheld are then written once, after the create. An
// in-place update still plans with AdjustConfigValue's value and writes
// nothing afterwards.
func TestContext2Apply_createConfigValueAdjusterOnReplace(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")

	for _, tc := range []struct {
		name       string
		lifecycle  string
		config     string
		wantAction plans.Action
	}{
		{"delete-then-create", "", "bar", plans.DeleteThenCreate},
		{"create-before-destroy", "lifecycle {\n    create_before_destroy = true\n  }", "bar", plans.CreateThenDelete},
		{"update", "", "foo", plans.Update},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testModuleInline(t, map[string]string{
				"main.tf": `
resource "test_object" "a" {
  test_string = "` + tc.config + `"
  test_number = 2
  ` + tc.lifecycle + `
}
`,
			})

			p := simpleMockProvider()
			var applyConfigs []cty.Value
			var mu sync.Mutex
			p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
				resp := providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
				if !req.PriorState.IsNull() && !req.ProposedNewState.IsNull() && !req.PriorState.GetAttr("test_string").RawEquals(req.ProposedNewState.GetAttr("test_string")) {
					resp.RequiresReplace = []cty.Path{cty.GetAttrPath("test_string")}
				}
				return resp
			}
			p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
				if !req.PlannedState.IsNull() {
					mu.Lock()
					// The planned state, not req.Config: apply re-evaluates
					// the configuration without the adjuster, and the
					// provider writes what it planned.
					applyConfigs = append(applyConfigs, req.PlannedState)
					mu.Unlock()
				}
				return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
			}

			s := states.BuildState(func(ss *states.SyncState) {
				ss.SetResourceInstanceCurrent(addr,
					&states.ResourceInstanceObjectSrc{
						Status:    states.ObjectReady,
						AttrsJSON: []byte(`{"test_string":"foo","test_number":1,"test_map":{"marker":"stamped"}}`),
					},
					addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule},
					addrs.NoKey,
				)
			})

			adjuster := &withholdingAdjuster{}
			ctx := testContext2(t, &ContextOpts{
				Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
					addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
				}, nil),
				ConfigValueAdjuster: adjuster,
			})

			plan, diags := ctx.Plan(context.Background(), m, s, DefaultPlanOpts)
			if diags.HasErrors() {
				t.Fatalf("plan: %s", diags.Err())
			}
			ric, err := plan.Changes.ResourceInstance(addr).Decode(&providers.Schema{Block: simpleTestSchema()})
			if err != nil {
				t.Fatalf("decoding the planned change: %s", err)
			}
			if ric.Action != tc.wantAction {
				t.Fatalf("action = %s, want %s", ric.Action, tc.wantAction)
			}
			plannedMap := ric.After.GetAttr("test_map")
			stamped := !plannedMap.IsNull() && plannedMap.Type().IsMapType() && plannedMap.HasIndex(cty.StringVal("marker")).True()
			if wantStamped := tc.wantAction == plans.Update; stamped != wantStamped {
				t.Errorf("planned test_map = %#v: stamped %v, want %v", plannedMap, stamped, wantStamped)
			}

			if _, diags := ctx.Apply(context.Background(), plan, m, nil); diags.HasErrors() {
				t.Fatalf("apply: %s", diags.Err())
			}
			if len(applyConfigs) != 1 {
				t.Fatalf("want 1 non-delete ApplyResourceChange, got %d", len(applyConfigs))
			}
			sentMap := applyConfigs[0].GetAttr("test_map")
			sentStamped := !sentMap.IsNull() && sentMap.HasIndex(cty.StringVal("marker")).True()
			if wantStamped := tc.wantAction == plans.Update; sentStamped != wantStamped {
				t.Errorf("ApplyResourceChange planned test_map = %#v: stamped %v, want %v", sentMap, sentStamped, wantStamped)
			}

			// The apply node sees a replace simplified to Create
			// (plans.ResourceInstanceChange.Simplify), so the writer is
			// called with Create for both replace shapes.
			var wantWrites []plans.Action
			if tc.wantAction.IsReplace() {
				wantWrites = []plans.Action{plans.Create}
			}
			if len(adjuster.writes) != len(wantWrites) || (len(wantWrites) == 1 && adjuster.writes[0] != wantWrites[0]) {
				t.Errorf("WriteAppliedMarkers calls = %v, want %v", adjuster.writes, wantWrites)
			}
		})
	}
}
