// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"slices"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

// TestTargetedResourcesIncludesDependencies is the property GitHub issue
// #352's fix rests on, and the reason the fork asks the graph instead of
// working targeting out for itself: -target is not "the addresses the user
// typed". Targeting module.B.aws_instance.bar has to keep
// module.A.aws_instance.foo as well, because bar's own argument reads A's
// output, which reads foo - three reference hops through two module
// boundaries, none of them visible in what the user typed.
//
// The live layer needs that closure to be the plan's own. A resource the plan
// still acts on but the stateless pipeline skipped would have no projection
// entry, and a plan with no prior state for a live object proposes creating a
// second one.
func TestTargetedResourcesIncludesDependencies(t *testing.T) {
	ctx, m := targetScopeContext(t, "plan-targeted-cross-module")

	got, diags := ctx.TargetedResources(context.Background(), m, states.NewState(), &PlanOpts{
		Mode: plans.NormalMode,
		Targets: []addrs.Targetable{
			addrs.RootModuleInstance.Child("B", addrs.NoKey).Resource(
				addrs.ManagedResourceMode, "aws_instance", "bar",
			),
		},
	})
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	assertTargetedResources(t, got, []string{
		"module.A.aws_instance.foo",
		"module.B.aws_instance.bar",
	})
}

// TestTargetedResourcesDropsAnUntargetedSibling is the other half: a resource
// nothing targeted and nothing targeted depends on is absent. That absence is
// what issue #352's refusal was: aws_budgets_budget was never targeted and
// nothing needed it, and the stateless pipeline evaluated its identity
// arguments anyway.
func TestTargetedResourcesDropsAnUntargetedSibling(t *testing.T) {
	ctx, m := targetScopeContext(t, "plan-targeted")

	got, diags := ctx.TargetedResources(context.Background(), m, states.NewState(), &PlanOpts{
		Mode: plans.NormalMode,
		Targets: []addrs.Targetable{
			addrs.RootModuleInstance.Resource(
				addrs.ManagedResourceMode, "aws_instance", "foo",
			),
		},
	})
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	// bar reads foo, so bar depends on foo and not the other way round:
	// targeting foo keeps foo alone.
	assertTargetedResources(t, got, []string{"aws_instance.foo"})
}

// TestTargetedResourcesHonorsExclude pins that -exclude reaches the same seam.
// It has the same defect as -target did and is fixed by the same call:
// excluding a resource excludes what depends on it, and the stateless
// pipeline has to see the same set the graph does.
func TestTargetedResourcesHonorsExclude(t *testing.T) {
	ctx, m := targetScopeContext(t, "plan-targeted")

	got, diags := ctx.TargetedResources(context.Background(), m, states.NewState(), &PlanOpts{
		Mode: plans.NormalMode,
		Excludes: []addrs.Targetable{
			addrs.RootModuleInstance.Resource(
				addrs.ManagedResourceMode, "aws_instance", "foo",
			),
		},
	})
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	// foo is excluded, and bar reads foo, so bar goes with it. The module's
	// own resource is untouched.
	assertTargetedResources(t, got, []string{"module.mod.aws_instance.foo"})
}

// TestTargetedResourcesIsNilWhenUntargeted is the common case. An untargeted
// run must not build a graph here at all: the transformer would be a no-op,
// every block would come back in the answer, and the whole build would have
// been schema fetches and reference analysis to learn nothing. A nil answer is
// how the live layer's callers tell "no filtering" from "filtered to nothing".
func TestTargetedResourcesIsNilWhenUntargeted(t *testing.T) {
	ctx, m := targetScopeContext(t, "plan-targeted")

	got, diags := ctx.TargetedResources(context.Background(), m, states.NewState(), &PlanOpts{
		Mode: plans.NormalMode,
	})
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	if got != nil {
		t.Errorf("TargetedResources with no -target/-exclude = %v, want nil", got)
	}
}

func targetScopeContext(t *testing.T, fixture string) (*Context, *configs.Config) {
	t.Helper()
	m := testModule(t, fixture)
	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn
	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		}, nil),
	})
	return ctx, m
}

func assertTargetedResources(t *testing.T, got map[string]addrs.ConfigResource, want []string) {
	t.Helper()
	keys := make([]string, 0, len(got))
	for k, addr := range got {
		if k != addr.String() {
			t.Errorf("key %q does not match its own address %s", k, addr)
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, want) {
		t.Errorf("TargetedResources =\n  %v\nwant\n  %v", keys, want)
	}
}
