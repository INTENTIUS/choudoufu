// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

const tgReplaceWarning = "Target group must be deleted before it is replaced"

func init() {
	proofs["aws-lb-target-group-replace-in-use"] = proof{
		schema: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"arn":         {Type: cty.String, Computed: true},
			"name":        {Type: cty.String, Optional: true},
			"name_prefix": {Type: cty.String, Optional: true},
			"port":        {Type: cty.Number, Optional: true},
		}},
		forceNew: []string{"name", "name_prefix", "port"},
		prior: cty.ObjectVal(map[string]cty.Value{
			"arn":         cty.StringVal("arn:aws:elasticloadbalancing:us-east-1:111122223333:targetgroup/repro-tg/1"),
			"name":        cty.StringVal("repro-tg"),
			"name_prefix": cty.NullVal(cty.String),
			"port":        cty.NumberIntVal(80),
		}),
		// The tg-listener-16889 repro's v2: the port changes, the name stays.
		config: cty.ObjectVal(map[string]cty.Value{
			"arn":         cty.NullVal(cty.String),
			"name":        cty.StringVal("repro-tg"),
			"name_prefix": cty.NullVal(cty.String),
			"port":        cty.NumberIntVal(8080),
		}),
		check: checkTargetGroupReplaceWarning,
	}
	proofs["aws-alb-target-group-replace-in-use"] = proofs["aws-lb-target-group-replace-in-use"]
}

// checkTargetGroupReplaceWarning is red-then-green: without the override a
// same-name replacement plans with no warning, with it the plan carries the
// warning and is otherwise the provider's own plan.
func checkTargetGroupReplaceWarning(t *testing.T, o Override) {
	p := proofs[o.Name]
	off := planWith(t, o, p, false)
	if len(off.RequiresReplace) == 0 {
		t.Fatalf("without the override the fake plans no replacement, so this proof cannot fail")
	}
	if hasWarning(off.Diagnostics, tgReplaceWarning, "") {
		t.Fatalf("without the override the plan already warns, so this proof cannot fail")
	}
	on := planWith(t, o, p, true)
	if on.Diagnostics.HasErrors() {
		t.Fatalf("errors with the override: %s", on.Diagnostics.Err())
	}
	if !hasWarning(on.Diagnostics, tgReplaceWarning, "name_prefix") {
		t.Errorf("with the override the plan does not warn or does not name the fix: %#v", on.Diagnostics)
	}
	if !hasWarning(on.Diagnostics, "Provider override applied", o.Name) {
		t.Errorf("the plan does not name the override that warned: %#v", on.Diagnostics)
	}
	if !on.PlannedState.RawEquals(off.PlannedState) {
		t.Errorf("the override changed the planned state:\n got %#v\nwant %#v", on.PlannedState, off.PlannedState)
	}
	if len(on.RequiresReplace) != len(off.RequiresReplace) {
		t.Errorf("the override changed RequiresReplace: got %#v, want %#v", on.RequiresReplace, off.RequiresReplace)
	}
}

// TestTargetGroupReplaceWarningOnlyForFixedName checks the warning stays off
// where the replacement can create first, and off where nothing is replaced.
func TestTargetGroupReplaceWarningOnlyForFixedName(t *testing.T) {
	o, _ := Lookup("aws-lb-target-group-replace-in-use")
	base := proofs[o.Name]
	with := func(prior, config map[string]cty.Value) proof {
		p := base
		pm, cm := base.prior.AsValueMap(), base.config.AsValueMap()
		for k, v := range prior {
			pm[k] = v
		}
		for k, v := range config {
			cm[k] = v
		}
		p.prior, p.config = cty.ObjectVal(pm), cty.ObjectVal(cm)
		return p
	}
	for _, tc := range []struct {
		name        string
		p           proof
		wantReplace bool
	}{
		{"the name changes too", with(nil, map[string]cty.Value{"name": cty.StringVal("repro-tg-8080")}), true},
		{"name_prefix", with(
			map[string]cty.Value{"name": cty.StringVal("repro-20261104"), "name_prefix": cty.StringVal("repro-")},
			map[string]cty.Value{"name": cty.NullVal(cty.String), "name_prefix": cty.StringVal("repro-")}), true},
		{"a generated name", with(
			map[string]cty.Value{"name": cty.StringVal("tf-20261104")},
			map[string]cty.Value{"name": cty.NullVal(cty.String)}), true},
		{"an unknown name", with(nil, map[string]cty.Value{"name": cty.UnknownVal(cty.String)}), true},
		{"no replacement", with(nil, map[string]cty.Value{"port": cty.NumberIntVal(80)}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := planWith(t, o, tc.p, true)
			if got := len(resp.RequiresReplace) != 0; got != tc.wantReplace {
				t.Fatalf("replacement planned = %v, want %v; the case does not test what it says", got, tc.wantReplace)
			}
			if len(resp.Diagnostics) != 0 {
				t.Errorf("diagnostics where the replacement can create first or nothing is replaced: %#v", resp.Diagnostics)
			}
		})
	}
}

// TestTargetGroupReplaceWarningOnDestroyAndCreate checks the warning stays
// off on a destroy plan (no configuration) and on a create (no prior state),
// even from a provider that answers every call with a replacement.
func TestTargetGroupReplaceWarningOnDestroyAndCreate(t *testing.T) {
	o, _ := Lookup("aws-lb-target-group-replace-in-use")
	p := proofs[o.Name]
	for _, tc := range []struct {
		name          string
		prior, config cty.Value
	}{
		{"create", cty.NullVal(p.prior.Type()), p.config},
		{"destroy", p.prior, cty.NullVal(p.config.Type())},
	} {
		resp, changed := warnFixedNameReplace(context.Background(), alwaysReplace{}, providers.PlanResourceChangeRequest{
			TypeName:   o.Type,
			PriorState: tc.prior,
			Config:     tc.config,
		})
		if changed || len(resp.Diagnostics) != 0 {
			t.Errorf("%s: the override acted: changed=%v %#v", tc.name, changed, resp.Diagnostics)
		}
	}
}

// alwaysReplace plans a replacement of name for every request.
type alwaysReplace struct{ providers.Interface }

func (alwaysReplace) PlanResourceChange(_ context.Context, req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
	return providers.PlanResourceChangeResponse{
		PlannedState:    req.ProposedNewState,
		RequiresReplace: []cty.Path{cty.GetAttrPath("name")},
	}
}
