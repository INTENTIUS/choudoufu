package identity

import (
	"testing"
)

func TestZZReachProbe(t *testing.T) {
	cases := map[string]string{
		"type-var-default": `
variable "t" {
  type    = string
  default = "Microsoft.Network/virtualNetworks@2024-01-01"
}
resource "azapi_resource" "x" {
  type      = var.t
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
  name      = "n"
}`,
		"type-local-interp": `
locals {
  v = "2024-01-01"
}
resource "azapi_resource" "x" {
  type      = "Microsoft.Network/virtualNetworks@${local.v}"
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
  name      = "n"
}`,
		"name-omitted-default-name": `
provider "azapi" {
  alias = "dn"
  subscription_id = "` + azureSub + `"
  default_name = "fixed"
}
resource "azapi_resource" "x" {
  provider  = azapi.dn
  type      = "Microsoft.Network/virtualNetworks@2024-01-01"
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
}`,
		"name-omitted": `
resource "azapi_resource" "x" {
  type      = "Microsoft.Network/virtualNetworks@2024-01-01"
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
}`,
		"parent-from-rg": `
resource "azapi_resource" "rg" {
  type      = "Microsoft.Resources/resourceGroups@2024-01-01"
  name      = "rg"
}
resource "azapi_resource" "x" {
  type      = "Microsoft.Network/virtualNetworks@2024-01-01"
  parent_id = azapi_resource.rg.id
  name      = "n"
}`,
		"type-from-other-resource": `
resource "azapi_resource" "a" {
  type      = "Microsoft.Network/virtualNetworks@2024-01-01"
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
  name      = "a"
}
resource "azapi_resource" "x" {
  type      = azapi_resource.a.type
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
  name      = "n"
}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			result, errs := resolveAzure(t, azureProviders+body)
			if errs != "" {
				t.Logf("REFUSED: %s", errs)
				return
			}
			for _, r := range result.All() {
				t.Logf("%s -> %s | %s | %s", r.Addr, r.Class, r.ImportID, r.Reason)
			}
		})
	}
}
