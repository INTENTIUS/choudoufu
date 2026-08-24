// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestContext2Plan_resourceIdentityResolverNilContract is the plan-node
// seam's nil contract (rfc/20260823-foundation-order-ruling.md, ruling 3;
// HANDOFF.md "The order", item 3): with no resolver and no adjuster
// configured, plugging the fields into ContextOpts must not change a single
// byte of an ordinary plan. It plans the same greenfield resource - no
// prior state, no import block, the shape TestContext2Plan_importResourceBasic
// uses for the same provider - once through a Context built without
// mentioning the new fields at all, and once through a Context that sets
// them explicitly to nil, and requires the two resulting
// ResourceInstanceChangeSrc values to be identical.
func TestContext2Plan_resourceIdentityResolverNilContract(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	})

	planWith := func(opts *ContextOpts) *plans.ResourceInstanceChangeSrc {
		t.Helper()

		p := simpleMockProvider()

		opts.Plugins = plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil)

		ctx := testContext2(t, opts)

		plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
		if diags.HasErrors() {
			t.Fatalf("unexpected errors\n%s", diags.Err())
		}

		instPlan := plan.Changes.ResourceInstance(addr)
		if instPlan == nil {
			t.Fatalf("no plan for %s at all", addr)
		}
		return instPlan
	}

	unset := planWith(&ContextOpts{})
	explicitNil := planWith(&ContextOpts{
		ResourceIdentityResolver: nil,
		ConfigValueAdjuster:      nil,
	})

	if diff := cmp.Diff(unset, explicitNil); diff != "" {
		t.Errorf("plan changed by merely mentioning the nil-valued fields in ContextOpts (-unset +explicitNil):\n%s", diff)
	}

	if unset.Action != plans.Create {
		t.Fatalf("wrong action for a resource with no prior state: got %s, want %s", unset.Action, plans.Create)
	}
	if unset.Importing != nil {
		t.Fatalf("unexpected import: a nil resolver must never cause an import, got %#v", unset.Importing)
	}
}

// stubResourceIdentityResolver is a ResourceIdentityResolver that resolves
// exactly one address to a fixed providers.ImportTarget and reports "not
// found" for every other address, to prove the resolver branch in
// managedResourceExecute actually reaches the provider's ImportResourceState
// call and produces an import in the plan.
type stubResourceIdentityResolver struct {
	addr   addrs.AbsResourceInstance
	target providers.ImportTarget

	calls []addrs.AbsResourceInstance
}

func (s *stubResourceIdentityResolver) ResolveResourceIdentity(_ context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (providers.ImportTarget, bool, tfdiags.Diagnostics) {
	s.calls = append(s.calls, addr)
	if config == cty.NilVal {
		panic("resolver called with no evaluated configuration value")
	}
	if schema.Block == nil {
		panic("resolver called with no resource schema")
	}
	if addr.Equal(s.addr) {
		return s.target, true, nil
	}
	return providers.ImportTarget{}, false, nil
}

func TestContext2Plan_resourceIdentityResolverStubImports(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	})

	p := simpleMockProvider()
	p.ImportResourceStateResponse = &providers.ImportResourceStateResponse{
		ImportedResources: []providers.ImportedResource{
			{
				TypeName: "test_object",
				State: cty.ObjectVal(map[string]cty.Value{
					"test_string": cty.StringVal("foo"),
				}),
			},
		},
	}
	p.ReadResourceResponse = &providers.ReadResourceResponse{
		NewState: cty.ObjectVal(map[string]cty.Value{
			"test_string": cty.StringVal("foo"),
		}),
	}

	resolver := &stubResourceIdentityResolver{
		addr:   addr,
		target: providers.ImportTarget{ID: "stub-123"},
	}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ResourceIdentityResolver: resolver,
	})

	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors\n%s", diags.Err())
	}

	instPlan := plan.Changes.ResourceInstance(addr)
	if instPlan == nil {
		t.Fatalf("no plan for %s at all", addr)
	}

	if instPlan.Importing == nil {
		t.Fatalf("expected the resolver's target to produce an import, got a non-import change (action %s)", instPlan.Action)
	}
	if instPlan.Importing.ID != "stub-123" {
		t.Errorf("wrong import ID: got %q, want %q", instPlan.Importing.ID, "stub-123")
	}

	if len(resolver.calls) != 1 || !resolver.calls[0].Equal(addr) {
		t.Errorf("expected the resolver to be asked once for %s, got calls %v", addr, resolver.calls)
	}
}

// stubConfigValueAdjuster is a ConfigValueAdjuster that sets a fixed value
// at one key of a map-typed attribute, to prove where in the pipeline that
// write lands relative to ignore_changes processing.
type stubConfigValueAdjuster struct {
	attr  string
	key   string
	value string

	calls []addrs.AbsResourceInstance
}

func (s *stubConfigValueAdjuster) AdjustConfigValue(_ context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	s.calls = append(s.calls, addr)
	if config == cty.NilVal {
		panic("adjuster called with no evaluated configuration value")
	}
	if schema.Block == nil {
		panic("adjuster called with no resource schema")
	}

	elems := config.AsValueMap()
	mapVal := elems[s.attr]
	mapElems := map[string]cty.Value{}
	if !mapVal.IsNull() {
		for it := mapVal.ElementIterator(); it.Next(); {
			k, v := it.Element()
			mapElems[k.AsString()] = v
		}
	}
	mapElems[s.key] = cty.StringVal(s.value)
	elems[s.attr] = cty.MapVal(mapElems)
	return cty.ObjectVal(elems), nil
}

// TestContext2Plan_configValueAdjusterHonoursIgnoreChanges is
// opentofu/opentofu#3016's ordering invariant (rfc/20260823-foundation-
// order-ruling.md, ruling 3; GitHub issue #388's stamp half), proven
// generically rather than through the fork's own marker implementation:
// [ConfigValueAdjuster] runs on the evaluated configuration value BEFORE
// n.processIgnoreChanges, so a key an operator's own lifecycle {
// ignore_changes } names is restored from prior state AFTER the adjuster
// writes to it - the adjuster's write is subordinate to ignore_changes,
// exactly as an ordinary hand-typed configuration change to that key would
// be. If the adjuster instead ran after ignore_changes (or after
// PlanResourceChange, which #3016 forbids outright), this test's ignored
// key would show the adjuster's value instead of the prior one.
func TestContext2Plan_configValueAdjusterHonoursIgnoreChanges(t *testing.T) {
	SkipExperimental(t, ExperimentalFeatureIgnoreChanges)

	addr := mustResourceInstanceAddr("test_object.a")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
  test_map = {
    other = "from config"
  }

  lifecycle {
    ignore_changes = [test_map["marker_key"]]
  }
}
`,
	})

	p := simpleMockProvider()
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
	}

	s := states.BuildState(func(ss *states.SyncState) {
		ss.SetResourceInstanceCurrent(
			addr,
			&states.ResourceInstanceObjectSrc{
				Status:    states.ObjectReady,
				AttrsJSON: []byte(`{"test_string":"foo","test_map":{"marker_key":"prior-value","other":"from state"}}`),
			},
			addrs.AbsProviderConfig{
				Provider: addrs.NewDefaultProvider("test"),
				Module:   addrs.RootModule,
			},
			addrs.NoKey,
		)
	})

	adjuster := &stubConfigValueAdjuster{attr: "test_map", key: "marker_key", value: "adjuster-value"}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ConfigValueAdjuster: adjuster,
	})

	plan, diags := ctx.Plan(context.Background(), m, s, DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors\n%s", diags.Err())
	}

	if len(adjuster.calls) != 1 || !adjuster.calls[0].Equal(addr) {
		t.Fatalf("expected the adjuster to be asked once for %s, got calls %v", addr, adjuster.calls)
	}

	instPlan := plan.Changes.ResourceInstance(addr)
	if instPlan == nil {
		t.Fatalf("no plan for %s at all", addr)
	}

	schema := &providers.Schema{Block: simpleTestSchema()}
	ric, err := instPlan.Decode(schema)
	if err != nil {
		t.Fatalf("decoding the planned change: %s", err)
	}

	after := ric.After
	gotMap := after.GetAttr("test_map")
	if gotMap.IsNull() || !gotMap.IsKnown() {
		t.Fatalf("test_map is null or unknown in the planned value: %#v", gotMap)
	}
	elems := gotMap.AsValueMap()

	// The ordering invariant: ignore_changes ran AFTER the adjuster and
	// restored the PRIOR value at the ignored key, not the adjuster's.
	if elems["marker_key"].AsString() != "prior-value" {
		t.Errorf("test_map[\"marker_key\"] = %q, want %q (ignore_changes must win over the adjuster's write)", elems["marker_key"].AsString(), "prior-value")
	}
	// The un-ignored key reflects the adjuster's own untouched pass-through
	// of the rest of the configuration.
	if elems["other"].AsString() != "from config" {
		t.Errorf("test_map[\"other\"] = %q, want %q (the adjuster must not have disturbed an unrelated key)", elems["other"].AsString(), "from config")
	}
}

// TestContext2Plan_resourceIdentityResolverAbsentTargetPlansCreate is edge 2
// of the plan-node seam (rfc/20260823-foundation-order-ruling.md, ruling 3;
// issue #388): a resolver-supplied target, unlike an import block, is a
// guess about a not-yet-applied instance's identity, not a promise that the
// object exists. When the provider answers ImportResourceState with an
// empty ImportedResources list - the ordinary shape for "no such object",
// exercised directly rather than through any one provider's diagnostic
// wording - the node must fall through to an ordinary no-prior-state plan
// (a Create, no import, no error), not abort with "Import returned no
// resources".
func TestContext2Plan_resourceIdentityResolverAbsentTargetPlansCreate(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	})

	p := simpleMockProvider()
	p.ImportResourceStateFn = func(providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{
			ImportedResources: nil,
		}
	}

	resolver := &stubResourceIdentityResolver{
		addr:   addr,
		target: providers.ImportTarget{ID: "guessed-but-absent"},
	}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ResourceIdentityResolver: resolver,
	})

	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: an absent resolver target must fall through to an ordinary create, not abort the plan\n%s", diags.Err())
	}

	instPlan := plan.Changes.ResourceInstance(addr)
	if instPlan == nil {
		t.Fatalf("no plan for %s at all", addr)
	}
	if instPlan.Action != plans.Create {
		t.Errorf("wrong action: got %s, want %s", instPlan.Action, plans.Create)
	}
	if instPlan.Importing != nil {
		t.Errorf("unexpected import: an absent target must not produce an import, got %#v", instPlan.Importing)
	}

	if len(resolver.calls) != 1 || !resolver.calls[0].Equal(addr) {
		t.Errorf("expected the resolver to be asked once for %s, got calls %v", addr, resolver.calls)
	}
}

// TestContext2Plan_resourceIdentityResolverAbsentTargetViaNotFoundDiagnostic
// is the same edge, exercised through the OTHER shape a provider reports
// absence in: an error-severity diagnostic out of ImportResourceState
// itself rather than an empty list, the aws_lambda_permission /
// ResourceNotFoundException shape issue #297 and
// internal/live/projection/build.go's notFoundDiagnostics document. This
// must ALSO fall through to an ordinary create.
func TestContext2Plan_resourceIdentityResolverAbsentTargetViaNotFoundDiagnostic(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	})

	p := simpleMockProvider()
	p.ImportResourceStateFn = func(providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var diags tfdiags.Diagnostics
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"couldn't find resource",
			"no such object",
		))
		return providers.ImportResourceStateResponse{Diagnostics: diags}
	}

	resolver := &stubResourceIdentityResolver{
		addr:   addr,
		target: providers.ImportTarget{ID: "guessed-but-absent"},
	}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ResourceIdentityResolver: resolver,
	})

	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: a not-found-shaped diagnostic must fall through to an ordinary create, not abort the plan\n%s", diags.Err())
	}

	instPlan := plan.Changes.ResourceInstance(addr)
	if instPlan == nil {
		t.Fatalf("no plan for %s at all", addr)
	}
	if instPlan.Action != plans.Create {
		t.Errorf("wrong action: got %s, want %s", instPlan.Action, plans.Create)
	}
	if instPlan.Importing != nil {
		t.Errorf("unexpected import: an absent target must not produce an import, got %#v", instPlan.Importing)
	}
}

// TestContext2Plan_resourceIdentityResolverGenuineErrorStaysFatal is edge
// 2's other half: a provider error that is NOT shaped like an ordinary
// absence - a credentials failure, a malformed request, an actual failure
// to answer - must still abort the plan exactly as it did before this
// edge's fix. Tolerating absence must never widen into tolerating an
// arbitrary provider failure.
func TestContext2Plan_resourceIdentityResolverGenuineErrorStaysFatal(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	})

	p := simpleMockProvider()
	p.ImportResourceStateFn = func(providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var diags tfdiags.Diagnostics
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"AccessDenied",
			"the caller is not authorized to perform this operation",
		))
		return providers.ImportResourceStateResponse{Diagnostics: diags}
	}

	resolver := &stubResourceIdentityResolver{
		addr:   addr,
		target: providers.ImportTarget{ID: "guessed"},
	}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ResourceIdentityResolver: resolver,
	})

	_, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if !diags.HasErrors() {
		t.Fatalf("expected a genuine provider error to abort the plan, got none")
	}
	if !strings.Contains(diags.Err().Error(), "AccessDenied") {
		t.Errorf("expected the genuine provider error to surface, got:\n%s", diags.Err())
	}
}

// TestResolverImportAbsentDiagnostics is a direct, mutation-checkable test
// of the classifier edge 2 relies on: it must accept exactly the shapes
// documented on resolverImportAbsentDiagnostics and reject everything else,
// including a mix of an absent-shaped diagnostic and a genuine one.
func TestResolverImportAbsentDiagnostics(t *testing.T) {
	notFound := func(summary, detail string) *tfdiags.Diagnostic {
		d := tfdiags.Sourceless(tfdiags.Error, summary, detail)
		return &d
	}

	tests := map[string]struct {
		diags tfdiags.Diagnostics
		want  bool
	}{
		"no diagnostics at all": {
			diags: nil,
			want:  false,
		},
		"only a warning": {
			diags: tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Warning, "something", "minor")),
			want:  false,
		},
		"importState's own no-resources summary": {
			diags: tfdiags.Diagnostics{}.Append(*notFound("Import returned no resources", "detail")),
			want:  true,
		},
		"importState's own null-resource summary": {
			diags: tfdiags.Diagnostics{}.Append(*notFound("Import returned null resource", "detail")),
			want:  true,
		},
		"importState's own post-refresh absence summary": {
			diags: tfdiags.Diagnostics{}.Append(*notFound("Cannot import non-existent remote object", "detail")),
			want:  true,
		},
		"provider not-found signal in the summary": {
			diags: tfdiags.Diagnostics{}.Append(*notFound("couldn't find resource", "no such object")),
			want:  true,
		},
		"provider not-found signal in the detail": {
			diags: tfdiags.Diagnostics{}.Append(*notFound("Error", "aws returned ResourceNotFoundException for this lookup")),
			want:  true,
		},
		"a genuine provider error": {
			diags: tfdiags.Diagnostics{}.Append(*notFound("AccessDenied", "the caller is not authorized")),
			want:  false,
		},
		"absence mixed with a genuine error must not be treated as absence": {
			diags: tfdiags.Diagnostics{}.
				Append(*notFound("Import returned no resources", "detail")).
				Append(*notFound("AccessDenied", "the caller is not authorized")),
			want: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := resolverImportAbsentDiagnostics(test.diags)
			if got != test.want {
				t.Errorf("resolverImportAbsentDiagnostics(...) = %v, want %v", got, test.want)
			}
		})
	}
}

// TestContext2Plan_resourceIdentityResolverNoClassicImporterSynthesizesStub
// is #388's own arm (b): internal/live/projection/build.go's importAndRead
// already recognizes a provider's "doesn't support import" /
// "Resource Import Not Implemented" answer as the type having no classic
// Importer at all - a fixed property of the provider's own code, not a
// transient failure - and, when this run already has a resolved identity
// for the instance, synthesizes the near-null stub ImportResourceState
// itself would have returned rather than refusing outright. Before this
// test, n.importState (the plan-node seam's own path to the identical
// provider RPC) had no equivalent: the SAME diagnostic surfaced as a raw,
// misleading provider error and aborted the plan even when a
// resolver-supplied target carried a real, resolved identity object.
//
// The resolved identity here is providers.ImportTarget.Identity - an
// object whose attribute names come from the provider's own identity
// schema, set only when this run resolved a real value, never a default -
// matching test_string on both the identity schema and the resource
// schema so [noimporter.SynthesizeStub] can place it. ReadResourceFn
// asserts, by value, that PriorState carried EXACTLY that value and
// nothing else, proving the stub came from the resolved identity and not
// from a guess.
func TestContext2Plan_resourceIdentityResolverNoClassicImporterSynthesizesStub(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	})

	p := simpleMockProvider()
	p.GetProviderSchemaResponse.ResourceTypes["test_object"] = providers.Schema{
		Block: simpleTestSchema(),
		IdentitySchema: &configschema.Object{
			Attributes: map[string]*configschema.Attribute{
				"test_string": {Type: cty.String, Required: true},
			},
			Nesting: configschema.NestingSingle,
		},
	}
	p.ImportResourceStateFn = func(providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var diags tfdiags.Diagnostics
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Error",
			"resource test_object doesn't support import",
		))
		return providers.ImportResourceStateResponse{Diagnostics: diags}
	}
	var priorStateSeen cty.Value
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		priorStateSeen = r.PriorState
		return providers.ReadResourceResponse{
			NewState: cty.ObjectVal(map[string]cty.Value{
				"test_string": cty.StringVal("resolved-value"),
				"test_number": cty.NullVal(cty.Number),
				"test_bool":   cty.NullVal(cty.Bool),
				"test_list":   cty.NullVal(cty.List(cty.String)),
				"test_map":    cty.NullVal(cty.Map(cty.String)),
			}),
		}
	}

	resolver := &stubResourceIdentityResolver{
		addr: addr,
		target: providers.ImportTarget{
			Identity: cty.ObjectVal(map[string]cty.Value{
				"test_string": cty.StringVal("resolved-value"),
			}),
		},
	}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ResourceIdentityResolver: resolver,
	})

	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: a resolved identity for a no-classic-Importer type must synthesize a stub, not abort\n%s", diags.Err())
	}

	instPlan := plan.Changes.ResourceInstance(addr)
	if instPlan == nil {
		t.Fatalf("no plan for %s at all", addr)
	}
	if instPlan.Importing == nil {
		t.Fatalf("expected the synthesized stub to still produce an import, got a non-import change (action %s)", instPlan.Action)
	}

	if priorStateSeen == cty.NilVal {
		t.Fatal("ReadResource was never called - the stub was not synthesized")
	}
	if got := priorStateSeen.GetAttr("test_string").AsString(); got != "resolved-value" {
		t.Errorf("PriorState.test_string = %q, want %q", got, "resolved-value")
	}
	if !priorStateSeen.GetAttr("test_number").IsNull() {
		t.Errorf("PriorState.test_number = %#v, want null - nothing in the resolved identity named it", priorStateSeen.GetAttr("test_number"))
	}
}

// TestContext2Plan_resourceIdentityResolverNoClassicImporterRefusesWithNoIdentityValues
// is the boundary the synthesis above must not cross: a resolver-supplied
// target that carries only an opaque import-ID string, with no identity
// object this run can name attribute values from, has nothing safe to
// synthesize a stub from. The refusal must stand, worded accurately
// ("Resource type has no classic Importer", the same wording
// internal/live/projection/build.go's importAndRead already gives this
// exact case) rather than surfacing the provider's raw "doesn't support
// import" text as if it were a transient failure, and ReadResource must
// never be called.
func TestContext2Plan_resourceIdentityResolverNoClassicImporterRefusesWithNoIdentityValues(t *testing.T) {
	addr := mustResourceInstanceAddr("test_object.a")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "a" {
  test_string = "foo"
}
`,
	})

	p := simpleMockProvider()
	p.ImportResourceStateFn = func(providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var diags tfdiags.Diagnostics
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Error",
			"resource test_object doesn't support import",
		))
		return providers.ImportResourceStateResponse{Diagnostics: diags}
	}
	p.ReadResourceFn = func(providers.ReadResourceRequest) providers.ReadResourceResponse {
		t.Fatal("ReadResource must never be called with no identity values to synthesize a stub from")
		return providers.ReadResourceResponse{}
	}

	resolver := &stubResourceIdentityResolver{
		addr:   addr,
		target: providers.ImportTarget{ID: "opaque-id-only"},
	}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ResourceIdentityResolver: resolver,
	})

	_, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	if !diags.HasErrors() {
		t.Fatalf("expected the refusal to stand with no identity values to synthesize from, got none")
	}
	var found *string
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		s := d.Description().Summary
		found = &s
	}
	if found == nil || *found != "Resource type has no classic Importer" {
		// build.go's own accurate wording, mirrored here rather than the
		// provider's raw "Error" summary this test's ImportResourceStateFn
		// returns - proving the reclassification happened rather than the
		// raw diagnostic merely passing through unchanged.
		t.Errorf("refusal summary = %v, want %q", found, "Resource type has no classic Importer")
	}
}
