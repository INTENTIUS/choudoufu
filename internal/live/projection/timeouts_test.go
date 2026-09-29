// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	legacyschema "github.com/intentius/choudoufu/internal/legacy/helper/schema"
	legacytofu "github.com/intentius/choudoufu/internal/legacy/tofu"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file's tests are GitHub issue #1185's. See [configuredTimeouts]'s
// own doc comment for the mechanism and for the kind-cluster measurement
// that found it; what is asserted here is the same fact the cluster showed,
// against helper/schema's own encoder and decoder rather than against this
// package's idea of them.
//
// The external source throughout is [legacyschema.ResourceTimeout], the
// in-tree copy of helper/schema's timeout encoding. Every private blob a
// stub provider hands out below is produced by its StateEncode, and every
// assertion about what the provider would have read back goes through its
// StateDecode. Nothing here compares this package's output against this
// package's own notion of the format.

// stubNamespaceSchema mirrors what helper/schema's coreConfigSchema renders
// for a resource declaring Timeouts: a NestingSingle "timeouts" block whose
// attributes are exactly the ResourceTimeout fields the resource sets
// non-nil. kubernetes_namespace - #1185's own subject - declares create and
// delete, so those are the two here.
func stubNamespaceSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"name": {Type: cty.String, Required: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"timeouts": {
				Nesting: configschema.NestingSingle,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"create": {Type: cty.String, Optional: true},
					"delete": {Type: cty.String, Optional: true},
				}},
			},
		},
	}}
}

// declaredDefaultPrivate is the private blob an SDKv2 provider hands back
// for an IMPORTED instance: helper/schema seeds the instance's meta from the
// resource's own declared Timeouts and marshals it, with nothing of the
// operator's configuration in it. Built through
// [legacyschema.ResourceTimeout.StateEncode] so the shape is the SDK's, not
// this test's, and carrying the "schema_version" key a real one also has
// (read off a live kind run: the projected kubernetes_namespace's private
// was {"e2bfb730-...":{"delete":300000000000},"schema_version":"0"}).
func declaredDefaultPrivate(t *testing.T) []byte {
	t.Helper()
	rt := &legacyschema.ResourceTimeout{
		Create: legacyschema.DefaultTimeout(5 * time.Minute),
		Delete: legacyschema.DefaultTimeout(5 * time.Minute),
	}
	is := &legacytofu.InstanceState{}
	if err := rt.StateEncode(is); err != nil {
		t.Fatalf("encoding the declared defaults: %s", err)
	}
	is.Meta["schema_version"] = "0"
	b, err := json.Marshal(is.Meta)
	if err != nil {
		t.Fatalf("marshalling the declared defaults: %s", err)
	}
	return b
}

// decodePrivateTimeouts reads a private blob back the way helper/schema's
// own Resource.Apply does on a destroy: json into the instance meta, then
// [legacyschema.ResourceTimeout.StateDecode].
func decodePrivateTimeouts(t *testing.T, private []byte) legacyschema.ResourceTimeout {
	t.Helper()
	if len(private) == 0 {
		t.Fatal("the projected object carries no private blob at all; a destroy would then get helper/schema's own 20-minute system default")
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(private, &meta); err != nil {
		t.Fatalf("the private blob is not the JSON object helper/schema writes: %s (%s)", err, private)
	}
	var rt legacyschema.ResourceTimeout
	if err := rt.StateDecode(&legacytofu.InstanceState{Meta: meta}); err != nil {
		t.Fatalf("helper/schema could not decode the private blob this package produced: %s (%s)", err, private)
	}
	return rt
}

func mustDuration(t *testing.T, name string, d *time.Duration) time.Duration {
	t.Helper()
	if d == nil {
		t.Fatalf("no %s timeout in the decoded meta at all", name)
	}
	return *d
}

// TestSdkv2TimeoutMetaKeyIsHelperSchemasOwn keeps this package's one magic
// constant tied to the source it was copied from. A typo here would make
// [withConfiguredTimeouts] write a key nothing reads, and every other test
// in this file - which would be writing and reading the same wrong key -
// would still pass.
func TestSdkv2TimeoutMetaKeyIsHelperSchemasOwn(t *testing.T) {
	if sdkv2TimeoutMetaKey != legacyschema.TimeoutKey {
		t.Errorf("sdkv2TimeoutMetaKey = %q, but helper/schema's TimeoutKey is %q; a destroy reads the meta under the SDK's key and nothing else",
			sdkv2TimeoutMetaKey, legacyschema.TimeoutKey)
	}
	if timeoutsBlockName != legacyschema.TimeoutsConfigKey {
		t.Errorf("timeoutsBlockName = %q, but helper/schema names the configuration block %q", timeoutsBlockName, legacyschema.TimeoutsConfigKey)
	}
}

// TestConfiguredTimeoutsDecodesTheBlock is the unit half. The value comes
// through a variable, so a decode that never reached the static evaluator
// would produce nothing.
func TestConfiguredTimeoutsDecodesTheBlock(t *testing.T) {
	cfg := loadConfig(t, "testdata/timeouts")
	schema := stubNamespaceSchema()

	t.Run("configured", func(t *testing.T) {
		rc := cfg.Module.ManagedResources["stub_ns.held"]
		if rc == nil {
			t.Fatalf("fixture does not declare stub_ns.held; it declares %v", keysOfResources(cfg))
		}
		got := configuredTimeouts(context.Background(), cfg.Module.StaticEvaluator, cfg.Path, rc, schema)
		want := map[string]int64{"delete": int64(20 * time.Second)}
		if len(got) != 1 || got["delete"] != want["delete"] {
			t.Errorf("configuredTimeouts = %v, want %v: `delete = var.hold` with hold defaulting to \"20s\"", got, want)
		}
		if _, ok := got["create"]; ok {
			t.Error("a create timeout was invented; the block declares only delete, and a key the configuration " +
				"does not name must keep whatever the provider's own declared default put there")
		}
	})

	t.Run("no block", func(t *testing.T) {
		rc := cfg.Module.ManagedResources["stub_ns.plain"]
		if rc == nil {
			t.Fatalf("fixture does not declare stub_ns.plain; it declares %v", keysOfResources(cfg))
		}
		if got := configuredTimeouts(context.Background(), cfg.Module.StaticEvaluator, cfg.Path, rc, schema); got != nil {
			t.Errorf("configuredTimeouts = %v for a resource with no timeouts block, want nil", got)
		}
	})

	t.Run("not a duration", func(t *testing.T) {
		rc := cfg.Module.ManagedResources["stub_ns.unparseable"]
		if rc == nil {
			t.Fatalf("fixture does not declare stub_ns.unparseable; it declares %v", keysOfResources(cfg))
		}
		if got := configuredTimeouts(context.Background(), cfg.Module.StaticEvaluator, cfg.Path, rc, schema); got != nil {
			t.Errorf("configuredTimeouts = %v for `delete = \"not-a-duration\"`, want nil: guessing a number here "+
				"would hand the provider a deadline no configuration asked for", got)
		}
	})

	t.Run("no timeouts block in the schema", func(t *testing.T) {
		// aws_launch_configuration's shape: no timeouts anywhere in the
		// provider's schema. The configuration cannot have declared one,
		// and nothing must be decoded from a block that does not exist.
		rc := cfg.Module.ManagedResources["stub_ns.held"]
		if got := configuredTimeouts(context.Background(), cfg.Module.StaticEvaluator, cfg.Path, rc, stubLaunchConfigSchema()); got != nil {
			t.Errorf("configuredTimeouts = %v against a schema with no timeouts block, want nil", got)
		}
	})
}

// TestWithConfiguredTimeoutsOverwritesOnlyTheConfiguredKeys pins the merge
// against what helper/schema's own ConfigDecode does on the plan path: start
// from the resource's declared defaults, overwrite per configured key, leave
// everything else - including private data that is not a timeout at all -
// exactly as it was.
func TestWithConfiguredTimeoutsOverwritesOnlyTheConfiguredKeys(t *testing.T) {
	private := declaredDefaultPrivate(t)

	got, changed := withConfiguredTimeouts(private, map[string]int64{"delete": int64(20 * time.Second)})
	if !changed {
		t.Fatal("withConfiguredTimeouts reported no change over a declared default of 5m and a configured delete of 20s")
	}
	rt := decodePrivateTimeouts(t, got)
	if d := mustDuration(t, "delete", rt.Delete); d != 20*time.Second {
		t.Errorf("delete = %s, want 20s", d)
	}
	if d := mustDuration(t, "create", rt.Create); d != 5*time.Minute {
		t.Errorf("create = %s, want the provider's own declared 5m: the configuration says nothing about create, "+
			"so nothing here may move it", d)
	}

	var meta map[string]json.RawMessage
	if err := json.Unmarshal(got, &meta); err != nil {
		t.Fatalf("result is not a JSON object: %s", err)
	}
	if string(meta["schema_version"]) != `"0"` {
		t.Errorf("schema_version = %s, want \"0\": private data that is not a timeout must survive untouched", meta["schema_version"])
	}

	if _, changed := withConfiguredTimeouts(private, map[string]int64{"delete": int64(5 * time.Minute)}); changed {
		t.Error("withConfiguredTimeouts reported a change for a configured value identical to the declared default")
	}
	if _, changed := withConfiguredTimeouts(private, nil); changed {
		t.Error("withConfiguredTimeouts reported a change with nothing configured")
	}
}

// TestWithConfiguredTimeoutsLeavesANonSdkPrivateAlone is the gate, and
// every shape that has to reach it.
//
// A terraform-plugin-framework resource's private state is a
// map[string][]byte the framework unmarshals strictly; writing an object
// under a new key would make it fail to load its own private state. The
// same applies to a provider with no private data at all.
//
// "null meta" is the case this test was written around after review: a JSON
// null under the SDK's own key unmarshals into a nil map with NO error, and
// writing into that nil map is a panic - "assignment to entry in nil map"
// on the projection build path, which takes down the whole plan for any
// resource carrying a timeouts block. It is also the case that decides the
// ruling: the null is LEFT ALONE rather than populated, because
// helper/schema's metaEncode writes the key only when it has a duration to
// put under it, so a null there is not an SDKv2 meta missing its values -
// it is somebody else's private, and re-deriving one would be inventing it.
//
// "half-readable meta" is the case the error half of the gate exists for
// and the nil half cannot see: [json.Unmarshal] leaves the map NON-nil with
// a partial entry when it fails on a value, so only the returned error
// tells that apart from a good decode.
func TestWithConfiguredTimeoutsLeavesANonSdkPrivateAlone(t *testing.T) {
	cfg := map[string]int64{"delete": int64(20 * time.Second)}
	for name, private := range map[string][]byte{
		"framework":          []byte(`{"framework_key":"YmFzZTY0"}`),
		"empty":              nil,
		"not json":           []byte("\x00\x01opaque"),
		"json array":         []byte(`[1,2,3]`),
		"null private":       []byte(`null`),
		"empty object":       []byte(`{}`),
		"null meta":          []byte(`{"e2bfb730-ecaa-11e6-8f88-34363bc7c4c0":null,"schema_version":"0"}`),
		"meta not an object": []byte(`{"e2bfb730-ecaa-11e6-8f88-34363bc7c4c0":"5m","schema_version":"0"}`),
		"half-readable meta": []byte(`{"e2bfb730-ecaa-11e6-8f88-34363bc7c4c0":{"delete":"twenty"},"schema_version":"0"}`),
	} {
		t.Run(name, func(t *testing.T) {
			// A panic here is the defect this test exists for, not a test
			// harness problem: it happens inside the projection build.
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("withConfiguredTimeouts panicked on %s: %v.\n"+
						"This runs on the projection build path for every live plan of a resource carrying a "+
						"timeouts block, so a panic here takes the whole plan down", private, rec)
				}
			}()
			got, changed := withConfiguredTimeouts(private, cfg)
			if changed {
				t.Errorf("withConfiguredTimeouts rewrote a private blob carrying no readable %s meta: %s", sdkv2TimeoutMetaKey, got)
			}
			if string(got) != string(private) {
				t.Errorf("private came back as %q, want %q byte for byte", got, private)
			}
		})
	}
}

// TestProjectionRederivesTheConfiguredDeleteTimeout is the reproduction, and
// the test that fails without the wiring in [builder.materialize].
//
// The stub provider is helper/schema's own behaviour for an imported
// instance, which is what makes this a reproduction rather than a
// restatement: ImportResourceState answers a bare identity with a private
// built from the RESOURCE'S declared defaults (there is no configuration for
// it to consult), and ReadResource hands the same private straight back.
// That is where a live plan's prior object comes from, and on a destroy
// helper/schema copies exactly that blob into PlannedPrivate and reads the
// delete deadline out of it.
//
// Measured against the real thing before this test was written: a
// kubernetes_namespace declaring `timeouts { delete = "20s" }`, held open by
// a finalizer on a ConfigMap inside it, on a kind cluster with
// hashicorp/kubernetes 3.2.1. Stock, state-backed: private delete
// 20000000000, `apply -destroy` 20s. The live path before this fix: private
// delete 300000000000, `apply -destroy` 311s. After: 20000000000 and 30s
// (20s of provider wait plus the projection's own reads).
func TestProjectionRederivesTheConfiguredDeleteTimeout(t *testing.T) {
	cfg := loadConfig(t, "testdata/timeouts")
	schema := stubNamespaceSchema()
	defaults := declaredDefaultPrivate(t)

	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_ns": schema},
		},
	}
	p.ConfigureProviderCalled = true
	stub := func(name string) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{
			"id":       cty.StringVal(name),
			"name":     cty.StringVal(name),
			"timeouts": cty.NullVal(cty.Object(map[string]cty.Type{"create": cty.String, "delete": cty.String})),
		})
	}
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State:    stub(r.Target.ID),
			Private:  defaults,
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		// helper/schema round-trips the meta it was handed; it never
		// consults the configuration on a read.
		return providers.ReadResourceResponse{
			NewState: r.PriorState,
			Private:  r.Private,
		}
	}

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `stub_ns.held`), Class: identity.ClassConcrete, ImportID: "held", IdentityValues: map[string]string{"name": "held"}},
		{Addr: mustAddr(t, `stub_ns.plain`), Class: identity.ClassConcrete, ImportID: "plain", IdentityValues: map[string]string{"name": "plain"}},
	}, SingleProvider(provAddr, p))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_ns.held`, `stub_ns.plain`})

	held := res.State.ResourceInstance(mustAddr(t, `stub_ns.held`))
	if held == nil || held.Current == nil {
		t.Fatal("no object recorded for stub_ns.held")
	}
	rt := decodePrivateTimeouts(t, held.Current.Private)
	if d := mustDuration(t, "delete", rt.Delete); d != 20*time.Second {
		t.Errorf("stub_ns.held's projected private carries delete = %s, want 20s.\n"+
			"This is the carrier a destroy reads its deadline from: planDestroy copies the prior object's Private "+
			"into PriorPrivate, helper/schema echoes it as PlannedPrivate for a null proposed state, and "+
			"Resource.Apply takes the delete deadline out of it. %s means the operator's `timeouts { delete = \"20s\" }` "+
			"was dropped and the provider's own declared default is what runs", d, d)
	}
	if d := mustDuration(t, "create", rt.Create); d != 5*time.Minute {
		t.Errorf("stub_ns.held's projected private carries create = %s, want the declared 5m", d)
	}

	// The value is not touched. A private write that also moved an attribute
	// would show up as a plan diff on every run, which is the shape of defect
	// #1177 and #1190 already are.
	if got := held.Current.AttrsJSON; !json.Valid(got) {
		t.Fatalf("stub_ns.held's attributes are not valid JSON: %s", got)
	} else {
		var attrs map[string]interface{}
		if err := json.Unmarshal(got, &attrs); err != nil {
			t.Fatal(err)
		}
		if attrs["timeouts"] != nil {
			t.Errorf("the projected VALUE gained a timeouts object (%v); this fix writes the private meta and "+
				"nothing else, and seeding the attribute is exactly what #1185's own refutation showed cannot "+
				"change a destroy's deadline anyway", attrs["timeouts"])
		}
	}

	plain := res.State.ResourceInstance(mustAddr(t, `stub_ns.plain`))
	if plain == nil || plain.Current == nil {
		t.Fatal("no object recorded for stub_ns.plain")
	}
	if string(plain.Current.Private) != string(defaults) {
		t.Errorf("stub_ns.plain's private = %s, want the provider's own answer %s byte for byte: the block declares "+
			"no timeouts, so there is nothing to re-derive", plain.Current.Private, defaults)
	}
}
