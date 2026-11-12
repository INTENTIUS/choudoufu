// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/sensitive"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

// A sensitive value inside a nested block on the new runtime, end to end
// (choudoufu fork port #2410, of #2256). #2405 fixed the new runtime's
// planning of a nested NestingList block under depends_on, whose whole
// configuration value the engine marks, but could cover a sensitive value
// inside such a block only at internal/resources' unit level, because the
// gap table refused every sensitive variable. With the sensitive fork port
// the run plans, applies and replans on the new runtime, and these tests
// hold what the old runtime does: the planned change, the applied state and
// the drift plan each carry the sensitive path, the path a provider schema
// marks sensitive is sensitive too, and nothing else is.

const sensitiveNestedConfig = `
variable "secret" {
  type      = string
  sensitive = true
  default   = "s3cr3t"
}

resource "test_net" "a" {
}

resource "test_svc" "b" {
  password = "pw"

  rule {
    name  = "with-secret"
    token = var.secret
  }
  rule {
    name  = "plain"
    token = "visible"
  }

  depends_on = [test_net.a]
}
`

// sensitiveNestedSvcSchema is test_svc's schema: a NestingList block
// "rule" and a "password" the schema itself marks sensitive.
func sensitiveNestedSvcSchema() *configschema.Block {
	return &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":       {Type: cty.String, Computed: true},
			"password": {Type: cty.String, Optional: true, Sensitive: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"rule": {
				Nesting: configschema.NestingList,
				Block: configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"name":  {Type: cty.String, Required: true},
						"token": {Type: cty.String, Optional: true},
					},
				},
			},
		},
	}
}

// sensitiveNestedProvider plans and applies test_net and test_svc, and
// reads test_svc back with its first rule's token replaced by
// *drifted, while that is not empty.
func sensitiveNestedProvider(drifted *string, mu *sync.Mutex) *MockProvider {
	p := &MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{
				"test_net": {Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"id": {Type: cty.String, Computed: true},
					},
				}},
				"test_svc": {Block: sensitiveNestedSvcSchema()},
			},
		},
	}
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
		attrs["id"] = cty.StringVal(req.TypeName + "-id")
		return providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(attrs)}
	}
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		mu.Lock()
		d := *drifted
		mu.Unlock()
		if req.TypeName != "test_svc" || d == "" || req.PriorState.IsNull() {
			return providers.ReadResourceResponse{NewState: req.PriorState}
		}
		attrs := req.PriorState.AsValueMap()
		rules := attrs["rule"].AsValueSlice()
		first := rules[0].AsValueMap()
		first["token"] = cty.StringVal(d)
		rules[0] = cty.ObjectVal(first)
		attrs["rule"] = cty.ListVal(rules)
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(attrs)}
	}
	return p
}

var (
	sensitiveNestedTokenPath    = cty.GetAttrPath("rule").IndexInt(0).GetAttr("token")
	sensitiveNestedPlainPath    = cty.GetAttrPath("rule").IndexInt(1).GetAttr("token")
	sensitiveNestedPasswordPath = cty.GetAttrPath("password")
)

// sensitivePathsOf is the paths pvms marks sensitive.
func sensitivePathsOf(pvms []cty.PathValueMarks) []cty.Path {
	var out []cty.Path
	for _, pvm := range pvms {
		if _, ok := pvm.Marks[marks.Sensitive]; ok {
			out = append(out, pvm.Path)
		}
	}
	return out
}

// assertSensitivePaths fails unless got is exactly want, in any order.
func assertSensitivePaths(t *testing.T, what string, got []cty.Path, want ...cty.Path) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: sensitive paths %#v, want %#v", what, got, want)
		return
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g.Equals(w) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: sensitive paths %#v lack %#v", what, got, w)
		}
	}
}

// sensitiveNestedPlanOpts assigns the variable its default, as the CLI
// would: core's Plan refuses an unassigned root variable.
func sensitiveNestedPlanOpts() *PlanOpts {
	return &PlanOpts{
		Mode:         plans.NormalMode,
		SetVariables: InputValues{"secret": &InputValue{Value: cty.StringVal("s3cr3t"), SourceType: ValueFromConfig}},
	}
}

func sensitiveNestedContext(t *testing.T, p *MockProvider) *Context {
	t.Helper()
	return testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
	})
}

// TestNewRuntimeSensitive_nestedBlockPlanApplyDrift: plan, apply, a replan
// of the unchanged world and a plan after drift, all on the new runtime.
func TestNewRuntimeSensitive_nestedBlockPlanApplyDrift(t *testing.T) {
	toggleOn(t)
	strictOn(t)
	m := testModuleInline(t, map[string]string{"main.tf": sensitiveNestedConfig})
	var mu sync.Mutex
	var drifted string
	p := sensitiveNestedProvider(&drifted, &mu)
	ctx := sensitiveNestedContext(t, p)
	svc := mustResourceInstanceAddr("test_svc.b")
	schema := providers.Schema{Block: sensitiveNestedSvcSchema()}

	// Plan: the change carries the variable's mark at rule[0].token and the
	// schema's at password, and nothing marks rule[1].token.
	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), sensitiveNestedPlanOpts())
	assertNoErrors(t, diags)
	if len(plan.ExecutionGraph) == 0 {
		t.Fatal("the plan has no execution graph, so it was not made by the new runtime; this test proves nothing")
	}
	rc := plan.Changes.ResourceInstance(svc)
	if rc == nil || rc.Action != plans.Create {
		t.Fatalf("planned change for %s is %#v, want a create", svc, rc)
	}
	assertSensitivePaths(t, "planned create's after", sensitivePathsOf(rc.AfterValMarks), sensitiveNestedTokenPath, sensitiveNestedPasswordPath)
	change, err := rc.Decode(&schema)
	if err != nil {
		t.Fatalf("decoding the planned change: %s", err)
	}
	if tok, err := sensitiveNestedTokenPath.Apply(change.After); err != nil || !tok.HasMark(marks.Sensitive) {
		t.Errorf("planned rule[0].token is %#v (%v), want it marked sensitive", tok, err)
	}

	// Apply: the stored object has exactly the two sensitive paths.
	state, diags := ctx.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)
	rs := state.ResourceInstance(svc)
	if rs == nil || rs.Current == nil {
		t.Fatalf("no applied object for %s:\n%s", svc, state)
	}
	assertSensitivePaths(t, "applied state", sensitivePathsOf(rs.Current.AttrSensitivePaths), sensitiveNestedTokenPath, sensitiveNestedPasswordPath)
	for _, pvm := range rs.Current.AttrSensitivePaths {
		if !pvm.Marks.Equal(cty.NewValueMarks(marks.Sensitive)) {
			t.Errorf("applied state stores marks %#v at %#v; a state holds only the sensitive mark", pvm.Marks, pvm.Path)
		}
	}

	// Replan, nothing changed: a no-op. The prior's sensitive paths, from
	// the state, match the configuration's, so sensitivity alone does not
	// make an update.
	plan, diags = ctx.Plan(context.Background(), m, state, sensitiveNestedPlanOpts())
	assertNoErrors(t, diags)
	if len(plan.ExecutionGraph) == 0 {
		t.Fatal("the replan was not made by the new runtime")
	}
	if rc := plan.Changes.ResourceInstance(svc); rc != nil && rc.Action != plans.NoOp {
		t.Errorf("replan of the unchanged world plans %s for %s, want no change", rc.Action, svc)
	}

	// Drift: the secret token changed out of band. The update's before and
	// after are both sensitive at the token, and the drifted value is not
	// rendered in the plan's JSON-facing paths as plain.
	mu.Lock()
	drifted = "rotated-out-of-band"
	mu.Unlock()
	plan, diags = ctx.Plan(context.Background(), m, state, sensitiveNestedPlanOpts())
	assertNoErrors(t, diags)
	if len(plan.ExecutionGraph) == 0 {
		t.Fatal("the drift plan was not made by the new runtime")
	}
	rc = plan.Changes.ResourceInstance(svc)
	if rc == nil || rc.Action != plans.Update {
		t.Fatalf("drift plan for %s is %#v, want an update", svc, rc)
	}
	assertSensitivePaths(t, "drift update's before", sensitivePathsOf(rc.BeforeValMarks), sensitiveNestedTokenPath, sensitiveNestedPasswordPath)
	assertSensitivePaths(t, "drift update's after", sensitivePathsOf(rc.AfterValMarks), sensitiveNestedTokenPath, sensitiveNestedPasswordPath)
	change, err = rc.Decode(&schema)
	if err != nil {
		t.Fatalf("decoding the drift change: %s", err)
	}
	before, _ := sensitiveNestedTokenPath.Apply(change.Before)
	if before.IsNull() || !before.HasMark(marks.Sensitive) {
		t.Errorf("drift before rule[0].token is %#v, want the drifted value marked sensitive", before)
	} else if v, _ := before.Unmark(); v.AsString() != "rotated-out-of-band" {
		t.Errorf("drift before rule[0].token is %q, want the value the refresh read", v.AsString())
	}
	if plain, _ := sensitiveNestedPlainPath.Apply(change.After); plain.HasMark(marks.Sensitive) {
		t.Errorf("rule[1].token is marked sensitive; only the variable's path and the schema's are")
	}

	state, diags = ctx.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)
	rs = state.ResourceInstance(svc)
	if rs == nil || rs.Current == nil {
		t.Fatalf("no object for %s after the drift apply", svc)
	}
	assertSensitivePaths(t, "state after the drift apply", sensitivePathsOf(rs.Current.AttrSensitivePaths), sensitiveNestedTokenPath, sensitiveNestedPasswordPath)
}

// TestNewRuntimeSensitive_undeclaredRootOutput: a root output whose value
// is sensitive must be declared sensitive, as on the old runtime.
func TestNewRuntimeSensitive_undeclaredRootOutput(t *testing.T) {
	toggleOn(t)
	strictOn(t)
	m := testModuleInline(t, map[string]string{"main.tf": sensitiveNestedConfig + `
output "leak" {
  value = var.secret
}
`})
	var mu sync.Mutex
	var drifted string
	_, diags := sensitiveNestedContext(t, sensitiveNestedProvider(&drifted, &mu)).Plan(context.Background(), m, states.NewState(), sensitiveNestedPlanOpts())
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), "Output refers to sensitive values") {
		t.Fatalf("want \"Output refers to sensitive values\", got: %v", diags.Err())
	}
}

// TestNewRuntimeSensitive_switchOff: with the port's switch set the gap
// table refuses the sensitive variable again.
func TestNewRuntimeSensitive_switchOff(t *testing.T) {
	toggleOn(t)
	strictOn(t)
	t.Setenv(forkport.Switch(sensitive.Name), "1")
	m := testModuleInline(t, map[string]string{"main.tf": sensitiveNestedConfig})
	var mu sync.Mutex
	var drifted string
	_, diags := sensitiveNestedContext(t, sensitiveNestedProvider(&drifted, &mu)).Plan(context.Background(), m, states.NewState(), sensitiveNestedPlanOpts())
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), `gap "sensitive"`) {
		t.Fatalf("want the sensitive row's refusal with the switch set, got: %v", diags.Err())
	}
}

// TestNewRuntimeSensitive_sensitivityOnlyChangeUpdates: an object stored
// without the sensitive paths its configuration now has plans an update,
// not a no-op, and the update stores the paths, as on the old runtime.
func TestNewRuntimeSensitive_sensitivityOnlyChangeUpdates(t *testing.T) {
	toggleOn(t)
	strictOn(t)
	m := testModuleInline(t, map[string]string{"main.tf": sensitiveNestedConfig})
	var mu sync.Mutex
	var drifted string
	ctx := sensitiveNestedContext(t, sensitiveNestedProvider(&drifted, &mu))
	svc := mustResourceInstanceAddr("test_svc.b")

	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), sensitiveNestedPlanOpts())
	assertNoErrors(t, diags)
	state, diags := ctx.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)

	// The same object, stored as a state written before the variable was
	// sensitive would hold it: only the schema's path.
	state = state.DeepCopy()
	obj := state.Module(addrs.RootModuleInstance).ResourceInstance(svc.Resource).Current
	obj.AttrSensitivePaths = []cty.PathValueMarks{{Path: sensitiveNestedPasswordPath, Marks: cty.NewValueMarks(marks.Sensitive)}}

	plan, diags = ctx.Plan(context.Background(), m, state, sensitiveNestedPlanOpts())
	assertNoErrors(t, diags)
	if len(plan.ExecutionGraph) == 0 {
		t.Fatal("the plan was not made by the new runtime")
	}
	rc := plan.Changes.ResourceInstance(svc)
	if rc == nil || rc.Action != plans.Update {
		t.Fatalf("a change of sensitivity alone plans %#v for %s, want an update", rc, svc)
	}
	state, diags = ctx.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)
	rs := state.ResourceInstance(svc)
	if rs == nil || rs.Current == nil {
		t.Fatalf("no object for %s after the update", svc)
	}
	assertSensitivePaths(t, "state after the sensitivity update", sensitivePathsOf(rs.Current.AttrSensitivePaths), sensitiveNestedTokenPath, sensitiveNestedPasswordPath)
}
