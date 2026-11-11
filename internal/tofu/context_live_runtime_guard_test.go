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

func hasLiveOnNewRuntimeRefusal(diags tfdiags.Diagnostics) bool {
	for _, d := range diags {
		if d.Severity() == tfdiags.Error && d.Description().Summary == LiveOnNewRuntimeSummary {
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

// TestContextLiveOnNewRuntimeRefused is the internal/tofu half of #2256 G:
// with a ConfigValueAdjuster set (every live entry point sets one) and the
// new runtime chosen by any means, Plan, Apply and Validate refuse before
// routing to internal/engine. Apply is given a plan made with the runtime
// off, which is the saved-plan case.
func TestContextLiveOnNewRuntimeRefused(t *testing.T) {
	src := map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	}
	adjuster := &stubConfigValueAdjuster{attr: "test_map", key: "marker_key", value: "v"}

	for name, switchOn := range liveGuardSwitches() {
		t.Run(name, func(t *testing.T) {
			m := testModuleInline(t, src)

			t.Run("plan", func(t *testing.T) {
				switchOn(t)
				ctx := testContext2(t, liveGuardOpts(adjuster))
				_, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
				if !hasLiveOnNewRuntimeRefusal(diags) {
					t.Fatalf("plan reached the new runtime with a ConfigValueAdjuster set; diagnostics:\n%s", diags.ErrWithWarnings())
				}
			})

			t.Run("apply of a plan made on the old runtime", func(t *testing.T) {
				ctx := testContext2(t, liveGuardOpts(adjuster))
				plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
				if diags.HasErrors() {
					t.Fatalf("plan on the old runtime failed: %s", diags.Err())
				}
				switchOn(t)
				ctx = testContext2(t, liveGuardOpts(adjuster))
				_, diags = ctx.Apply(context.Background(), plan, m, nil)
				if !hasLiveOnNewRuntimeRefusal(diags) {
					t.Fatalf("apply reached the new runtime with a ConfigValueAdjuster set; diagnostics:\n%s", diags.ErrWithWarnings())
				}
			})

			t.Run("validate", func(t *testing.T) {
				switchOn(t)
				ctx := testContext2(t, liveGuardOpts(adjuster))
				diags := ctx.Validate(context.Background(), m)
				if !hasLiveOnNewRuntimeRefusal(diags) {
					t.Fatalf("validate reached the new runtime with a ConfigValueAdjuster set; diagnostics:\n%s", diags.ErrWithWarnings())
				}
			})
		})
	}
}

// TestContextLiveOnNewRuntimeNotRefusedWithoutAdjuster is the control: the
// guard is about live mode, so with no adjuster the new runtime is chosen
// as before and nothing names the refusal. The run's own result on the new
// runtime is not this test's business.
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
