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

// GitHub issue #2119: no Azure type is in the markerless veto, because
// every admitted azurerm type's id is built from arguments. Seven admitted
// untaggable types make the naming argument Optional and Computed at
// azurerm 5.10.0, and the provider mints a GUID when it is left out
// (azurerm_management_group's create sets `groupName = uuid.New().String()`
// when the name is empty). Such a block is refused by name rather than created with an id
// nothing could find again: there is no tag to carry a marker. Pinned here
// so a change that starts admitting the omitted case has to say how the
// object would be found.
func TestAzureOptionalNameOmittedIsRefusedByName(t *testing.T) {
	schemas := map[string]providers.Schema{
		// azurerm_management_group at 5.10.0, reduced: name optional and
		// computed, no tags, no identity schema (template class).
		"azurerm_management_group": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":           {Type: cty.String, Computed: true},
			"name":         {Type: cty.String, Optional: true, Computed: true},
			"display_name": {Type: cty.String, Optional: true, Computed: true},
		}}},
	}
	resolve := func(body string) (*Result, string) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
  }
}
`+azureProviders[:strings.Index(azureProviders, `provider "azapi"`)]+body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := loadConfig(t, dir, nil)
		res, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
		if diags.HasErrors() {
			return res, diags.Err().Error()
		}
		return res, ""
	}

	res, errs := resolve(`
resource "azurerm_management_group" "named" {
  name = "mg-platform"
}
`)
	if errs != "" {
		t.Fatalf("a named management group: %s", errs)
	}
	if got := azureResolution(t, res, "azurerm_management_group.named").ImportID; got != "/providers/Microsoft.Management/managementGroups/mg-platform" {
		t.Errorf("named management group resolved to %q", got)
	}

	_, errs = resolve(`
resource "azurerm_management_group" "minted" {
  display_name = "Platform"
}
`)
	if !strings.Contains(errs, "Identity argument not set") || !strings.Contains(errs, "azurerm_management_group.minted") {
		t.Errorf("a management group with no name: %q, want the refusal naming the address", errs)
	}
}
