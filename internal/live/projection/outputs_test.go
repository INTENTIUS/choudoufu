// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// TestApplyRootOutputValuesEvaluatesAgainstProjectedState is GitHub issue
// #348's unit-level pin: given a state that already carries a resource
// instance, ApplyRootOutputValues has to fill in that resource's root
// outputs with the SAME values a real plan would compute for them - a bare
// attribute reference, an expression built from one, and a sensitive
// output - while an output reading a resource this state carries no
// instance for (the "about to be created" case) has to come back unset
// rather than a value the eval graph invented, because [Module.SetOutputValue]
// with an unknown value would give the real plan graph's own
// NodeApplyableOutput.setValue (internal/tofu/node_output.go) a "before"
// value it cannot safely diff against a wholly-known "after" - see the
// deliberate skip's comment in outputs.go for the resulting panic this
// avoids, and TestLivePlan_rootOutputsChangeWhenResourceIsCreated in
// internal/command for the same case exercised through the whole live-plan
// pipeline.
func TestApplyRootOutputValuesEvaluatesAgainstProjectedState(t *testing.T) {
	cfg := loadConfig(t, "testdata/output-eval")

	stubSchema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"names": {Type: cty.List(cty.String), Optional: true},
			"id":    {Type: cty.String, Computed: true},
		},
	}}
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			ResourceTypes: map[string]providers.Schema{"stub_cert": stubSchema},
		},
	}

	core, ctxDiags := tofu.NewContext(&tofu.ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("stub"): func() (providers.Interface, error) { return mock, nil },
		}, nil),
	})
	if ctxDiags.HasErrors() {
		t.Fatalf("tofu.NewContext: %s", ctxDiags.Err())
	}

	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "stub_cert", Name: "cert"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"cert-123","names":["example.com"]}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")},
		addrs.NoKey,
	)
	// stub_cert.future is declared in the fixture but deliberately absent
	// from state - it stands in for a resource about to be created.

	diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil, nil)
	if diags.HasErrors() {
		t.Fatalf("ApplyRootOutputValues: %s", diags.Err())
	}

	got := state.RootModule().OutputValues
	check := func(name string, want cty.Value, wantSensitive bool) {
		t.Helper()
		ov, ok := got[name]
		if !ok {
			t.Fatalf("output %q was not set", name)
		}
		if !ov.Value.RawEquals(want) {
			t.Errorf("output %q = %#v, want %#v", name, ov.Value, want)
		}
		if ov.Sensitive != wantSensitive {
			t.Errorf("output %q sensitive = %v, want %v", name, ov.Sensitive, wantSensitive)
		}
	}
	check("cert_id", cty.StringVal("cert-123"), false)
	check("cert_label", cty.StringVal("cert-cert-123"), false)
	check("cert_secret", cty.StringVal("cert-123"), true)

	if _, ok := got["future_id"]; ok {
		t.Errorf("future_id was set even though stub_cert.future has no instance in state - it should be left absent, not recorded as unknown")
	}
}

// TestApplyRootOutputValuesNoOutputsIsANoOp pins the cheap path: a
// configuration with no root-level outputs must not touch state at all.
func TestApplyRootOutputValuesNoOutputsIsANoOp(t *testing.T) {
	cfg := loadConfig(t, "testdata/plan-computed")
	state := states.NewState()

	core, ctxDiags := tofu.NewContext(&tofu.ContextOpts{})
	if ctxDiags.HasErrors() {
		t.Fatalf("tofu.NewContext: %s", ctxDiags.Err())
	}

	diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil, nil)
	if diags.HasErrors() {
		t.Fatalf("ApplyRootOutputValues: %s", diags.Err())
	}
	if len(state.RootModule().OutputValues) != 0 {
		t.Errorf("OutputValues is %v, want empty - the fixture declares no outputs", state.RootModule().OutputValues)
	}
}

// TestApplyRootOutputValuesSeesThroughZeroInstanceBlocks is GitHub issue
// #349's unit-level pin.
//
// Every output in the fixture reaches a resource block that provably
// produces no instances - through count, through for_each, through a data
// source, and through a module output, which is corpus-lambda-simple's own
// shape. A real plan resolves each of them to its try() alternative,
// because its graph expanded the configuration and knows instance 0 does
// not exist; before the fix, core.Eval's graph could not tell that block
// apart from one merely absent from state, answered cty.DynamicVal, and
// try() had no error to recover from - so every one of these outputs was
// left unset and rendered "+ <name>" on every single live-plan run.
//
// unknowable_id is the soundness control, and it is deliberately the one
// case this fix does NOT answer: its count reads a resource attribute, so
// the expansion does not resolve, so no husk is seeded and the output stays
// unset. Reading "could not evaluate the count" as "zero instances" would
// make this output resolve to "fell-through" - which happens to be the
// value a real plan computes, and would still be the wrong thing to do,
// because the same reading applied to a count that is genuinely nonzero
// puts a confidently wrong prior value in front of the diff and renders it
// as clean. See [identity.ZeroInstanceBlocks].
func TestApplyRootOutputValuesSeesThroughZeroInstanceBlocks(t *testing.T) {
	cfg := loadConfigWithModules(t, "testdata/output-eval-zero")

	stubSchema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"names": {Type: cty.List(cty.String), Optional: true},
			"id":    {Type: cty.String, Computed: true},
		},
	}}
	lookupSchema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"name": {Type: cty.String, Optional: true},
			"id":   {Type: cty.String, Computed: true},
		},
	}}
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			ResourceTypes: map[string]providers.Schema{"stub_cert": stubSchema},
			DataSources:   map[string]providers.Schema{"stub_lookup": lookupSchema},
		},
	}

	core, ctxDiags := tofu.NewContext(&tofu.ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("stub"): func() (providers.Interface, error) { return mock, nil },
		}, nil),
	})
	if ctxDiags.HasErrors() {
		t.Fatalf("tofu.NewContext: %s", ctxDiags.Err())
	}

	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "stub_cert", Name: "cert"}.Instance(addrs.IntKey(0)),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"cert-123","names":["example.com"]}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")},
		addrs.NoKey,
	)

	variables := tofu.InputValues{}
	for name, v := range cfg.Module.Variables {
		variables[name] = &tofu.InputValue{Value: v.Default, SourceType: tofu.ValueFromConfig}
	}

	before := state.DeepCopy()

	diags := ApplyRootOutputValues(t.Context(), core, cfg, state, variables, nil)
	if diags.HasErrors() {
		t.Fatalf("ApplyRootOutputValues: %s", diags.Err())
	}

	got := state.RootModule().OutputValues
	for _, tc := range []struct {
		name string
		want cty.Value
	}{
		{"layer_id", cty.StringVal("")},
		{"each_layer_id", cty.StringVal("")},
		{"log_group_arn", cty.StringVal("cert-123")},
		{"layer_count", cty.NumberIntVal(0)},
		{"module_layer_id", cty.StringVal("")},
	} {
		ov, ok := got[tc.name]
		if !ok {
			t.Errorf("output %q was not set - #349's symptom is exactly this, and it renders as \"+ %s\" on every run", tc.name, tc.name)
			continue
		}
		if !ov.Value.RawEquals(tc.want) {
			t.Errorf("output %q = %#v, want %#v", tc.name, ov.Value, tc.want)
		}
	}

	if ov, ok := got["unknowable_id"]; ok {
		t.Errorf("unknowable_id was set to %#v: stub_cert.unknowable's count reads a resource attribute and does not resolve from configuration, so no husk may be seeded for it and the output must be left unset rather than answered", ov.Value)
	}

	// The husks are an evaluation device, not a fact about the estate: the
	// plan runs against the caller's own state a moment later, and a
	// zero-instance resource appearing in it would be a claim nothing read.
	for _, mod := range before.Modules {
		for key := range mod.Resources {
			if state.Module(mod.Addr) == nil || state.Module(mod.Addr).Resources[key] == nil {
				t.Errorf("resource %s in %s disappeared from the caller's state", key, mod.Addr)
			}
		}
	}
	var extra []string
	for _, mod := range state.Modules {
		beforeMod := before.Module(mod.Addr)
		for key := range mod.Resources {
			if beforeMod == nil || beforeMod.Resources[key] == nil {
				extra = append(extra, mod.Addr.String()+" "+key)
			}
		}
	}
	if len(extra) > 0 {
		t.Errorf("ApplyRootOutputValues added resources to the caller's state: %v - the husks belong to the evaluation copy only", extra)
	}
}

// TestApplyRootOutputValuesSeedsLiveDataValues is GitHub issue #349's
// sub-problem 2 at the evaluation layer: a root output whose value is built
// from a data source nobody read gets the provider's own answer, because
// [dataread.ReadForOutputs] read it and the value was seeded into the
// evaluation's copy of state.
//
// The same test carries the blast-radius control. The fixture's second
// output cannot be evaluated at all against this state - it indexes a block
// with no instances, with no try() to recover - and a real plan's own graph
// reports that itself, from the node that owns the output, a moment later.
// This function must therefore leave it unset and raise NOTHING, because a
// pre-plan probe failing on one output is not a reason to refuse an estate.
// That was the exact shape of the risk #349's scoping named: a widened
// demand under a fatal contract turns "one output shows +" into "live-plan
// refuses the whole estate".
func TestApplyRootOutputValuesSeedsLiveDataValues(t *testing.T) {
	cfg := loadConfig(t, "testdata/output-eval-data")

	stubSchema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"names": {Type: cty.List(cty.String), Optional: true},
			"id":    {Type: cty.String, Computed: true},
		},
	}}
	lookupSchema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"name": {Type: cty.String, Optional: true},
			"id":   {Type: cty.String, Computed: true},
		},
	}}
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			ResourceTypes: map[string]providers.Schema{"stub_cert": stubSchema},
			DataSources:   map[string]providers.Schema{"stub_lookup": lookupSchema},
		},
	}

	core, ctxDiags := tofu.NewContext(&tofu.ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("stub"): func() (providers.Interface, error) { return mock, nil },
		}, nil),
	})
	if ctxDiags.HasErrors() {
		t.Fatalf("tofu.NewContext: %s", ctxDiags.Err())
	}

	state := states.NewState()
	dataValues := map[string]cty.Value{
		"data.stub_lookup.current": cty.ObjectVal(map[string]cty.Value{
			"name": cty.StringVal("here"),
			"id":   cty.StringVal("lookup-1"),
		}),
	}

	diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil, dataValues)
	if diags.HasErrors() {
		t.Fatalf("ApplyRootOutputValues raised errors: %s - nothing about one unevaluable output may fail a run", diags.Err())
	}

	got := state.RootModule().OutputValues
	ov, ok := got["static_arn"]
	if !ok {
		t.Fatalf("static_arn was not set - the live data-source value did not reach the evaluation, which is #349's symptom exactly")
	}
	if want := cty.StringVal("arn:lookup-1"); !ov.Value.RawEquals(want) {
		t.Errorf("static_arn = %#v, want %#v", ov.Value, want)
	}
	if _, ok := got["boom"]; ok {
		t.Errorf("boom was set even though its expression does not evaluate against this state")
	}

	// The seed is an evaluation device, exactly like the zero-instance
	// husks: the plan runs against the caller's own state a moment later,
	// and a data source appearing in it would be a claim the plan did not
	// make.
	if state.Resource(addrs.Resource{Mode: addrs.DataResourceMode, Type: "stub_lookup", Name: "current"}.Absolute(addrs.RootModuleInstance)) != nil {
		t.Errorf("data.stub_lookup.current was written into the caller's state; the seeded value belongs to the evaluation copy only")
	}
}
