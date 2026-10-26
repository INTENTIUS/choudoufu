// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is GitHub issue #2261 (mirrored from INTENTIUS/chant#4470): an
// in-place update of hashicorp/aws's aws_ssm_parameter under a live block
// planned insecure_value as unknown and applied as a no-op.
//
// stubParameterSchema mirrors aws_ssm_parameter's value attributes
// (hashicorp/aws 5.100.0): value and insecure_value are both Optional and
// Computed, value is Sensitive, and value_wo is the write-only sibling.
// The fake provider below encodes the two behaviours of the real one that
// combine into the bug, both read from the provider's source
// (internal/service/ssm/parameter.go at v5.100.0):
//
//   - resourceParameterRead writes the parameter's value into
//     insecure_value only when the prior already has insecure_value set,
//     and into value otherwise.
//   - CustomizeDiff marks value computed when insecure_value changes, and
//     insecure_value computed when value changes. A legacy-SDK HasChange
//     compares the prior with a computed value's zero value (""), so a
//     prior value of "v1" reads as changed once value is computed.

func stubParameterSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":             {Type: cty.String, Optional: true, Computed: true},
			"name":           {Type: cty.String, Required: true},
			"type":           {Type: cty.String, Required: true},
			"value":          {Type: cty.String, Optional: true, Computed: true, Sensitive: true},
			"insecure_value": {Type: cty.String, Optional: true, Computed: true},
			"value_wo":       {Type: cty.String, Optional: true, Sensitive: true, WriteOnly: true},
			"version":        {Type: cty.Number, Computed: true},
		},
	}}
}

// stubParameterProvider is the fake cloud: one parameter, /p, holding live.
func stubParameterProvider(live string) *tofu.MockProvider {
	schema := stubParameterSchema()
	ty := schema.Block.ImpliedType()
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_parameter": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		attrs := map[string]cty.Value{}
		for name, aty := range ty.AttributeTypes() {
			attrs[name] = cty.NullVal(aty)
		}
		attrs["id"] = cty.StringVal(r.Target.ID)
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State:    cty.ObjectVal(attrs),
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		prior, _ := r.PriorState.UnmarkDeep()
		attrs := prior.AsValueMap()
		attrs["id"] = cty.StringVal("/p")
		attrs["name"] = cty.StringVal("/p")
		attrs["type"] = cty.StringVal("String")
		attrs["version"] = cty.NumberIntVal(1)
		attrs["value_wo"] = cty.NullVal(cty.String)
		if iv := attrs["insecure_value"]; !iv.IsNull() && iv.AsString() != "" {
			attrs["insecure_value"] = cty.StringVal(live)
		} else {
			attrs["value"] = cty.StringVal(live)
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(attrs)}
	}
	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		prior, _ := r.PriorState.UnmarkDeep()
		proposed, _ := r.ProposedNewState.UnmarkDeep()
		if prior.IsNull() || proposed.IsNull() {
			return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState, LegacyTypeSystem: true}
		}
		attrs := proposed.AsValueMap()
		// sdkString is a legacy-SDK read of a string: null and unknown
		// both read as "".
		sdkString := func(v cty.Value) string {
			if v.IsNull() || !v.IsKnown() {
				return ""
			}
			return v.AsString()
		}
		if sdkString(prior.GetAttr("insecure_value")) != sdkString(attrs["insecure_value"]) || !attrs["insecure_value"].IsKnown() {
			attrs["value"] = cty.UnknownVal(cty.String)
		}
		if sdkString(prior.GetAttr("value")) != sdkString(attrs["value"]) {
			attrs["insecure_value"] = cty.UnknownVal(cty.String)
			attrs["version"] = cty.UnknownVal(cty.Number)
		}
		return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(attrs), LegacyTypeSystem: true}
	}
	return p
}

// TestSSMShapedInsecureValueUpdatePlansTheConfiguredValue is issue #2261's
// reproduction. The record store holds the residue the last apply wrote
// (insecure_value = "v1"), the cloud still holds "v1", and configuration
// says "v2". The projected prior must carry insecure_value = "v1" with
// value null, as a state-backed refresh's prior does, and the plan must be
// an update whose insecure_value is the configured "v2".
//
// Before the fix the residue was only filled after the read, so the read
// put "v1" into value and the fill put "v1" into insecure_value; the plan
// then carried insecure_value as unknown, and terraform-plugin-sdk/v2's
// apply dropped the unknown and sent no PutParameter.
func TestSSMShapedInsecureValueUpdatePlansTheConfiguredValue(t *testing.T) {
	cfg := loadConfig(t, "testdata/ssm-insecure-value")
	addr := mustAddr(t, `stub_parameter.p`)
	schema := stubParameterSchema()

	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("ssm-2261"))
	rf, err := encodeResidueFields(map[string]cty.Value{"insecure_value": cty.StringVal("v1")})
	if err != nil {
		t.Fatalf("encoding the residue fixture: %s", err)
	}
	ctx := context.Background()
	if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Residue = rf
	}); err != nil {
		t.Fatalf("writing the residue fixture: %s", err)
	}

	p := stubParameterProvider("v1")
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	res, diags := BuildWith(ctx, cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "/p", IdentityValues: map[string]string{"name": "/p"}},
	}, SingleProvider(provAddr, p), Options{RecordStore: store})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_parameter.p`})

	ri := res.State.ResourceInstance(addr)
	if ri == nil || ri.Current == nil {
		t.Fatal("stub_parameter.p is not in the projection")
	}
	var prior map[string]interface{}
	if err := json.Unmarshal(ri.Current.AttrsJSON, &prior); err != nil {
		t.Fatalf("the projected prior is not valid JSON: %s", err)
	}
	if prior["insecure_value"] != "v1" {
		t.Errorf("projected insecure_value = %#v, want \"v1\" (the residue record, confirmed by the read)", prior["insecure_value"])
	}
	if prior["value"] != nil {
		t.Errorf("projected value = %#v, want null: a state-backed refresh's prior carries insecure_value into the read, "+
			"so the provider never writes the parameter's value into value. A prior holding it in both is what makes "+
			"CustomizeDiff mark insecure_value computed.", prior["value"])
	}

	plan, planDiags := stubContext(t, p).Plan(ctx, cfg, res.State, &tofu.PlanOpts{Mode: plans.NormalMode})
	if planDiags.HasErrors() {
		t.Fatalf("planning against the projected state: %s", planDiags.Err())
	}
	src := plan.Changes.ResourceInstance(addr)
	if src == nil {
		t.Fatalf("the plan holds no change for %s; changes: %v", addr, changedAddrs(plan))
	}
	if src.Action != plans.Update {
		t.Fatalf("%s plans as %s, want Update", addr, src.Action)
	}
	change, err := src.Decode(&schema)
	if err != nil {
		t.Fatalf("decoding the planned change: %s", err)
	}
	after, _ := change.After.UnmarkDeep()
	got := after.GetAttr("insecure_value")
	if !got.IsKnown() {
		t.Fatal("the planned insecure_value is unknown (known after apply). terraform-plugin-sdk/v2 drops an unknown " +
			"planned value before diffing it at apply, so this update would apply as a no-op and send no PutParameter.")
	}
	if got.IsNull() || got.AsString() != "v2" {
		t.Errorf("the planned insecure_value = %#v, want the configured \"v2\"", got)
	}
}
