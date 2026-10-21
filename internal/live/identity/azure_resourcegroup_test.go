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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The fixtures here are written to a temporary directory rather than kept
// under testdata/, because internal/live/check's identity golden sweeps
// every configuration directory under internal/live and an Azure fixture
// would move it for a warning that has nothing to do with identity.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func foreignGroupDetails(diags tfdiags.Diagnostics) []string {
	var out []string
	for _, d := range diags {
		if d.Description().Summary == SummaryForeignResourceGroup {
			if d.Severity() != tfdiags.Warning {
				out = append(out, "NOT A WARNING: "+d.Description().Detail)
				continue
			}
			out = append(out, d.Description().Detail)
		}
	}
	return out
}

// TestForeignResourceGroupWarning is GitHub issue #1122's named warning:
// a declared azurerm or azapi resource in a resource group the
// configuration does not declare.
func TestForeignResourceGroupWarning(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		// want is one substring per expected warning, in order; nil means
		// no warning at all.
		want []string
	}{
		{
			name: "a reference to a declared group",
			files: map[string]string{"main.tf": `
resource "azurerm_resource_group" "rg" {
  name     = "estate-rg"
  location = "westeurope"
}
resource "azurerm_storage_account" "sa" {
  name                = "estatesa"
  resource_group_name = azurerm_resource_group.rg.name
}
`},
		},
		{
			name: "a literal naming a group nobody declares",
			files: map[string]string{"main.tf": `
resource "azurerm_resource_group" "rg" {
  name     = "estate-rg"
  location = "westeurope"
}
resource "azurerm_storage_account" "sa" {
  name                = "estatesa"
  resource_group_name = "someone-elses-rg"
}
resource "azurerm_key_vault" "kv" {
  name                = "estatekv"
  resource_group_name = "Someone-Elses-RG"
}
`},
			want: []string{`azurerm_key_vault.kv, azurerm_storage_account.sa are declared in resource group "Someone-Elses-RG", and no azurerm_resource_group`},
		},
		{
			name: "a literal matching a declared group, in another case",
			files: map[string]string{"main.tf": `
resource "azurerm_resource_group" "rg" {
  name     = "Estate-RG"
  location = "westeurope"
}
resource "azurerm_storage_account" "sa" {
  name                = "estatesa"
  resource_group_name = "ESTATE-rg"
}
`},
		},
		{
			name: "a group read through a data source",
			files: map[string]string{"main.tf": `
data "azurerm_resource_group" "shared" {
  name = "shared-rg"
}
resource "azurerm_virtual_network" "vnet" {
  name                = "vnet"
  resource_group_name = data.azurerm_resource_group.shared.name
}
`},
			want: []string{`azurerm_virtual_network.vnet is declared in resource group "shared-rg" (read through data.azurerm_resource_group.shared)`},
		},
		{
			name: "a data source that reads a group the estate declares",
			files: map[string]string{"main.tf": `
resource "azurerm_resource_group" "rg" {
  name     = "estate-rg"
  location = "westeurope"
}
data "azurerm_resource_group" "rg" {
  name = azurerm_resource_group.rg.name
}
resource "azurerm_virtual_network" "vnet" {
  name                = "vnet"
  resource_group_name = data.azurerm_resource_group.rg.name
}
`},
		},
		{
			name: "a module input set to a declared group, and one set to a foreign one",
			files: map[string]string{
				"main.tf": `
resource "azurerm_resource_group" "rg" {
  name     = "estate-rg"
  location = "westeurope"
}
module "inside" {
  source = "./mod"
  rg     = azurerm_resource_group.rg.name
}
module "outside" {
  source = "./mod"
  rg     = "elsewhere-rg"
}
`,
				"mod/main.tf": `
variable "rg" { type = string }
locals { group = var.rg }
resource "azurerm_storage_account" "sa" {
  name                = "sa"
  resource_group_name = local.group
}
`,
			},
			want: []string{`module.outside.azurerm_storage_account.sa is declared in resource group "elsewhere-rg"`},
		},
		{
			name: "azapi parent ids",
			files: map[string]string{"main.tf": `
resource "azapi_resource" "rg" {
  type      = "Microsoft.Resources/resourceGroups@2024-03-01"
  name      = "estate-rg"
  parent_id = "/subscriptions/00000000-0000-0000-0000-000000000000"
}
resource "azapi_resource" "vnet" {
  type      = "Microsoft.Network/virtualNetworks@2024-05-01"
  name      = "vnet"
  parent_id = azapi_resource.rg.id
}
resource "azapi_resource" "own_by_literal" {
  type      = "Microsoft.Network/virtualNetworks@2024-05-01"
  name      = "vnet2"
  parent_id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/ESTATE-RG"
}
resource "azapi_resource" "foreign" {
  type      = "Microsoft.Network/virtualNetworks@2024-05-01"
  name      = "vnet3"
  parent_id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/other-rg"
}
resource "azapi_resource" "subscription_scoped" {
  type      = "Microsoft.Resources/deployments@2024-03-01"
  name      = "dep"
  parent_id = "/subscriptions/00000000-0000-0000-0000-000000000000"
}
`},
			want: []string{`azapi_resource.foreign is declared in resource group "other-rg"`},
		},
		{
			name: "a declared group whose name cannot be read says nothing",
			files: map[string]string{"main.tf": `
variable "rg_name" { type = string }
resource "azurerm_resource_group" "rg" {
  name     = var.rg_name
  location = "westeurope"
}
resource "azurerm_storage_account" "sa" {
  name                = "sa"
  resource_group_name = "maybe-the-same-rg"
}
`},
		},
		{
			name: "for_each groups are read per instance",
			files: map[string]string{"main.tf": `
resource "azurerm_resource_group" "rg" {
  for_each = toset(["app", "data"])
  name     = "estate-${each.key}"
  location = "westeurope"
}
resource "azurerm_storage_account" "in" {
  name                = "sa"
  resource_group_name = "estate-data"
}
resource "azurerm_storage_account" "out" {
  name                = "sa2"
  resource_group_name = "estate-web"
}
`},
			want: []string{`azurerm_storage_account.out is declared in resource group "estate-web"`},
		},
		{
			name: "no Azure resources at all",
			files: map[string]string{"main.tf": `
resource "aws_s3_bucket" "b" {
  bucket = "b"
}
`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTree(t, tc.files)
			cfg := loadConfigTree(t, dir, nil)
			got := foreignResourceGroupWarnings(context.Background(), cfg, nil)
			details := foreignGroupDetails(got)
			if len(details) != len(tc.want) {
				t.Fatalf("got %d warnings, want %d:\n%s", len(details), len(tc.want), strings.Join(details, "\n"))
			}
			for i, w := range tc.want {
				if !strings.Contains(details[i], w) {
					t.Errorf("warning %d:\n%s\ndoes not contain\n%s", i, details[i], w)
				}
				if !strings.Contains(details[i], `live/MARKERS.md, "Granting an Azure estate"`) {
					t.Errorf("warning %d does not point at its documentation", i)
				}
			}
		})
	}
}

// TestForeignResourceGroupWarningIsWired: the warning reaches a caller of
// ResolveWith, which is the path every live plan takes, and a block outside
// the run's -target scope is not warned about.
func TestForeignResourceGroupWarningIsWired(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.tf": `
resource "azurerm_storage_account" "sa" {
  name                = "sa"
  resource_group_name = "not-ours"
}
`})
	cfg := loadConfigTree(t, dir, nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{})
	if got := foreignGroupDetails(diags); len(got) != 1 {
		t.Fatalf("ResolveWith raised %d foreign-group warnings, want 1:\n%s", len(got), renderDiags(diags))
	}

	_, diags = ResolveWith(context.Background(), cfg, Context{Scope: func(addrs.ConfigResource) bool { return false }})
	if got := foreignGroupDetails(diags); len(got) != 0 {
		t.Fatalf("a block outside the run's scope was warned about:\n%s", strings.Join(got, "\n"))
	}
}
