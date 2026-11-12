// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

// This test covers a marked configuration value reaching the apply phase's
// final plan on the new runtime (#2256). depends_on puts a resource instance
// mark on the whole configuration object. The apply engine re-plans each
// change from that marked value, and objchange.ProposedNew panicked in cty's
// LengthInt on the first NestingList block it read. Found by the gauntlet
// estate reference-k8s, stage drift_reconverge.
//
// A sensitive value inside a block is covered in internal/resources
// (TestPlanChanges_markedNestedListBlock): a sensitive variable or output
// sends the whole run to the default runtime (the "sensitive" row of
// internal/live/newruntime/gaps.json), so a context test cannot put one in
// front of the new runtime.

const markedApplyConfig = `
resource "test_dep" "a" {
}

resource "test_svc" "b" {
  depends_on = [test_dep.a]

  name = "svc"

  rule {
    port = 80
  }
  rule {
    port  = 443
    token = "t0k"
  }
}
`

// markedApplyProvider has a test_svc resource type with a NestingList
// block, the shape of a kubernetes_service's spec and port blocks. Its
// ReadResource returns whatever drift says for test_svc, so a test can
// change the object out of band.
func markedApplyProvider() (*MockProvider, func(func(cty.Value) cty.Value)) {
	p := &MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{
				"test_dep": {Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"id": {Type: cty.String, Computed: true},
					},
				}},
				"test_svc": {Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"id":   {Type: cty.String, Computed: true},
						"name": {Type: cty.String, Optional: true},
					},
					BlockTypes: map[string]*configschema.NestedBlock{
						"rule": {
							Nesting: configschema.NestingList,
							Block: configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"port":  {Type: cty.Number, Optional: true},
									"token": {Type: cty.String, Optional: true, Sensitive: true},
								},
							},
						},
					},
				}},
			},
		},
	}

	var mu sync.Mutex
	var drift func(cty.Value) cty.Value
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		if req.ProposedNewState.IsNull() {
			return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
		}
		attrs := req.ProposedNewState.AsValueMap()
		if req.PriorState.IsNull() {
			attrs["id"] = cty.UnknownVal(cty.String)
		}
		return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(attrs)}
	}
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		if req.PlannedState.IsNull() {
			return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
		}
		attrs := req.PlannedState.AsValueMap()
		if !attrs["id"].IsKnown() {
			attrs["id"] = cty.StringVal(req.TypeName + "-id")
		}
		return providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(attrs)}
	}
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		mu.Lock()
		d := drift
		mu.Unlock()
		if d == nil || req.TypeName != "test_svc" || req.PriorState.IsNull() {
			return providers.ReadResourceResponse{NewState: req.PriorState, Private: req.Private}
		}
		return providers.ReadResourceResponse{NewState: d(req.PriorState), Private: req.Private}
	}
	return p, func(f func(cty.Value) cty.Value) {
		mu.Lock()
		defer mu.Unlock()
		drift = f
	}
}

func markedApplyContext(t *testing.T, p *MockProvider) *Context {
	t.Helper()
	return testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
	})
}

// markedApplyRules reads the rule ports of test_svc.b in state, in order.
func markedApplyRules(t *testing.T, p *MockProvider, state *states.State) []string {
	t.Helper()
	addr := mustResourceInstanceAddr("test_svc.b")
	rs := state.ResourceInstance(addr)
	if rs == nil || rs.Current == nil {
		t.Fatalf("no current object for %s in the applied state:\n%s", addr, state)
	}
	schema := p.GetProviderSchemaResponse.ResourceTypes["test_svc"].Block
	obj, err := rs.Current.Decode(schema.ImpliedType())
	if err != nil {
		t.Fatalf("decoding the applied object: %s", err)
	}
	// The schema marks token sensitive, so the stored object carries that
	// path, as on the old runtime (choudoufu fork port #2410).
	tokenPath := cty.GetAttrPath("rule").IndexInt(1).GetAttr("token")
	if !slices.ContainsFunc(rs.Current.AttrSensitivePaths, func(pvm cty.PathValueMarks) bool { return pvm.Path.Equals(tokenPath) }) {
		t.Errorf("the applied object's sensitive paths %#v lack the schema-sensitive rule[1].token", rs.Current.AttrSensitivePaths)
	}
	val, _ := obj.Value.UnmarkDeep()
	var got []string
	for it := val.GetAttr("rule").ElementIterator(); it.Next(); {
		_, v := it.Element()
		s := v.GetAttr("port").AsBigFloat().String()
		if tok := v.GetAttr("token"); !tok.IsNull() {
			s += ":" + tok.AsString()
		}
		got = append(got, s)
	}
	return got
}

// TestNewRuntimePlanApply_markedNestedListBlock: a resource with depends_on
// and a NestingList block plans and applies on the new runtime, through the apply engine's final
// plan. Then the object drifts, and the reconverging plan and apply put it
// back, which re-plans from a non-null prior.
func TestNewRuntimePlanApply_markedNestedListBlock(t *testing.T) {
	forceExperimentalRuntimeForTesting(t)
	m := testModuleInline(t, map[string]string{"main.tf": markedApplyConfig})
	p, setDrift := markedApplyProvider()
	ctx := markedApplyContext(t, p)
	svcAddr := mustResourceInstanceAddr("test_svc.b")

	// Greenfield.
	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	assertNoErrors(t, diags)
	if len(plan.ExecutionGraph) == 0 {
		t.Fatal("the plan has no execution graph, so it was not made by the new runtime; this test proves nothing")
	}
	if rc := plan.Changes.ResourceInstance(svcAddr); rc == nil || rc.Action != plans.Create {
		t.Fatalf("planned change for %s is %#v, want a create", svcAddr, rc)
	}
	state, diags := ctx.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)
	if got, want := markedApplyRules(t, p, state), []string{"80", "443:t0k"}; !slices.Equal(got, want) {
		t.Fatalf("applied rules are %v, want %v", got, want)
	}

	// Drift: the first rule's port is changed out of band.
	setDrift(func(prior cty.Value) cty.Value {
		attrs := prior.AsValueMap()
		rules := attrs["rule"].AsValueSlice()
		r0 := rules[0].AsValueMap()
		r0["port"] = cty.NumberIntVal(8080)
		rules[0] = cty.ObjectVal(r0)
		attrs["rule"] = cty.ListVal(rules)
		return cty.ObjectVal(attrs)
	})
	plan, diags = ctx.Plan(context.Background(), m, state, DefaultPlanOpts)
	assertNoErrors(t, diags)
	if len(plan.ExecutionGraph) == 0 {
		t.Fatal("the drift plan has no execution graph, so it was not made by the new runtime; this test proves nothing")
	}
	if rc := plan.Changes.ResourceInstance(svcAddr); rc == nil || rc.Action != plans.Update {
		t.Fatalf("drift plan's change for %s is %#v, want an update", svcAddr, rc)
	}
	setDrift(nil)
	state, diags = ctx.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)
	if got, want := markedApplyRules(t, p, state), []string{"80", "443:t0k"}; !slices.Equal(got, want) {
		t.Fatalf("reconverged rules are %v, want %v", got, want)
	}
}
