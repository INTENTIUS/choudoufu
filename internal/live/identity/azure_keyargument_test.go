// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1120's second Azure lane run: corpus-azapi-role-assignment
// (azapi's own published Microsoft.Authorization_roleAssignments example)
// declares its identity's resource group as
// `resource_group_name = azapi_resource.resourceGroup.name`, and the
// static resolver refused it with "Not an identity attribute": azapi
// marks name Optional+Computed and names only id as an identity
// attribute. But name is one of the three arguments the resource group's
// own ARM id is built from (#1079's natural key), so the value it reads is
// the configured one. With the refusal the identity fell out of the
// pre-walk projection, the plan-node seam bound it from its record as an
// import, and every plan, the one after the adopting apply included,
// proposed that import again.

// resolveAzureWith is [resolveAzure] with azurerm_user_assigned_identity's
// 5.10.0 shape added: name, resource_group_name and location required, and
// the identity schema the provider serves.
func resolveAzureWith(t *testing.T, body string) (*Result, string) {
	t.Helper()
	schemas := azureSchemas()
	schemas["azurerm_user_assigned_identity"] = withIdentity(azurermSchema("name", "resource_group_name", "location"),
		"name", "resource_group_name", "subscription_id")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
    azapi   = { source = "azure/azapi" }
  }
}
`+body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir, nil)
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	if diags.HasErrors() {
		return result, diags.Err().Error()
	}
	return result, ""
}

func withIdentity(s providers.Schema, required ...string) providers.Schema {
	attrs := map[string]*configschema.Attribute{}
	for _, a := range required {
		attrs[a] = &configschema.Attribute{Type: cty.String, Required: true}
	}
	s.IdentitySchema = &configschema.Object{Nesting: configschema.NestingSingle, Attributes: attrs}
	return s
}

func TestAzapiKeyArgumentResolvesWithoutTheCloud(t *testing.T) {
	const root = `
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
`
	result, errs := resolveAzureWith(t, azureProviders+root)
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got := azureResolution(t, result, "azurerm_user_assigned_identity.uai")
	want := "/subscriptions/" + azureSub + "/resourceGroups/acctest0001/providers/Microsoft.ManagedIdentity/userAssignedIdentities/TestUAI"
	if got.Class != ClassConcrete || got.ImportID != want {
		t.Errorf("= %s, want CONCRETE %s", got, want)
	}
	if v := got.IdentityValues; v["resource_group_name"] != "acctest0001" || v["name"] != "TestUAI" {
		t.Errorf("identity values %v, want resource_group_name acctest0001 and name TestUAI", v)
	}

	// parent_id is a key argument too: a reference to it reads the
	// parent's own configured value.
	result, errs = resolveAzureWith(t, azureProviders+`
resource "azapi_resource" "snet" {
  type      = "Microsoft.Network/virtualNetworks/subnets@2024-05-01"
  parent_id = "/subscriptions/`+azureSub+`/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet1"
  name      = "snet"
}
resource "azapi_resource" "lock" {
  type      = "Microsoft.Authorization/locks@2020-05-01"
  parent_id = azapi_resource.snet.parent_id
  name      = "nodelete"
}
`)
	if errs != "" {
		t.Fatalf("parent_id refused: %s", errs)
	}
	got = azureResolution(t, result, "azapi_resource.lock")
	if want := "/subscriptions/" + azureSub + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet1/providers/Microsoft.Authorization/locks/nodelete"; got.Class != ClassConcrete || got.ImportID != want {
		t.Errorf("lock = %s, want CONCRETE %s", got, want)
	}
}

// The boundary: an Optional+Computed attribute that is not one of the id's
// arguments stays refused, since the provider may store a value other than
// the configured one. location is the case: azapi plans it from the
// parent's when it is omitted, and normalises what is written.
func TestAzapiNonKeyComputedAttributeStaysRefused(t *testing.T) {
	_, errs := resolveAzureWith(t, azureProviders+`
resource "azapi_resource" "resourceGroup" {
  type     = "Microsoft.Resources/resourceGroups@2020-06-01"
  name     = "eastus"
  location = "eastus"
}
resource "azurerm_user_assigned_identity" "uai" {
  name                = "TestUAI"
  resource_group_name = azapi_resource.resourceGroup.location
  location            = "eastus"
}
`)
	if !strings.Contains(errs, "Not an identity attribute") || !strings.Contains(errs, `"location" is not an identity attribute of azapi_resource`) {
		t.Errorf("a reference to azapi's location: %q, want the Not an identity attribute refusal", errs)
	}
}
