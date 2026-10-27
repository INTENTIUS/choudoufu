// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/svchost"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/terminal"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/intentius/choudoufu/pkg/azsweep"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// GitHub issue #1120's second Azure lane run: corpus-azapi-role-assignment's
// test_plan, the plan with no state file after live-import -approve, read
// "Plan: 1 to import, 0 to add, 0 to change, 0 to destroy", and the replan
// after choudoufu's own adopting apply was not empty either. The import was
// azurerm_user_assigned_identity.uai, whose resource_group_name reads
// azapi_resource.resourceGroup.name. The static resolver refused that
// reference ("Not an identity attribute"), so the identity never reached the
// pre-walk projection, which binds a marked object silently; the plan-node
// seam bound it instead, from the identity record live-import wrote beside
// its marker, and every binding that seam makes is an import in the plan.
// Live mode keeps no state file, so nothing the apply imported was in the
// next plan's prior state: each plan proposed the same import again.
//
// floci-az's Resource Graph refuses the sweep's query (lex00/floci-az#11),
// so the sweep binds nothing here either, as in the lane's run.

const (
	recBoundEstate = "corpus-azapi-role-assignment"
	recBoundSub    = "00000000-0000-0000-0000-000000000001"
	recBoundRGID   = "/subscriptions/" + recBoundSub + "/resourceGroups/acctest0001"
	recBoundUAIID  = recBoundRGID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/TestUAI"
	recBoundRAID   = recBoundRGID + "/providers/Microsoft.Authorization/roleAssignments/6faae21a-0cd6-4536-8c23-a278823d12ed"
	recBoundRAType = "Microsoft.Authorization/roleAssignments@2022-04-01"
)

// recBoundRoot is the published root with the run's live block and both
// provider blocks' subscription. The role assignment's body (the identity's
// principal_id and a role definition read through a data source) is left
// out: nothing here reads it, and the record, not the body, binds it.
const recBoundRoot = `
terraform {
  required_providers {
    azapi   = { source = "azure/azapi" }
    azurerm = { source = "hashicorp/azurerm" }
  }
  live {
    estate = "` + recBoundEstate + `"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

provider "azurerm" {
  subscription_id = "` + recBoundSub + `"
  features {
  }
}

provider "azapi" {
  subscription_id = "` + recBoundSub + `"
}

variable "resource_name" {
  type    = string
  default = "acctest0001"
}

resource "azapi_resource" "resourceGroup" {
  type     = "Microsoft.Resources/resourceGroups@2020-06-01"
  name     = var.resource_name
  location = "eastus"
}

resource "azurerm_user_assigned_identity" "uai" {
  name                = "TestUAI"
  resource_group_name = azapi_resource.resourceGroup.name
  location            = azapi_resource.resourceGroup.location
}

resource "azapi_resource" "roleAssignments" {
  type      = "` + recBoundRAType + `"
  name      = "6faae21a-0cd6-4536-8c23-a278823d12ed"
  parent_id = azapi_resource.resourceGroup.id
}
`

// recBoundARM serves both providers' objects by ARM id, compared
// case-insensitively, and counts the imports the plan engine asks for.
type recBoundARM struct {
	mu      sync.Mutex
	objects map[string]cty.Value
}

func (a *recBoundARM) get(id string) (cty.Value, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, v := range a.objects {
		if strings.EqualFold(k, id) {
			return v, true
		}
	}
	return cty.NilVal, false
}

func (a *recBoundARM) provider(typeName string, schema providers.Schema, providerBlock *configschema.Block) *tofu.MockProvider {
	p := &tofu.MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
		Provider:      providers.Schema{Block: providerBlock},
		ResourceTypes: map[string]providers.Schema{typeName: schema},
	}}
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		id := r.Target.ID
		if r.Target.IsIdentityBased() {
			// azurerm's identity object: the ARM id's three parts.
			v := r.Target.Identity
			id = "/subscriptions/" + v.GetAttr("subscription_id").AsString() + "/resourceGroups/" + v.GetAttr("resource_group_name").AsString() +
				"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + v.GetAttr("name").AsString()
		}
		obj, ok := a.get(id)
		if !ok {
			return providers.ImportResourceStateResponse{}
		}
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{TypeName: r.TypeName, State: obj}}}
	}
	// The proposed state as planned: a Computed argument the configuration
	// leaves out (azapi's parent_id for a resource group) keeps the value
	// read, as the providers plan it, where the mock's default would plan
	// it unknown.
	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState, PlannedPrivate: r.PriorPrivate}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		obj, ok := a.get(r.PriorState.GetAttr("id").AsString())
		if !ok {
			return providers.ReadResourceResponse{NewState: cty.NullVal(r.PriorState.Type())}
		}
		return providers.ReadResourceResponse{NewState: obj}
	}
	return p
}

func recBoundTags(addr string) cty.Value {
	return cty.MapVal(map[string]cty.Value{
		markers.TagEstate:  cty.StringVal(recBoundEstate),
		markers.TagAddress: cty.StringVal(markers.EscapeAddress(addr)),
	})
}

func TestLivePlan_azureRecordBoundIdentityIsNotAnImport(t *testing.T) {
	for _, name := range azsweep.EnvNames() {
		t.Setenv(name, "")
	}
	t.Setenv("ARM_USE_CLI", "false")
	td := t.TempDir()
	t.Chdir(td)
	if err := os.WriteFile(filepath.Join(td, "main.tf"), []byte(recBoundRoot), 0o600); err != nil {
		t.Fatal(err)
	}

	azapiSchema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":        {Type: cty.String, Computed: true},
		"type":      {Type: cty.String, Required: true},
		"parent_id": {Type: cty.String, Optional: true, Computed: true},
		"name":      {Type: cty.String, Optional: true, Computed: true},
		"location":  {Type: cty.String, Optional: true, Computed: true},
		"tags":      {Type: cty.Map(cty.String), Optional: true, Computed: true},
		"body":      {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
	}}}
	uaiSchema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":                  {Type: cty.String, Computed: true},
		"name":                {Type: cty.String, Required: true},
		"resource_group_name": {Type: cty.String, Required: true},
		"location":            {Type: cty.String, Required: true},
		"tags":                {Type: cty.Map(cty.String), Optional: true},
	}},
		// azurerm 5.10.0's identity schema for the type.
		IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
			"name":                {Type: cty.String, Required: true},
			"resource_group_name": {Type: cty.String, Required: true},
			"subscription_id":     {Type: cty.String, Required: true},
		}},
	}
	// Both after live-import -approve: each carries this estate's marker.
	arm := &recBoundARM{objects: map[string]cty.Value{
		recBoundRGID: cty.ObjectVal(map[string]cty.Value{
			"id":        cty.StringVal(recBoundRGID),
			"type":      cty.StringVal("Microsoft.Resources/resourceGroups@2020-06-01"),
			"parent_id": cty.StringVal("/subscriptions/" + recBoundSub),
			"name":      cty.StringVal("acctest0001"),
			"location":  cty.StringVal("eastus"),
			"tags":      recBoundTags("azapi_resource.resourceGroup"),
			"body":      cty.EmptyObjectVal,
		}),
		// Untaggable at every API version: held by its record alone.
		recBoundRAID: cty.ObjectVal(map[string]cty.Value{
			"id":        cty.StringVal(recBoundRAID),
			"type":      cty.StringVal(recBoundRAType),
			"parent_id": cty.StringVal(recBoundRGID),
			"name":      cty.StringVal("6faae21a-0cd6-4536-8c23-a278823d12ed"),
			"location":  cty.NullVal(cty.String),
			"tags":      cty.MapValEmpty(cty.String),
			"body":      cty.EmptyObjectVal,
		}),
		recBoundUAIID: cty.ObjectVal(map[string]cty.Value{
			"id":                  cty.StringVal(recBoundUAIID),
			"name":                cty.StringVal("TestUAI"),
			"resource_group_name": cty.StringVal("acctest0001"),
			"location":            cty.StringVal("eastus"),
			"tags":                recBoundTags("azurerm_user_assigned_identity.uai"),
		}),
	}}

	// live-import -approve records a stamped instance's identity beside its
	// marker (liveimport's seedIdentityFor): the record the node seam read.
	local, err := staterecord.NewLocalStore(filepath.Join(td, ".tofu-records"))
	if err != nil {
		t.Fatal(err)
	}
	store := projection.NewRecordEnvelopeStore(local, projection.RecordKeyPrefix(recBoundEstate))
	azurerm := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}
	uai, diags := addrs.ParseAbsResourceInstanceStr("azurerm_user_assigned_identity.uai")
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if _, err := projection.SeedLocatedForInstance(t.Context(), store, uai, azurerm, projection.LocatedRecord{ImportID: recBoundUAIID}); err != nil {
		t.Fatal(err)
	}
	// The role assignment's record, the 1 skipped, under the stock state's
	// host, as live-import wrote it before az/recorded-provider: the
	// adopting apply's write-back has to rewrite it under the configured
	// provider, and it has to bind either way.
	ra, diags := addrs.ParseAbsResourceInstanceStr("azapi_resource.roleAssignments")
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	azapi := addrs.NewProvider(addrs.DefaultProviderRegistryHost, "azure", "azapi")
	stockAzapi := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewProvider(svchost.Hostname("registry.terraform.io"), "azure", "azapi")}
	if _, err := projection.SeedLocatedForInstance(t.Context(), store, ra, stockAzapi, projection.LocatedRecord{ImportID: recBoundRAID}); err != nil {
		t.Fatal(err)
	}

	azurermBlock := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{"subscription_id": {Type: cty.String, Optional: true}},
		BlockTypes: map[string]*configschema.NestedBlock{"features": {Nesting: configschema.NestingList, Block: configschema.Block{}}},
	}
	azapiBlock := &configschema.Block{Attributes: map[string]*configschema.Attribute{"subscription_id": {Type: cty.String, Optional: true}}}

	meta := func() (Meta, func(*testing.T) *terminal.TestOutput) {
		view, done := testView(t)
		return Meta{
			WorkingDir: workdir.NewDir("."),
			View:       view,
			testingOverrides: &testingOverrides{Providers: map[addrs.Provider]providers.Factory{
				azapi: func() (providers.Interface, error) {
					return pairLister{arm.provider("azapi_resource", azapiSchema, azapiBlock)}, nil
				},
				addrs.NewDefaultProvider("azurerm"): func() (providers.Interface, error) {
					return pairLister{arm.provider("azurerm_user_assigned_identity", uaiSchema, azurermBlock)}, nil
				},
			}},
		}, done
	}
	plan := func(run string) {
		t.Helper()
		m, done := meta()
		code := (&PlanCommand{Meta: m}).Run([]string{"-no-color"})
		output := done(t)
		stdout, stderr := output.Stdout(), output.Stderr()
		if code != 0 {
			t.Fatalf("%s: exit code %d\nstdout:\n%s\nstderr:\n%s", run, code, stdout, stderr)
		}
		if !strings.Contains(stdout, "No changes.") || strings.Contains(stdout, "to import") {
			t.Errorf("%s is not empty:\n%s\nstderr:\n%s", run, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "Not an identity attribute") {
			t.Errorf("%s still refuses azapi_resource.resourceGroup.name:\n%s%s", run, stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, azsweep.SummarySweepUnavailable) {
			t.Errorf("%s: no %q warning, so this is not the lane's sweep-less case:\n%s%s", run, azsweep.SummarySweepUnavailable, stdout, stderr)
		}
	}

	// test_plan, then the run's adopting apply and the replan after it.
	// Live mode keeps no state file, so the apply leaves nothing the replan
	// reads but the cloud and the record store.
	plan("the plan with no state file")
	if _, provider, _, _, err := store.GetIdentityWithProvider(t.Context(), ra); err != nil || provider != stockAzapi.String() {
		t.Fatalf("before the apply the role assignment's record names %q (err %v), want the stock state's %s", provider, err, stockAzapi)
	}
	m, done := meta()
	code := (&ApplyCommand{Meta: m}).Run([]string{"-no-color", "-auto-approve"})
	output := done(t)
	if code != 0 {
		t.Fatalf("the adopting apply: exit code %d\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	plan("the replan after the adopting apply")

	// The role assignment's record after the apply: still its id, now under
	// the provider configuration the plan resolved.
	rec, provider, _, found, err := store.GetIdentityWithProvider(t.Context(), ra)
	if err != nil || !found || !strings.EqualFold(rec.ImportID, recBoundRAID) {
		t.Fatalf("the role assignment's record after the apply: %+v found %v err %v, want %s", rec, found, err, recBoundRAID)
	}
	if want := (addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: azapi}).String(); provider != want {
		t.Errorf("the role assignment's record names %s after the apply, want the configured %s", provider, want)
	}
}
