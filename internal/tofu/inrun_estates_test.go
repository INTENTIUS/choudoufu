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
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

func inRunEstatesProviders(t *testing.T, readCalls *int) plugins.Library {
	t.Helper()
	test := simpleMockProvider()
	tf := &MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			DataSources: map[string]providers.Schema{
				"terraform_estate_outputs": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
					"estate": {Type: cty.String, Required: true},
					"names":  {Type: cty.Set(cty.String), Required: true},
					"values": {Type: cty.DynamicPseudoType, Computed: true},
				}}},
			},
		},
	}
	tf.ReadDataSourceFn = func(providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
		*readCalls++
		return providers.ReadDataSourceResponse{}
	}
	return plugins.NewLibrary(map[addrs.Provider]providers.Factory{
		addrs.NewDefaultProvider("test"):      testProviderFuncFixed(test),
		addrs.NewBuiltInProvider("terraform"): testProviderFuncFixed(tf),
	}, nil)
}

const inRunEstatesApp = `
data "terraform_estate_outputs" "net" {
  estate = "net"
  names  = ["vpc_id"]
}

resource "test_object" "subnet" {
  test_string = data.terraform_estate_outputs.net.values.vpc_id
}
`

// A composed run's read of an estate in the run waits for that estate's
// module, is deferred to the apply while the module has changes, and is
// answered from the module's outputs, never from the provider.
func TestContext2Plan_inRunEstateRead(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
module "net" {
  source = "./net"
}
module "app" {
  source = "./app"
}
`,
		"net/main.tf": `
resource "test_object" "vpc" {
  test_string = "vpc-1"
}
output "vpc_id" {
  value = test_object.vpc.test_string
}
`,
		"app/main.tf": inRunEstatesApp,
	})

	readCalls := 0
	ctx := testContext2(t, &ContextOpts{
		Plugins:      inRunEstatesProviders(t, &readCalls),
		InRunEstates: map[string]addrs.Module{"net": {"net"}},
	})

	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors\n%s", diags.Err())
	}
	dataAddr := mustResourceInstanceAddr("module.app.data.terraform_estate_outputs.net")
	dc := plan.Changes.ResourceInstance(dataAddr)
	if dc == nil || dc.Action != plans.Read {
		t.Fatalf("the read should be deferred to the apply, got %#v", dc)
	}
	if dc.ActionReason != plans.ResourceInstanceReadBecauseDependencyPending {
		t.Fatalf("wrong reason %s", dc.ActionReason)
	}

	state, diags := ctx.Apply(context.Background(), plan, m, nil)
	if diags.HasErrors() {
		t.Fatalf("unexpected apply errors\n%s", diags.Err())
	}
	if readCalls != 0 {
		t.Fatalf("the provider was asked to read an estate that is in the run (%d calls)", readCalls)
	}
	// The write-back records a module's outputs as its estate's root
	// outputs, so the final state has to carry them.
	if ms := state.Module(addrs.RootModuleInstance.Child("net", addrs.NoKey)); ms == nil || ms.OutputValues["vpc_id"] == nil {
		t.Fatalf("the final state does not carry module.net's output")
	}
	subnet := state.ResourceInstance(mustResourceInstanceAddr("module.app.test_object.subnet"))
	if subnet == nil || subnet.Current == nil || !strings.Contains(string(subnet.Current.AttrsJSON), `"vpc-1"`) {
		t.Fatalf("the subnet did not get the net module's output: %#v", subnet)
	}
}

// With nothing pending in the module the read happens in the plan, from the
// outputs the walk holds.
func TestContext2Plan_inRunEstateReadNothingPending(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
module "net" {
  source = "./net"
}
module "app" {
  source = "./app"
}
`,
		"net/main.tf": `
resource "test_object" "vpc" {
  test_string = "vpc-1"
}
output "vpc_id" {
  value = test_object.vpc.test_string
}
`,
		"app/main.tf": inRunEstatesApp,
	})
	prior := states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(mustResourceInstanceAddr("module.net.test_object.vpc"), &states.ResourceInstanceObjectSrc{
			Status:    states.ObjectReady,
			AttrsJSON: []byte(`{"id":"vpc","test_string":"vpc-1"}`),
		}, mustProviderConfig(`provider["registry.opentofu.org/hashicorp/test"]`), addrs.NoKey)
	})

	readCalls := 0
	ctx := testContext2(t, &ContextOpts{
		Plugins:      inRunEstatesProviders(t, &readCalls),
		InRunEstates: map[string]addrs.Module{"net": {"net"}},
	})
	opts := *DefaultPlanOpts
	opts.SkipRefresh = true
	plan, diags := ctx.Plan(context.Background(), m, prior, &opts)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors\n%s", diags.Err())
	}
	if readCalls != 0 {
		t.Fatalf("the provider was asked to read an estate that is in the run")
	}
	sc := plan.Changes.ResourceInstance(mustResourceInstanceAddr("module.app.test_object.subnet"))
	if sc == nil || sc.Action != plans.Create {
		t.Fatalf("subnet should be created, got %#v", sc)
	}
	if dc := plan.Changes.ResourceInstance(mustResourceInstanceAddr("module.app.data.terraform_estate_outputs.net")); dc != nil && dc.Action == plans.Read {
		t.Fatalf("the read should have happened in the plan")
	}
}

// An estate reading its own outputs is refused by name.
func TestContext2Plan_inRunEstateSelfRead(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
module "net" {
  source = "./net"
}
`,
		"net/main.tf": `
data "terraform_estate_outputs" "me" {
  estate = "net"
  names  = ["vpc_id"]
}
output "vpc_id" {
  value = "x"
}
`,
	})
	readCalls := 0
	ctx := testContext2(t, &ContextOpts{
		Plugins:      inRunEstatesProviders(t, &readCalls),
		InRunEstates: map[string]addrs.Module{"net": {"net"}},
	})
	_, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), "cannot read its own outputs") {
		t.Fatalf("want the self-read refusal, got %v", diags.Err())
	}
}
