// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

func TestVaultNameFromARMID(t *testing.T) {
	id := "/subscriptions/s/resourceGroups/g/providers/Microsoft.KeyVault/vaults/my-vault"
	if n, ok := vaultNameFromARMID(id); !ok || n != "my-vault" {
		t.Errorf("got %q, %v", n, ok)
	}
	if _, ok := vaultNameFromARMID("/subscriptions/s"); ok {
		t.Error("an id naming no vault gave a name")
	}
}

func TestKeyVaultNamesFor(t *testing.T) {
	dir := t.TempDir()
	src := `
provider "azurerm" {
  features {}
}
variable "dyn" {
  type = string
}
resource "azurerm_key_vault" "main" {
  name = "vault-main"
}
resource "azurerm_key_vault" "dynamic" {
  name = var.dyn
}
resource "azurerm_key_vault_secret" "viaref" {
  key_vault_id = azurerm_key_vault.main.id
}
resource "azurerm_key_vault_key" "viaid" {
  key_vault_id = "/subscriptions/s/resourceGroups/g/providers/Microsoft.KeyVault/vaults/Vault-Other"
}
resource "azurerm_key_vault_certificate" "unknown" {
  key_vault_id = var.dyn
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	config := liveTestLoadConfig(t, dir)
	addr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}
	names, problems := keyVaultNamesFor(t.Context(), config, addr)
	if strings.Join(names, ",") != "Vault-Other,vault-main" {
		t.Errorf("names = %v", names)
	}
	if len(problems) != 2 {
		t.Errorf("problems = %q, want the dynamic vault and the unreadable key_vault_id", problems)
	}
	other := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm"), Alias: "other"}
	if names, _ := keyVaultNamesFor(t.Context(), config, other); len(names) != 0 {
		t.Errorf("another provider configuration was given vaults: %v", names)
	}
}
