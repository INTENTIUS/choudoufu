// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"fmt"
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

// These tests cover attributes written in block syntax on the new runtime
// (#2256): a list- or set-of-object attribute, such as aws_security_group's
// ingress and egress, written as one nested block per element. The default
// runtime accepts that through internal/lang/blocktoattr; the new runtime
// rejected it with "Unsupported block type" until it applied the same fixup.
// Found by the gauntlet estate reference-ec2-vpc.

// attrsAsBlocksConfig writes the set-of-object attribute "ingress" as
// blocks: two static ones and one dynamic one. Two of them refer to
// test_net.a, whose cidr_block is known only after apply, so test_fw.b can
// only be planned and applied after test_net.a if the references inside the
// blocks were found.
const attrsAsBlocksConfig = `
resource "test_net" "a" {
}

resource "test_fw" "b" {
  ingress {
    from_port   = 22
    cidr_blocks = [test_net.a.cidr_block]
  }
  ingress {
    from_port   = 443
    cidr_blocks = ["10.0.0.0/8"]
  }
  dynamic "ingress" {
    for_each = [8080]
    content {
      from_port   = ingress.value
      cidr_blocks = [test_net.a.cidr_block]
    }
  }
}
`

const attrsAsBlocksNetCIDR = "10.1.0.0/16"

// attrsAsBlocksProvider has a test_fw resource type whose ingress is an
// optional set-of-object attribute, the shape SchemaConfigModeAttr gives
// aws_security_group's ingress, and records the order resources are
// created in.
func attrsAsBlocksProvider() (*MockProvider, func() []string) {
	ingressType := cty.Set(cty.Object(map[string]cty.Type{
		"from_port":   cty.Number,
		"cidr_blocks": cty.List(cty.String),
	}))
	p := &MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{
				"test_net": {Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"id":         {Type: cty.String, Computed: true},
						"cidr_block": {Type: cty.String, Computed: true},
					},
				}},
				"test_fw": {Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"id":      {Type: cty.String, Computed: true},
						"ingress": {Type: ingressType, Optional: true},
					},
				}},
			},
		},
	}

	var mu sync.Mutex
	var created []string
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		if req.ProposedNewState.IsNull() {
			return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
		}
		attrs := req.ProposedNewState.AsValueMap()
		if req.PriorState.IsNull() {
			attrs["id"] = cty.UnknownVal(cty.String)
			if req.TypeName == "test_net" {
				attrs["cidr_block"] = cty.UnknownVal(cty.String)
			}
		}
		return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(attrs)}
	}
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		if req.PlannedState.IsNull() {
			return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
		}
		mu.Lock()
		created = append(created, req.TypeName)
		mu.Unlock()
		attrs := req.PlannedState.AsValueMap()
		attrs["id"] = cty.StringVal(req.TypeName + "-id")
		if req.TypeName == "test_net" {
			attrs["cidr_block"] = cty.StringVal(attrsAsBlocksNetCIDR)
		}
		return providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(attrs)}
	}
	return p, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(created)
	}
}

func attrsAsBlocksContext(t *testing.T, p *MockProvider) *Context {
	t.Helper()
	return testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
	})
}

// TestNewRuntimeValidate_attrsAsBlocks: validate accepts a set-of-object
// attribute written as static and dynamic blocks.
func TestNewRuntimeValidate_attrsAsBlocks(t *testing.T) {
	forceExperimentalRuntimeForTesting(t)
	m := testModuleInline(t, map[string]string{"main.tf": attrsAsBlocksConfig})
	p, _ := attrsAsBlocksProvider()

	diags := attrsAsBlocksContext(t, p).Validate(context.Background(), m)
	assertNoErrors(t, diags)
}

// TestNewRuntimePlanApply_attrsAsBlocks: the same configuration plans and
// applies on the new runtime. The references inside the ingress blocks make
// test_fw.b depend on test_net.a: the plan carries the unknown cidr_block
// into both blocks that refer to it, apply creates test_net.a first, and the
// applied test_fw.b holds the CIDR test_net.a was created with.
func TestNewRuntimePlanApply_attrsAsBlocks(t *testing.T) {
	forceExperimentalRuntimeForTesting(t)
	m := testModuleInline(t, map[string]string{"main.tf": attrsAsBlocksConfig})
	p, created := attrsAsBlocksProvider()
	ctx := attrsAsBlocksContext(t, p)

	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	assertNoErrors(t, diags)
	if len(plan.ExecutionGraph) == 0 {
		t.Fatal("the plan has no execution graph, so it was not made by the new runtime; this test proves nothing")
	}

	fwAddr := mustResourceInstanceAddr("test_fw.b")
	fwSchema := p.GetProviderSchemaResponse.ResourceTypes["test_fw"].Block
	rc := plan.Changes.ResourceInstance(fwAddr)
	if rc == nil || rc.Action != plans.Create {
		t.Fatalf("planned change for %s is %#v, want a create", fwAddr, rc)
	}
	fwProviderSchema := p.GetProviderSchemaResponse.ResourceTypes["test_fw"]
	change, err := rc.Decode(&fwProviderSchema)
	if err != nil {
		t.Fatalf("decoding the planned change: %s", err)
	}
	ingress := change.After.GetAttr("ingress")
	if ingress.IsNull() || ingress.IsWhollyKnown() {
		t.Fatalf("planned ingress is %#v; the blocks referring to test_net.a.cidr_block should hold it unknown until apply", ingress)
	}

	newState, diags := ctx.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)

	if got, want := fmt.Sprint(created()), "[test_net test_fw]"; got != want {
		t.Errorf("resources were created in the order %s, want %s", got, want)
	}

	rs := newState.ResourceInstance(fwAddr)
	if rs == nil || rs.Current == nil {
		t.Fatalf("no current object for %s in the applied state:\n%s", fwAddr, newState)
	}
	obj, err := rs.Current.Decode(fwSchema.ImpliedType())
	if err != nil {
		t.Fatalf("decoding the applied object: %s", err)
	}
	got := map[string]string{}
	for it := obj.Value.GetAttr("ingress").ElementIterator(); it.Next(); {
		_, v := it.Element()
		port := v.GetAttr("from_port").AsBigFloat().String()
		var cidrs []string
		for _, c := range v.GetAttr("cidr_blocks").AsValueSlice() {
			cidrs = append(cidrs, c.AsString())
		}
		got[port] = fmt.Sprint(cidrs)
	}
	want := map[string]string{
		"22":   "[" + attrsAsBlocksNetCIDR + "]",
		"443":  "[10.0.0.0/8]",
		"8080": "[" + attrsAsBlocksNetCIDR + "]",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("applied ingress is %v, want %v", got, want)
	}
}
