// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// configArgsSchema is a field-granular schema in hashicorp/kubernetes
// 3.2.1's shape: field_manager and force Optional (the SDK's Default is
// invisible over the protocol), a metadata block of name and namespace,
// and the written fields.
func configArgsSchema(env bool) providers.Schema {
	attrs := map[string]*configschema.Attribute{
		"id":            {Type: cty.String, Optional: true, Computed: true},
		"api_version":   {Type: cty.String, Required: true},
		"kind":          {Type: cty.String, Required: true},
		"field_manager": {Type: cty.String, Optional: true},
		"force":         {Type: cty.Bool, Optional: true},
	}
	blocks := map[string]*configschema.NestedBlock{
		"metadata": {Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1, Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
			"name":      {Type: cty.String, Required: true},
			"namespace": {Type: cty.String, Optional: true},
		}}},
	}
	if env {
		attrs["container"] = &configschema.Attribute{Type: cty.String, Optional: true}
		attrs["init_container"] = &configschema.Attribute{Type: cty.String, Optional: true}
		blocks["env"] = &configschema.NestedBlock{Nesting: configschema.NestingList, MinItems: 1, Block: configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"name":  {Type: cty.String, Required: true},
				"value": {Type: cty.String, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"value_from": {Nesting: configschema.NestingList, MaxItems: 1, Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"field_path": {Type: cty.String, Optional: true},
				}}},
			},
		}}
	} else {
		attrs["labels"] = &configschema.Attribute{Type: cty.Map(cty.String), Required: true}
	}
	return providers.Schema{Block: &configschema.Block{Attributes: attrs, BlockTypes: blocks}}
}

// configArgsProvider mimics the reads of hashicorp/kubernetes 3.2.1's
// field-granular types against one live object each:
//
//   - stub_labels has no Importer (ImportResourceState answers "doesn't
//     support import"), and its Read keeps the live labels the prior's
//     field_manager owns plus the keys the prior names. field_manager and
//     force are never read back: the prior's value is what comes out.
//   - stub_env has ImportStatePassthroughContext (a stub of the id alone),
//     and its Read finds the container by the prior's container argument,
//     erroring `could not find container with name ""` without one, and
//     keeps only the env vars the prior's env names (getManagedEnvs returns
//     nil on the first entry it matches, so no var is ever "managed"). The
//     metadata block is never set by the Read.
func configArgsProvider(t *testing.T, labelOwner string) (*tofu.MockProvider, *[]cty.Value) {
	t.Helper()
	labelsSchema, envSchema := configArgsSchema(false), configArgsSchema(true)
	p := &tofu.MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
		Provider:      providers.Schema{Block: &configschema.Block{}},
		ResourceTypes: map[string]providers.Schema{"stub_labels": labelsSchema, "stub_env": envSchema},
	}}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var resp providers.ImportResourceStateResponse
		if r.TypeName != "stub_env" {
			resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("resource %s doesn't support import", r.TypeName))
			return resp
		}
		stub := envSchema.Block.EmptyValue().AsValueMap()
		stub["id"] = cty.StringVal(r.Target.ID)
		resp.ImportedResources = []providers.ImportedResource{{TypeName: r.TypeName, State: cty.ObjectVal(stub)}}
		return resp
	}
	var priors []cty.Value
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		var resp providers.ReadResourceResponse
		priors = append(priors, r.PriorState)
		out := r.PriorState.AsValueMap()
		mgr := ""
		if v := r.PriorState.GetAttr("field_manager"); !v.IsNull() {
			mgr = v.AsString()
		}
		switch r.TypeName {
		case "stub_labels":
			live := map[string]cty.Value{}
			if mgr == labelOwner {
				live["app.shared/team"] = cty.StringVal("app")
			}
			if pl := r.PriorState.GetAttr("labels"); !pl.IsNull() {
				for k := range pl.AsValueMap() {
					if k == "app.shared/team" {
						live[k] = cty.StringVal("app")
					}
				}
			}
			out["labels"] = cty.MapValEmpty(cty.String)
			if len(live) > 0 {
				out["labels"] = cty.MapVal(live)
			}
		case "stub_env":
			c := r.PriorState.GetAttr("container")
			if c.IsNull() || c.AsString() != "web" {
				name := ""
				if !c.IsNull() {
					name = c.AsString()
				}
				resp.Diagnostics = resp.Diagnostics.Append(tfdiags.Sourceless(tfdiags.Error, fmt.Sprintf("could not find container with name %q", name), ""))
				return resp
			}
			ety := envSchema.Block.BlockTypes["env"].Block.ImpliedType()
			var kept []cty.Value
			if pe := r.PriorState.GetAttr("env"); !pe.IsNull() {
				for _, e := range pe.AsValueSlice() {
					if e.GetAttr("name").AsString() == "APP_MODE" {
						el := envSchema.Block.BlockTypes["env"].Block.EmptyValue().AsValueMap()
						el["name"] = cty.StringVal("APP_MODE")
						el["value"] = cty.StringVal("shared")
						kept = append(kept, cty.ObjectVal(el))
					}
				}
			}
			out["env"] = cty.ListValEmpty(ety)
			if len(kept) > 0 {
				out["env"] = cty.ListVal(kept)
			}
		}
		resp.NewState = cty.ObjectVal(out)
		return resp
	}
	return p, &priors
}

// TestFieldGranularReadsCarryTheConfigOnlyArguments is reference-k8s-
// shared-objects' test_plan failure (#1885): after a migration off stock
// state files, app's plan with no state file proposed field_manager "Terraform" ->
// "choudoufu:shared-app" on every field-granular block, and kubernetes_env
// failed its read with `could not find container with name ""`.
//
// field_manager: the read is made under the estate's manager, and
// hashicorp/kubernetes never reads the manager back, so the read's answer
// is the seed. The residue record live-import wrote from the stock state
// holds "Terraform", and the residue fill took the echoed seed for "no
// information" and put the stock manager back over it.
//
// kubernetes_env: the import stub is the id alone. The read needs the
// container and the env names from the prior, and the plan needs the
// metadata block the read never sets, or it proposes a new object.
func TestFieldGranularReadsCarryTheConfigOnlyArguments(t *testing.T) {
	const estate, manager = "app", "choudoufu:app"
	cfg := loadConfig(t, "testdata/fieldgranular-config-args")
	labels := mustAddr(t, `stub_labels.ns`)
	env := mustAddr(t, `stub_env.web`)
	ctx := context.Background()

	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("fieldgranular-config-args"))
	// What live-import records off a stock state file: the stock manager
	// as residue (the provider never reads it back, so the classifier
	// keeps it), and the hand-over evidence.
	for _, a := range []addrs.AbsResourceInstance{labels, env} {
		rf, err := encodeResidueFields(map[string]cty.Value{
			"field_manager": cty.StringVal("Terraform"),
			"force":         cty.True,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.mergeEnvelope(ctx, a, "", func(e *recordEnvelope) { e.Residue = rf }); err != nil {
			t.Fatal(err)
		}
	}

	p, priors := configArgsProvider(t, manager)
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	res, diags := BuildWith(ctx, cfg, []identity.Resolution{
		{Addr: labels, Class: identity.ClassConcrete, ImportID: "apiVersion=v1,kind=Namespace,name=shared",
			IdentityValues: map[string]string{"api_version": "v1", "kind": "Namespace", "name": "shared"}},
		{Addr: env, Class: identity.ClassConcrete, ImportID: "apiVersion=apps/v1,kind=Deployment,namespace=shared,name=web",
			IdentityValues: map[string]string{"api_version": "apps/v1", "kind": "Deployment", "namespace": "shared", "name": "web"}},
	}, SingleProvider(provAddr, p), Options{RecordStore: store, Ownership: &Ownership{Estate: estate}})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_env.web`, `stub_labels.ns`})

	for _, prior := range *priors {
		if fm := prior.GetAttr("field_manager"); fm.IsNull() || fm.AsString() != manager {
			t.Errorf("a read was made with field_manager %#v; want %q, the estate's manager", fm, manager)
		}
	}

	projected := func(a addrs.AbsResourceInstance) string {
		is := res.State.ResourceInstance(a)
		if is == nil || is.Current == nil {
			t.Fatalf("%s is not in the projection", a)
		}
		return string(is.Current.AttrsJSON)
	}
	for _, c := range []struct {
		addr addrs.AbsResourceInstance
		want []string
	}{
		{labels, []string{`"field_manager":"choudoufu:app"`, `"force":true`, `"app.shared/team":"app"`}},
		{env, []string{
			`"field_manager":"choudoufu:app"`,
			`"container":"web"`,
			`"api_version":"apps/v1"`,
			`"kind":"Deployment"`,
			`"metadata":[{"name":"web","namespace":"shared"}]`,
			`"name":"APP_MODE"`,
		}},
	} {
		got := projected(c.addr)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s's projected prior lacks %s - the plan would propose it as a change:\n%s", c.addr, w, got)
			}
		}
	}
}
