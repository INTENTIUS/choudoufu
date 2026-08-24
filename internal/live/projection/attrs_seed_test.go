// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is the reproduction for GitHub issues #395 and #376, both the
// same defect [configuredAttrsSeed]'s doc comment in build.go describes:
// choudoufu keeps no persisted state, so [importAndRead]'s import stub is
// far barer than what an ordinary, state-backed OpenTofu run would hand
// ReadResource, and a provider whose Read depends on what PriorState
// already held for a non-Computed argument - preserving a FORMAT, or never
// sourcing the argument from the remote at all - answers wrong forever.
//
// Both fake providers below encode the mechanism CONFIRMED against a real
// floci build and a real hashicorp/aws 6.59.0 plugin in a throwaway
// standalone repro (not committed - see the PR description for the
// reproduce command), not invented: DescribeServices on the wire always
// carries the full ARN, but terraform-provider-aws's aws_ecs_service Read
// reformats task_definition to the short "family:revision" form whenever
// PriorState carries no prior value for it (#395); its
// aws_ecs_task_definition Read never sources track_latest from the API at
// all, so a null PriorState comes back as the SDK's own zero-valued
// default, discarding whatever configuration declared (#376).

// stubServiceSchema mirrors aws_ecs_service's task_definition shape: a
// plain string, Optional, never Computed - the property
// [configuredAttrsSeed] keys on, not the type name.
func stubServiceSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":              {Type: cty.String, Computed: true},
			"name":            {Type: cty.String, Required: true},
			"task_definition": {Type: cty.String, Optional: true},
		},
	}}
}

// TestConfiguredAttrsSeedFixesTaskDefinitionFormat is GitHub issue #395's
// reproduction and fix confirmation.
func TestConfiguredAttrsSeedFixesTaskDefinitionFormat(t *testing.T) {
	cfg := loadConfig(t, "testdata/attrs-seed-fargate")
	addr := mustAddr(t, `stub_service.this`)
	schema := stubServiceSchema()

	const wireARN = "arn:aws:ecs:eu-west-1:000000000000:task-definition/mini-td:1"
	const shortForm = "mini-td:1"

	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_service": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		// ImportResourceState has no configuration to draw from - only the
		// identity it was given, the same near-null stub
		// [noimporter.SynthesizeStub] and a real ImportResourceState RPC
		// both produce.
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"id":              cty.StringVal(r.Target.ID),
				"name":            cty.NullVal(cty.String),
				"task_definition": cty.NullVal(cty.String),
			}),
		}}}
	}
	var sawSeededPrior bool
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		prior := r.PriorState.GetAttr("task_definition")
		// The confirmed provider quirk: a null prior means the SDK has no
		// format to preserve and falls back to the short form; a non-null
		// prior (what a real state file's PriorState, or this fix's seed,
		// would carry) makes it echo the wire ARN it actually reads,
		// exactly as floci's own DescribeServices always returns it.
		out := shortForm
		if !prior.IsNull() {
			sawSeededPrior = true
			out = wireARN
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"id":              cty.StringVal("arn:aws:ecs:eu-west-1:000000000000:service/mini-cluster/mini-svc"),
			"name":            cty.StringVal("svc"),
			"task_definition": cty.StringVal(out),
		})}
	}

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "arn:aws:ecs:eu-west-1:000000000000:service/mini-cluster/mini-svc"},
	}, SingleProvider(provAddr, p))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_service.this`})

	if !sawSeededPrior {
		t.Fatal("the provider's ReadResource never saw a non-null task_definition in PriorState, so " +
			"issue #395's fix never reached the provider - this test would pass for a change that stopped " +
			"seeding entirely")
	}

	is := res.State.ResourceInstance(addr)
	if is == nil || is.Current == nil {
		t.Fatal("stub_service.this is not in the projection")
	}
	got := string(is.Current.AttrsJSON)
	if !strings.Contains(got, wireARN) {
		t.Errorf("the projected task_definition does not carry the live ARN:\n%s\nwant it to contain %q", got, wireARN)
	}
	if strings.Contains(got, `"task_definition":"`+shortForm+`"`) {
		t.Errorf("the projected task_definition regressed to the short family:revision form:\n%s", got)
	}
}

// stubTaskDefinitionSchema mirrors aws_ecs_task_definition's track_latest
// shape: Optional bool, never Computed, and (per issue #376) never sourced
// from the API at all - the provider's Read leaves it exactly as PriorState
// held it.
func stubTaskDefinitionSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":           {Type: cty.String, Computed: true},
			"family":       {Type: cty.String, Required: true},
			"track_latest": {Type: cty.Bool, Optional: true},
		},
	}}
}

// TestConfiguredAttrsSeedFixesClientSideOnlyDefault is GitHub issue #376's
// reproduction and fix confirmation: track_latest is client-side-only (the
// provider's Read never sources it from any API call), so a bare
// ImportResourceState stub with nothing to preserve comes back as the
// SDK's own zero-valued default (false) rather than the configuration's
// own declared true.
func TestConfiguredAttrsSeedFixesClientSideOnlyDefault(t *testing.T) {
	cfg := loadConfig(t, "testdata/attrs-seed-fargate")
	addr := mustAddr(t, `stub_task_definition.this`)
	schema := stubTaskDefinitionSchema()

	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_task_definition": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"id":           cty.StringVal(r.Target.ID),
				"family":       cty.NullVal(cty.String),
				"track_latest": cty.NullVal(cty.Bool),
			}),
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		prior := r.PriorState.GetAttr("track_latest")
		// The confirmed provider quirk: never read from the API at all.
		// Whatever PriorState held survives untouched; a null prior comes
		// back as the SDK's own zero-valued default.
		out := prior
		if prior.IsNull() {
			out = cty.False
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"id":           cty.StringVal("mini-td"),
			"family":       cty.StringVal("mini-td"),
			"track_latest": out,
		})}
	}

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "mini-td"},
	}, SingleProvider(provAddr, p))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_task_definition.this`})

	is := res.State.ResourceInstance(addr)
	if is == nil || is.Current == nil {
		t.Fatal("stub_task_definition.this is not in the projection")
	}
	got := string(is.Current.AttrsJSON)
	if !strings.Contains(got, `"track_latest":true`) {
		t.Errorf("the projected track_latest is not true:\n%s\nwant the configuration's own declared value, "+
			"not the client-side-only SDK default", got)
	}
}

// TestConfiguredAttrsSeedBoundaries is the mutation-shaped check the unit
// asked for: an attribute the provider genuinely reads back (Computed) is
// never seed-carried even when configuration happens to set it, and an
// attribute configuration genuinely leaves absent stays unseeded rather
// than seeded with a zero value. Both directions matter equally - a rule
// that only ever widened would eventually seed a Computed answer the
// provider owns, and a rule that only ever narrowed would silently stop
// fixing #395/#376.
func TestConfiguredAttrsSeedBoundaries(t *testing.T) {
	cfg := loadConfig(t, "testdata/attrs-seed-fargate")
	rc := cfg.Module.ManagedResources["stub_widget.this"]
	if rc == nil {
		t.Fatalf("fixture does not declare stub_widget.this; it declares %v", keysOfResources(cfg))
	}

	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":            {Type: cty.String, Computed: true},
		"name":          {Type: cty.String, Required: true},
		"computed_flag": {Type: cty.String, Optional: true, Computed: true},
		"unset_flag":    {Type: cty.String, Optional: true},
	}}}

	seed := configuredAttrsSeed(context.Background(), cfg.Module.StaticEvaluator, cfg.Path, rc, schema)

	if v, ok := seed["name"]; !ok || v.AsString() != "widget-1" {
		t.Errorf("name (Required, not Computed, statically set) must be seeded; seed=%#v", seed)
	}
	if v, ok := seed["computed_flag"]; ok {
		t.Errorf("computed_flag (Optional+Computed) must never be seeded even though configuration sets "+
			"it to %q - Computed means the PROVIDER may answer independent of configuration, and seeding "+
			"it would risk masking a real, independent live value with a stale configured one; got seeded "+
			"as %#v", v.AsString(), v)
	}
	if v, ok := seed["unset_flag"]; ok {
		t.Errorf("unset_flag (Optional, not Computed, never set in configuration) must not be seeded - "+
			"a null config value carries nothing to seed, not a zero value; got %#v", v)
	}
	if _, ok := seed["id"]; ok {
		t.Errorf("id (Computed-only) must never be seeded; seed=%#v", seed)
	}
}
