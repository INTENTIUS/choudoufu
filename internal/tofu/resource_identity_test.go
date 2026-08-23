// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestContext2Plan_resourceIdentityResolverNilContract is the plan-node
// seam's nil contract (rfc/20260823-foundation-order-ruling.md, ruling 3;
// HANDOFF.md "The order", item 3): with no resolver and no adjuster
// configured, plugging the fields into ContextOpts must not change a single
// byte of an ordinary plan. It plans the same greenfield resource - no
// prior state, no import block, the shape TestContext2Plan_importResourceBasic
// uses for the same provider - once through a Context built without
// mentioning the new fields at all, and once through a Context that sets
// them explicitly to nil, and requires the two resulting
// ResourceInstanceChangeSrc values to be identical.
func TestContext2Plan_resourceIdentityResolverNilContract(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	})

	planWith := func(opts *ContextOpts) *plans.ResourceInstanceChangeSrc {
		t.Helper()

		p := simpleMockProvider()

		opts.Plugins = plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil)

		ctx := testContext2(t, opts)

		plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
		if diags.HasErrors() {
			t.Fatalf("unexpected errors\n%s", diags.Err())
		}

		instPlan := plan.Changes.ResourceInstance(addr)
		if instPlan == nil {
			t.Fatalf("no plan for %s at all", addr)
		}
		return instPlan
	}

	unset := planWith(&ContextOpts{})
	explicitNil := planWith(&ContextOpts{
		ResourceIdentityResolver: nil,
		ConfigValueAdjuster:      nil,
	})

	if diff := cmp.Diff(unset, explicitNil); diff != "" {
		t.Errorf("plan changed by merely mentioning the nil-valued fields in ContextOpts (-unset +explicitNil):\n%s", diff)
	}

	if unset.Action != plans.Create {
		t.Fatalf("wrong action for a resource with no prior state: got %s, want %s", unset.Action, plans.Create)
	}
	if unset.Importing != nil {
		t.Fatalf("unexpected import: a nil resolver must never cause an import, got %#v", unset.Importing)
	}
}

// stubResourceIdentityResolver is a ResourceIdentityResolver that resolves
// exactly one address to a fixed providers.ImportTarget and reports "not
// found" for every other address, to prove the resolver branch in
// managedResourceExecute actually reaches the provider's ImportResourceState
// call and produces an import in the plan.
type stubResourceIdentityResolver struct {
	addr   addrs.AbsResourceInstance
	target providers.ImportTarget

	calls []addrs.AbsResourceInstance
}

func (s *stubResourceIdentityResolver) ResolveResourceIdentity(_ context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (providers.ImportTarget, bool, tfdiags.Diagnostics) {
	s.calls = append(s.calls, addr)
	if config == cty.NilVal {
		panic("resolver called with no evaluated configuration value")
	}
	if schema.Block == nil {
		panic("resolver called with no resource schema")
	}
	if addr.Equal(s.addr) {
		return s.target, true, nil
	}
	return providers.ImportTarget{}, false, nil
}

func TestContext2Plan_resourceIdentityResolverStubImports(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	})

	p := simpleMockProvider()
	p.ImportResourceStateResponse = &providers.ImportResourceStateResponse{
		ImportedResources: []providers.ImportedResource{
			{
				TypeName: "test_object",
				State: cty.ObjectVal(map[string]cty.Value{
					"test_string": cty.StringVal("foo"),
				}),
			},
		},
	}
	p.ReadResourceResponse = &providers.ReadResourceResponse{
		NewState: cty.ObjectVal(map[string]cty.Value{
			"test_string": cty.StringVal("foo"),
		}),
	}

	resolver := &stubResourceIdentityResolver{
		addr:   addr,
		target: providers.ImportTarget{ID: "stub-123"},
	}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ResourceIdentityResolver: resolver,
	})

	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors\n%s", diags.Err())
	}

	instPlan := plan.Changes.ResourceInstance(addr)
	if instPlan == nil {
		t.Fatalf("no plan for %s at all", addr)
	}

	if instPlan.Importing == nil {
		t.Fatalf("expected the resolver's target to produce an import, got a non-import change (action %s)", instPlan.Action)
	}
	if instPlan.Importing.ID != "stub-123" {
		t.Errorf("wrong import ID: got %q, want %q", instPlan.Importing.ID, "stub-123")
	}

	if len(resolver.calls) != 1 || !resolver.calls[0].Equal(addr) {
		t.Errorf("expected the resolver to be asked once for %s, got calls %v", addr, resolver.calls)
	}
}
