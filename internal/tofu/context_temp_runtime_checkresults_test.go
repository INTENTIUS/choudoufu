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
	"github.com/intentius/choudoufu/internal/checks"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/checkrules"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

// choudoufu fork port (#2410): remove when upstream #4390 lands. The
// checkrules port's check results (C3) on the new runtime: every kind of
// checkable object, per instance, and objects in modules with no instances,
// which upstream's TestContext2Apply_moduleCountZeroChecks and its siblings
// cover only with a check block's scoped data source, refused by the
// data-sources row. It drives newEnginePlan and newEngineApply directly, so
// nothing falls back to the default runtime.
func TestNewRuntimeCheckrulesPortResults(t *testing.T) {
	child := `
variable "v" {
  type    = string
  default = "ok"
  validation {
    condition     = var.v != ""
    error_message = "Empty."
  }
}

resource "test_resource" "r" {
  value = var.v
  lifecycle {
    precondition {
      condition     = var.v != "nope"
      error_message = "Wrong v."
    }
  }
}

output "o" {
  value = test_resource.r.output
  precondition {
    condition     = var.v != "nope"
    error_message = "Wrong v."
  }
}

check "c" {
  assert {
    condition     = test_resource.r.output != ""
    error_message = "Blank."
  }
}
`
	m := testModuleInline(t, map[string]string{
		"main.tf": `
variable "boop" {
  type = string
}

resource "test_resource" "a" {
  count = 2
  value = var.boop
  lifecycle {
    postcondition {
      condition     = self.output != ""
      error_message = "Output must not be blank."
    }
  }
}

check "warn" {
  assert {
    condition     = var.boop != "warn"
    error_message = "Boop is warn."
  }
}

module "zero" {
  count  = 0
  source = "./child"
}

module "empty" {
  for_each = toset([])
  source   = "./child"
}

module "one" {
  source = "./child"
}

# The default runtime's apply skips a module output nothing refers to, and
# leaves its check results unknown; the new runtime checks it all the same.
output "one" {
  value = module.one.o
}
`,
		"child/main.tf": child,
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
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) (resp providers.PlanResourceChangeResponse) {
		m := req.ProposedNewState.AsValueMap()
		m["output"] = m["value"]
		resp.PlannedState = cty.ObjectVal(m)
		return resp
	}
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) (resp providers.ApplyResourceChangeResponse) {
		resp.NewState = req.PlannedState
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
	unset := func(t *testing.T) {
		t.Setenv(forkport.AllOff, "")
		t.Setenv(forkport.Switch(checkrules.Name), "")
	}

	one := addrs.RootModuleInstance.Child("one", addrs.NoKey)
	childVar := addrs.InputVariable{Name: "v"}
	childRes := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_resource", Name: "r"}
	childOut := addrs.OutputValue{Name: "o"}
	childCheck := addrs.Check{Name: "c"}
	rootRes := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_resource", Name: "a"}

	// object is the status of one checkable object, and its number of
	// failure messages.
	type object struct {
		status checks.Status
		msgs   int
	}
	// aggregate is a configuration object's status and its objects.
	type aggregate struct {
		status  checks.Status
		objects map[string]object
	}
	collect := func(t *testing.T, results *states.CheckResults) map[string]aggregate {
		t.Helper()
		if results == nil {
			t.Fatal("no check results")
		}
		got := make(map[string]aggregate)
		for _, elem := range results.ConfigResults.Elems {
			agg := aggregate{status: elem.Value.Status}
			if elem.Value.ObjectResults.Elems != nil {
				agg.objects = make(map[string]object)
				for _, o := range elem.Value.ObjectResults.Elems {
					agg.objects[o.Key.String()] = object{o.Value.Status, len(o.Value.FailureMessages)}
				}
			}
			got[elem.Key.String()] = agg
		}
		return got
	}
	pass := object{status: checks.StatusPass}
	// expect is the results with boop set to "ok" or "warn"; the check
	// block in the root fails with "warn".
	expect := func(warn bool) map[string]aggregate {
		warnCheck := aggregate{status: checks.StatusPass, objects: map[string]object{"check.warn": pass}}
		if warn {
			warnCheck = aggregate{status: checks.StatusFail, objects: map[string]object{"check.warn": {checks.StatusFail, 1}}}
		}
		none := aggregate{status: checks.StatusPass, objects: map[string]object{}}
		ret := map[string]aggregate{
			rootRes.InModule(addrs.RootModule).String(): {status: checks.StatusPass, objects: map[string]object{
				"test_resource.a[0]": pass,
				"test_resource.a[1]": pass,
			}},
			"check.warn": warnCheck,
		}
		for _, name := range []string{"zero", "empty", "one"} {
			mod := addrs.RootModule.Child(name)
			for _, configAddr := range []addrs.ConfigCheckable{
				childVar.InModule(mod), childRes.InModule(mod), childOut.InModule(mod), childCheck.InModule(mod),
			} {
				ret[configAddr.String()] = none
			}
		}
		ret[childVar.InModule(addrs.RootModule.Child("one")).String()] = aggregate{status: checks.StatusPass, objects: map[string]object{childVar.Absolute(one).String(): pass}}
		ret[childRes.InModule(addrs.RootModule.Child("one")).String()] = aggregate{status: checks.StatusPass, objects: map[string]object{childRes.Instance(addrs.NoKey).Absolute(one).String(): pass}}
		ret[childOut.InModule(addrs.RootModule.Child("one")).String()] = aggregate{status: checks.StatusPass, objects: map[string]object{childOut.Absolute(one).String(): pass}}
		ret[childCheck.InModule(addrs.RootModule.Child("one")).String()] = aggregate{status: checks.StatusPass, objects: map[string]object{childCheck.Absolute(one).String(): pass}}
		return ret
	}
	compare := func(t *testing.T, got, want map[string]aggregate) {
		t.Helper()
		for k, w := range want {
			g, ok := got[k]
			if !ok {
				t.Errorf("no result for %s", k)
				continue
			}
			if g.status != w.status || len(g.objects) != len(w.objects) || (g.objects == nil) != (w.objects == nil) {
				t.Errorf("wrong result for %s\ngot:  %v\nwant: %v", k, g, w)
				continue
			}
			for ok, ow := range w.objects {
				if og := g.objects[ok]; og != ow {
					t.Errorf("wrong result for %s object %s\ngot:  %v\nwant: %v", k, ok, og, ow)
				}
			}
		}
		for k := range got {
			if _, ok := want[k]; !ok {
				t.Errorf("unexpected result for %s", k)
			}
		}
	}

	t.Run("on", func(t *testing.T) {
		unset(t)
		for _, boop := range []string{"ok", "warn"} {
			t.Run(boop, func(t *testing.T) {
				pl, diags := newCtx(t).newEnginePlan(context.Background(), m, states.NewState(), &PlanOpts{Mode: plans.NormalMode, SetVariables: vars(boop)})
				if diags.HasErrors() {
					t.Fatal(diags.Err())
				}
				compare(t, collect(t, pl.Checks), expect(boop == "warn"))
				state, diags := newCtx(t).newEngineApply(context.Background(), m, pl, vars(boop))
				if diags.HasErrors() {
					t.Fatal(diags.Err())
				}
				compare(t, collect(t, state.CheckResults), expect(boop == "warn"))
			})
		}
	})
	// The expected results are the default runtime's.
	t.Run("default runtime", func(t *testing.T) {
		t.Setenv("TOFU_X_EXPERIMENTAL_RUNTIME", "")
		if experimentalRuntimeEnabled() {
			t.Skip("the new runtime is forced on")
		}
		for _, boop := range []string{"ok", "warn"} {
			t.Run(boop, func(t *testing.T) {
				ctx := newCtx(t)
				pl, diags := ctx.Plan(context.Background(), m, states.NewState(), &PlanOpts{Mode: plans.NormalMode, SetVariables: vars(boop)})
				if diags.HasErrors() {
					t.Fatal(diags.Err())
				}
				compare(t, collect(t, pl.Checks), expect(boop == "warn"))
				state, diags := ctx.Apply(context.Background(), pl, m, nil)
				if diags.HasErrors() {
					t.Fatal(diags.Err())
				}
				compare(t, collect(t, state.CheckResults), expect(boop == "warn"))
			})
		}
	})
	t.Run(forkport.Switch(checkrules.Name), func(t *testing.T) {
		unset(t)
		t.Setenv(forkport.Switch(checkrules.Name), "1")
		pl, diags := newCtx(t).newEnginePlan(context.Background(), m, states.NewState(), &PlanOpts{Mode: plans.NormalMode, SetVariables: vars("ok")})
		if diags.HasErrors() {
			t.Fatal(diags.Err())
		}
		if pl.Checks != nil {
			t.Fatal("the port is off, but the plan has check results")
		}
	})
}
