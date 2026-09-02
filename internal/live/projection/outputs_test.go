// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/plans"
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

	diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil, nil, nil)
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

	diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil, nil, nil)
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

	diags := ApplyRootOutputValues(t.Context(), core, cfg, state, variables, nil, nil)
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

	diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil, dataValues, nil)
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

// TestApplyRootOutputValuesUnmarksBeforeStoring is the audit's second
// finding, and it is a crash fix.
//
// The evaluator marks a value that reaches a sensitive schema attribute. A
// real state file's output values never carry marks - they do not survive
// serialization - and internal/tofu/node_output.go's setValue relies on that,
// saying so in its own comment before it evaluates
// unmarkedVal.Equals(before).True() against whatever it read out of state.
// cty.Value.Equals propagates its operands' marks onto its result and True()
// asserts unmarked, so a marked "before" PANICS the plan.
//
// This has been reachable since #348 for any root output reaching a sensitive
// MANAGED attribute; widening the pre-plan read class to data sources made it
// common rather than made it possible.
//
// Both legs matter. The stored value must be unmarked, and a real plan over
// that state must reach a NoOp for the output rather than dying - the second
// is the one that would have caught it, since the first can be satisfied by
// unmarking somewhere that does not reach this store.
func TestApplyRootOutputValuesUnmarksBeforeStoring(t *testing.T) {
	cfg := loadConfig(t, "testdata/output-eval-sensitive")

	stubSchema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"names":    {Type: cty.List(cty.String), Optional: true},
			"id":       {Type: cty.String, Computed: true},
			"password": {Type: cty.String, Computed: true, Sensitive: true},
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
			AttrsJSON: []byte(`{"id":"cert-123","names":["example.com"],"password":"hunter2"}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")},
		addrs.NoKey,
	)

	if diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil, nil, nil); diags.HasErrors() {
		t.Fatalf("ApplyRootOutputValues: %s", diags.Err())
	}

	ov, ok := state.RootModule().OutputValues["cert_password"]
	if !ok {
		t.Fatalf("cert_password was not set; the fixture no longer exercises a sensitive attribute reaching a root output")
	}
	if ov.Value.IsMarked() {
		t.Errorf("the stored prior output value carries cty marks; a real state file's never does, and node_output.go panics comparing against one")
	}
	if !ov.Value.RawEquals(cty.StringVal("hunter2")) {
		t.Errorf("cert_password = %#v, want an unmarked \"hunter2\" - unmarking must not change the value", ov.Value)
	}
	if !ov.Sensitive {
		t.Errorf("cert_password lost its sensitivity; the bool is what carries it once the mark is gone")
	}

	// The leg that would actually have caught this: plan against the state
	// the function just wrote. Before the unmark this panicked inside
	// NodeApplyableOutput.setValue.
	mock.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
	}
	mock.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: req.PriorState}
	}
	plan, planDiags := core.Plan(t.Context(), cfg, state, &tofu.PlanOpts{Mode: plans.NormalMode, SkipRefresh: true})
	if planDiags.HasErrors() {
		t.Fatalf("plan over the state ApplyRootOutputValues wrote: %s", planDiags.Err())
	}
	var change *plans.OutputChangeSrc
	for _, oc := range plan.Changes.Outputs {
		if oc.Addr.OutputValue.Name == "cert_password" {
			change = oc
		}
	}
	if change == nil {
		t.Fatalf("the plan carries no change for cert_password; changes: %d", len(plan.Changes.Outputs))
	}
	if change.Action != plans.NoOp {
		t.Errorf("cert_password planned as %s, want NoOp - nothing about it moved, and the prior value is exactly what the plan recomputes", change.Action)
	}
}

// applyRootOutputEvalFixture is the shared setup for the two recorded-value
// tests below: the testdata/output-eval fixture, a provider with the stub
// schema, and a state carrying stub_cert.cert but not stub_cert.future - so
// cert_id evaluates and future_id cannot.
func applyRootOutputEvalFixture(t *testing.T) (*tofu.Context, *configs.Config, *states.State) {
	t.Helper()
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
	return core, cfg, state
}

// TestApplyRootOutputValuesFallsBackToTheRecordedValue is GitHub issue
// #349's remaining half at unit level: an output the projection cannot
// evaluate at all takes the value the estate REMEMBERS it settled on, which
// is exactly what `tofu plan` reads out of a stock state file for the same
// output.
//
// future_id reads stub_cert.future, which this state carries no instance
// for, so the evaluation leaves it unset - that is the negative half of
// TestApplyRootOutputValuesEvaluatesAgainstProjectedState, unchanged. With a
// recorded value in hand it must come back as THAT value, by value, and
// carry the output block's own sensitivity rather than anything the record
// says.
func TestApplyRootOutputValuesFallsBackToTheRecordedValue(t *testing.T) {
	core, cfg, state := applyRootOutputEvalFixture(t)

	recorded := map[string]cty.Value{
		"future_id": cty.StringVal("future-from-the-last-apply"),
	}
	if diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil, nil, recorded); diags.HasErrors() {
		t.Fatalf("ApplyRootOutputValues: %s", diags.Err())
	}

	ov, ok := state.RootModule().OutputValues["future_id"]
	if !ok {
		t.Fatalf("future_id was not set even though the estate remembers a value for it")
	}
	if !ov.Value.RawEquals(cty.StringVal("future-from-the-last-apply")) {
		t.Errorf("future_id = %#v, want the recorded value by value", ov.Value)
	}
	if ov.Value.IsMarked() {
		t.Errorf("the recorded value was stored marked; node_output.go panics comparing a marked before")
	}
	if ov.Sensitive {
		t.Errorf("future_id was stored sensitive; sensitivity comes from the output block, which does not declare it")
	}
	// Without a record, the same output must still come back unset - the
	// fallback may not invent one.
	core2, cfg2, bare := applyRootOutputEvalFixture(t)
	if diags := ApplyRootOutputValues(t.Context(), core2, cfg2, bare, nil, nil, nil); diags.HasErrors() {
		t.Fatalf("ApplyRootOutputValues (no records): %s", diags.Err())
	}
	if _, ok := bare.RootModule().OutputValues["future_id"]; ok {
		t.Errorf("future_id was set with no recorded value at all")
	}
}

// TestApplyRootOutputValuesRecordNeverOverridesAnEvaluatedOutput is the
// soundness pin for the fallback, and it is the load-bearing one.
//
// The whole safety argument for using a remembered value (rootoutput.go, "the
// soundness rule") is that it is ONE-DIRECTIONAL: it can only supply a prior
// value where the evaluation produced none, so it can only ever turn a
// "+ name = value" line into nothing or into "~ old -> new". If a record
// could override an evaluated value, a stale one would put a confidently
// wrong "before" in front of the diff, and a wrong before that happens to
// equal the after renders a real change as clean - HANDOFF.md's "a wrong
// marker outranks a missing one", one carrier over.
//
// So this feeds a deliberately WRONG record for every output the fixture can
// evaluate and asserts each one keeps the value the projection produced.
func TestApplyRootOutputValuesRecordNeverOverridesAnEvaluatedOutput(t *testing.T) {
	core, cfg, state := applyRootOutputEvalFixture(t)

	recorded := map[string]cty.Value{
		"cert_id":     cty.StringVal("WRONG-cert-id"),
		"cert_label":  cty.StringVal("WRONG-cert-label"),
		"cert_secret": cty.StringVal("WRONG-cert-secret"),
	}
	if diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil, nil, recorded); diags.HasErrors() {
		t.Fatalf("ApplyRootOutputValues: %s", diags.Err())
	}

	got := state.RootModule().OutputValues
	for name, want := range map[string]cty.Value{
		"cert_id":     cty.StringVal("cert-123"),
		"cert_label":  cty.StringVal("cert-cert-123"),
		"cert_secret": cty.StringVal("cert-123"),
	} {
		ov, ok := got[name]
		if !ok {
			t.Fatalf("output %q was not set", name)
		}
		if !ov.Value.RawEquals(want) {
			t.Errorf("output %q = %#v, want %#v - a remembered value must never displace one the projection evaluated", name, ov.Value, want)
		}
	}
}

// TestApplyRootOutputValuesIgnoresAnUnusableRecordedValue pins the two shapes
// the fallback refuses to store, both for the reason the evaluated path
// refuses them: [Module.SetOutputValue] with a not-wholly-known value gives
// the real plan graph's NodeApplyableOutput.setValue a "before" it assumes
// cannot exist, and it panics rather than handling it.
func TestApplyRootOutputValuesIgnoresAnUnusableRecordedValue(t *testing.T) {
	core, cfg, state := applyRootOutputEvalFixture(t)

	recorded := map[string]cty.Value{
		"future_id": cty.UnknownVal(cty.String),
	}
	if diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil, nil, recorded); diags.HasErrors() {
		t.Fatalf("ApplyRootOutputValues: %s", diags.Err())
	}
	if ov, ok := state.RootModule().OutputValues["future_id"]; ok {
		t.Errorf("future_id was set to %#v from an unknown recorded value; it must be left unset", ov.Value)
	}

	core, cfg, state = applyRootOutputEvalFixture(t)
	recorded = map[string]cty.Value{"future_id": cty.NilVal}
	if diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil, nil, recorded); diags.HasErrors() {
		t.Fatalf("ApplyRootOutputValues: %s", diags.Err())
	}
	if _, ok := state.RootModule().OutputValues["future_id"]; ok {
		t.Errorf("future_id was set from a nil recorded value")
	}
}
