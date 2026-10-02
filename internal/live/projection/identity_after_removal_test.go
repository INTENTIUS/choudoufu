// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is the second gap hashicorp/kubernetes 3.3.0 opened in
// corpus-quickpizza (2026-10-02), at greenfield: kubernetes_namespace_v1
// is now a plugin-framework resource with an identity schema, imported by
// ID through resource.ImportStatePassthroughWithIdentity, which sets the
// state's id and leaves the identity null. When the namespace does not
// exist, the provider's Read calls RemoveResource and returns, and
// terraform-plugin-framework v1.16.1 (fwserver/server_readresource.go)
// then fails the read with "Missing Resource Identity After Read" because
// the identity is still null - it does not exempt a removed resource.
// Stock tofu fails the same way on an import block for a missing
// namespace; a projection imports by ID on every plan, so for it this is
// the ordinary "nothing there yet" answer every greenfield create needs.

func identityAfterRemovalSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"name": {Type: cty.String, Optional: true},
		}},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"api_version": {Type: cty.String, Required: true},
				"kind":        {Type: cty.String, Required: true},
				"name":        {Type: cty.String, Required: true},
			},
		},
		IdentitySchemaVersion: 1,
	}
}

func identityAfterRemovalProvider(newState cty.Value, summary string) *tofu.MockProvider {
	ty := identityAfterRemovalSchema().Block.ImpliedType()
	p := &tofu.MockProvider{}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"id":   cty.StringVal(r.Target.ID),
				"name": cty.NullVal(cty.String),
			}),
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		if newState == cty.NilVal {
			newState = cty.NullVal(ty)
		}
		var resp providers.ReadResourceResponse
		resp.NewState = newState
		resp.Diagnostics = resp.Diagnostics.Append(tfdiags.Sourceless(tfdiags.Error, summary,
			"The Terraform Provider unexpectedly returned no resource identity data after having no errors in the resource read. This is always an issue in the Terraform Provider and should be reported to the provider developers."))
		return resp
	}
	return p
}

// TestIdentityCheckAfterRemovalIsAbsence: a null state with the
// framework's identity complaint is the provider answering "absent".
func TestIdentityCheckAfterRemovalIsAbsence(t *testing.T) {
	p := identityAfterRemovalProvider(cty.NilVal, "Missing Resource Identity After Read")
	target := providers.ImportTarget{ID: "quickpizza"}
	_, _, status, diags := importAndRead(t.Context(), p, identityAfterRemovalSchema(), "kubernetes_namespace_v1", target, target.ID, nil, nil, nil, nil)
	if diags.HasErrors() {
		t.Fatalf("a removed resource's identity complaint was reported as a failure: %s", diags.Err())
	}
	if status != statusAbsent {
		t.Fatalf("status = %v, want statusAbsent", status)
	}
	warned := false
	for _, d := range diags {
		if d.Severity() == tfdiags.Warning && d.Description().Summary == SummaryRemovedWithoutIdentity {
			warned = true
		}
	}
	if !warned {
		t.Errorf("absence was taken silently; want a %q warning naming what was set aside", SummaryRemovedWithoutIdentity)
	}
}

// TestIdentityCheckOnALiveObjectStillFails is the control on each side:
// the same complaint over a non-null state is a provider genuinely
// returning an object with no identity, and a different error over a null
// state is a read that failed. Both stay failures.
func TestIdentityCheckOnALiveObjectStillFails(t *testing.T) {
	live := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("quickpizza"), "name": cty.StringVal("quickpizza")})
	for name, p := range map[string]*tofu.MockProvider{
		"identity complaint over a live object": identityAfterRemovalProvider(live, "Missing Resource Identity After Read"),
		"another error over a null state":       identityAfterRemovalProvider(cty.NilVal, "Failed to read namespace"),
	} {
		t.Run(name, func(t *testing.T) {
			target := providers.ImportTarget{ID: "quickpizza"}
			_, _, status, diags := importAndRead(t.Context(), p, identityAfterRemovalSchema(), "kubernetes_namespace_v1", target, target.ID, nil, nil, nil, nil)
			if !diags.HasErrors() || status != statusFailed {
				t.Errorf("status = %v, errors = %v; want statusFailed with errors", status, diags.HasErrors())
			}
		})
	}
}
