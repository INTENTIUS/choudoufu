// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
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

// TestSSMShapedInsecureValueWithNoRecordPlansTheConfiguredValue is
// INTENTIUS/choudoufu#2269: the same parameter before any apply under this
// estate has written a record, so there is no residue to seed. The read from
// the bare import stub puts the live "v1" into value; the Optional+Computed
// probe (optionalcomputedprobe.go) reads again with insecure_value named in
// the prior, the provider answers "v1" there, and that read is the projected
// prior. The plan must then carry the configured "v2", not unknown.
func TestSSMShapedInsecureValueWithNoRecordPlansTheConfiguredValue(t *testing.T) {
	cfg := loadConfig(t, "testdata/ssm-insecure-value")
	addr := mustAddr(t, `stub_parameter.p`)
	schema := stubParameterSchema()
	ctx := context.Background()

	// An empty store: the parameter has no record of any kind.
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("ssm-2269"))
	p := stubParameterProvider("v1")
	reads := 0
	read := p.ReadResourceFn
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		reads++
		return read(r)
	}
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	res, diags := BuildWith(ctx, cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "/p", IdentityValues: map[string]string{"name": "/p"}},
	}, SingleProvider(provAddr, p), Options{RecordStore: store})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_parameter.p`})
	if reads != 2 {
		t.Errorf("%d reads of stub_parameter.p, want 2: the bare read and the probe", reads)
	}

	ri := res.State.ResourceInstance(addr)
	if ri == nil || ri.Current == nil {
		t.Fatal("stub_parameter.p is not in the projection")
	}
	var prior map[string]interface{}
	if err := json.Unmarshal(ri.Current.AttrsJSON, &prior); err != nil {
		t.Fatalf("the projected prior is not valid JSON: %s", err)
	}
	if prior["insecure_value"] != "v1" {
		t.Errorf("projected insecure_value = %#v, want \"v1\" (the probe read, which named insecure_value in the prior)", prior["insecure_value"])
	}
	if prior["value"] != nil {
		t.Errorf("projected value = %#v, want null: the probe read is the one kept, and it never writes value", prior["value"])
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
		t.Fatal("the planned insecure_value is unknown (known after apply); with no record the update would still apply as a no-op")
	}
	if got.IsNull() || got.AsString() != "v2" {
		t.Errorf("the planned insecure_value = %#v, want the configured \"v2\"", got)
	}
}

// TestOptionalComputedProbeKeepsAnEchoOut: a provider that keeps an
// Optional+Computed attribute from the prior without reading it answers the
// probe with the sentinel. The first read then stands, with the attribute
// null, so the configured value never reaches the prior through the probe.
func TestOptionalComputedProbeKeepsAnEchoOut(t *testing.T) {
	cfg := loadConfig(t, "testdata/ssm-insecure-value")
	addr := mustAddr(t, `stub_parameter.p`)
	ctx := context.Background()
	p := stubParameterProvider("v1")
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		prior, _ := r.PriorState.UnmarkDeep()
		attrs := prior.AsValueMap()
		attrs["id"] = cty.StringVal("/p")
		attrs["name"] = cty.StringVal("/p")
		attrs["type"] = cty.StringVal("String")
		attrs["version"] = cty.NumberIntVal(1)
		attrs["value_wo"] = cty.NullVal(cty.String)
		attrs["value"] = cty.StringVal("v1")
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(attrs)}
	}
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	res, diags := BuildWith(ctx, cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "/p", IdentityValues: map[string]string{"name": "/p"}},
	}, SingleProvider(provAddr, p), Options{RecordStore: NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("ssm-2269-echo"))})
	assertNoErrors(t, diags)
	ri := res.State.ResourceInstance(addr)
	if ri == nil || ri.Current == nil {
		t.Fatal("stub_parameter.p is not in the projection")
	}
	var prior map[string]interface{}
	if err := json.Unmarshal(ri.Current.AttrsJSON, &prior); err != nil {
		t.Fatalf("the projected prior is not valid JSON: %s", err)
	}
	if prior["insecure_value"] != nil {
		t.Errorf("projected insecure_value = %#v, want null: the provider only echoed the probe, so the first read stands", prior["insecure_value"])
	}
}

// TestSSMShapedSensitiveInsecureValuePlansTheConfiguredValue is
// INTENTIUS/choudoufu#2278: insecure_value set from a `sensitive = true`
// variable, with no record. The probe takes part even though the configured
// value is marked: it sends only the sentinel, and its compare unmarks the
// second read with paths and re-marks what it keeps. The projected prior
// carries the live "v1" in insecure_value marked sensitive, and the plan is
// an update whose insecure_value is the configured "v2", known and marked
// sensitive. Before #2278 the probe skipped the marked value, the first read
// stood, and the plan carried insecure_value as unknown.
func TestSSMShapedSensitiveInsecureValuePlansTheConfiguredValue(t *testing.T) {
	dir := t.TempDir()
	const src = `
terraform {
  live {
    estate = "ssm-2278-sensitive"
  }
}

variable "v" {
  type      = string
  sensitive = true
  default   = "v2"
}

resource "stub_parameter" "p" {
  name           = "/p"
  type           = "String"
  insecure_value = var.v
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	cfg := loadConfig(t, dir)
	addr := mustAddr(t, `stub_parameter.p`)
	schema := stubParameterSchema()
	ctx := context.Background()
	p := stubParameterProvider("v1")
	reads := 0
	read := p.ReadResourceFn
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		reads++
		return read(r)
	}
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	res, diags := BuildWith(ctx, cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "/p", IdentityValues: map[string]string{"name": "/p"}},
	}, SingleProvider(provAddr, p), Options{RecordStore: NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("ssm-2278-sensitive"))})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_parameter.p`})
	if reads != 2 {
		t.Errorf("%d reads of stub_parameter.p, want 2: a marked configured value is probed too", reads)
	}
	ri := res.State.ResourceInstance(addr)
	if ri == nil || ri.Current == nil {
		t.Fatal("stub_parameter.p is not in the projection")
	}
	var prior map[string]interface{}
	if err := json.Unmarshal(ri.Current.AttrsJSON, &prior); err != nil {
		t.Fatalf("the projected prior is not valid JSON: %s", err)
	}
	if prior["insecure_value"] != "v1" {
		t.Errorf("projected insecure_value = %#v, want \"v1\" (the probe read)", prior["insecure_value"])
	}
	if prior["value"] != nil {
		t.Errorf("projected value = %#v, want null: the probe read is the one kept, and it never writes value", prior["value"])
	}
	priorSensitive := false
	for _, pvm := range ri.Current.AttrSensitivePaths {
		if pvm.Path.Equals(cty.GetAttrPath("insecure_value")) {
			priorSensitive = true
		}
	}
	if !priorSensitive {
		t.Error("the projected prior's insecure_value is not marked sensitive; the probe's compare dropped the configuration's mark")
	}

	plan, planDiags := stubContext(t, p).Plan(ctx, cfg, res.State, &tofu.PlanOpts{Mode: plans.NormalMode})
	if planDiags.HasErrors() {
		t.Fatalf("planning against the projected state: %s", planDiags.Err())
	}
	chg := plan.Changes.ResourceInstance(addr)
	if chg == nil {
		t.Fatalf("the plan holds no change for %s; changes: %v", addr, changedAddrs(plan))
	}
	if chg.Action != plans.Update {
		t.Fatalf("%s plans as %s, want Update", addr, chg.Action)
	}
	change, err := chg.Decode(&schema)
	if err != nil {
		t.Fatalf("decoding the planned change: %s", err)
	}
	got := change.After.GetAttr("insecure_value")
	if !got.HasMark(marks.Sensitive) {
		t.Error("the planned insecure_value is not marked sensitive")
	}
	got, _ = got.UnmarkDeep()
	if !got.IsKnown() {
		t.Fatal("the planned insecure_value is unknown (known after apply); the update would apply as a no-op")
	}
	if got.IsNull() || got.AsString() != "v2" {
		t.Errorf("the planned insecure_value = %#v, want the configured \"v2\"", got)
	}
}

// TestOptionalComputedProbeKeepsMarksOnASecondRead: the probe's second read
// hands back the probed attribute marked (configuration marks restored by
// readImported). probeOptionalComputed compares it unmarked and returns the
// second read with the mark still on it (#2278).
func TestOptionalComputedProbeKeepsMarksOnASecondRead(t *testing.T) {
	ctx := context.Background()
	schema := stubParameterSchema()
	ty := schema.Block.ImpliedType()
	p := stubParameterProvider("v1")
	stubAttrs := map[string]cty.Value{}
	for name, aty := range ty.AttributeTypes() {
		stubAttrs[name] = cty.NullVal(aty)
	}
	stubAttrs["id"] = cty.StringVal("/p")
	stub := states.ResourceInstanceObject{Status: states.ObjectReady, Value: cty.ObjectVal(stubAttrs)}

	firstAttrs := map[string]cty.Value{}
	for k, v := range stubAttrs {
		firstAttrs[k] = v
	}
	firstAttrs["name"] = cty.StringVal("/p")
	firstAttrs["type"] = cty.StringVal("String")
	firstAttrs["version"] = cty.NumberIntVal(1)
	firstAttrs["value"] = cty.StringVal("v1")
	first := &states.ResourceInstanceObject{Status: states.ObjectReady, Value: cty.ObjectVal(firstAttrs)}

	configMarks := []cty.PathValueMarks{{
		Path:  cty.GetAttrPath("insecure_value"),
		Marks: cty.NewValueMarks(marks.Sensitive),
	}}
	got := probeOptionalComputed(ctx, p, schema, "stub_parameter", "/p", stub, nil, configMarks, nil, []string{"insecure_value"}, first)
	if got == first {
		t.Fatal("the probe returned the first read; want the second read, compared unmarked")
	}
	iv := got.Value.GetAttr("insecure_value")
	if !iv.HasMark(marks.Sensitive) {
		t.Error("the kept second read's insecure_value lost its sensitive mark")
	}
	if uv, _ := iv.Unmark(); uv.IsNull() || uv.AsString() != "v1" {
		t.Errorf("the kept second read's insecure_value = %#v, want \"v1\"", uv)
	}
}
