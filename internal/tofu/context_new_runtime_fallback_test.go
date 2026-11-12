// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/deferral"
	"github.com/intentius/choudoufu/internal/engine/forkport/replacecycle"
	"github.com/intentius/choudoufu/internal/live/newruntime"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The fallback (#2397): with the toggle on and CHOUDOUFU_NEW_RUNTIME_STRICT
// unset, a run the new runtime cannot handle runs on the default runtime as
// a whole, and says so. Each test below has the strict twin that refuses.

// fallbackOn is the toggle on and the strict switch explicitly off, so the
// tests hold under the experimental-runtime lane, which sets it.
func fallbackOn(t *testing.T) {
	t.Helper()
	showNewRuntimeWarningForTesting(t)
	toggleOn(t)
	t.Setenv(newruntime.StrictVariable, "")
}

// fellBackFor reports whether diags carry the fallback warning naming the
// row, the variable, what the row waits for, and the strict switch.
func fellBackFor(diags tfdiags.Diagnostics, id string) bool {
	r, ok := newruntime.RowByID(id)
	if !ok {
		return false
	}
	for _, d := range diags {
		desc := d.Description()
		if d.Severity() == tfdiags.Warning && desc.Summary == newruntime.FallbackSummary &&
			strings.Contains(desc.Detail, "- "+id+":") && strings.Contains(desc.Detail, r.WaitsFor()) &&
			strings.Contains(desc.Detail, newruntime.Variable) && strings.Contains(desc.Detail, newruntime.StrictVariable) {
			return true
		}
	}
	return false
}

// assertFellBack fails unless the run planned without errors on the default
// runtime: the fallback warning names the row, and the new-runtime warning,
// which only a run that reached the engine prints, is absent.
func assertFellBack(t *testing.T, plan *plans.Plan, diags tfdiags.Diagnostics, id string) {
	t.Helper()
	if diags.HasErrors() {
		t.Fatalf("the run was not planned on the default runtime; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	if plan == nil {
		t.Fatal("no plan was returned")
	}
	if !fellBackFor(diags, id) {
		t.Errorf("no fallback warning naming gap %q; diagnostics:\n%s", id, diags.ErrWithWarnings())
	}
	if hasDiag(diags, tfdiags.Warning, newruntime.WarningSummary) {
		t.Errorf("a run that fell back printed the new-runtime warning, which says the engine ran")
	}
	if len(plan.ExecutionGraph) != 0 {
		t.Errorf("a plan that fell back carries an execution graph, so it was made by the new runtime")
	}
}

// fallbackProvider is toggleProvider with a test_object data source.
func fallbackProvider() *MockProvider {
	p := toggleProvider()
	p.GetProviderSchemaResponse.DataSources = map[string]providers.Schema{"test_object": {Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{"name": {Type: cty.String, Optional: true}},
	}}}
	p.ReadDataSourceFn = func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
		return providers.ReadDataSourceResponse{State: req.Config}
	}
	return p
}

const fallbackDataConfig = `
data "test_object" "d" {
  name = "d"
}
resource "test_object" "a" {
  name = data.test_object.d.name
}
`

// TestNewRuntimeFallback_preflight: a preflight row (data-sources). Under
// fallback the plan is made on the default runtime and applies there, on the
// same context, with no second warning. Strict, it is refused.
func TestNewRuntimeFallback_preflight(t *testing.T) {
	m := testModuleInline(t, map[string]string{"main.tf": fallbackDataConfig})

	t.Run("fallback", func(t *testing.T) {
		fallbackOn(t)
		p := fallbackProvider()
		ctx := toggleContext(t, p)
		plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
		assertFellBack(t, plan, diags, "data-sources")

		state, diags := ctx.Apply(context.Background(), plan, m, nil)
		if diags.HasErrors() {
			t.Fatalf("the fallback plan did not apply on the default runtime: %s", diags.ErrWithWarnings())
		}
		if hasDiag(diags, tfdiags.Warning, newruntime.WarningSummary) {
			t.Errorf("the fallback plan's apply printed the new-runtime warning")
		}
		if hasDiag(diags, tfdiags.Warning, defaultShapedPlanSummary) {
			t.Errorf("the apply repeated that it runs on the default runtime, after the plan's fallback warning said so")
		}
		if state.ResourceInstance(mustResourceInstanceAddr("test_object.a")) == nil {
			t.Errorf("test_object.a is not in the applied state:\n%s", state)
		}
	})

	t.Run("strict", func(t *testing.T) {
		fallbackOn(t)
		strictOn(t)
		_, diags := toggleContext(t, fallbackProvider()).Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
		if !refusedUnder(diags, "data-sources") {
			t.Fatalf("strict: a data source was not refused; diagnostics:\n%s", diags.ErrWithWarnings())
		}
		if hasDiag(diags, tfdiags.Warning, newruntime.FallbackSummary) {
			t.Errorf("strict: a refused run printed the fallback warning")
		}
	})
}

// TestNewRuntimeFallback_savedPlanApply: a plan saved by a fallback run is
// applied by another context, as apply PLANFILE does. It has no execution
// graph, so it applies on the default runtime and says so.
func TestNewRuntimeFallback_savedPlanApply(t *testing.T) {
	fallbackOn(t)
	m := testModuleInline(t, map[string]string{"main.tf": fallbackDataConfig})
	p := fallbackProvider()
	plan, diags := toggleContext(t, p).Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	assertFellBack(t, plan, diags, "data-sources")

	state, diags := toggleContext(t, p).Apply(context.Background(), plan, m, nil)
	if diags.HasErrors() {
		t.Fatalf("the saved fallback plan did not apply on the default runtime: %s", diags.ErrWithWarnings())
	}
	if !hasDiag(diags, tfdiags.Warning, defaultShapedPlanSummary) {
		t.Errorf("the apply does not say it ran on the default runtime; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	if hasDiag(diags, tfdiags.Warning, newruntime.WarningSummary) {
		t.Errorf("the apply printed the new-runtime warning")
	}
	if state.ResourceInstance(mustResourceInstanceAddr("test_object.a")) == nil {
		t.Errorf("test_object.a is not in the applied state:\n%s", state)
	}

	// A fallback plan with no resource changes has no execution graph, as
	// an empty plan of the new runtime has none: the checks tell them apart,
	// and this one, which they refuse, still applies on the default runtime.
	m = testModuleInline(t, map[string]string{"main.tf": "data \"test_object\" \"d\" {\n  name = \"d\"\n}\noutput \"o\" {\n  value = data.test_object.d.name\n}\n"})
	plan, diags = toggleContext(t, p).Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	assertFellBack(t, plan, diags, "data-sources")
	state, diags = toggleContext(t, p).Apply(context.Background(), plan, m, nil)
	if diags.HasErrors() || !hasDiag(diags, tfdiags.Warning, defaultShapedPlanSummary) {
		t.Fatalf("a saved fallback plan with no resource changes did not apply on the default runtime; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	if o := state.RootModule().OutputValues["o"]; o == nil || o.Value != cty.StringVal("d") {
		t.Errorf("output o is %#v, want \"d\"", o)
	}
}

// TestNewRuntimeFallback_postPlan: the destroy-target row, a post-plan
// check. The new runtime's plan deletes test_object.a for -target
// test_object.b; under fallback it is discarded and the default runtime
// deletes only the target.
func TestNewRuntimeFallback_postPlan(t *testing.T) {
	fallbackOn(t)
	m := testModuleInline(t, map[string]string{"main.tf": destroyTargetConfig})
	opts := SimplePlanOpts(plans.DestroyMode, nil)
	opts.Targets = []addrs.Targetable{mustResourceInstanceAddr("test_object.b")}
	prior := destroyTargetState()
	plan, diags := toggleContext(t, toggleProvider()).Plan(context.Background(), m, prior, opts)
	assertFellBack(t, plan, diags, "destroy-target")
	if got := strings.Join(deletes(plan), " "); got != "test_object.b" {
		t.Errorf("destroy -target test_object.b deletes %q, want only test_object.b", got)
	}
	if prior.ResourceInstance(mustResourceInstanceAddr("test_object.a")) == nil {
		t.Errorf("the caller's prior state lost test_object.a")
	}
}

// TestNewRuntimeFallback_engine: the deferral row, raised inside planning.
// With the deferral fork port off, test_object.b, whose configuration
// derives from the excluded test_object.a, is deferred for a reason the new
// runtime cannot name, and refused. Under fallback the default runtime plans
// it, leaving b out as it leaves out what depends on an excluded resource.
func TestNewRuntimeFallback_engine(t *testing.T) {
	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_object" "a" {
  name = "a"
}
resource "test_object" "b" {
  name = test_object.a.id
}
`})
	opts := SimplePlanOpts(plans.NormalMode, nil)
	opts.Excludes = []addrs.Targetable{mustAbsResourceAddr("test_object.a")}

	t.Run("fallback", func(t *testing.T) {
		fallbackOn(t)
		t.Setenv(forkport.Switch(deferral.Name), "1")
		plan, diags := toggleContext(t, toggleProvider()).Plan(context.Background(), m, states.NewState(), opts)
		assertFellBack(t, plan, diags, "deferral")
	})
	t.Run("strict", func(t *testing.T) {
		fallbackOn(t)
		strictOn(t)
		t.Setenv(forkport.Switch(deferral.Name), "1")
		_, diags := toggleContext(t, toggleProvider()).Plan(context.Background(), m, states.NewState(), opts)
		if !refusedUnder(diags, "deferral") {
			t.Fatalf("strict: the deferral was not refused; diagnostics:\n%s", diags.ErrWithWarnings())
		}
	})
}

// TestNewRuntimeFallback_newRuntimePlanCannotFallBackAtApply: a plan the new
// runtime made carries an execution graph, which only the new runtime can
// apply. If the checks refuse it at apply (here the replacecycle port was on
// for the plan and is off for the apply), there is nothing to fall back to:
// the apply is refused and asks for a new plan.
func TestNewRuntimeFallback_newRuntimePlanCannotFallBackAtApply(t *testing.T) {
	fallbackOn(t)
	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(replacecycle.Name), "")
	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_object" "a" {
  name = "new"
}
`})
	p := toggleProvider()
	plan, diags := toggleContext(t, p).Plan(context.Background(), m, stateWith("old"), DefaultPlanOpts)
	if diags.HasErrors() || len(plan.ExecutionGraph) == 0 {
		t.Fatalf("the new runtime did not plan the replace with the port on: %s", diags.ErrWithWarnings())
	}
	t.Setenv(forkport.Switch(replacecycle.Name), "1")
	_, diags = toggleContext(t, p).Apply(context.Background(), plan, m, nil)
	if !refusedUnder(diags, "replace-cbd-cycle") || !hasDiag(diags, tfdiags.Error, "Plan again to fall back to the default runtime") {
		t.Fatalf("want the refusal and the request to plan again; diagnostics:\n%s", diags.ErrWithWarnings())
	}
}

// TestNewRuntimeFallback_cleanRunsOnNewRuntime: a configuration with nothing
// refused does not fall back. It plans and applies on the new runtime, with
// the new-runtime warning and no fallback warning, and so does an empty one.
func TestNewRuntimeFallback_cleanRunsOnNewRuntime(t *testing.T) {
	fallbackOn(t)
	for name, src := range map[string]string{
		"one resource": "resource \"test_object\" \"a\" {\n  name = \"a\"\n}\n",
		"empty":        "",
	} {
		t.Run(name, func(t *testing.T) {
			m := testModuleInline(t, map[string]string{"main.tf": src})
			p := toggleProvider()
			plan, diags := toggleContext(t, p).Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
			if diags.HasErrors() {
				t.Fatalf("plan failed: %s", diags.ErrWithWarnings())
			}
			if hasDiag(diags, tfdiags.Warning, newruntime.FallbackSummary) || !hasDiag(diags, tfdiags.Warning, newruntime.WarningSummary) {
				t.Fatalf("want the new-runtime warning and no fallback; diagnostics:\n%s", diags.ErrWithWarnings())
			}
			t.Logf("execution graph: %d bytes", len(plan.ExecutionGraph))
			_, diags = toggleContext(t, p).Apply(context.Background(), plan, m, nil)
			if diags.HasErrors() {
				t.Fatalf("apply failed: %s", diags.ErrWithWarnings())
			}
			if hasDiag(diags, tfdiags.Warning, defaultShapedPlanSummary) || !hasDiag(diags, tfdiags.Warning, newruntime.WarningSummary) {
				t.Errorf("want the apply on the new runtime; diagnostics:\n%s", diags.ErrWithWarnings())
			}
		})
	}
}

// TestNewRuntimeFallback_import: an import whose configuration the preflight
// refuses (a data source) runs the default runtime's import, with the
// fallback warning. Validate on the same configuration takes the default
// runtime too, and prints nothing about it.
func TestNewRuntimeFallback_import(t *testing.T) {
	fallbackOn(t)
	m := testModuleInline(t, map[string]string{"main.tf": fallbackDataConfig})
	p := fallbackProvider()
	p.ImportResourceStateResponse = &providers.ImportResourceStateResponse{
		ImportedResources: []providers.ImportedResource{{
			TypeName: "test_object",
			State:    cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("i-1"), "name": cty.StringVal("d")}),
		}},
	}
	ctx := toggleContext(t, p)
	if diags := ctx.Validate(context.Background(), m); diags.HasErrors() || hasDiag(diags, tfdiags.Warning, newruntime.FallbackSummary) {
		t.Fatalf("validate: %s", diags.ErrWithWarnings())
	}
	state, diags := ctx.Import(context.Background(), m, states.NewState(), &ImportOpts{
		Targets: []*ImportTarget{{CommandLineImportTarget: &CommandLineImportTarget{Addr: mustResourceInstanceAddr("test_object.a"), ID: "i-1"}}},
	})
	if diags.HasErrors() {
		t.Fatalf("the import did not run on the default runtime: %s", diags.ErrWithWarnings())
	}
	if !fellBackFor(diags, "data-sources") || hasDiag(diags, tfdiags.Warning, newruntime.WarningSummary) {
		t.Errorf("want the fallback warning naming data-sources and no new-runtime warning; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	if state.ResourceInstance(mustResourceInstanceAddr("test_object.a")) == nil {
		t.Errorf("test_object.a was not imported:\n%s", state)
	}
}
