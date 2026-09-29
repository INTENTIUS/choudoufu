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
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file's tests are GitHub issue #1240's: the terraform-plugin-framework
// half of #1185. Same schema shape, same configuration, a different
// carrier. See [withConfiguredTimeoutsBlock]'s doc comment for the
// mechanism.
//
// The stub provider below is terraform-plugin-framework's own behaviour for
// an imported instance, read off the framework's source
// (internal/fwserver/server_importresourcestate.go and
// server_readresource.go, resource/import_state.go) and the
// terraform-plugin-framework-timeouts module (resource/timeouts/schema.go,
// timeouts.go), at their main branches on 2026-09-21:
//
//   - ImportResourceState starts from a state that is null at every
//     attribute (fromproto5's EmptyState is tftypes.NewValue(type, nil)),
//     and ImportStatePassthroughID sets the one id attribute. Every other
//     attribute, the `timeouts` block included, is null on the stub. The
//     private that comes back is the framework's own map[string][]byte
//     carrying only its ".import_before_read" marker, JSON-encoded with the
//     values base64'd.
//   - ReadResource hands the resource req.State = the prior verbatim; a
//     framework Read (every hashicorp/aws framework resource with a
//     timeouts block: request.State.Get(&data) ... response.State.Set(&data))
//     round-trips the model's Timeouts field without consulting the API,
//     because there is nothing in any API to read a timeout from. The
//     framework then deletes the import marker, and Data.Bytes returns nil
//     for a private with nothing left in it.
//   - timeouts.Block renders a SingleNestedBlock of Optional string
//     attributes, and Value.Delete reads the "delete" attribute off the
//     state object handed to Delete, falling back to the caller's default
//     when it is null. No private state is involved anywhere.
//
// So after import-and-read a framework resource's prior carries a NULL
// timeouts object and an EMPTY private. That is the reproduction.

// frameworkImportPrivate is what terraform-plugin-framework's
// ImportResourceState puts in the private: its internal
// ".import_before_read" marker, and nothing of the provider's. json.Marshal
// over a map[string][]byte base64-encodes the value, so `true` reads as
// dHJ1ZQ==.
var frameworkImportPrivate = []byte(`{".import_before_read":"dHJ1ZQ=="}`)

// frameworkStubProvider is the provider described in this file's header:
// import answers a bare id with everything else null, read round-trips the
// state it was handed, and neither writes an SDKv2 timeout meta anywhere.
// The schema is [stubNamespaceSchema]'s, because the two SDKs render the
// same block and that is the whole reason #1240 exists.
func frameworkStubProvider() *tofu.MockProvider {
	schema := stubNamespaceSchema()
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_ns": schema},
		},
	}
	p.ConfigureProviderCalled = true
	ty := schema.Block.ImpliedType()
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		// ImportStatePassthroughID: id set, every other attribute null.
		attrs := make(map[string]cty.Value, len(ty.AttributeTypes()))
		for name, aty := range ty.AttributeTypes() {
			attrs[name] = cty.NullVal(aty)
		}
		attrs["id"] = cty.StringVal(r.Target.ID)
		attrs["name"] = cty.StringVal(r.Target.ID)
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State:    cty.ObjectVal(attrs),
			Private:  frameworkImportPrivate,
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		// State.Get then State.Set of the same model; the import marker is
		// cleared and Data.Bytes returns nil for an empty private.
		return providers.ReadResourceResponse{
			NewState: r.PriorState,
			Private:  nil,
		}
	}
	return p
}

var stubTimeoutsType = cty.Object(map[string]cty.Type{"create": cty.String, "delete": cty.String})

// projectFrameworkStub runs the fixture's held and plain instances through
// the same BuildFrom path #1185's test drives, against the framework stub.
func projectFrameworkStub(t *testing.T) (*configs.Config, *tofu.MockProvider, *Result) {
	t.Helper()
	cfg := loadConfig(t, "testdata/timeouts")
	p := frameworkStubProvider()
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `stub_ns.held`), Class: identity.ClassConcrete, ImportID: "held", IdentityValues: map[string]string{"name": "held"}},
		{Addr: mustAddr(t, `stub_ns.plain`), Class: identity.ClassConcrete, ImportID: "plain", IdentityValues: map[string]string{"name": "plain"}},
		{Addr: mustAddr(t, `stub_ns.unparseable`), Class: identity.ClassConcrete, ImportID: "unparseable", IdentityValues: map[string]string{"name": "unparseable"}},
	}, SingleProvider(provAddr, p))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_ns.held`, `stub_ns.plain`, `stub_ns.unparseable`})
	return cfg, p, res
}

// projectedTimeouts reads the projected prior's timeouts attribute the way
// the plan will: off the encoded AttrsJSON, not off any value this package
// still holds in memory.
func projectedTimeouts(t *testing.T, res *Result, addr string) interface{} {
	t.Helper()
	ri := res.State.ResourceInstance(mustAddr(t, addr))
	if ri == nil || ri.Current == nil {
		t.Fatalf("no object recorded for %s", addr)
	}
	var attrs map[string]interface{}
	if err := json.Unmarshal(ri.Current.AttrsJSON, &attrs); err != nil {
		t.Fatalf("%s's attributes are not valid JSON: %s", addr, err)
	}
	return attrs["timeouts"]
}

// TestProjectionSeedsAFrameworkTimeoutsBlockFromConfiguration is #1240's
// measurement and, once [withConfiguredTimeoutsBlock] is wired, its
// reproduction: the projected prior of a framework resource declaring
// `timeouts { delete = "20s" }` carries that object, where stock's
// state-backed prior would carry what the last apply wrote there, which is
// the same thing.
//
// Measured at 53f445300b, before the fix: the projected prior's timeouts
// attribute was JSON null for stub_ns.held, so a framework Delete reading
// data.Timeouts.Delete(ctx, default) off that prior would get the
// provider's hard-coded default rather than the configured 20s.
func TestProjectionSeedsAFrameworkTimeoutsBlockFromConfiguration(t *testing.T) {
	_, p, res := projectFrameworkStub(t)

	got := projectedTimeouts(t, res, `stub_ns.held`)
	want := map[string]interface{}{"create": nil, "delete": "20s"}
	if got == nil {
		t.Fatalf("stub_ns.held's projected prior carries timeouts = null.\n"+
			"A terraform-plugin-framework Delete reads its deadline from the timeouts object on the prior state "+
			"(timeouts.Value.Delete), and a null there is the provider's hard-coded default: the operator's "+
			"`timeouts { delete = \"20s\" }` was dropped. Want %v", want)
	}
	gotMap, ok := got.(map[string]interface{})
	if !ok || gotMap["delete"] != "20s" || gotMap["create"] != nil {
		t.Errorf("stub_ns.held's projected prior carries timeouts = %v, want %v", got, want)
	}

	// The private is the framework's, and it is left exactly as the read
	// returned it: nothing here writes an SDKv2 meta into a private that
	// the framework would then fail to load.
	held := res.State.ResourceInstance(mustAddr(t, `stub_ns.held`))
	if len(held.Current.Private) != 0 {
		t.Errorf("stub_ns.held's private = %s, want the empty private the framework's read returned", held.Current.Private)
	}

	if got := projectedTimeouts(t, res, `stub_ns.plain`); got != nil {
		t.Errorf("stub_ns.plain's projected prior gained timeouts = %v; the block declares none, so the read's null is the answer", got)
	}
	if got := projectedTimeouts(t, res, `stub_ns.unparseable`); got != nil {
		t.Errorf("stub_ns.unparseable's projected prior gained timeouts = %v; `delete = \"not-a-duration\"` is a configuration "+
			"the framework's own duration validator refuses, so no applied state ever held it and none is invented here", got)
	}

	// The read saw the null the import produced, not a value seeded on the
	// way in: this seed is applied to what the read RETURNED, so a provider
	// whose Read does fill the block from somewhere keeps the last word.
	if rp := p.ReadResourceRequest.PriorState; rp != cty.NilVal && !rp.GetAttr("timeouts").IsNull() {
		t.Errorf("the read was handed timeouts = %#v; the seed is post-read and must not reach the provider", rp.GetAttr("timeouts"))
	}
}

// stubContext is a core context over the same stub provider the projection
// used, so a plan and an apply against the projected state ask the same
// provider the projection asked.
func stubContext(t *testing.T, p *tofu.MockProvider) *tofu.Context {
	t.Helper()
	core, diags := tofu.NewContext(&tofu.ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("stub"): func() (providers.Interface, error) { return p, nil },
		}, nil),
	})
	if diags.HasErrors() {
		t.Fatalf("tofu.NewContext: %s", diags.Err())
	}
	return core
}

// stubVariables assigns the fixture's one root variable its own default, as
// the CLI would; core's Plan refuses an unassigned root variable outright.
func stubVariables() tofu.InputValues {
	return tofu.InputValues{"hold": &tofu.InputValue{Value: cty.StringVal("20s"), SourceType: tofu.ValueFromCaller}}
}

// withNullTimeouts rewrites one instance's projected prior so its timeouts
// object is null: what the live path carried for a framework resource
// before this fix, kept here as the measured baseline the seed exists for.
func withNullTimeouts(t *testing.T, st *states.State, addr addrs.AbsResourceInstance) *states.State {
	t.Helper()
	ty := stubNamespaceSchema().Block.ImpliedType()
	out := st.DeepCopy()
	ri := out.ResourceInstance(addr)
	if ri == nil || ri.Current == nil {
		t.Fatalf("no object recorded for %s", addr)
	}
	obj, err := ri.Current.Decode(ty)
	if err != nil {
		t.Fatal(err)
	}
	attrs := obj.Value.AsValueMap()
	attrs["timeouts"] = cty.NullVal(stubTimeoutsType)
	obj.Value = cty.ObjectVal(attrs)
	src, err := obj.Encode(ty, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	rs := out.Resource(addr.ContainingResource())
	out.Module(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, rs.ProviderConfig, addrs.NoKey)
	return out
}

// TestFrameworkTimeoutsSeedReplansAsNoChange is the replan measurement the
// value-side seed owes (#1240's own framing: unlike #1185's private write,
// a seed into obj.Value can move a plan diff). Against the same
// configuration, the seeded prior plans as stock's state-backed prior does,
// with no change; the null prior the live path carried before this fix
// plans as an in-place update on every run, forever, which is the second
// symptom on top of the dropped deadline.
func TestFrameworkTimeoutsSeedReplansAsNoChange(t *testing.T) {
	cfg, p, res := projectFrameworkStub(t)
	held := mustAddr(t, `stub_ns.held`)

	planAction := func(t *testing.T, st *states.State) plans.Action {
		t.Helper()
		plan, diags := stubContext(t, p).Plan(context.Background(), cfg, st, &tofu.PlanOpts{Mode: plans.NormalMode, SetVariables: stubVariables()})
		if diags.HasErrors() {
			t.Fatalf("planning against the projected state: %s", diags.Err())
		}
		change := plan.Changes.ResourceInstance(held)
		if change == nil {
			t.Fatalf("the plan holds no change at all for %s; changes: %v", held, changedAddrs(plan))
		}
		return change.Action
	}

	t.Run("seeded prior", func(t *testing.T) {
		if got := planAction(t, res.State); got != plans.NoOp {
			t.Errorf("a prior seeded with the configured timeouts block replans as %s against the same configuration, want NoOp: "+
				"that is what stock's state-backed prior does", got)
		}
	})

	t.Run("null prior", func(t *testing.T) {
		// Not a regression guard: this pins the baseline the seed is
		// measured against. If it ever reads NoOp the seed has stopped
		// being needed for the plan and this file's reasoning is stale.
		if got := planAction(t, withNullTimeouts(t, res.State, held)); got != plans.Update {
			t.Errorf("the pre-fix null prior replans as %s, want Update: the measured baseline this seed exists for has moved", got)
		}
	})
}

// TestFrameworkDestroyReceivesTheConfiguredTimeouts is the destroy leg: the
// prior state handed to the provider's delete carries timeouts.delete =
// "20s", which is exactly what a framework Delete reads
// (request.State.Get(&data); data.Timeouts.Delete(ctx, default)). Core's
// planDestroy puts the prior object's value in the change's Before, and the
// apply hands it to ApplyResourceChange as PriorState.
func TestFrameworkDestroyReceivesTheConfiguredTimeouts(t *testing.T) {
	cfg, p, res := projectFrameworkStub(t)

	priors := map[string]cty.Value{}
	p.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		if !r.PriorState.IsNull() {
			priors[r.PriorState.GetAttr("id").AsString()] = r.PriorState
		}
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState, Private: r.PlannedPrivate}
	}

	core := stubContext(t, p)
	plan, diags := core.Plan(context.Background(), cfg, res.State, &tofu.PlanOpts{Mode: plans.DestroyMode, SetVariables: stubVariables()})
	if diags.HasErrors() {
		t.Fatalf("destroy plan: %s", diags.Err())
	}
	if _, diags := core.Apply(context.Background(), plan, cfg, nil); diags.HasErrors() {
		t.Fatalf("destroy apply: %s", diags.Err())
	}

	prior, ok := priors["held"]
	if !ok {
		t.Fatalf("no destroy reached the provider for stub_ns.held; deletes seen: %v", priors)
	}
	timeouts := prior.GetAttr("timeouts")
	if timeouts.IsNull() {
		t.Fatalf("the destroy's prior state carries timeouts = null; a framework Delete would use its hard-coded default")
	}
	if d := timeouts.GetAttr("delete"); d.IsNull() || d.AsString() != "20s" {
		t.Errorf("the destroy's prior state carries timeouts.delete = %#v, want \"20s\"", d)
	}
	if plain, ok := priors["plain"]; ok && !plain.GetAttr("timeouts").IsNull() {
		t.Errorf("stub_ns.plain's destroy carries timeouts = %#v, want null: it declares no block", plain.GetAttr("timeouts"))
	}
}

// TestWithConfiguredTimeoutsBlockGates pins every condition under which the
// value write must NOT happen, each one mutated out during review to
// confirm the test sees it: a private carrying #1185's SDKv2 meta (that
// carrier's own test also pins the value untouched), a read that answered
// the block itself, a seed whose type is not the schema's, a schema with no
// NestingSingle timeouts block, and a marked prior, whose marks must
// survive the write.
func TestWithConfiguredTimeoutsBlockGates(t *testing.T) {
	block := stubNamespaceSchema().Block
	seed := cty.ObjectVal(map[string]cty.Value{"create": cty.NullVal(cty.String), "delete": cty.StringVal("20s")})
	prior := func(timeouts cty.Value) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{
			"id":       cty.StringVal("held"),
			"name":     cty.StringVal("held"),
			"timeouts": timeouts,
		})
	}
	nullPrior := prior(cty.NullVal(stubTimeoutsType))

	if got, ok := withConfiguredTimeoutsBlock(nullPrior, seed, block, nil); !ok || !got.GetAttr("timeouts").RawEquals(seed) {
		t.Fatalf("the plain case did not seed: ok=%v timeouts=%#v", ok, got.GetAttr("timeouts"))
	}
	if _, ok := withConfiguredTimeoutsBlock(nullPrior, seed, block, frameworkImportPrivate); !ok {
		t.Error("the framework's own import-marker private refused the seed; it carries no SDKv2 meta")
	}
	if _, ok := withConfiguredTimeoutsBlock(nullPrior, seed, block, declaredDefaultPrivate(t)); ok {
		t.Error("seeded over a private carrying helper/schema's timeout meta: that is #1185's carrier, and its value is left alone by design")
	}
	answered := prior(cty.ObjectVal(map[string]cty.Value{"create": cty.StringVal("1m"), "delete": cty.NullVal(cty.String)}))
	if got, ok := withConfiguredTimeoutsBlock(answered, seed, block, nil); ok || !got.RawEquals(answered) {
		t.Error("overwrote a timeouts object the read itself returned; the read keeps the last word")
	}
	if _, ok := withConfiguredTimeoutsBlock(nullPrior, cty.ObjectVal(map[string]cty.Value{"delete": cty.StringVal("20s")}), block, nil); ok {
		t.Error("seeded an object whose type is not the schema's nested block type; the prior would no longer conform")
	}
	if _, ok := withConfiguredTimeoutsBlock(nullPrior, seed, stubLaunchConfigSchema().Block, nil); ok {
		t.Error("seeded against a schema with no timeouts block")
	}
	if _, ok := withConfiguredTimeoutsBlock(nullPrior, cty.NilVal, block, nil); ok {
		t.Error("seeded with nothing configured")
	}

	marked := nullPrior.MarkWithPaths([]cty.PathValueMarks{{Path: cty.GetAttrPath("name"), Marks: cty.NewValueMarks("sensitive")}})
	got, ok := withConfiguredTimeoutsBlock(marked, seed, block, nil)
	if !ok {
		t.Fatal("a marked prior refused the seed")
	}
	if !got.GetAttr("name").HasMark("sensitive") {
		t.Error("the write dropped the prior's sensitivity mark on name")
	}
	if got.GetAttr("timeouts").IsMarked() {
		t.Error("the seeded block acquired a mark it was never given")
	}
}

// TestTimeoutsBlockSeedRefusesWhatNoStateCouldHold pins [timeoutsBlockSeed]'s
// own refusals, which are the part of the gate the projection test above
// reaches only through the fixture's unparseable instance.
func TestTimeoutsBlockSeedRefusesWhatNoStateCouldHold(t *testing.T) {
	schema := stubNamespaceSchema()
	obj := func(create, del cty.Value) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{"create": create, "delete": del})
	}
	for name, tc := range map[string]struct {
		block cty.Value
		want  bool
	}{
		"delete set":             {obj(cty.NullVal(cty.String), cty.StringVal("20s")), true},
		"both set":               {obj(cty.StringVal("1m"), cty.StringVal("20s")), true},
		"nothing set":            {obj(cty.NullVal(cty.String), cty.NullVal(cty.String)), false},
		"one not a duration":     {obj(cty.StringVal("1m"), cty.StringVal("twenty")), false},
		"unknown":                {obj(cty.NullVal(cty.String), cty.UnknownVal(cty.String)), false},
		"missing attribute":      {cty.ObjectVal(map[string]cty.Value{"delete": cty.StringVal("20s")}), false},
		"nil":                    {cty.NilVal, false},
		"no block in the schema": {cty.NilVal, false},
	} {
		t.Run(name, func(t *testing.T) {
			s := schema
			if name == "no block in the schema" {
				s = stubLaunchConfigSchema()
				tc.block = obj(cty.NullVal(cty.String), cty.StringVal("20s"))
			}
			got := timeoutsBlockSeed(tc.block, s)
			if (got != cty.NilVal) != tc.want {
				t.Errorf("timeoutsBlockSeed = %#v, want seeded=%v", got, tc.want)
			}
			if tc.want && !got.RawEquals(tc.block) {
				t.Errorf("the seed is %#v, not the block as configured %#v", got, tc.block)
			}
		})
	}
}
