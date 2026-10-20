// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// decodeRecordStoreSrc decodes one record_store block from src.
func decodeRecordStoreSrc(t *testing.T, src string) (*LiveRecordStore, hcl.Diagnostics) {
	t.Helper()
	f, diags := hclsyntax.ParseConfig([]byte(src), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatal(diags.Error())
	}
	content, diags := f.Body.Content(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{{Type: "record_store", LabelNames: []string{"type"}}}})
	if diags.HasErrors() || len(content.Blocks) != 1 {
		t.Fatalf("parsing: %s", diags.Error())
	}
	return decodeRecordStoreBlock(content.Blocks[0], "prod")
}

// TestRecordStoreAzureRM is the Azure epic's record store block (#1120):
// stock azurerm backend arguments, three required, and each refused on a
// backend that has no use for it.
func TestRecordStoreAzureRM(t *testing.T) {
	rs, diags := decodeRecordStoreSrc(t, `
record_store "azurerm" {
  storage_account_name = "acmerecords"
  container_name       = "records"
  resource_group_name  = "records-rg"
  subscription_id      = "sub"
  client_certificate   = "cert"
  use_azuread_auth     = true
  use_cli              = false
  key_prefix           = "tofu-records/prod"
  allow_insecure       = ["lifecycle_management"]
}`)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	a := rs.AzureRM
	if rs.Type != "azurerm" || a.StorageAccountName != "acmerecords" || a.ContainerName != "records" || a.ResourceGroupName != "records-rg" || a.ClientCertificate != "cert" {
		t.Errorf("decoded %+v", rs)
	}
	if a.UseAzureADAuth == nil || !*a.UseAzureADAuth || a.UseCLI == nil || *a.UseCLI || a.UseMSI != nil {
		t.Errorf("booleans: azuread %v cli %v msi %v; an unset one must stay nil so its ARM_* default applies", a.UseAzureADAuth, a.UseCLI, a.UseMSI)
	}

	for _, tc := range []struct {
		name, src, want string
	}{
		{"missing resource group", `record_store "azurerm" {
  storage_account_name = "acmerecords"
  container_name       = "records"
}`, "Missing record_store resource_group_name"},
		{"bad account name", `record_store "azurerm" {
  storage_account_name = "Acme_Records"
  container_name       = "records"
  resource_group_name  = "rg"
}`, "Invalid record_store storage_account_name"},
		{"bucket on azurerm", `record_store "azurerm" {
  storage_account_name = "acmerecords"
  container_name       = "records"
  resource_group_name  = "rg"
  bucket               = "b"
}`, "has no meaning for record_store \"azurerm\""},
		{"kubernetes argument on azurerm", `record_store "azurerm" {
  storage_account_name = "acmerecords"
  container_name       = "records"
  resource_group_name  = "rg"
  namespace            = "ns"
}`, "Invalid argument for the azurerm record store"},
		{"azurerm argument on s3", `record_store "s3" {
  bucket       = "b"
  sas_token    = "t"
}`, "Azure Storage container"},
		{"another store's waiver", `record_store "azurerm" {
  storage_account_name = "acmerecords"
  container_name       = "records"
  resource_group_name  = "rg"
  allow_insecure       = ["versioning"]
}`, "Invalid record_store allow_insecure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, diags := decodeRecordStoreSrc(t, tc.src)
			if !diags.HasErrors() || !strings.Contains(diags.Error(), tc.want) {
				t.Errorf("diagnostics %q, want one containing %q", diags.Error(), tc.want)
			}
		})
	}

	// client_certificate belongs to both kubernetes and azurerm, and is
	// refused on neither.
	if _, diags := decodeRecordStoreSrc(t, `record_store "kubernetes" {
  client_certificate = "pem"
}`); diags.HasErrors() {
		t.Errorf("client_certificate on kubernetes: %s", diags.Error())
	}
}
