// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/msgpack"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file holds the reproductions for the two call sites in this package
// that put a MARKED cty.Value to a provider, which nothing can serialize.
//
// The rule is stated in three places already - internal/live/dataread's
// read.go:372, residue.go:604's "a marked value cannot cross the plugin
// channel", and build.go's normalizeIdentityAttrs - and these two sites did
// not follow it. Both were found by auditing the #343/#344 fix rather than by
// an estate refusing, which is exactly why they need tests: one of them fails
// silently by design and the other takes the whole estate down.
//
// # Why these tests marshal
//
// tofu.MockProvider does not serialize anything, so a mock alone can only
// assert the PROPERTY (nothing put to the provider is marked) and never the
// failure. [refuseMarkedOnTheWire] closes that: it is what
// internal/plugin/grpc_provider.go does to every cty.Value on its way into a
// request - msgpack.Marshal against the schema's implied type - and cty
// answers a marked value there with "value has marks, so it cannot be
// serialized" (cty/msgpack/marshal.go:50), returned as an ordinary error.
// Feeding that error back as a provider diagnostic is precisely how the real
// plugin client reports it, and it is what both call sites then misread.
//
// So these tests fail on unfixed code the way production does, not the way a
// mock's assertion helper does.

// refuseMarkedOnTheWire is the plugin channel's own refusal, reproduced.
// internal/plugin/grpc_provider.go marshals each cty.Value of a request with
// msgpack.Marshal(v, ty) before it leaves the process; a marked value never
// gets past that line. A nil value is not an error - the zero cty.Value is
// what several optional request fields legitimately carry.
func refuseMarkedOnTheWire(field string, v cty.Value, ty cty.Type) error {
	if v == cty.NilVal {
		return nil
	}
	if _, err := msgpack.Marshal(v, ty); err != nil {
		return fmt.Errorf("%s could not be sent to the provider: %w", field, err)
	}
	return nil
}

// stubDBSchema is [testdata/plan-sensitive]'s type: an argument the
// configuration states, an argument it takes from a sensitive variable, and a
// computed attribute the provider derives from the first one.
//
// `password` is deliberately NOT Sensitive in the schema. The mark under test
// has to come from the input variable and from nowhere else, or the test
// would pass for a fix that only handled schema sensitivity - which
// normalizeIdentityAttrs already handles and this call site does not.
func stubDBSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":       {Type: cty.String, Computed: true},
			"name":     {Type: cty.String, Required: true},
			"password": {Type: cty.String, Optional: true},
			"derived":  {Type: cty.String, Computed: true},
		},
	}}
}

// marshallingDBStub answers PlanResourceChange the way a real plugin client
// would: it refuses anything that cannot cross the wire, and otherwise
// derives `derived` from `name`.
func marshallingDBStub() *planStub {
	schema := stubDBSchema()
	ty := schema.Block.ImpliedType()
	return &planStub{
		schemas: map[string]providers.Schema{"stub_db": schema},
		plan: func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
			var resp providers.PlanResourceChangeResponse
			for _, f := range []struct {
				name string
				val  cty.Value
			}{
				{"Config", req.Config},
				{"PriorState", req.PriorState},
				{"ProposedNewState", req.ProposedNewState},
			} {
				if err := refuseMarkedOnTheWire(f.name, f.val, ty); err != nil {
					resp.Diagnostics = resp.Diagnostics.Append(err)
					return resp
				}
			}
			obj := req.ProposedNewState.AsValueMap()
			obj["derived"] = cty.StringVal("arn:" + obj["name"].AsString())
			obj["id"] = cty.StringVal("db-1")
			return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(obj)}
		},
	}
}

// TestPlanInstancesSurvivesASensitiveVariable is the reproduction.
//
// The estate declares `variable "db_password" { sensitive = true }` and a
// resource that reads it. internal/configs/static_scope.go marks the
// variable's value on the way into the static evaluation context, and
// StaticEvaluator.DecodeBlock has no guard that refuses a marked value the
// way its DecodeExpression sibling does - so planOne's configVal arrives
// marked, the msgpack encoder refuses it, and the refusal comes back as an
// ordinary provider diagnostic.
//
// planOne reads ANY provider diagnostic as "the provider declined" and drops
// the resource, and [PlanInstances] never fails its caller. So the whole
// resource vanishes from the second resolution pass's ManagedResults with one
// TRACE line as the only evidence, every instance whose identity depends on it
// then refuses to resolve, and `live-plan` proposes CREATING resources that
// already exist. That is the wrong-marker-outranks-a-missing-one case stated
// in HANDOFF.md, reached without a single wrong value being computed.
//
// Verified to reproduce: with planOne's UnmarkDeepWithPaths reverted, this
// test fails at the "absent" check below, and TestPlanInstancesReturnsWhatThe
// ProviderDerives and the rest of plan_test.go stay green - which is the
// reason this went unnoticed.
func TestPlanInstancesSurvivesASensitiveVariable(t *testing.T) {
	cfg := loadConfig(t, "testdata/plan-sensitive")
	stub := marshallingDBStub()

	got, diags := PlanInstances(context.Background(), cfg, anyProvider(stub))
	if diags.HasErrors() {
		t.Fatalf("PlanInstances: %s", diags.Err())
	}

	val, ok := got["stub_db.main"]
	if !ok {
		t.Fatalf("stub_db.main is absent from the planned values (got %v). A resource whose configuration "+
			"reads a sensitive variable decodes to a marked value; the provider channel refuses to serialize one "+
			"and the refusal reads here as \"the provider declined\", so every identity derived from this block "+
			"refuses and live-plan proposes creating a resource that already exists", keysOf(got))
	}

	// The value the caller actually wanted, and the whole reason the resource
	// must not drop out: it is derived from a non-sensitive argument and
	// nothing about it is secret.
	derived := val.GetAttr("derived")
	if derived.IsMarked() {
		t.Fatal("derived came back marked; it is computed from `name`, which is not sensitive, " +
			"and marking it would refuse an identity component that is perfectly safe to render")
	}
	if derived.AsString() != "arn:app-db" {
		t.Errorf("derived = %q, want %q", derived.AsString(), "arn:app-db")
	}

	// And the marks are back on the ANSWER. Dropping them would turn this
	// silent omission into a silent leak: internal/live/identity refuses a
	// marked value as an identity component precisely because a component
	// becomes a cloud tag in plaintext, and an unmarked password would sail
	// through that refusal.
	if !val.GetAttr("password").IsMarked() {
		t.Error("password came back unmarked. The unmark is for the question put to the provider, " +
			"not for the answer handed to identity resolution - an identity component becomes a cloud " +
			"tag in plaintext, which is why internal/live/identity refuses a marked value")
	}
}

// TestPlanOneAsksTheProviderWithAnUnmarkedValue is the property behind the
// reproduction above, asserted directly at the seam, in the same shape
// TestNormalizeIdentityAttrsAsksTheProviderWithAnUnmarkedValue asserts it one
// call site over.
//
// It is worth having beside the reproduction rather than instead of it: the
// reproduction says the resource survives, and this says WHY - nothing marked
// was ever put to the provider. A future change that made the failure quiet
// some other way (a stub that tolerates marks, a provider that answers
// anyway) would leave the reproduction green and turn this red.
func TestPlanOneAsksTheProviderWithAnUnmarkedValue(t *testing.T) {
	cfg := loadConfig(t, "testdata/plan-sensitive")

	asked := 0
	stub := &planStub{
		schemas: map[string]providers.Schema{"stub_db": stubDBSchema()},
		plan: func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
			asked++
			for name, v := range map[string]cty.Value{
				"Config":           req.Config,
				"ProposedNewState": req.ProposedNewState,
				"PriorState":       req.PriorState,
			} {
				if v != cty.NilVal && v.ContainsMarked() {
					t.Errorf("%s put to the provider contains a mark; cty's msgpack encoder refuses it "+
						"and planOne reads the refusal as \"the provider declined\"", name)
				}
			}
			obj := req.ProposedNewState.AsValueMap()
			obj["derived"] = cty.StringVal("arn:" + obj["name"].AsString())
			obj["id"] = cty.StringVal("db-1")
			return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(obj)}
		},
	}

	if _, diags := PlanInstances(context.Background(), cfg, anyProvider(stub)); diags.HasErrors() {
		t.Fatalf("PlanInstances: %s", diags.Err())
	}
	if asked == 0 {
		t.Fatal("the provider was never asked to plan, so this test asserted nothing")
	}
}

// stubBucketSchema is a taggable type in the AWS provider's own
// transparent-tagging shape, which is what [configuredTagsSeed] gates on:
// markers.Taggable (an optional map-of-string "tags") plus a "tags_all"
// attribute. No type name is involved - nearly every AWS type satisfies both,
// which is what makes the tags defect broad rather than exotic.
func stubBucketSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":       {Type: cty.String, Computed: true},
			"name":     {Type: cty.String, Required: true},
			"tags":     {Type: cty.Map(cty.String), Optional: true},
			"tags_all": {Type: cty.Map(cty.String), Computed: true},
		},
	}}
}

// TestConfiguredTagsSeedUnmarksASensitiveTagValue is the unit half: the seed
// this function returns must be something a provider can be told.
//
// Both shapes are checked because cty treats them differently and neither was
// caught. `tags = { Owner = var.owner }` leaves the mark on the map's ELEMENT
// - an object constructor does not hoist its elements' marks - while
// `tags = var.tags` marks the container. IsNull is indifferent to a mark, and
// cty's own IsWhollyKnown unmarks before it recurses (cty/value.go:83), so
// both cleared every guard the function had and travelled on into
// ReadResourceRequest.PriorState unaltered.
func TestConfiguredTagsSeedUnmarksASensitiveTagValue(t *testing.T) {
	cfg := loadConfig(t, "testdata/tags-sensitive")

	for _, name := range []string{"stub_bucket.main", "stub_bucket.whole"} {
		t.Run(name, func(t *testing.T) {
			rc := cfg.Module.ManagedResources[name]
			if rc == nil {
				t.Fatalf("fixture does not declare %s; it declares %v", name, keysOfResources(cfg))
			}

			seed, ok := configuredTagsSeed(context.Background(), cfg.Module.StaticEvaluator, cfg.Path, rc, stubBucketSchema())
			if !ok {
				t.Fatal("no tags seed was produced for a resource whose tags are entirely static. " +
					"Refusing to seed is not the fix here: the seed exists to tell the provider's own " +
					"ReadResource which raw tags the configuration declares, and without it every plan " +
					"re-hits GitHub issue #287's default_tags ambiguity")
			}
			if seed.ContainsMarked() {
				t.Fatal("the tags seed carries a mark. It goes straight into ReadResourceRequest.PriorState, " +
					"cty's msgpack encoder refuses a marked value, and importAndRead turns the refusal into a " +
					"\"Cannot read for projection\" ERROR - which fails BuildWith for the whole estate, not just " +
					"this resource")
			}
			owner := seed.AsValueMap()["Owner"]
			if owner == cty.NilVal || owner.IsNull() || owner.AsString() != "alice@example.com" {
				t.Errorf("the seeded Owner tag is %#v, want the configuration's own value in the clear - "+
					"which is what a persisted state file's PriorState.tags would have shown the provider too", owner)
			}
		})
	}
}

// TestProjectionSurvivesASensitiveTagValue is the reproduction, and its blast
// radius is the reason it is separate from the unit test above.
//
// [importAndRead] treats a ReadResource error as statusFailed AND appends it
// to b.diags, and an error in b.diags fails BuildWith. So one resource tagged
// with a value derived from a sensitive variable does not lose one resource -
// it refuses `live-plan` for the entire estate, which is the loudest failure
// of the three fixed here and the easiest to trigger: any estate that writes
// `tags = { Owner = var.owner }` for a sensitive variable, on any of the
// hundreds of AWS types carrying the tags/tags_all convention.
//
// Verified to reproduce: with configuredTagsSeed's UnmarkDeep reverted, this
// test fails on assertNoErrors with "Cannot read for projection".
func TestProjectionSurvivesASensitiveTagValue(t *testing.T) {
	cfg := loadConfig(t, "testdata/tags-sensitive")
	addr := mustAddr(t, `stub_bucket.main`)
	schema := stubBucketSchema()
	ty := schema.Block.ImpliedType()

	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_bucket": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"id":       cty.StringVal(r.Target.ID),
				"name":     cty.NullVal(cty.String),
				"tags":     cty.NullVal(cty.Map(cty.String)),
				"tags_all": cty.NullVal(cty.Map(cty.String)),
			}),
		}}}
	}
	sawSeed := false
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		var resp providers.ReadResourceResponse
		// The plugin client's own refusal, not an assertion helper: this is
		// the line the real failure comes off.
		if err := refuseMarkedOnTheWire("PriorState", r.PriorState, ty); err != nil {
			resp.Diagnostics = resp.Diagnostics.Append(err)
			return resp
		}
		if tags := r.PriorState.GetAttr("tags"); !tags.IsNull() && tags.LengthInt() > 0 {
			sawSeed = true
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"id":       cty.StringVal("bucket-1"),
			"name":     cty.StringVal("app-bucket"),
			"tags":     cty.MapVal(map[string]cty.Value{"Owner": cty.StringVal("alice@example.com")}),
			"tags_all": cty.MapVal(map[string]cty.Value{"Owner": cty.StringVal("alice@example.com")}),
		})}
	}

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "app-bucket", IdentityValues: map[string]string{"name": "app-bucket"}},
	}, SingleProvider(provAddr, p))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_bucket.main`})

	if !sawSeed {
		t.Error("the provider's ReadResource never saw the configuration's own tags in PriorState, " +
			"so GitHub issue #287 item 8's default_tags signal is gone and this test would pass for a " +
			"fix that simply stopped seeding")
	}
}

// keysOfResources names what a fixture declared, for a failure message that
// says what was there instead of only what was not.
func keysOfResources(cfg *configs.Config) []string {
	out := make([]string, 0, len(cfg.Module.ManagedResources))
	for k := range cfg.Module.ManagedResources {
		out = append(out, k)
	}
	return out
}
