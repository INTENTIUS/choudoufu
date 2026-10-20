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

// GitHub issue #2119, D4 as #2119 states it: an untaggable, server-minted
// Azure type is held by its record (record-located), as on AWS. No Azure
// type is in the markerless veto, because every admitted azurerm type's id
// is built from arguments, but seven admitted untaggable types make the
// naming argument Optional and Computed at azurerm 5.10.0 and the provider
// mints a GUID when it is left out (azurerm_management_group's create sets
// `groupName = uuid.New().String()` for an empty name). Such an instance
// has no name to compute and no tag to carry a marker, so with the record
// store a live block implies it drops to the record rung through
// [resolver.recordFallback], exactly as aws_autoscaling_group's name_prefix
// instance does; a named one resolves from configuration.
func TestAzureMintedNameIsRecordLocated(t *testing.T) {
	schemas := map[string]providers.Schema{
		// azurerm_management_group at 5.10.0, reduced: name optional and
		// computed, no tags, no identity schema (template class).
		"azurerm_management_group": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":           {Type: cty.String, Computed: true},
			"name":         {Type: cty.String, Optional: true, Computed: true},
			"display_name": {Type: cty.String, Optional: true, Computed: true},
		}}},
	}
	resolve := func(live, body string) *Result {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
  }
`+live+`
}
`+azureProviders[:strings.Index(azureProviders, `provider "azapi"`)]+body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := loadConfig(t, dir, nil)
		res, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
		if diags.HasErrors() {
			t.Fatalf("resolving: %s", diags.Err())
		}
		return res
	}
	const blocks = `
resource "azurerm_management_group" "named" {
  name = "mg-platform"
}
resource "azurerm_management_group" "minted" {
  display_name = "Platform"
}
`
	res := resolve(`
  live {
    estate = "mg"
    record_store "local" {
      path = ".tofu-records"
    }
  }`, blocks)
	if got := azureResolution(t, res, "azurerm_management_group.named"); got.Class != ClassConcrete || got.ImportID != "/providers/Microsoft.Management/managementGroups/mg-platform" {
		t.Errorf("named management group: %s %q", got.Class, got.ImportID)
	}
	minted := azureResolution(t, res, "azurerm_management_group.minted")
	if minted.Class != ClassRecordLocated || !minted.RecordFallback || minted.ImportID != "" {
		t.Errorf("minted management group: %+v, want record-located through the record fallback with no import id of its own", minted)
	}

	// No record store: the omitted name is a value the run cannot know,
	// AWS's NAME_OMITTED, never a guess.
	res = resolve("", blocks)
	if got := azureResolution(t, res, "azurerm_management_group.minted"); got.Class != ClassNeedsDiscovery || got.Cause != DiscoveryNameOmitted {
		t.Errorf("minted management group with no record store: %s %s, want needs-discovery, name omitted", got.Class, got.Cause)
	}
}
