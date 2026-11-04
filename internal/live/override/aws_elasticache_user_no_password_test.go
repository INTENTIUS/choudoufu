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

// The proof uses check rather than the plan-shaped fields because
// authentication_mode is a nested block with a computed attribute inside
// it (password_count). OpenTofu's proposed new state carries that computed
// value over from prior, and the shared proposedNewState only does so for
// top-level attributes, so this file builds the proposed state itself.

var elasticacheUserSchema = &configschema.Block{
	Attributes: map[string]*configschema.Attribute{
		"id":            {Type: cty.String, Computed: true},
		"arn":           {Type: cty.String, Computed: true},
		"access_string": {Type: cty.String, Required: true},
		"engine":        {Type: cty.String, Required: true},
		"user_id":       {Type: cty.String, Required: true},
		"user_name":     {Type: cty.String, Required: true},
	},
	BlockTypes: map[string]*configschema.NestedBlock{
		"authentication_mode": {
			Nesting:  configschema.NestingList,
			MaxItems: 1,
			Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
				"passwords":      {Type: cty.Set(cty.String), Optional: true, Sensitive: true},
				"password_count": {Type: cty.Number, Computed: true},
				"type":           {Type: cty.String, Required: true},
			}},
		},
	},
}

// elasticacheUser is the user's value with authentication_mode.type set to
// authType and password_count to count (null in configuration).
func elasticacheUser(id, authType string, count cty.Value) cty.Value {
	idv, arn := cty.StringVal(id), cty.StringVal("arn:aws:elasticache:us-east-1:1:user:"+id)
	if id == "" {
		idv, arn = cty.NullVal(cty.String), cty.NullVal(cty.String)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"id":            idv,
		"arn":           arn,
		"access_string": cty.StringVal("on ~* +@all"),
		"engine":        cty.StringVal("redis"),
		"user_id":       cty.StringVal("u1-user"),
		"user_name":     cty.StringVal("u1-user"),
		"authentication_mode": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"passwords":      cty.NullVal(cty.Set(cty.String)),
			"password_count": count,
			"type":           cty.StringVal(authType),
		})}),
	})
}

// elasticacheUserPlan plans configured type authType against a prior AWS
// read back as "no-password", with or without the override.
func elasticacheUserPlan(t *testing.T, o Override, authType string, on bool) (providers.PlanResourceChangeResponse, cty.Value) {
	t.Helper()
	prior := elasticacheUser("u1-user", "no-password", cty.NumberIntVal(0))
	config := elasticacheUser("", authType, cty.NullVal(cty.Number))
	// What OpenTofu proposes: configuration, with computed values the
	// configuration leaves null (id, arn, password_count) taken from prior.
	proposed := elasticacheUser("u1-user", authType, cty.NumberIntVal(0))
	fake := &fakeProvider{typeName: o.Type, proof: proof{schema: elasticacheUserSchema, forceNew: []string{"user_id"}}}
	var inner providers.Interface = fake
	if on {
		inner = Wrap(fake, map[string][]Override{o.Type: {o}})
	}
	return inner.PlanResourceChange(context.Background(), providers.PlanResourceChangeRequest{
		TypeName:         o.Type,
		PriorState:       prior,
		ProposedNewState: proposed,
		Config:           config,
	}), prior
}

func init() {
	proofs["aws-elasticache-user-no-password"] = proof{
		schema: elasticacheUserSchema,
		check: func(t *testing.T, o Override) {
			off, prior := elasticacheUserPlan(t, o, "no-password-required", false)
			if off.PlannedState.RawEquals(prior) {
				t.Fatalf("without the override the fake plans no change, so this proof cannot fail")
			}
			on, _ := elasticacheUserPlan(t, o, "no-password-required", true)
			if on.Diagnostics.HasErrors() {
				t.Fatalf("errors with the override: %s", on.Diagnostics.Err())
			}
			if len(on.RequiresReplace) != 0 {
				t.Errorf("with the override the plan replaces: %#v", on.RequiresReplace)
			}
			if !on.PlannedState.RawEquals(prior) {
				t.Errorf("with the override the plan is not the prior state:\n got %#v\nwant %#v", on.PlannedState, prior)
			}
			if !hasWarning(on.Diagnostics, "Provider override applied", o.Name) {
				t.Errorf("the plan does not name the override that changed it: %#v", on.Diagnostics)
			}
		},
	}
}

// TestElasticacheUserRealChangeStillPlans checks the override does not hide
// a real change: moving a no-password user to "password" or "iam" still
// plans the update, and says nothing about the override.
func TestElasticacheUserRealChangeStillPlans(t *testing.T) {
	o, _ := Lookup("aws-elasticache-user-no-password")
	for _, authType := range []string{"password", "iam"} {
		resp, prior := elasticacheUserPlan(t, o, authType, true)
		if resp.PlannedState.RawEquals(prior) {
			t.Errorf("type %q against prior \"no-password\" planned no change", authType)
		}
		if hasWarning(resp.Diagnostics, "Provider override applied", o.Name) {
			t.Errorf("type %q: the override claims to have applied", authType)
		}
	}
}
