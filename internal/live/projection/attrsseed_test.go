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

// stubLaunchConfigSchema mirrors aws_launch_configuration's own shape (per
// hashicorp/aws's real source, fetched and confirmed against
// corpus-eks-basic's crossing): user_data_base64 is a plain Optional
// attribute, and the provider's Read only writes user_data_base64/user_data
// depending on which one PriorState already had set - GetOk-conditional
// logic no schema field exposes, exactly what [configuredAttrsSeed] exists
// to give a bare import stub a fighting chance at.
func stubLaunchConfigSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":                {Type: cty.String, Computed: true},
			"name":              {Type: cty.String, Required: true},
			"user_data":         {Type: cty.String, Optional: true, Computed: true},
			"user_data_base64":  {Type: cty.String, Optional: true},
			"enable_monitoring": {Type: cty.Bool, Optional: true, Computed: true},
			"tags":              {Type: cty.Map(cty.String), Optional: true},
		},
	}}
}

// TestConfiguredAttrsSeedSeedsStaticNonTagAttributes is the unit half: given
// testdata/attrs-seed, the seed must carry user_data_base64 for the
// resource that sets it statically, must NOT carry "tags" (configuredTagsSeed
// already owns that name), must NOT carry "id" (the identity attribute -
// seeding it would put this mechanism in a position to influence which
// object a plan binds to, which HANDOFF.md's safety rule reserves for the
// record and the marker alone), and must produce NOTHING at all for a
// resource whose only non-identity, non-tags argument reads a sibling
// managed resource's own Computed attribute - the same "leave it alone"
// answer [configuredTagsSeed] gives a non-static tags argument.
func TestConfiguredAttrsSeedSeedsStaticNonTagAttributes(t *testing.T) {
	cfg := loadConfig(t, "testdata/attrs-seed")
	schema := stubLaunchConfigSchema()

	t.Run("static", func(t *testing.T) {
		rc := cfg.Module.ManagedResources["stub_lc.main"]
		if rc == nil {
			t.Fatalf("fixture does not declare stub_lc.main; it declares %v", keysOfResources(cfg))
		}
		seed := configuredAttrsSeed(context.Background(), cfg.Module.StaticEvaluator, cfg.Path, rc, schema)
		if seed == nil {
			t.Fatal("no seed at all for a resource with a statically-set user_data_base64")
		}
		got, ok := seed["user_data_base64"]
		if !ok {
			t.Fatalf("user_data_base64 missing from the seed; got keys %v", seedKeys(seed))
		}
		want := "aGVsbG8gd29ybGQ=" // base64("hello world")
		if got.AsString() != want {
			t.Errorf("user_data_base64 = %q, want %q", got.AsString(), want)
		}
		if _, ok := seed["tags"]; ok {
			t.Error("\"tags\" is in the seed; configuredTagsSeed already owns that name, and seeding it " +
				"twice from two independent paths is exactly the drift a single mechanism prevents")
		}
		if _, ok := seed["id"]; ok {
			t.Error("\"id\" (the identity attribute) is in the seed; that would put this mechanism in a " +
				"position to influence which object a plan binds to")
		}
	})

	t.Run("dynamic", func(t *testing.T) {
		// Per-attribute isolation, [configuredAttrsSeed]'s own doc comment:
		// user_data_base64 here reads a sibling resource's own Computed
		// attribute, which the static evaluator cannot resolve - refusing
		// to seed THAT ONE is the same "leave it alone" choice
		// configuredTagsSeed makes for a non-static tags argument - but
		// "name", set statically on the very same resource, must still
		// seed: one dynamic argument must not blank out every other
		// attribute's seed.
		rc := cfg.Module.ManagedResources["stub_lc.dynamic"]
		if rc == nil {
			t.Fatalf("fixture does not declare stub_lc.dynamic; it declares %v", keysOfResources(cfg))
		}
		seed := configuredAttrsSeed(context.Background(), cfg.Module.StaticEvaluator, cfg.Path, rc, schema)
		if _, ok := seed["user_data_base64"]; ok {
			t.Errorf("seed = %v, want no user_data_base64: it reads a sibling resource's own Computed "+
				"attribute here, which the static evaluator cannot resolve", seedKeys(seed))
		}
		if got, ok := seed["name"]; !ok || got.AsString() != "dynamic-workers" {
			t.Errorf(`seed["name"] = %#v, ok=%v, want "dynamic-workers": a sibling attribute failing to `+
				"resolve statically must not blank out this one's own static value", got, ok)
		}
	})
}

func seedKeys(m map[string]cty.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestProjectionSurvivesTheLaunchConfigurationUserDataShape is the
// reproduction: a provider whose Read branches on GetOk("user_data_base64")
// - real hashicorp/aws code for aws_launch_configuration, confirmed via its
// published source - must see user_data_base64 in ReadResource's PriorState
// so it takes the branch a genuinely persisted state file would have taken,
// rather than falling back to hashing the raw UserData response into
// "user_data", which a real refresh (with real prior state) never produces
// and which then reads as a perpetual ForceNew replace on every later plan.
//
// Verified to reproduce: with configuredAttrsSeed wired out of materialize
// (attrsSeed left nil), this test fails - the stub read sets "user_data" to
// a non-null hash instead of leaving it null, because PriorState.user_data_base64
// arrives null and GetOk-style logic in the stub provider takes the "else"
// branch, exactly hashicorp/aws's own Read does against a bare import stub.
func TestProjectionSurvivesTheLaunchConfigurationUserDataShape(t *testing.T) {
	cfg := loadConfig(t, "testdata/attrs-seed")
	addr := mustAddr(t, `stub_lc.main`)
	schema := stubLaunchConfigSchema()

	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_lc": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"id":                cty.StringVal(r.Target.ID),
				"name":              cty.NullVal(cty.String),
				"user_data":         cty.NullVal(cty.String),
				"user_data_base64":  cty.NullVal(cty.String),
				"enable_monitoring": cty.NullVal(cty.Bool),
				"tags":              cty.NullVal(cty.Map(cty.String)),
			}),
		}}}
	}
	sawUserDataBase64 := false
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		// The exact hashicorp/aws shape (real source, fetched and quoted in
		// configuredAttrsSeed's own doc comment):
		//
		//	if _, ok := d.GetOk("user_data_base64"); ok {
		//	        d.Set("user_data_base64", v)
		//	} else {
		//	        d.Set("user_data", userDataHashSum(v))
		//	}
		//
		// modelled here as: PriorState.user_data_base64 present and
		// non-null means "GetOk succeeded", preserve it and leave
		// user_data null; PriorState.user_data_base64 null means "GetOk
		// failed", compute a non-null user_data stand-in instead - the
		// wrong-shaped answer a bare, unseeded stub produces.
		prior := r.PriorState
		userDataBase64 := prior.GetAttr("user_data_base64")
		var userData, base64Out cty.Value
		if !userDataBase64.IsNull() {
			sawUserDataBase64 = true
			base64Out = userDataBase64
			userData = cty.NullVal(cty.String)
		} else {
			base64Out = cty.NullVal(cty.String)
			userData = cty.StringVal("wrong-hash-artifact")
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"id":                cty.StringVal("lc-1"),
			"name":              cty.StringVal("workers"),
			"user_data":         userData,
			"user_data_base64":  base64Out,
			"enable_monitoring": cty.BoolVal(true),
			"tags":              cty.MapVal(map[string]cty.Value{"Owner": cty.StringVal("alice@example.com")}),
		})}
	}

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "lc-1", IdentityValues: map[string]string{"name": "workers"}},
	}, SingleProvider(provAddr, p))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_lc.main`})

	if !sawUserDataBase64 {
		t.Fatal("the provider's ReadResource never saw user_data_base64 in PriorState, so a real " +
			"GetOk-conditional provider takes the branch a bare, unseeded stub takes - not the branch " +
			"a genuinely persisted state file would have - and this test would pass for a fix that " +
			"simply stopped seeding")
	}

	is := res.State.ResourceInstance(addr)
	if is == nil || is.Current == nil {
		t.Fatalf("no object recorded for %s", addr)
	}
	attrsJSON := string(is.Current.AttrsJSON)
	if strings.Contains(attrsJSON, "wrong-hash-artifact") {
		t.Errorf("user_data carries the hash-shaped artifact (%s): a real refresh (real prior state "+
			"already carrying user_data_base64) never computes this, so a plan comparing it against the "+
			"configuration's own null desired value would show a perpetual, false ForceNew replace on "+
			"every run", attrsJSON)
	}
}
