// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// GitHub issue #2133, finding 4: a subscription named only by
// ARM_SUBSCRIPTION_ID. Both providers read it when their block leaves
// subscription_id unset (azurerm 5.10.0 internal/provider/provider.go:118,
// azapi 2.13.0 internal/provider/provider.go:364-368), and the run hands it
// to identity as CloudContext.SubscriptionID, so a block that is silent
// must resolve under it, a block that states one must win over it, and a
// block that sets one this run cannot see must not be answered from it.

const azureEnvSub = "00000000-0000-0000-0000-0000000000ee"

func resolveAzureIn(t *testing.T, cloud CloudContext, body string) *Result {
	t.Helper()
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
	result, diags := ResolveWith(context.Background(), cfg, Context{Cloud: cloud, Schemas: azureSchemas()})
	if diags.HasErrors() {
		t.Fatalf("refused: %s", diags.Err())
	}
	return result
}

func dnsZoneID(sub string) string {
	return "/subscriptions/" + sub + "/resourceGroups/rg-prod/providers/Microsoft.Network/dnsZones/example.com"
}

const dnsZone = `
resource "azurerm_dns_zone" "z" {
  name                = "example.com"
  resource_group_name = "rg-prod"
}
`

func wantConcrete(t *testing.T, result *Result, addr, want string) {
	t.Helper()
	got := azureResolution(t, result, addr)
	if got.Class != ClassConcrete || got.ImportID != want {
		t.Errorf("%s = %s (%s), want CONCRETE %s", addr, got, got.ImportID, want)
	}
}

func wantSubscriptionUnknown(t *testing.T, result *Result, addr string) {
	t.Helper()
	got := azureResolution(t, result, addr)
	if got.Class != ClassNeedsDiscovery || got.Cause != DiscoveryCloudUnknown || len(got.CauseArgs) == 0 || got.CauseArgs[0] != string(CloudSubscriptionID) {
		t.Errorf("%s = %s cause %s %v, want NEEDS_DISCOVERY naming the subscription", addr, got, got.Cause, got.CauseArgs)
	}
}

func TestAzureSubscriptionBlockWinsOverEnvironment(t *testing.T) {
	result := resolveAzureIn(t, CloudContext{SubscriptionID: azureEnvSub}, azureProviders+dnsZone)
	wantConcrete(t, result, "azurerm_dns_zone.z", dnsZoneID(azureSub))
}

func TestAzureSubscriptionFromEnvironmentWhenBlockIsSilent(t *testing.T) {
	cloud := CloudContext{SubscriptionID: azureEnvSub}
	// A block that sets no subscription_id.
	result := resolveAzureIn(t, cloud, `
provider "azurerm" {
  features {}
}
`+dnsZone)
	wantConcrete(t, result, "azurerm_dns_zone.z", dnsZoneID(azureEnvSub))

	// No provider block at all: OpenTofu's implied empty configuration,
	// which the provider configures from its environment just the same.
	result = resolveAzureIn(t, cloud, dnsZone)
	wantConcrete(t, result, "azurerm_dns_zone.z", dnsZoneID(azureEnvSub))

	// azapi's resource-group parent default reads the same answer.
	result = resolveAzureIn(t, cloud, `
resource "azapi_resource" "rg" {
  type     = "Microsoft.Resources/resourceGroups@2024-03-01"
  name     = "rg-prod"
  location = "westeurope"
}
`)
	wantConcrete(t, result, "azapi_resource.rg", "/subscriptions/"+azureEnvSub+"/resourceGroups/rg-prod")
}

func TestAzureSubscriptionNeitherSetIsCloudUnknown(t *testing.T) {
	result := resolveAzureIn(t, CloudContext{}, `
provider "azurerm" {
  features {}
}
`+dnsZone)
	wantSubscriptionUnknown(t, result, "azurerm_dns_zone.z")
}

// A block that sets subscription_id to something this run cannot evaluate
// is a subscription the provider uses and nobody here can see. The
// environment does not stand in for it, because the provider does not let
// it.
func TestAzureSubscriptionUnresolvableBlockIgnoresEnvironment(t *testing.T) {
	result := resolveAzureIn(t, CloudContext{SubscriptionID: azureEnvSub}, `
variable "sub" {
  type = string
}
provider "azurerm" {
  features {}
  subscription_id = var.sub
}
`+dnsZone)
	wantSubscriptionUnknown(t, result, "azurerm_dns_zone.z")
}

// An aliased block with its own subscription_id names its resources under
// that one, while the default block, silent, takes the environment's.
func TestAzureSubscriptionAliasBlockKeepsItsOwn(t *testing.T) {
	const aliasSub = "00000000-0000-0000-0000-0000000000aa"
	result := resolveAzureIn(t, CloudContext{SubscriptionID: azureEnvSub}, `
provider "azurerm" {
  features {}
}
provider "azurerm" {
  alias           = "other"
  features {}
  subscription_id = "`+aliasSub+`"
}
resource "azurerm_dns_zone" "z" {
  name                = "example.com"
  resource_group_name = "rg-prod"
}
resource "azurerm_dns_zone" "o" {
  provider            = azurerm.other
  name                = "example.com"
  resource_group_name = "rg-prod"
}
`)
	wantConcrete(t, result, "azurerm_dns_zone.z", dnsZoneID(azureEnvSub))
	wantConcrete(t, result, "azurerm_dns_zone.o", dnsZoneID(aliasSub))
}
