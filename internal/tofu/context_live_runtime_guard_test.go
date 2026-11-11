// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// liveGuardOptInVariables are the variables that switch this package's
// runtime for plan and apply. After upstream's split (`#4823`, taken in
// #2256 U1) that is TOFU_X_EXPERIMENTAL_RUNTIME only.
// TOFU_X_EXPERIMENTAL_RUNTIME_NEXT is the command layer's
// (Meta.NewRuntimeEnabled) and is covered in internal/command's
// live_new_runtime_guard_test.go.
var liveGuardOptInVariables = []string{"TOFU_X_EXPERIMENTAL_RUNTIME"}

// liveGuardSwitches are the ways a test switches the new runtime on: each
// opt-in variable, and the test hook with no variable at all. The hook row
// is what shows the guard sits on the decision, not on a variable's name
// (#2339).
func liveGuardSwitches() map[string]func(t *testing.T) {
	out := map[string]func(t *testing.T){
		"test hook, no variable": forceExperimentalRuntimeForTesting,
	}
	for _, name := range liveGuardOptInVariables {
		out[name] = func(t *testing.T) { t.Setenv(name, "1") }
	}
	return out
}

// liveOnNewRuntimeRefusalSummary is the summary G's runtime-decision check
// (#2339) gave before #2256 U6 lifted it, kept so the tests below can show
// it is gone.
const liveOnNewRuntimeRefusalSummary = "Live mode cannot run on the new runtime yet"

func hasLiveOnNewRuntimeRefusal(diags tfdiags.Diagnostics) bool {
	for _, d := range diags {
		if d.Severity() == tfdiags.Error && d.Description().Summary == liveOnNewRuntimeRefusalSummary {
			return true
		}
	}
	return false
}

func liveGuardOpts(adjuster ConfigValueAdjuster) *ContextOpts {
	p := simpleMockProvider()
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
	}
	return &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ConfigValueAdjuster: adjuster,
	}
}

// TestContextLiveOnNewRuntimeNotRefused is the internal/tofu half of #2256
// G as #2256 U6 left it: with a ConfigValueAdjuster set (every live entry
// point sets one) and the new runtime chosen by any means, Plan and Apply are
// no longer refused and reach internal/engine, which the toggle's warning
// shows (only newEnginePlan and newEngineApply emit it). Validate emits no
// warning, so it is checked for the refusal and for errors only. Apply is
// given a plan made on the new runtime with the adjuster set.
func TestContextLiveOnNewRuntimeNotRefused(t *testing.T) {
	src := map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	}

	for name, switchOn := range liveGuardSwitches() {
		t.Run(name, func(t *testing.T) {
			m := testModuleInline(t, src)
			switchOn(t)
			adjuster := &stubConfigValueAdjuster{attr: "test_map", key: "marker_key", value: "v"}

			ctx := testContext2(t, liveGuardOpts(adjuster))
			plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
			if hasLiveOnNewRuntimeRefusal(diags) {
				t.Fatalf("plan with a ConfigValueAdjuster was refused on the new runtime; diagnostics:\n%s", diags.ErrWithWarnings())
			}
			assertNoErrors(t, diags)
			if !hasNewRuntimeWarning(diags) {
				t.Fatalf("plan did not reach internal/engine; diagnostics:\n%s", diags.ErrWithWarnings())
			}
			if len(adjuster.calls) == 0 {
				t.Errorf("the new runtime planned without asking the ConfigValueAdjuster")
			}

			ctx = testContext2(t, liveGuardOpts(adjuster))
			_, diags = ctx.Apply(context.Background(), plan, m, nil)
			if hasLiveOnNewRuntimeRefusal(diags) {
				t.Fatalf("apply with a ConfigValueAdjuster was refused on the new runtime; diagnostics:\n%s", diags.ErrWithWarnings())
			}
			assertNoErrors(t, diags)
			if !hasNewRuntimeWarning(diags) {
				t.Fatalf("apply did not reach internal/engine; diagnostics:\n%s", diags.ErrWithWarnings())
			}

			ctx = testContext2(t, liveGuardOpts(adjuster))
			diags = ctx.Validate(context.Background(), m)
			if hasLiveOnNewRuntimeRefusal(diags) {
				t.Fatalf("validate with a ConfigValueAdjuster was refused on the new runtime; diagnostics:\n%s", diags.ErrWithWarnings())
			}
			assertNoErrors(t, diags)
		})
	}
}

// TestContextLiveOnNewRuntimeNotRefusedWithoutAdjuster: with no adjuster the
// new runtime is chosen as before and nothing names the refusal. The run's
// own result on the new runtime is not this test's business.
func TestContextLiveOnNewRuntimeNotRefusedWithoutAdjuster(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	})
	forceExperimentalRuntimeForTesting(t)
	ctx := testContext2(t, liveGuardOpts(nil))
	_, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if hasLiveOnNewRuntimeRefusal(diags) {
		t.Fatalf("a run with no ConfigValueAdjuster was refused as live mode")
	}
}
