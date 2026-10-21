// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

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

// GitHub issue #1120, unit 10: the Azure family's identities, pinned by
// value through the same Dir -> Analyze path TestIdentityGolden uses, with
// schemas, so that lint's admission and the resolver's synthesis are both
// exercised. TestIdentityGolden cannot carry these rows: it runs with no
// schemas, and every Azure identity is a synthesis (no azurerm or azapi
// type has a ratified row), so an Azure fixture there renders nothing.
//
// Unlike TestSynthesisGolden's fictitious types, these are the real type
// names, because the azurerm route is keyed on the type's row in
// live/azurerm-ids.json; the schemas are the real providers' shapes at
// azurerm 5.10.0 and azapi 2.13.0, reduced to what identity reads.
//
// Three fixtures, as #1120 unit 10 asks: an azurerm virtual network with a
// subnet (both on the identity-schema route, which #1122 keeps for the 196
// types with an identity schema; since unit 13 that route answers
// subscription_id from the provider block and renders the generated ARM id
// template), an azapi virtual network with a subnet (whose identity values
// are azapi's identity schema, id and type@api-version),
// and one DNS zone declared through azurerm and through azapi in different
// letter case. Regenerate with
//
//	env -u PWD go test ./internal/live/check -run TestAzureSynthesisGolden -update
//
// and read the diff.

const azureGoldenPath = "testdata/azure-synthesis-golden.txt"

const azureGoldenHeader = `
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
    azapi   = { source = "azure/azapi" }
  }
}
provider "azurerm" {
  features {}
  subscription_id = "00000000-0000-0000-0000-000000000001"
}
provider "azapi" {
  subscription_id = "00000000-0000-0000-0000-000000000001"
}
`

var azureGoldenFixtures = []struct{ name, body string }{
	{"azurerm-vnet-subnet", `
resource "azurerm_virtual_network" "vnet" {
  name                = "vnet1"
  resource_group_name = "rg"
  location            = "westeurope"
  address_space       = ["10.0.0.0/16"]
}
resource "azurerm_subnet" "snet" {
  name                 = "snet1"
  resource_group_name  = "rg"
  virtual_network_name = azurerm_virtual_network.vnet.name
  address_prefixes     = ["10.0.1.0/24"]
}
resource "azurerm_dns_zone" "zone" {
  name                = "example.com"
  resource_group_name = "rg"
}
`},
	{"azapi-vnet-subnet", `
resource "azapi_resource" "vnet" {
  type      = "Microsoft.Network/virtualNetworks@2024-05-01"
  parent_id = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg"
  name      = "vnet1"
  location  = "westeurope"
}
resource "azapi_resource" "snet" {
  type      = "Microsoft.Network/virtualNetworks/subnets@2024-05-01"
  parent_id = azapi_resource.vnet.id
  name      = "snet1"
}
`},
	{"duplicate-across-providers", `
resource "azurerm_dns_zone" "zone" {
  name                = "Example.com"
  resource_group_name = "RG"
}
resource "azapi_resource" "zone" {
  type      = "Microsoft.Network/dnsZones@2018-05-01"
  parent_id = "/subscriptions/00000000-0000-0000-0000-000000000001/resourcegroups/rg"
  name      = "example.com"
}
`},
}

func azureGoldenSchemas() map[string]providers.Schema {
	str := func(req bool) *configschema.Attribute {
		return &configschema.Attribute{Type: cty.String, Required: req, Optional: !req}
	}
	tags := &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
	idSchema := func(names ...string) *configschema.Object {
		o := &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{}}
		for _, n := range names {
			o.Attributes[n] = &configschema.Attribute{Type: cty.String, Required: true}
		}
		return o
	}
	return map[string]providers.Schema{
		"azurerm_virtual_network": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true}, "name": str(true), "resource_group_name": str(true), "location": str(true),
			"address_space": {Type: cty.List(cty.String), Optional: true}, "tags": tags,
		}}, IdentitySchema: idSchema("name", "resource_group_name", "subscription_id")},
		"azurerm_subnet": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true}, "name": str(true), "resource_group_name": str(true), "virtual_network_name": str(true),
			"address_prefixes": {Type: cty.List(cty.String), Required: true},
		}}, IdentitySchema: idSchema("name", "resource_group_name", "subscription_id", "virtual_network_name")},
		"azurerm_dns_zone": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true}, "name": str(true), "resource_group_name": str(true), "tags": tags,
		}}},
		"azapi_resource": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":        {Type: cty.String, Computed: true},
			"type":      {Type: cty.String, Required: true},
			"parent_id": {Type: cty.String, Optional: true, Computed: true},
			"name":      {Type: cty.String, Optional: true, Computed: true},
			"location":  {Type: cty.String, Optional: true, Computed: true},
			"tags":      {Type: cty.Map(cty.String), Optional: true, Computed: true},
		}}, IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Required: true},
			"type": {Type: cty.String, Optional: true},
		}}},
	}
}

func TestAzureSynthesisGolden(t *testing.T) {
	var b strings.Builder
	b.WriteString("# testdata/azure-synthesis-golden.txt - GitHub issue #1120, unit 10\n")
	b.WriteString("# Generated by TestAzureSynthesisGolden -update. Do not hand-edit.\n")
	for _, fx := range azureGoldenFixtures {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(azureGoldenHeader+fx.body), 0o600); err != nil {
			t.Fatal(err)
		}
		report := Dir(context.Background(), dir, Context{Schemas: azureGoldenSchemas()})
		b.WriteString("#\n# fixture " + fx.name + "\n")
		b.WriteString(strings.Replace(synthesisGoldenRender(report), "# testdata/synthesis-golden.txt - GitHub issue #1608\n# Generated by TestSynthesisGolden -update. Do not hand-edit.\n", "", 1))
	}
	got := b.String()

	if *updateIdentityGolden {
		if err := os.WriteFile(azureGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(azureGoldenPath)
	if err != nil {
		t.Fatalf("reading %s: %s\nRun the test with -update to create it.", azureGoldenPath, err)
	}
	if string(want) != got {
		t.Errorf("Azure synthesis golden mismatch (regenerate with -update after a deliberate change):\n--- want ---\n%s--- got ---\n%s", want, got)
	}
}
