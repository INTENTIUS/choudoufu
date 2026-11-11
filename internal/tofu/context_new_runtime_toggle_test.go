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
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/deferral"
	"github.com/intentius/choudoufu/internal/engine/forkport/replacecycle"
	"github.com/intentius/choudoufu/internal/live/newruntime"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/version"
)

// toggleProvider is a provider whose test_object has a computed id and a
// name that forces replacement, enough to plan creates, replaces and an
// unknown for_each on either runtime.
func toggleProvider() *MockProvider {
	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":   {Type: cty.String, Computed: true},
		"name": {Type: cty.String, Optional: true},
	}}
	p := &MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
		Provider:      providers.Schema{Block: &configschema.Block{}},
		ResourceTypes: map[string]providers.Schema{"test_object": {Block: block}},
	}}
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		if req.ProposedNewState.IsNull() {
			return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
		}
		vals := req.ProposedNewState.AsValueMap()
		var replace []cty.Path
		if req.PriorState.IsNull() {
			vals["id"] = cty.UnknownVal(cty.String)
		} else if !req.PriorState.GetAttr("name").RawEquals(vals["name"]) {
			vals["id"] = cty.UnknownVal(cty.String)
			replace = append(replace, cty.GetAttrPath("name"))
		} else {
			vals["id"] = req.PriorState.GetAttr("id")
		}
		return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(vals), RequiresReplace: replace}
	}
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		if req.PlannedState.IsNull() {
			return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
		}
		vals := req.PlannedState.AsValueMap()
		if !vals["id"].IsKnown() {
			vals["id"] = cty.StringVal("id-" + vals["name"].GoString())
		}
		return providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(vals)}
	}
	return p
}

func toggleContext(t *testing.T, p *MockProvider) *Context {
	t.Helper()
	return testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
	})
}

func hasDiag(diags tfdiags.Diagnostics, sev tfdiags.Severity, summary string) bool {
	for _, d := range diags {
		if d.Severity() == sev && d.Description().Summary == summary {
			return true
		}
	}
	return false
}

// showNewRuntimeWarningForTesting turns the toggle's warning back on for a
// test that is about it; TestMain hides it for the rest of this package.
func showNewRuntimeWarningForTesting(t *testing.T) {
	t.Helper()
	prev := newRuntimeWarningHiddenForTesting
	newRuntimeWarningHiddenForTesting = false
	t.Cleanup(func() { newRuntimeWarningHiddenForTesting = prev })
}

func toggleOn(t *testing.T) {
	t.Helper()
	t.Setenv(newruntime.Variable, "1")
}

func stateWith(name string) *states.State {
	return states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(
			mustResourceInstanceAddr("test_object.a"),
			&states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{"id":"i-1","name":"` + name + `"}`)},
			addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule},
			addrs.NoKey,
		)
	})
}

// TestNewRuntimeToggle_preflightRefuses: the preflight check kind. With the
// toggle on, a configuration holding a refused construct (a data source) is
// refused before planning, and no warning is printed. This is also the
// second negative control: a refused run never reaches the engine.
func TestNewRuntimeToggle_preflightRefuses(t *testing.T) {
	showNewRuntimeWarningForTesting(t)
	toggleOn(t)
	m := testModuleInline(t, map[string]string{"main.tf": `
data "test_object" "d" {}
resource "test_object" "a" {
  name = "a"
}
`})
	_, diags := toggleContext(t, toggleProvider()).Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if !hasDiag(diags, tfdiags.Error, newruntime.RefusalSummary) {
		t.Fatalf("a data source was not refused by the preflight; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	if hasDiag(diags, tfdiags.Warning, newruntime.WarningSummary) {
		t.Errorf("a run the preflight refused printed the new-runtime warning, so the warning does not mean the engine ran")
	}
}

// TestNewRuntimeToggle_movedPlansOutsideLiveMode: #2256 U10 (#2374)
// admitted moved blocks in live mode, where they are resolved by marker
// before the engine runs. Outside live mode (no ConfigValueAdjuster) the gap
// table's moved row refused them until #2378 verified that upstream's new
// runtime executes the move itself, so the row is closed and the plan moves
// the object.
func TestNewRuntimeToggle_movedPlansOutsideLiveMode(t *testing.T) {
	toggleOn(t)
	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_object" "b" {
  name = "a"
}
moved {
  from = test_object.a
  to   = test_object.b
}
`})
	plan, diags := toggleContext(t, toggleProvider()).Plan(context.Background(), m, stateWith("a"), DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("a moved block outside live mode was refused or failed; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	moved := false
	for _, ch := range plan.Changes.Resources {
		moved = moved || (ch.Addr.String() == "test_object.b" && ch.PrevRunAddr.String() == "test_object.a")
	}
	if !moved {
		t.Fatalf("the plan has no change for test_object.b moved from test_object.a")
	}
}

// TestNewRuntimeToggle_postPlanRefusesReplace: the post-plan check kind. A
// plan the new runtime makes with a replace in it is refused before it is
// returned. The replacecycle fork port (#2380) lifts that refusal while it
// is on, so its switch is set here.
func TestNewRuntimeToggle_postPlanRefusesReplace(t *testing.T) {
	showNewRuntimeWarningForTesting(t)
	toggleOn(t)
	t.Setenv(forkport.Switch(replacecycle.Name), "1")
	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_object" "a" {
  name = "new"
}
`})
	plan, diags := toggleContext(t, toggleProvider()).Plan(context.Background(), m, stateWith("old"), DefaultPlanOpts)
	if !hasDiag(diags, tfdiags.Error, newruntime.RefusalSummary) {
		t.Fatalf("a replace was not refused by the post-plan check; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	if plan != nil {
		t.Errorf("a refused plan was returned")
	}
}

// TestNewRuntimeToggle_savedPlanApplyRefuses: the saved-plan apply check
// kind. A plan made with the toggle off, holding a replace, is applied with
// the toggle on: both checks run against it before the engine. The
// replacecycle fork port (#2380) is switched off so a replace is refused.
func TestNewRuntimeToggle_savedPlanApplyRefuses(t *testing.T) {
	showNewRuntimeWarningForTesting(t)
	t.Setenv(forkport.Switch(replacecycle.Name), "1")
	// The plan is made on the default runtime: clear the toggle even when
	// the experimental-runtime lane sets it for the whole package.
	t.Setenv(newruntime.Variable, "")
	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_object" "a" {
  name = "new"
}
`})
	p := toggleProvider()
	plan, diags := toggleContext(t, p).Plan(context.Background(), m, stateWith("old"), DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("planning on the default runtime failed: %s", diags.Err())
	}
	if rc := plan.Changes.ResourceInstance(mustResourceInstanceAddr("test_object.a")); rc == nil || !rc.Action.IsReplace() {
		t.Fatalf("the default runtime did not plan a replace, so this test proves nothing: %#v", rc)
	}

	toggleOn(t)
	_, diags = toggleContext(t, p).Apply(context.Background(), plan, m, nil)
	if !hasDiag(diags, tfdiags.Error, newruntime.RefusalSummary) {
		t.Fatalf("applying a saved plan with a replace on the new runtime was not refused by the gap-table checks; diagnostics:\n%s", diags.ErrWithWarnings())
	}
}

// TestNewRuntimeToggle_engineRefusesDeferral: the engine check kind. A
// for_each over a value only known after apply makes the new runtime defer
// the instance, for a reason that is neither -target nor -exclude. The
// deferral fork port (#2382) answers this one with the old runtime's
// "Invalid for_each argument", so it is switched off here to reach the row.
func TestNewRuntimeToggle_engineRefusesDeferral(t *testing.T) {
	showNewRuntimeWarningForTesting(t)
	toggleOn(t)
	t.Setenv(forkport.Switch(deferral.Name), "1")
	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_object" "a" {
  name = "a"
}
resource "test_object" "b" {
  for_each = toset([test_object.a.id])
  name     = each.key
}
`})
	_, diags := toggleContext(t, toggleProvider()).Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if !hasDiag(diags, tfdiags.Error, newruntime.RefusalSummary) {
		t.Fatalf("a deferral was not refused inside planning; diagnostics:\n%s", diags.ErrWithWarnings())
	}
}

// TestNewRuntimeToggle_deferralPortUnknownForEach: the same configuration
// with the deferral fork port (#2382) on gets the default runtime's error
// for an unknown for_each, and no refusal.
func TestNewRuntimeToggle_deferralPortUnknownForEach(t *testing.T) {
	toggleOn(t)
	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(deferral.Name), "")
	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_object" "a" {
  name = "a"
}
resource "test_object" "b" {
  for_each = toset([test_object.a.id])
  name     = each.key
}
`})
	_, diags := toggleContext(t, toggleProvider()).Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if hasDiag(diags, tfdiags.Error, newruntime.RefusalSummary) || !hasDiag(diags, tfdiags.Error, "Invalid for_each argument") {
		t.Fatalf("want the default runtime's for_each error and no refusal; diagnostics:\n%s", diags.ErrWithWarnings())
	}
}

// TestNewRuntimeToggle_releaseModeCleanFixture: a release build
// (version.dev=no) on a 1.14.0-dev VERSION, toggle on, plans and applies on
// local state a configuration with none of the refused constructs: managed
// resources across a module, with count and for_each. Both carry the
// warning.
func TestNewRuntimeToggle_releaseModeCleanFixture(t *testing.T) {
	showNewRuntimeWarningForTesting(t)
	if version.Prerelease != "dev" {
		t.Fatalf("VERSION prerelease is %q, want dev (1.14.0-dev)", version.Prerelease)
	}
	t.Cleanup(version.SetReleaseBuildForTesting(true))
	toggleOn(t)
	if !experimentalRuntimeEnabled() {
		t.Fatal("a release build with the toggle set does not take the new runtime")
	}

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "counted" {
  count = 2
  name  = "c${count.index}"
}
module "child" {
  source = "./child"
}
`,
		"child/main.tf": `
resource "test_object" "each" {
  for_each = toset(["x", "y"])
  name     = each.key
}
`,
	})
	p := toggleProvider()
	plan, diags := toggleContext(t, p).Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("the clean fixture did not plan on the new runtime: %s", diags.ErrWithWarnings())
	}
	if !hasDiag(diags, tfdiags.Warning, newruntime.WarningSummary) {
		t.Errorf("the plan does not carry the new-runtime warning")
	}
	if n := len(plan.Changes.Resources); n != 4 {
		t.Errorf("planned %d resource changes, want 4 (2 counted, 2 in the module)", n)
	}

	state, diags := toggleContext(t, p).Apply(context.Background(), plan, m, nil)
	if diags.HasErrors() {
		t.Fatalf("the clean fixture did not apply on the new runtime: %s", diags.ErrWithWarnings())
	}
	if !hasDiag(diags, tfdiags.Warning, newruntime.WarningSummary) {
		t.Errorf("the apply does not carry the new-runtime warning")
	}
	for _, addr := range []string{"test_object.counted[0]", "test_object.counted[1]", `module.child.test_object.each["x"]`, `module.child.test_object.each["y"]`} {
		if state.ResourceInstance(mustResourceInstanceAddr(addr)) == nil {
			t.Errorf("%s is not in the applied state", addr)
		}
	}
}

// TestNewRuntimeToggle_offPrintsNoWarning is the first negative control:
// with the toggle off, the same plan carries no warning.
func TestNewRuntimeToggle_offPrintsNoWarning(t *testing.T) {
	showNewRuntimeWarningForTesting(t)
	t.Setenv(newruntime.Variable, "")
	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_object" "a" {
  name = "a"
}
`})
	_, diags := toggleContext(t, toggleProvider()).Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("plan on the default runtime failed: %s", diags.Err())
	}
	if hasDiag(diags, tfdiags.Warning, newruntime.WarningSummary) {
		t.Errorf("the default runtime printed the new-runtime warning")
	}
	_ = plans.NormalMode
}
