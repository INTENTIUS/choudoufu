// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// These tests are #2256 U5 (#2341): the applied-marker verifier and writer
// on the new runtime.
//
// They were written while G's runtime-decision check (#2339) refused any run
// with a ConfigValueAdjuster on the new runtime, which #2256 U6 (#2342)
// lifted. So these tests plan with no adjuster set, then set it and call
// newEngineApply directly. Planning with no adjuster is also what these
// tests want: marker stamping at plan time is U4 (#2340), and here the plan
// carries no marker, which is the tag_on_create false shape the writer
// exists for (GitHub issue #1084).

// appliedMarkerStub is an adjuster with the two apply-time capabilities.
// It changes no configuration value. Its writer adds test_map["marker"] to
// the created object; its verifier reports an error naming any object whose
// planned test_map carried a marker the applied object does not have, or,
// with requireMarker set, any object that has no marker at all.
type appliedMarkerStub struct {
	requireMarker bool

	mu       sync.Mutex
	writes   []string
	verifies []plans.Action
}

const appliedMarkerStubMissing = "Ownership marker missing after apply"

func (s *appliedMarkerStub) AdjustConfigValue(_ context.Context, _ addrs.AbsResourceInstance, config cty.Value, _ providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	return config, nil
}

func (s *appliedMarkerStub) VerifyAppliedMarkers(_ context.Context, addr addrs.AbsResourceInstance, action plans.Action, planned, applied cty.Value, _ providers.Schema) tfdiags.Diagnostics {
	s.mu.Lock()
	s.verifies = append(s.verifies, action)
	s.mu.Unlock()
	var diags tfdiags.Diagnostics
	wanted := s.requireMarker || hasStubMarker(planned)
	if wanted && !hasStubMarker(applied) {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, appliedMarkerStubMissing, addr.String()+" was stored without its marker."))
	}
	return diags
}

func (s *appliedMarkerStub) WriteAppliedMarkers(_ context.Context, addr addrs.AbsResourceInstance, _ addrs.AbsProviderConfig, _ plans.Action, applied cty.Value, _ providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	s.mu.Lock()
	s.writes = append(s.writes, addr.String())
	s.mu.Unlock()
	elems := applied.AsValueMap()
	m := map[string]cty.Value{}
	if v := elems["test_map"]; !v.IsNull() {
		for it := v.ElementIterator(); it.Next(); {
			k, e := it.Element()
			m[k.AsString()] = e
		}
	}
	m["marker"] = cty.StringVal("written")
	elems["test_map"] = cty.MapVal(m)
	return cty.ObjectVal(elems), nil
}

var (
	_ ConfigValueAdjuster   = (*appliedMarkerStub)(nil)
	_ AppliedMarkerVerifier = (*appliedMarkerStub)(nil)
	_ AppliedMarkerWriter   = (*appliedMarkerStub)(nil)
)

func hasStubMarker(v cty.Value) bool {
	if v == cty.NilVal || v.IsNull() || !v.IsKnown() || !v.Type().IsObjectType() || !v.Type().HasAttribute("test_map") {
		return false
	}
	m := v.GetAttr("test_map")
	return !m.IsNull() && m.IsKnown() && m.HasIndex(cty.StringVal("marker")).True()
}

// newEngineApplyWithAdjuster plans m from state on the new runtime with no
// adjuster, then applies the plan through newEngineApply with adj set.
func newEngineApplyWithAdjuster(t *testing.T, m *configs.Config, state *states.State, p *MockProvider, adj ConfigValueAdjuster) (*states.State, tfdiags.Diagnostics) {
	t.Helper()
	forceExperimentalRuntimeForTesting(t)
	tctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
	})
	plan, diags := tctx.Plan(context.Background(), m, state, DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("plan: %s", diags.Err())
	}
	if len(plan.ExecutionGraph) == 0 {
		t.Fatal("plan has no execution graph: it was not made by the new runtime")
	}
	variables, diags := tctx.mergePlanAndApplyVariables(m, plan, nil)
	if diags.HasErrors() {
		t.Fatalf("variables: %s", diags.Err())
	}
	tctx.configValueAdjuster = adj
	return tctx.newEngineApply(context.Background(), m, plan, variables)
}

func echoPlannedProvider() *MockProvider {
	p := simpleMockProvider()
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
	}
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
	}
	return p
}

// TestNewRuntimeApply_appliedMarkerWriterOnCreate: a create on the new
// runtime calls the AppliedMarkerWriter once, and the object it returns,
// marker included, is what the final state holds (#1316). An in-place
// update calls no writer. Without the engine wiring there are no writes and
// the state's test_map has no marker.
func TestNewRuntimeApply_appliedMarkerWriterOnCreate(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "created" {
  test_string = "new"
}

resource "test_object" "updated" {
  test_string = "after"
}
`,
	})
	updatedAddr := mustResourceInstanceAddr("test_object.updated")
	prior := states.BuildState(func(ss *states.SyncState) {
		ss.SetResourceInstanceCurrent(updatedAddr,
			&states.ResourceInstanceObjectSrc{
				Status:    states.ObjectReady,
				AttrsJSON: []byte(`{"test_string":"before"}`),
			},
			addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule},
			addrs.NoKey,
		)
	})

	p := echoPlannedProvider()
	var sent []cty.Value
	var mu sync.Mutex
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		mu.Lock()
		sent = append(sent, req.PlannedState)
		mu.Unlock()
		return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
	}
	adj := &appliedMarkerStub{}

	state, diags := newEngineApplyWithAdjuster(t, m, prior, p, adj)
	assertNoErrors(t, diags)

	if len(adj.writes) != 1 || adj.writes[0] != "test_object.created" {
		t.Fatalf("WriteAppliedMarkers calls = %v, want exactly [test_object.created]", adj.writes)
	}
	for _, v := range sent {
		if hasStubMarker(v) {
			t.Errorf("ApplyResourceChange was sent a marker (%#v); the writer's marker is written after the create", v)
		}
	}

	schema := simpleTestSchema().ImpliedType()
	created := state.ResourceInstance(mustResourceInstanceAddr("test_object.created"))
	if created == nil || created.Current == nil {
		t.Fatal("test_object.created is not in the final state")
	}
	obj, err := created.Current.Decode(schema)
	if err != nil {
		t.Fatalf("decoding test_object.created: %s", err)
	}
	if !hasStubMarker(obj.Value) || obj.Value.GetAttr("test_map").Index(cty.StringVal("marker")).AsString() != "written" {
		t.Errorf("test_object.created in state = %#v, want test_map.marker = \"written\"", obj.Value)
	}

	updated := state.ResourceInstance(updatedAddr)
	if updated == nil || updated.Current == nil {
		t.Fatal("test_object.updated is not in the final state")
	}
	uobj, err := updated.Current.Decode(schema)
	if err != nil {
		t.Fatalf("decoding test_object.updated: %s", err)
	}
	if hasStubMarker(uobj.Value) {
		t.Errorf("test_object.updated in state = %#v: an update must not be marked by the writer", uobj.Value)
	}
}

// TestNewRuntimeApply_appliedMarkerVerifierReportsMissingMarker: a verifier
// reporting a missing marker fails the new runtime's apply with its own
// diagnostic, for a create and for an update, and is not asked about a
// delete. The verifier runs before the writer and the error stops the
// writer, as on the old runtime. Without the engine wiring the apply
// succeeds with no diagnostic.
func TestNewRuntimeApply_appliedMarkerVerifierReportsMissingMarker(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "test_object" "created" {
  test_string = "new"
}

resource "test_object" "updated" {
  test_string = "after"
}
`,
	})
	providerAddr := addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule}
	prior := states.BuildState(func(ss *states.SyncState) {
		ss.SetResourceInstanceCurrent(mustResourceInstanceAddr("test_object.updated"),
			&states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{"test_string":"before"}`)},
			providerAddr, addrs.NoKey,
		)
		ss.SetResourceInstanceCurrent(mustResourceInstanceAddr("test_object.deleted"),
			&states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{"test_string":"gone"}`)},
			providerAddr, addrs.NoKey,
		)
	})

	adj := &appliedMarkerStub{requireMarker: true}
	_, diags := newEngineApplyWithAdjuster(t, m, prior, echoPlannedProvider(), adj)
	if !diags.HasErrors() {
		t.Fatal("apply succeeded; want the verifier's missing-marker error")
	}
	reported := map[string]bool{}
	for _, d := range diags {
		if d.Severity() == tfdiags.Error && d.Description().Summary == appliedMarkerStubMissing {
			reported[d.Description().Detail] = true
		}
	}
	for _, want := range []string{
		"test_object.created was stored without its marker.",
		"test_object.updated was stored without its marker.",
	} {
		if !reported[want] {
			t.Errorf("no %q diagnostic with detail %q; got %s", appliedMarkerStubMissing, want, diags.ErrWithWarnings())
		}
	}
	if len(reported) != 2 {
		t.Errorf("verifier reported %v, want only the create and the update", reported)
	}

	adj.mu.Lock()
	defer adj.mu.Unlock()
	for _, a := range adj.verifies {
		if a == plans.Delete {
			t.Errorf("VerifyAppliedMarkers was called for a delete")
		}
	}
	if len(adj.verifies) != 2 {
		t.Errorf("VerifyAppliedMarkers calls = %v, want one Create and one Update", adj.verifies)
	}
	if len(adj.writes) != 0 {
		t.Errorf("WriteAppliedMarkers calls = %v, want none after the verifier's error", adj.writes)
	}
}
