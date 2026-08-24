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
	"github.com/intentius/choudoufu/internal/live/strict"
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

	seed, _ := configuredAttrsSeed(context.Background(), cfg.Module.StaticEvaluator, cfg.Path, rc, schema)

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

// TestResidueSeedForFixesAManagedReferenceAttribute is GitHub issue #395's
// OWN real shape, not the literal simplification
// TestConfiguredAttrsSeedFixesTaskDefinitionFormat proves the mechanism
// with: task_definition = stub_task_definition.this.arn is a reference to
// another resource's computed attribute, which
// [configs.StaticEvaluator] - configuredAttrsSeed's only source - can
// never resolve at all (the config-language subset is var/local/path/
// terminal, never a managed resource; see configuredAttrsSeed's own doc
// comment). Confirmed against the real corpus estate: with only the
// static-config seed landed, this exact shape (aws_ecs_service.
// task_definition = aws_ecs_task_definition.this[0].arn) still showed
// #395's wrong short-form value on a real re-run - configuredAttrsSeed
// alone was not the whole fix.
//
// A residue record left over from an earlier migrate or apply - written
// under [residueConfigSourced]'s widening of [classifyResidue] - is the
// other source [builder.residueSeedFor] reads, and this seeds the import
// stub from THAT when static configuration cannot answer. The provider
// fake below is deliberately the identical quirk
// TestConfiguredAttrsSeedFixesTaskDefinitionFormat uses, so the only
// variable between the two tests is where the seed comes from.
func TestResidueSeedForFixesAManagedReferenceAttribute(t *testing.T) {
	cfg := loadConfig(t, "testdata/residue-seed-managed-ref")
	addr := mustAddr(t, `stub_service.this`)
	schema := stubServiceSchema()

	const wireARN = "arn:aws:ecs:eu-west-1:000000000000:task-definition/mini-td:1"
	const shortForm = "mini-td:1"

	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("residue-seed-managed-ref"))
	rf, err := encodeResidueFields(map[string]cty.Value{"task_definition": cty.StringVal(wireARN)})
	if err != nil {
		t.Fatalf("encoding the residue fixture: %s", err)
	}
	ctx := context.Background()
	if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Residue = rf
	}); err != nil {
		t.Fatalf("writing the residue fixture: %s", err)
	}

	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_service": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
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

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "arn:aws:ecs:eu-west-1:000000000000:service/mini-cluster/mini-svc"},
	}, SingleProvider(provAddr, p), Options{RecordStore: store})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_service.this`})

	if !sawSeededPrior {
		t.Fatal("the provider's ReadResource never saw a non-null task_definition in PriorState - " +
			"residueSeedFor never reached it, so a config-language-subset reference will show issue #395's " +
			"wrong value on EVERY plan forever, exactly as it did against the real corpus estate")
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

// TestResidueSeedForWithoutARecordReproducesIssue395Unfixed is
// TestResidueSeedForFixesAManagedReferenceAttribute's own control: the
// identical fixture and provider fake, with no residue record written at
// all - the shape a FIRST plan after migrate has, before any apply has
// classified residue and before MIGRATE'S OWN ratify wrote one either.
// Proves the fixture genuinely reproduces #395's original bug rather than
// passing regardless of residueSeedFor's involvement.
func TestResidueSeedForWithoutARecordReproducesIssue395Unfixed(t *testing.T) {
	cfg := loadConfig(t, "testdata/residue-seed-managed-ref")
	addr := mustAddr(t, `stub_service.this`)
	schema := stubServiceSchema()

	const wireARN = "arn:aws:ecs:eu-west-1:000000000000:task-definition/mini-td:1"
	const shortForm = "mini-td:1"

	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("residue-seed-managed-ref-control"))

	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_service": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"id":              cty.StringVal(r.Target.ID),
				"name":            cty.NullVal(cty.String),
				"task_definition": cty.NullVal(cty.String),
			}),
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		prior := r.PriorState.GetAttr("task_definition")
		out := shortForm
		if !prior.IsNull() {
			out = wireARN
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"id":              cty.StringVal("arn:aws:ecs:eu-west-1:000000000000:service/mini-cluster/mini-svc"),
			"name":            cty.StringVal("svc"),
			"task_definition": cty.StringVal(out),
		})}
	}

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "arn:aws:ecs:eu-west-1:000000000000:service/mini-cluster/mini-svc"},
	}, SingleProvider(provAddr, p), Options{RecordStore: store})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_service.this`})

	is := res.State.ResourceInstance(addr)
	if is == nil || is.Current == nil {
		t.Fatal("stub_service.this is not in the projection")
	}
	got := string(is.Current.AttrsJSON)
	if !strings.Contains(got, `"task_definition":"`+shortForm+`"`) {
		t.Fatalf("this control must reproduce #395's ORIGINAL bug with no residue record available: "+
			"got %s, want it to contain the short form %q. If this fails, the fixture no longer exercises "+
			"the managed-reference case at all and the paired test above proves nothing.", got, shortForm)
	}
}

// TestResidueSeedForNeverSeedsAComputedAttribute is residueSeedFor's own
// boundary: a residue record can legitimately hold a Computed-only
// attribute too (aws_nat_gateway.regional_nat_gateway_address is the
// confirmed case fillResidue's own doc comment names), and
// residueSeedFor must leave those to fillResidueFor's POST-read fill
// rather than seeding them pre-read - see residueSeedFor's own doc
// comment for why the two are not proven equivalent in this change.
func TestResidueSeedForNeverSeedsAComputedAttribute(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("residue-seed-boundary"))
	addr := locatedTestAddr(t, "aws_lambda_function", "check-links")

	recorded, err := RecordResidueForInstance(ctx, store, addr, addrs.AbsProviderConfig{}, lambdaLikeSchema(), lambdaApplied(), strict.DefaultSecrets, sdkv2LikeRead)
	if err != nil {
		t.Fatalf("RecordResidueForInstance: %s", err)
	}
	if !recorded {
		t.Fatal("setup: this fixture must classify as residue for the boundary check below to mean anything")
	}

	b := &builder{opts: Options{RecordStore: store}}
	seed := b.residueSeedFor(ctx, wanted{addr: addr}, lambdaLikeSchema())

	if v, ok := seed["filename"]; !ok || v.AsString() != "check_links.py.zip" {
		t.Errorf("filename (Optional, not Computed) must be seeded from the residue record; seed=%#v", seed)
	}
	if v, ok := seed["publish"]; !ok || !v.RawEquals(cty.False) {
		t.Errorf("publish (Optional, not Computed) must be seeded from the residue record; seed=%#v", seed)
	}
	if v, ok := seed["source_code_hash"]; ok {
		t.Errorf("source_code_hash (Optional+Computed) must NOT be pre-read seeded even though it is "+
			"recorded as residue - Computed means the provider may answer independent of configuration, "+
			"and fillResidueFor's own post-read, carriesNoInformation-gated fill already handles this "+
			"population correctly; got seeded as %#v", v)
	}
}

// TestResidueSeedForSeedsWhenTheCapturedIdentityAgrees is issue #398's
// acceptance (a): a residue record whose envelope also carries an
// identity ([LocatedRecordFrom]'s best-effort recording, GitHub issue #364
// unit A2) that AGREES with this materialize attempt's own identity seeds
// exactly as it always has. This is the corpus-eks-basic /
// corpus-ecs-fargate case - the same object, read again - and it must stay
// green: their launch-config user_data and task_definition-reference legs
// depend on residue seeding still working when nothing about the object
// changed.
func TestResidueSeedForSeedsWhenTheCapturedIdentityAgrees(t *testing.T) {
	cfg := loadConfig(t, "testdata/residue-seed-managed-ref")
	addr := mustAddr(t, `stub_service.this`)
	schema := stubServiceSchema()

	const wireARN = "arn:aws:ecs:eu-west-1:000000000000:task-definition/mini-td:1"
	const shortForm = "mini-td:1"
	const serviceARN = "arn:aws:ecs:eu-west-1:000000000000:service/mini-cluster/mini-svc"

	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("residue-seed-identity-agrees"))
	ctx := context.Background()
	rf, err := encodeResidueFields(map[string]cty.Value{"task_definition": cty.StringVal(wireARN)})
	if err != nil {
		t.Fatalf("encoding the residue fixture: %s", err)
	}
	if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: serviceARN}
		env.Residue = rf
	}); err != nil {
		t.Fatalf("writing the identity+residue fixture: %s", err)
	}

	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_service": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
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
		out := shortForm
		if !prior.IsNull() {
			sawSeededPrior = true
			out = wireARN
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"id":              cty.StringVal(serviceARN),
			"name":            cty.StringVal("svc"),
			"task_definition": cty.StringVal(out),
		})}
	}

	// The SAME identity the record's envelope was just written under - the
	// "nothing about the object changed" case.
	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: serviceARN},
	}, SingleProvider(provAddr, p), Options{RecordStore: store})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_service.this`})

	if !sawSeededPrior {
		t.Fatal("a captured identity that AGREES with the current resolution must still seed - " +
			"this is issue #398's acceptance (a), and it is what the eks/ecs corpus depends on")
	}
	is := res.State.ResourceInstance(addr)
	got := string(is.Current.AttrsJSON)
	if !strings.Contains(got, wireARN) {
		t.Errorf("the projected task_definition does not carry the live ARN:\n%s\nwant it to contain %q", got, wireARN)
	}
}

// TestResidueSeedForRefusesWhenTheCapturedIdentityDisagrees is issue #398's
// acceptance (b): a residue record whose envelope's OWN captured identity
// (written by [LocatedRecordFrom] at the same apply or migrate that wrote
// the residue) DISAGREES with this materialize attempt's current identity
// must not be seeded. This is the destroyed-and-recreated-outside-choudoufu
// shape #398 describes: the object at this address is a different physical
// object than the one the residue was captured from, and the old residue -
// an ARN reference nothing but this seed would ever resupply - must never
// reach the new object's read as PriorState.
//
// This calls [builder.residueSeedFor] directly rather than through
// [BuildWith], and deliberately so: a resolution with a captured identity
// already in the record store goes through [builder.applyRecordFirst]
// first, which reads that SAME captured identity as w.importID for its own
// attempt - comparing a recordFirst w against the record it came from
// would be circular and could never disagree by construction. What that
// path actually relies on for staleness is [ownershipStale] in
// ownership.go, checked post-read against the live object's own marker;
// this residue check exists for every OTHER path to materialize() - the
// marker sweep, the static evaluator, or a classic retry after
// [ownershipStale] rejects the record - where w's identity is derived
// independently of the record being read here. Calling residueSeedFor
// directly is what isolates that path's behaviour without needing to
// choreograph applyRecordFirst's rejection through a full BuildWith run.
func TestResidueSeedForRefusesWhenTheCapturedIdentityDisagrees(t *testing.T) {
	ctx := context.Background()
	schema := stubServiceSchema()
	addr := mustAddr(t, `stub_service.this`)

	const staleARN = "arn:aws:ecs:eu-west-1:000000000000:task-definition/mini-td:1"
	const oldServiceARN = "arn:aws:ecs:eu-west-1:000000000000:service/mini-cluster/mini-svc"
	const newServiceARN = "arn:aws:ecs:eu-west-1:000000000000:service/mini-cluster/mini-svc-recreated"

	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("residue-seed-identity-disagrees"))
	rf, err := encodeResidueFields(map[string]cty.Value{"task_definition": cty.StringVal(staleARN)})
	if err != nil {
		t.Fatalf("encoding the residue fixture: %s", err)
	}
	if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: oldServiceARN}
		env.Residue = rf
	}); err != nil {
		t.Fatalf("writing the identity+residue fixture: %s", err)
	}

	b := &builder{opts: Options{RecordStore: store}}
	seed := b.residueSeedFor(ctx, wanted{addr: addr, importID: newServiceARN}, schema)

	if v, ok := seed["task_definition"]; ok {
		t.Fatalf("a captured identity (%q) that DISAGREES with the current resolution (%q) must not be "+
			"seeded - the residue was recorded against a different physical object, and issue #398's "+
			"self-reinforcing round-trip is exactly what seeding here would reproduce; got seeded as %#v",
			oldServiceARN, newServiceARN, v)
	}
}

// TestIdentitiesAgree is [identitiesAgree]'s own direct table: the
// comparison [residueIdentityStale] runs, isolated from the record store
// and the materialize path around it.
func TestIdentitiesAgree(t *testing.T) {
	tests := []struct {
		name            string
		current         LocatedRecord
		captured        LocatedRecord
		wantAgreement   bool
		wantExplanation string
	}{
		{
			name:            "both empty",
			current:         LocatedRecord{},
			captured:        LocatedRecord{},
			wantAgreement:   true,
			wantExplanation: "nothing to compare, so nothing to disagree about - a legacy record or a type with no derivable identity",
		},
		{
			name:            "current unknown, captured present",
			current:         LocatedRecord{},
			captured:        LocatedRecord{ImportID: "arn:aws:ecs:eu-west-1:000000000000:service/x"},
			wantAgreement:   true,
			wantExplanation: "permissive: nothing on the current side to disagree with",
		},
		{
			name:            "matching single-string identity",
			current:         LocatedRecord{ImportID: "arn:aws:ecs:eu-west-1:000000000000:service/x"},
			captured:        LocatedRecord{ImportID: "arn:aws:ecs:eu-west-1:000000000000:service/x"},
			wantAgreement:   true,
			wantExplanation: "the same object, read again",
		},
		{
			name:            "different single-string identity",
			current:         LocatedRecord{ImportID: "arn:aws:ecs:eu-west-1:000000000000:service/x"},
			captured:        LocatedRecord{ImportID: "arn:aws:ecs:eu-west-1:000000000000:service/x-recreated"},
			wantAgreement:   false,
			wantExplanation: "issue #398's own shape: destroyed and recreated at a different identity",
		},
		{
			name:            "matching composite identity",
			current:         LocatedRecord{Components: map[string]string{"zone_id": "Z1", "name": "a.example.com."}},
			captured:        LocatedRecord{Components: map[string]string{"zone_id": "Z1", "name": "a.example.com."}},
			wantAgreement:   true,
			wantExplanation: "same composite components",
		},
		{
			name:            "disagreeing composite identity",
			current:         LocatedRecord{Components: map[string]string{"zone_id": "Z1", "name": "a.example.com."}},
			captured:        LocatedRecord{Components: map[string]string{"zone_id": "Z1", "name": "b.example.com."}},
			wantAgreement:   false,
			wantExplanation: "a component differs",
		},
		{
			name:            "composite identity with a differing component count",
			current:         LocatedRecord{Components: map[string]string{"zone_id": "Z1", "name": "a.example.com."}},
			captured:        LocatedRecord{Components: map[string]string{"zone_id": "Z1"}},
			wantAgreement:   false,
			wantExplanation: "fewer components captured than resolved now - not provably the same object",
		},
		{
			name:            "mismatched form: single-string now, composite captured",
			current:         LocatedRecord{ImportID: "arn:aws:ecs:eu-west-1:000000000000:service/x"},
			captured:        LocatedRecord{Components: map[string]string{"zone_id": "Z1"}},
			wantAgreement:   false,
			wantExplanation: "the identity's own SHAPE changed, which is at least as suspicious as its value changing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := identitiesAgree(tt.current, tt.captured)
			if got != tt.wantAgreement {
				t.Errorf("identitiesAgree(%#v, %#v) = %v, want %v (%s)", tt.current, tt.captured, got, tt.wantAgreement, tt.wantExplanation)
			}
		})
	}
}
