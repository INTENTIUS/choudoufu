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
	"github.com/intentius/choudoufu/internal/engine/forkport/checkrules"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// choudoufu fork port (#2409): remove when upstream #4390 lands. The
// checkrules port on the new runtime, against the default runtime's
// behaviour in TestContext2Plan_resourcePreconditionPostcondition,
// TestContext2Apply_resourcePostcondition and
// TestContext2Validate_precondition_count. Those stay skipped behind
// ExperimentalFeatureCondition, because their other cases read check
// results from the plan and state, which the port does not record (C3,
// #2410).
//
// It drives newEnginePlan, newEngineApply and newEngineValidate directly,
// past the gap table's preflight, so with the port's switch set the
// conditions are not refused but ignored: a failing one reports nothing.
func TestNewRuntimeCheckrulesPort(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
variable "boop" {
  type = string
}

resource "test_resource" "a" {
  value = var.boop
  lifecycle {
    precondition {
      condition     = var.boop != "nope"
      error_message = "Wrong boop."
    }
    postcondition {
      condition     = self.output != ""
      error_message = "Output must not be blank."
    }
  }
}

check "c" {
  assert {
    condition     = test_resource.a.output != "warn"
    error_message = "Output is warn."
  }
}
`,
	})
	p := testProvider("test")
	p.GetProviderSchemaResponse = getProviderSchemaResponseFromProviderSchema(&ProviderSchema{
		ResourceTypes: map[string]*configschema.Block{
			"test_resource": {
				Attributes: map[string]*configschema.Attribute{
					"value":  {Type: cty.String, Required: true},
					"output": {Type: cty.String, Computed: true},
				},
			},
		},
	})
	// The planned output is the configured value with "out-" in front, so
	// a test picks the output by picking the value: "" for "", unknown for
	// "later", and the applied output is "" for "blank-at-apply".
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) (resp providers.PlanResourceChangeResponse) {
		m := req.ProposedNewState.AsValueMap()
		switch v := m["value"].AsString(); v {
		case "blank":
			m["output"] = cty.StringVal("")
		case "warn":
			m["output"] = cty.StringVal("warn")
		case "later", "blank-at-apply":
			m["output"] = cty.UnknownVal(cty.String)
		default:
			m["output"] = cty.StringVal("out-" + v)
		}
		resp.PlannedState = cty.ObjectVal(m)
		resp.LegacyTypeSystem = true
		return resp
	}
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) (resp providers.ApplyResourceChangeResponse) {
		m := req.PlannedState.AsValueMap()
		if !m["output"].IsKnown() {
			m["output"] = cty.StringVal("")
			if m["value"].AsString() == "later" {
				m["output"] = cty.StringVal("done")
			}
		}
		resp.NewState = cty.ObjectVal(m)
		return resp
	}
	newCtx := func(t *testing.T) *Context {
		return testContext2(t, &ContextOpts{
			Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
				addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
			}, nil),
		})
	}
	vars := func(boop string) InputValues {
		return InputValues{"boop": &InputValue{Value: cty.StringVal(boop), SourceType: ValueFromCLIArg}}
	}
	prior := states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(mustResourceInstanceAddr("test_resource.a"), &states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"value":"boop","output":"blorp"}`),
			Status:    states.ObjectReady,
		}, mustProviderConfig(`provider["registry.opentofu.org/hashicorp/test"]`), addrs.NoKey)
	})
	plan := func(t *testing.T, mode plans.Mode, state *states.State, boop string) (*plans.Plan, tfdiags.Diagnostics) {
		t.Helper()
		return newCtx(t).newEnginePlan(context.Background(), m, state, &PlanOpts{Mode: mode, SetVariables: vars(boop)})
	}
	unset := func(t *testing.T) {
		t.Setenv(forkport.AllOff, "")
		t.Setenv(forkport.Switch(checkrules.Name), "")
	}
	// summaries lists each diagnostic as "<severity> <summary>: <detail>".
	summaries := func(diags tfdiags.Diagnostics) string {
		var lines []string
		for _, d := range diags {
			sev := "warning"
			if d.Severity() == tfdiags.Error {
				sev = "error"
			}
			lines = append(lines, sev+" "+d.Description().Summary+": "+d.Description().Detail)
		}
		return strings.Join(lines, "\n")
	}
	want := func(t *testing.T, diags tfdiags.Diagnostics, want string) {
		t.Helper()
		if got := summaries(diags); got != want {
			t.Fatalf("wrong diagnostics\ngot:\n%s\nwant:\n%s", got, want)
		}
	}

	t.Run("on", func(t *testing.T) {
		unset(t)
		t.Run("conditions pass", func(t *testing.T) {
			_, diags := plan(t, plans.NormalMode, states.NewState(), "boop")
			want(t, diags, "")
		})
		t.Run("precondition fail", func(t *testing.T) {
			_, diags := plan(t, plans.NormalMode, states.NewState(), "nope")
			want(t, diags, "error Resource precondition failed: Wrong boop.")
			if p.PlanResourceChangeCalled {
				t.Error("the provider was asked to plan an instance whose precondition failed")
			}
		})
		t.Run("precondition fail refresh-only", func(t *testing.T) {
			_, diags := plan(t, plans.RefreshOnlyMode, prior, "nope")
			want(t, diags, "warning Resource precondition failed: Wrong boop.")
		})
		t.Run("precondition fail destroy", func(t *testing.T) {
			_, diags := plan(t, plans.DestroyMode, prior, "nope")
			want(t, diags, "")
		})
		t.Run("postcondition fail", func(t *testing.T) {
			_, diags := plan(t, plans.NormalMode, states.NewState(), "blank")
			want(t, diags, "error Resource postcondition failed: Output must not be blank.")
		})
		t.Run("check block fail", func(t *testing.T) {
			_, diags := plan(t, plans.NormalMode, states.NewState(), "warn")
			want(t, diags, "warning Check block assertion failed: Output is warn.")
		})
		t.Run("check block unknown, then apply", func(t *testing.T) {
			pl, diags := plan(t, plans.NormalMode, states.NewState(), "later")
			want(t, diags, "warning Check block assertion known after apply: The condition could not be evaluated at this time, a result will be known when this plan is applied.")
			_, diags = newCtx(t).newEngineApply(context.Background(), m, pl, vars("later"))
			want(t, diags, "")
		})
		t.Run("postcondition fail at apply", func(t *testing.T) {
			pl, diags := plan(t, plans.NormalMode, states.NewState(), "blank-at-apply")
			want(t, diags, "warning Check block assertion known after apply: The condition could not be evaluated at this time, a result will be known when this plan is applied.")
			state, diags := newCtx(t).newEngineApply(context.Background(), m, pl, vars("blank-at-apply"))
			want(t, diags, "error Resource postcondition failed: Output must not be blank.")
			// As in the default runtime, the object is saved all the same.
			if state.ResourceInstance(mustResourceInstanceAddr("test_resource.a")) == nil {
				t.Fatal("the object was not saved")
			}
		})
		t.Run("validate", func(t *testing.T) {
			diags := newCtx(t).newEngineValidate(context.Background(), m, vars("nope"))
			if diags.HasErrors() {
				t.Fatalf("validate reported a condition failure:\n%s", summaries(diags))
			}
		})
	})
	t.Run(forkport.Switch(checkrules.Name), func(t *testing.T) {
		unset(t)
		t.Setenv(forkport.Switch(checkrules.Name), "1")
		_, diags := plan(t, plans.NormalMode, states.NewState(), "nope")
		want(t, diags, "")
		_, diags = plan(t, plans.NormalMode, states.NewState(), "warn")
		want(t, diags, "")
	})
}
