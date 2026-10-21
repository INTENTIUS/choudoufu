// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// corpus-azapi-role-assignment's day2_rename, distilled: a `moved` block
// renames an azapi role assignment (Microsoft.Authorization/roleAssignments,
// which takes no tags at any API version), whose only ownership proof is the
// identity record an earlier apply wrote under the OLD address. Stock plans
// the move with no churn. Before the record-first read consulted moved
// aliases, the new address found no record, fell through to its declared id,
// found its own object there unrecorded, and refused it as an overwrite
// (#1546/#1120). The apply must also move the record, so the next plan finds
// it under the new address and nothing is left at the old one to read as a
// removed block's record once the moved block is deleted.

var azapiTestProvider = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewProvider(addrs.DefaultProviderRegistryHost, "azure", "azapi"),
}

const (
	movedRoleType   = "Microsoft.Authorization/roleAssignments@2022-04-01"
	movedRoleParent = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg"
	movedRoleName   = "6faae21a-0cd6-4536-8c23-a278823d12ed"
)

// azapiRecordSchema is azapiShapedSchema with azapi 2.13.0's identity schema,
// which is what makes write-back record the instance.
func azapiRecordSchema() providers.Schema {
	s := azapiShapedSchema()
	s.IdentitySchema = &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
		"id":   {Type: cty.String, Required: true},
		"type": {Type: cty.String, Optional: true},
	}}
	return s
}

func azapiRoleObject() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":        cty.StringVal(armRoleID),
		"type":      cty.StringVal(movedRoleType),
		"name":      cty.StringVal(movedRoleName),
		"parent_id": cty.StringVal(movedRoleParent),
		"body":      cty.EmptyObjectVal,
		"tags":      cty.MapValEmpty(cty.String),
	})
}

// azapiRoleProvider serves the one live role assignment at armRoleID.
func azapiRoleProvider(imported *[]string) providers.Interface {
	schema := azapiRecordSchema()
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"azapi_resource": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		id := r.Target.ID
		if r.Target.IsIdentityBased() {
			id = r.Target.Identity.GetAttr("id").AsString()
		}
		if imported != nil {
			*imported = append(*imported, id)
		}
		var resp providers.ImportResourceStateResponse
		if id != armRoleID {
			return resp
		}
		stub := cty.ObjectVal(map[string]cty.Value{
			"id":        cty.StringVal(id),
			"type":      cty.NullVal(cty.String),
			"name":      cty.NullVal(cty.String),
			"parent_id": cty.NullVal(cty.String),
			"body":      cty.NullVal(cty.DynamicPseudoType),
			"tags":      cty.NullVal(cty.Map(cty.String)),
		})
		resp.ImportedResources = []providers.ImportedResource{{TypeName: r.TypeName, State: stub}}
		return resp
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		if r.PriorState.GetAttr("id").AsString() != armRoleID {
			return providers.ReadResourceResponse{NewState: cty.NullVal(schema.Block.ImpliedType())}
		}
		return providers.ReadResourceResponse{NewState: azapiRoleObject()}
	}
	return p
}

func writeAzapiRoleFixture(t *testing.T, withMoved bool) string {
	t.Helper()
	src := `
terraform {
  required_providers {
    azapi = { source = "azure/azapi" }
  }
}
resource "azapi_resource" "role_assignment" {
  type      = "` + movedRoleType + `"
  parent_id = "` + movedRoleParent + `"
  name      = "` + movedRoleName + `"
}
`
	if withMoved {
		src += `
moved {
  from = azapi_resource.roleAssignments
  to   = azapi_resource.role_assignment
}
`
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRecordFirst_movedBlockCarriesAnUntaggableRecord(t *testing.T) {
	ctx := context.Background()
	const estate = k8sOwnershipEstate
	oldAddr := mustAddr(t, `azapi_resource.roleAssignments`)
	newAddr := mustAddr(t, `azapi_resource.role_assignment`)

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := NewRecordEnvelopeStore(raw, RecordKeyPrefix(estate))
	seed, ok := LocatedRecordFrom("azapi_resource", azapiRecordSchema(), azapiRoleObject())
	if !ok {
		t.Fatal("no record derivable for the role assignment")
	}
	if _, err := store.mergeEnvelope(ctx, oldAddr, "", func(env *recordEnvelope) {
		env.Identity = identityPayloadFrom(seed)
		env.Provider = providerString(azapiTestProvider)
	}); err != nil {
		t.Fatalf("seeding the record under the OLD address: %s", err)
	}

	plan := func(t *testing.T, withMoved bool) (*Result, []string) {
		t.Helper()
		var imported []string
		res, diags := BuildWith(ctx, loadConfig(t, writeAzapiRoleFixture(t, withMoved)),
			// What identity resolution gives the declared block: a
			// concrete id folded from type, parent_id and name.
			[]identity.Resolution{{Addr: newAddr, Class: identity.ClassConcrete, ImportID: armRoleID}},
			SingleProvider(azapiTestProvider, azapiRoleProvider(&imported)),
			Options{RecordStore: store, Ownership: &Ownership{Estate: estate}})
		if errs := errorDiagsWith(diags, overwriteSummary); len(errs) != 0 {
			t.Fatalf("the renamed role assignment was refused as an unrecorded overwrite:\n%s", renderDiags(diags))
		}
		assertNoErrors(t, diags)
		return res, imported
	}

	// The plan: admitted at the new address from the old address's record.
	res, imported := plan(t, true)
	assertMaterialized(t, res, []string{newAddr.String()})
	if res.Has(oldAddr) {
		t.Error("the moved-from address materialized as a second prior-state entry")
	}
	if len(imported) != 1 || imported[0] != armRoleID {
		t.Errorf("imported %v, want exactly [%q]", imported, armRoleID)
	}
	if len(res.EnvelopeVersions) != 0 {
		t.Errorf("EnvelopeVersions = %v; a version read through a moved alias belongs to the old key and must not become the new address's CAS expectation", res.EnvelopeVersions)
	}

	// The apply changes nothing, so its final state is the prior state.
	diags := WriteBack(ctx, WriteBackRequest{
		Store:      store,
		FinalState: res.State,
		Schemas: &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
			azapiTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{"azapi_resource": azapiRecordSchema()}},
		}},
		Config:           loadConfig(t, writeAzapiRoleFixture(t, true)),
		EnvelopeVersions: res.EnvelopeVersions,
	})
	assertNoErrors(t, diags)

	got, _, _, found, err := store.GetIdentityFresh(ctx, newAddr)
	if err != nil || !found || got.ImportID != armRoleID {
		t.Fatalf("after the apply the new address's record is %+v (found %v, err %v), want ImportID %q", got, found, err, armRoleID)
	}
	if _, _, exists, err := store.getRawFresh(ctx, oldAddr); err != nil || exists {
		t.Errorf("the moved-from key survived the apply (exists %v, err %v); once the moved block is deleted it reads as a removed block's record", exists, err)
	}

	// The next plan finds the record under the new address, with or without
	// the moved block, and now reads its version as its own.
	for _, withMoved := range []bool{true, false} {
		res, _ := plan(t, withMoved)
		assertMaterialized(t, res, []string{newAddr.String()})
		if len(res.EnvelopeVersions) != 1 || !res.EnvelopeVersions[0].Addr.Equal(newAddr) {
			t.Errorf("withMoved=%v: EnvelopeVersions = %v, want the new address's own", withMoved, res.EnvelopeVersions)
		}
	}
}

// TestRecordFirst_movedRecordNeedsTheMovedBlock is the mutation check: the
// same record under the old address, with no moved block joining the two,
// must not bind the new address. The refusal stands, as it does for any
// unrecorded untaggable object at a declared id.
func TestRecordFirst_movedRecordNeedsTheMovedBlock(t *testing.T) {
	ctx := context.Background()
	const estate = k8sOwnershipEstate
	oldAddr := mustAddr(t, `azapi_resource.roleAssignments`)
	newAddr := mustAddr(t, `azapi_resource.role_assignment`)

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := NewRecordEnvelopeStore(raw, RecordKeyPrefix(estate))
	seed, _ := LocatedRecordFrom("azapi_resource", azapiRecordSchema(), azapiRoleObject())
	if _, err := store.mergeEnvelope(ctx, oldAddr, "", func(env *recordEnvelope) {
		env.Identity = identityPayloadFrom(seed)
	}); err != nil {
		t.Fatal(err)
	}

	res, diags := BuildWith(ctx, loadConfig(t, writeAzapiRoleFixture(t, false)),
		[]identity.Resolution{{Addr: newAddr, Class: identity.ClassConcrete, ImportID: armRoleID}},
		SingleProvider(azapiTestProvider, azapiRoleProvider(nil)),
		Options{RecordStore: store, Ownership: &Ownership{Estate: estate}})
	if errs := errorDiagsWith(diags, overwriteSummary); len(errs) != 1 {
		t.Fatalf("want the unrecorded-overwrite refusal with no moved block, got:\n%s", renderDiags(diags))
	}
	if res.Has(newAddr) {
		t.Error("the new address bound to an unrelated address's record")
	}
}
