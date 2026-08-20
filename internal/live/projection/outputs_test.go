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

	diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil)
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

	diags := ApplyRootOutputValues(t.Context(), core, cfg, state, nil)
	if diags.HasErrors() {
		t.Fatalf("ApplyRootOutputValues: %s", diags.Err())
	}
	if len(state.RootModule().OutputValues) != 0 {
		t.Errorf("OutputValues is %v, want empty - the fixture declares no outputs", state.RootModule().OutputValues)
	}
}
