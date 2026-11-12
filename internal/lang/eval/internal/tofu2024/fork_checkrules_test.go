// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu2024

// choudoufu fork port (#2418): remove when upstream #4390 lands.

import (
	"context"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcltest"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/engine/forkport/checkrules"
	"github.com/intentius/choudoufu/internal/lang/exprs"
	"github.com/intentius/choudoufu/internal/lang/grapheval"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestForkPostconditionsGateDependents: a failing postcondition while
// planning hands what refers to the instance an unknown value marked as an
// evaluation error, as the default runtime's plan walk stops there; during
// the apply, and when the postcondition passes, the result goes through
// unchanged.
func TestForkPostconditionsGateDependents(t *testing.T) {
	t.Setenv(forkport.AllOff, "")
	t.Setenv(forkport.Switch(checkrules.Name), "")

	config := &configs.Resource{
		Mode: addrs.DataResourceMode,
		Type: "test_data_source",
		Name: "a",
		Postconditions: []*configs.CheckRule{{
			Condition:    hcltest.MockExprTraversalSrc("self.ok"),
			ErrorMessage: hcltest.MockExprLiteral(cty.StringVal("Results cannot be empty.")),
		}},
	}
	addr := addrs.Resource{Mode: addrs.DataResourceMode, Type: "test_data_source", Name: "a"}.Absolute(addrs.RootModuleInstance).Instance(addrs.NoKey)
	check := forkPostconditions(config, exprs.FlatScopeForTesting(nil), addr)
	if check == nil {
		t.Fatal("no postcondition check with the port on")
	}

	failing := cty.ObjectVal(map[string]cty.Value{"ok": cty.False})
	passing := cty.ObjectVal(map[string]cty.Value{"ok": cty.True})

	tests := map[string]struct {
		phase    checkrules.Phase
		result   cty.Value
		gated    bool
		severity tfdiags.Severity
	}{
		"plan, failing":         {checkrules.Plan, failing, true, tfdiags.Error},
		"refresh-only, failing": {checkrules.RefreshOnly, failing, true, tfdiags.Warning},
		"apply, failing":        {checkrules.Apply, failing, false, tfdiags.Error},
		"plan, passing":         {checkrules.Plan, passing, false, 0},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := grapheval.ContextWithNewWorker(t.Context())
			ctx = grapheval.ContextWithRequestTracker(ctx, grapheval.NewRequestTracker())
			ctx = checkrules.WithPhase(ctx, test.phase)

			got, diags := check(ctx, test.result)
			if test.severity == 0 {
				if len(diags) != 0 {
					t.Fatalf("unexpected diagnostics: %s", diags.ErrWithWarnings())
				}
			} else if len(diags) != 1 || diags[0].Severity() != test.severity || diags[0].Description().Summary != "Resource postcondition failed" {
				t.Fatalf("want one %s \"Resource postcondition failed\", got: %s", test.severity, diags.ErrWithWarnings())
			}
			if test.gated {
				if !exprs.IsEvalError(got) || got.IsKnown() || !got.Type().Equals(test.result.Type()) {
					t.Errorf("dependents see %#v, want an unknown %s marked as an evaluation error", got, test.result.Type().FriendlyName())
				}
			} else if !got.RawEquals(test.result) {
				t.Errorf("dependents see %#v, want the result %#v", got, test.result)
			}
		})
	}
}

// TestForkRefuseScopedResource: a check block's scoped data resource can be
// referred to only from that check block's assertions; anywhere else the
// reference is refused once, from its static check, with the default
// runtime's error, and evaluates to an error placeholder.
func TestForkRefuseScopedResource(t *testing.T) {
	scopedAddr := addrs.Resource{Mode: addrs.DataResourceMode, Type: "checks_object", Name: "nested"}
	otherAddr := addrs.Resource{Mode: addrs.DataResourceMode, Type: "checks_object", Name: "outside"}
	inst := &CompiledModuleInstance{
		forkScopedResources: map[addrs.Resource]addrs.Check{scopedAddr: {Name: "error"}},
	}
	top := &moduleInstanceScope{inst: inst}
	own := &moduleInstanceScope{inst: inst, forkCheck: &addrs.Check{Name: "error"}}
	another := &moduleInstanceScope{inst: inst, forkCheck: &addrs.Check{Name: "another"}}
	rng := tfdiags.SourceRangeFromHCL(hcl.Range{Filename: "main.tf"})

	if refused := own.forkRefuseScopedResource(scopedAddr, rng); refused != nil {
		t.Errorf("the check block's own assertions were refused its scoped data resource")
	}
	if refused := top.forkRefuseScopedResource(otherAddr, rng); refused != nil {
		t.Errorf("an unscoped data resource was refused")
	}
	for name, scope := range map[string]*moduleInstanceScope{"module": top, "another check": another} {
		t.Run(name, func(t *testing.T) {
			refused := scope.forkRefuseScopedResource(scopedAddr, rng)
			if refused == nil {
				t.Fatal("reference allowed")
			}
			diags := refused.StaticCheckTraversal(nil)
			want := `Reference to scoped resource: The referenced data resource "checks_object" "nested" is not available from this context.`
			if !diags.HasErrors() || diags.Err().Error() != want {
				t.Errorf("wrong static check diagnostics\ngot:  %v\nwant: %s", diags.Err(), want)
			}
			v, _ := refused.Value(context.Background())
			if !exprs.IsEvalError(v) || v.IsKnown() {
				t.Errorf("refused reference evaluates to %#v, want an error placeholder", v)
			}
		})
	}
}
