// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/newruntime"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// reference-k8s' drift_reconverge on the new runtime (#2256): a ConfigMap
// with depends_on, tampered out of band, planned with its ownership label
// and address annotation stamped by the adjuster, then failed its apply with
// "Provider produced inconsistent final plan ... element "tofu-estate" has
// vanished". depends_on marks the new runtime's configuration value as a
// whole: at apply with the evaluator's ResourceInstanceMark, which the
// planning oracle strips and the applying oracle does not, and at plan with
// objchange.PendingChange when the dependency itself is changing. The real
// adjuster (internal/live/projection, NodeResolver) declines a
// configuration marked as a whole, so one phase stamped the markers and the
// other did not. configadjust.Adjust now peels the engine's marks off
// before asking (peelEngineMarks).
//
// metadataMarkerStub mirrors that contract: it stamps metadata[0].labels and
// metadata[0].annotations the way the Kubernetes label surface does, or tags
// the way the AWS and azurerm tag surface does, and leaves a configuration
// marked as a whole alone. reference-azure's day2_crash recovery apply hit
// the same thing on a create, with the adjuster's own warning "Cannot set
// ownership markers on a marked configuration value".
type metadataMarkerStub struct {
	markedCalls int
}

func (s *metadataMarkerStub) AdjustConfigValue(_ context.Context, _ addrs.AbsResourceInstance, config cty.Value, _ providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	if config.IsNull() || !config.IsKnown() {
		return config, nil
	}
	if config.IsMarked() {
		s.markedCalls++
		return config, nil
	}
	attrs := config.AsValueMap()
	if _, tagged := attrs["tags"]; tagged {
		// The tag surface (AWS, azurerm): reference-azure day2_crash's
		// shape, a create of a resource with depends_on.
		attrs["tags"] = cty.MapVal(map[string]cty.Value{
			"tofu-estate":  cty.StringVal("est"),
			"tofu-address": cty.StringVal("test_tagged.second"),
		})
		return cty.ObjectVal(attrs), nil
	}
	meta := attrs["metadata"]
	if meta.IsNull() || !meta.IsKnown() || meta.LengthInt() != 1 {
		return config, nil
	}
	elem := meta.Index(cty.NumberIntVal(0)).AsValueMap()
	elem["labels"] = cty.MapVal(map[string]cty.Value{"tofu-estate": cty.StringVal("est")})
	elem["annotations"] = cty.MapVal(map[string]cty.Value{"choudoufu.intentius.io/tofu-address": cty.StringVal("test_meta.app")})
	attrs["metadata"] = cty.ListVal([]cty.Value{cty.ObjectVal(elem)})
	return cty.ObjectVal(attrs), nil
}

func taggedMarkerSchema() *configschema.Block {
	return &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"name": {Type: cty.String, Optional: true},
			"tags": {Type: cty.Map(cty.String), Optional: true},
		},
	}
}

func finalMarkersProvider() *MockProvider {
	p := &MockProvider{
		GetProviderSchemaResponse: getProviderSchemaResponseFromProviderSchema(&ProviderSchema{
			Provider: &configschema.Block{},
			ResourceTypes: map[string]*configschema.Block{
				"test_meta":   metadataMarkerSchema(),
				"test_tagged": taggedMarkerSchema(),
			},
		}),
	}
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
	}
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		planned := req.PlannedState.AsValueMap()
		if id := planned["id"]; !id.IsKnown() {
			planned["id"] = cty.StringVal("assigned")
		}
		return providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(planned)}
	}
	return p
}

func metadataMarkerSchema() *configschema.Block {
	return &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"data": {Type: cty.Map(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {
				Nesting:  configschema.NestingList,
				MinItems: 1,
				MaxItems: 1,
				Block: configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"name":        {Type: cty.String, Required: true},
						"labels":      {Type: cty.Map(cty.String), Optional: true},
						"annotations": {Type: cty.Map(cty.String), Optional: true},
					},
				},
			},
		},
	}
}

// TestNewRuntimeApply_driftUpdateKeepsMetadataMarkers: an in-place update of
// a resource with depends_on, whose markers the adjuster stamps into
// metadata labels and annotations, applies on the new runtime and the
// applied object carries both markers.
func TestNewRuntimeApply_driftUpdateKeepsMetadataMarkers(t *testing.T) {
	forceExperimentalRuntimeForTesting(t)
	t.Setenv(newruntime.StrictVariable, "1")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_meta" "ns" {
  metadata {
    name = "ns"
  }
}

resource "test_meta" "app" {
  metadata {
    name = "app-config"
  }
  data = {
    greeting = "hello"
  }
  depends_on = [test_meta.ns]
}
`,
	})

	p := finalMarkersProvider()

	provider := mustProviderConfig(`provider["registry.opentofu.org/hashicorp/test"]`)
	state := states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(mustResourceInstanceAddr("test_meta.ns"), &states.ResourceInstanceObjectSrc{
			Status:    states.ObjectReady,
			AttrsJSON: []byte(`{"id":"ns","data":null,"metadata":[{"name":"ns","labels":{"tofu-estate":"est"},"annotations":{"choudoufu.intentius.io/tofu-address":"test_meta.app"}}]}`),
		}, provider, addrs.NoKey)
		// Tampered out of band: data no longer matches the configuration,
		// the markers are intact.
		s.SetResourceInstanceCurrent(mustResourceInstanceAddr("test_meta.app"), &states.ResourceInstanceObjectSrc{
			Status:       states.ObjectReady,
			AttrsJSON:    []byte(`{"id":"app","data":{"greeting":"tampered"},"metadata":[{"name":"app-config","labels":{"tofu-estate":"est"},"annotations":{"choudoufu.intentius.io/tofu-address":"test_meta.app"}}]}`),
			Dependencies: []addrs.ConfigResource{mustConfigResourceAddr("test_meta.ns")},
		}, provider, addrs.NoKey)
	})

	adj := &metadataMarkerStub{}
	c := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
		ConfigValueAdjuster: adj,
	})

	plan, diags := c.Plan(context.Background(), m, state, DefaultPlanOpts)
	assertNoErrors(t, diags)
	if len(plan.ExecutionGraph) == 0 {
		t.Fatal("plan has no execution graph: it was not made by the new runtime")
	}
	appAddr := mustResourceInstanceAddr("test_meta.app")
	if ch := plan.Changes.ResourceInstance(appAddr); ch == nil || ch.Action.String() != "Update" {
		t.Fatalf("want an in-place update of %s, got %#v", appAddr, ch)
	}

	newState, diags := c.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)

	rs := newState.ResourceInstance(appAddr)
	if rs == nil || rs.Current == nil {
		t.Fatalf("no applied object for %s", appAddr)
	}
	obj, err := rs.Current.Decode(metadataMarkerSchema().ImpliedType())
	if err != nil {
		t.Fatalf("decoding the applied object: %s", err)
	}
	meta := obj.Value.GetAttr("metadata").Index(cty.NumberIntVal(0))
	if got := meta.GetAttr("labels"); got.IsNull() || !got.HasIndex(cty.StringVal("tofu-estate")).True() {
		t.Errorf("applied labels lost tofu-estate: %#v", got)
	}
	if got := meta.GetAttr("annotations"); got.IsNull() || !got.HasIndex(cty.StringVal("choudoufu.intentius.io/tofu-address")).True() {
		t.Errorf("applied annotations lost the address: %#v", got)
	}
	if got := obj.Value.GetAttr("data").Index(cty.StringVal("greeting")); !got.RawEquals(cty.StringVal("hello")) {
		t.Errorf("applied data.greeting = %#v, want the configured value", got)
	}
	if adj.markedCalls != 0 {
		t.Errorf("the adjuster was handed a configuration marked as a whole %d time(s); the engine must strip the evaluator's internal marks first", adj.markedCalls)
	}
}

// TestNewRuntimeApply_createWithDependsOnKeepsTagMarkers: a create of a
// resource with depends_on, whose markers the adjuster stamps into tags,
// applies on the new runtime and the created object carries both markers.
func TestNewRuntimeApply_createWithDependsOnKeepsTagMarkers(t *testing.T) {
	forceExperimentalRuntimeForTesting(t)
	t.Setenv(newruntime.StrictVariable, "1")

	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_tagged" "first" {
  name = "first"
}

resource "test_tagged" "second" {
  name       = "second"
  depends_on = [test_tagged.first]
}
`,
	})

	adj := &metadataMarkerStub{}
	c := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(finalMarkersProvider()),
		}, nil),
		ConfigValueAdjuster: adj,
	})

	plan, diags := c.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	assertNoErrors(t, diags)
	if len(plan.ExecutionGraph) == 0 {
		t.Fatal("plan has no execution graph: it was not made by the new runtime")
	}

	newState, diags := c.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)

	addr := mustResourceInstanceAddr("test_tagged.second")
	rs := newState.ResourceInstance(addr)
	if rs == nil || rs.Current == nil {
		t.Fatalf("no applied object for %s", addr)
	}
	obj, err := rs.Current.Decode(taggedMarkerSchema().ImpliedType())
	if err != nil {
		t.Fatalf("decoding the applied object: %s", err)
	}
	tags := obj.Value.GetAttr("tags")
	for _, key := range []string{"tofu-estate", "tofu-address"} {
		if tags.IsNull() || !tags.HasIndex(cty.StringVal(key)).True() {
			t.Errorf("applied tags lost %s: %#v", key, tags)
		}
	}
	if adj.markedCalls != 0 {
		t.Errorf("the adjuster was handed a configuration marked as a whole %d time(s); the engine must strip the evaluator's internal marks first", adj.markedCalls)
	}
}
