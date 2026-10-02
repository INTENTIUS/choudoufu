// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// identityRemovedProvider is a plugin-framework resource with an identity
// schema, imported by ID with a null identity, whose Read finds nothing:
// the provider removes the resource and terraform-plugin-framework v1.16.1
// then reports "Missing Resource Identity After Read" over the null state
// (hashicorp/kubernetes 3.3.0's kubernetes_namespace_v1, 2026-10-02).
// liveState, when not NilVal, is returned instead, for the control.
func identityRemovedProvider(liveState cty.Value) *MockProvider {
	p := simpleMockProvider()
	p.GetProviderSchemaResponse = &providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{
			"test_instance": {
				Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
					"id": {Type: cty.String, Computed: true},
				}},
				IdentitySchema: &configschema.Object{
					Nesting: configschema.NestingSingle,
					Attributes: map[string]*configschema.Attribute{
						"kind": {Type: cty.String, Required: true},
						"name": {Type: cty.String, Required: true},
					},
				},
				IdentitySchemaVersion: 1,
			},
		},
	}
	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: "test_instance",
			State:    cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal(req.Target.ID)}),
		}}}
	}
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		var resp providers.ReadResourceResponse
		resp.NewState = cty.NullVal(cty.Object(map[string]cty.Type{"id": cty.String}))
		if liveState != cty.NilVal {
			resp.NewState = liveState
		}
		resp.Diagnostics = resp.Diagnostics.Append(tfdiags.Sourceless(tfdiags.Error, "Missing Resource Identity After Read",
			"The Terraform Provider unexpectedly returned no resource identity data after having no errors in the resource read."))
		return resp
	}
	return p
}

func planImportOfMissing(t *testing.T, p *MockProvider) tfdiags.Diagnostics {
	t.Helper()
	m := testModuleInline(t, map[string]string{"main.tf": `
resource "test_instance" "a" {}

import {
  to = test_instance.a
  id = "nope"
}
`})
	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		}, nil),
	})
	_, diags := ctx.Plan(t.Context(), m, states.NewState(), DefaultPlanOpts)
	return diags
}

// TestPlanImportOfMissingFrameworkObjectSaysItDoesNotExist: the read
// removed the object, so the import reports what it reports for every
// other provider's missing object, which is also the summary a live plan's
// resolver-supplied import folds into absence.
func TestPlanImportOfMissingFrameworkObjectSaysItDoesNotExist(t *testing.T) {
	diags := planImportOfMissing(t, identityRemovedProvider(cty.NilVal))
	if !diags.HasErrors() {
		t.Fatal("an import of a missing object planned without error")
	}
	msg := diags.Err().Error()
	if strings.Contains(msg, "Missing Resource Identity After Read") {
		t.Errorf("the framework's identity complaint over a removed object surfaced as the failure:\n%s", msg)
	}
	if !strings.Contains(msg, "Cannot import non-existent remote object") {
		t.Errorf("want the ordinary missing-object refusal, got:\n%s", msg)
	}
}

// TestPlanImportFrameworkIdentityComplaintOverALiveObjectStillFails is the
// control: the same complaint over a non-null state is a provider
// returning an object with no identity, and still fails as it did.
func TestPlanImportFrameworkIdentityComplaintOverALiveObjectStillFails(t *testing.T) {
	diags := planImportOfMissing(t, identityRemovedProvider(cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("nope")})))
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), "Missing Resource Identity After Read") {
		t.Errorf("want the framework's identity complaint to stand over a live object, got: %v", diags.Err())
	}
}
