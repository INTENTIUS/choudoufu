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
	"github.com/intentius/choudoufu/internal/live/newruntime"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// These tests are #2256 U6 (#2342): on the new runtime, the Context's
// ResourceIdentityResolver is asked about each desired instance with no prior
// object, and a found target becomes an import of that object through the
// planning engine's import path (`#4726`, `#4727`). They run through
// Context.Plan and Context.Apply with the runtime forced on, and check the
// toggle's warning, which only newEnginePlan and newEngineApply emit, so a
// pass cannot come from the old runtime. resource_identity_test.go holds the
// same cases for whichever runtime the environment selects.

func hasNewRuntimeWarning(diags tfdiags.Diagnostics) bool {
	for _, d := range diags {
		if d.Severity() == tfdiags.Warning && d.Description().Summary == newruntime.WarningSummary {
			return true
		}
	}
	return false
}

const newRuntimeIdentitySrc = `
resource "test_object" "a" {
  test_string = "foo"
}
`

// newRuntimeIdentityProvider answers an import of "existing-1" with an object
// matching the configuration and reads it back unchanged, so an adopted
// object plans no change. Every other ID is absent.
func newRuntimeIdentityProvider() *MockProvider {
	p := simpleMockProvider()
	existing := cty.ObjectVal(map[string]cty.Value{
		"test_string": cty.StringVal("foo"),
		"test_number": cty.NullVal(cty.Number),
		"test_bool":   cty.NullVal(cty.Bool),
		"test_list":   cty.NullVal(cty.List(cty.String)),
		"test_map":    cty.NullVal(cty.Map(cty.String)),
	})
	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		if req.Target.ID != "existing-1" {
			return providers.ImportResourceStateResponse{}
		}
		return providers.ImportResourceStateResponse{
			ImportedResources: []providers.ImportedResource{{TypeName: "test_object", State: existing}},
		}
	}
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
	}
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
	}
	return p
}

func newRuntimeIdentityContext(t *testing.T, p *MockProvider, resolver ResourceIdentityResolver) *Context {
	t.Helper()
	return testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ResourceIdentityResolver: resolver,
	})
}

// TestNewRuntimeResolverAdoptsWithoutCreate: the resolver's target is
// imported, the plan records the import and proposes no create, and applying
// it calls no ApplyResourceChange and leaves the imported object in state.
// Without the planning engine's resolver call the plan is a Create with no
// import.
func TestNewRuntimeResolverAdoptsWithoutCreate(t *testing.T) {
	forceExperimentalRuntimeForTesting(t)
	addr := mustResourceInstanceAddr("test_object.a")
	m := testModuleInline(t, map[string]string{"main.tf": newRuntimeIdentitySrc})
	p := newRuntimeIdentityProvider()
	resolver := &stubResourceIdentityResolver{addr: addr, target: providers.ImportTarget{ID: "existing-1"}}

	ctx := newRuntimeIdentityContext(t, p, resolver)
	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	assertNoErrors(t, diags)
	if !hasNewRuntimeWarning(diags) {
		t.Fatalf("the plan did not run on the new runtime; diagnostics:\n%s", diags.ErrWithWarnings())
	}

	change := plan.Changes.ResourceInstance(addr)
	if change == nil {
		t.Fatalf("no planned change for %s", addr)
	}
	if change.Importing == nil || change.Importing.ID != "existing-1" {
		t.Fatalf("the resolver's target did not become an import: action %s, importing %#v", change.Action, change.Importing)
	}
	if change.Action != plans.NoOp {
		t.Errorf("adopting an object that matches its configuration planned %s, want %s", change.Action, plans.NoOp)
	}
	if len(resolver.calls) != 1 || !resolver.calls[0].Equal(addr) {
		t.Errorf("expected the resolver to be asked once for %s, got %v", addr, resolver.calls)
	}

	ctx = newRuntimeIdentityContext(t, p, resolver)
	state, diags := ctx.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)
	if !hasNewRuntimeWarning(diags) {
		t.Fatalf("the apply did not run on the new runtime; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	if p.ApplyResourceChangeCalled {
		t.Errorf("adopting an existing object called ApplyResourceChange (a create or an update)")
	}
	if rs := state.ResourceInstance(addr); rs == nil || rs.Current == nil {
		t.Errorf("the adopted object is not in the applied state:\n%s", state)
	}
}

// TestNewRuntimeResolverAbsentTargetPlansCreate: a resolver's target is a
// guess, so an import that finds nothing plans an ordinary create instead of
// failing with "Import returned no resources", as on the old runtime.
func TestNewRuntimeResolverAbsentTargetPlansCreate(t *testing.T) {
	forceExperimentalRuntimeForTesting(t)
	addr := mustResourceInstanceAddr("test_object.a")
	m := testModuleInline(t, map[string]string{"main.tf": newRuntimeIdentitySrc})
	p := newRuntimeIdentityProvider()
	resolver := &stubResourceIdentityResolver{addr: addr, target: providers.ImportTarget{ID: "not-there"}}

	ctx := newRuntimeIdentityContext(t, p, resolver)
	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	assertNoErrors(t, diags)
	if !hasNewRuntimeWarning(diags) {
		t.Fatalf("the plan did not run on the new runtime; diagnostics:\n%s", diags.ErrWithWarnings())
	}
	change := plan.Changes.ResourceInstance(addr)
	if change == nil {
		t.Fatalf("no planned change for %s", addr)
	}
	if change.Action != plans.Create || change.Importing != nil {
		t.Errorf("an absent resolver target planned %s with importing %#v, want a plain create", change.Action, change.Importing)
	}
	if !p.ImportResourceStateCalled {
		t.Errorf("the resolver's target was never imported, so the absence path was not exercised")
	}
}

// TestNewRuntimeResolverErrorFailsThePlan: a resolver error (NodeResolver's
// "No source for this instance's identity", say) fails the plan on the new
// runtime as it does on the old one.
func TestNewRuntimeResolverErrorFailsThePlan(t *testing.T) {
	forceExperimentalRuntimeForTesting(t)
	m := testModuleInline(t, map[string]string{"main.tf": newRuntimeIdentitySrc})
	p := newRuntimeIdentityProvider()

	ctx := newRuntimeIdentityContext(t, p, refusingResolver{})
	_, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if !diags.HasErrors() {
		t.Fatal("the resolver's refusal did not fail the plan")
	}
	found := false
	for _, d := range diags {
		if d.Severity() == tfdiags.Error && d.Description().Summary == "No source for this instance's identity" {
			found = true
		}
	}
	if !found {
		t.Errorf("the resolver's refusal is not among the errors:\n%s", diags.Err())
	}
	if !hasNewRuntimeWarning(diags) {
		t.Errorf("the plan did not run on the new runtime; diagnostics:\n%s", diags.ErrWithWarnings())
	}
}

type refusingResolver struct{}

func (refusingResolver) ResolveResourceIdentity(_ context.Context, addr addrs.AbsResourceInstance, _ cty.Value, _ providers.Schema) (providers.ImportTarget, bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	return providers.ImportTarget{}, false, diags.Append(tfdiags.Sourceless(tfdiags.Error, "No source for this instance's identity", addr.String()))
}
