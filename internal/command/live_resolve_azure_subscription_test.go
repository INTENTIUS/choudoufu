// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/pkg/azsweep"
)

// GitHub issue #2133, finding 4: live-plan's identity pass reads
// ARM_SUBSCRIPTION_ID for an azurerm block that names no subscription, as
// the provider does and as the Resource Graph sweep already did. Before
// the fix the sweep swept that subscription while identity left the
// template-routed resource NEEDS_DISCOVERY, so no ownership read reached
// it and the overwrite refusal never fired.
func TestLiveResolveReadsTheSubscriptionFromTheEnvironment(t *testing.T) {
	const sub = "00000000-0000-0000-0000-0000000000ee"
	for _, name := range azsweep.EnvNames() {
		t.Setenv(name, "")
	}
	t.Setenv("ARM_SUBSCRIPTION_ID", sub)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
  }
}
provider "azurerm" {
  features {}
}
resource "azurerm_dns_zone" "z" {
  name                = "example.com"
  resource_group_name = "rg-prod"
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := providerManagedLoadConfig(t, dir)
	schemas := map[string]providers.Schema{
		"azurerm_dns_zone": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":                  {Type: cty.String, Computed: true},
			"name":                {Type: cty.String, Required: true},
			"resource_group_name": {Type: cty.String, Required: true},
			"tags":                {Type: cty.Map(cty.String), Optional: true},
		}}},
	}

	res, diags := liveResolve(t.Context(), cfg, nil, schemas, nil, nil)
	if diags.HasErrors() {
		t.Fatalf("liveResolve refused: %s", diags.Err())
	}
	want := "/subscriptions/" + sub + "/resourceGroups/rg-prod/providers/Microsoft.Network/dnsZones/example.com"
	for _, r := range res.All() {
		if r.Addr.String() != "azurerm_dns_zone.z" {
			continue
		}
		if r.Class != identity.ClassConcrete || r.ImportID != want {
			t.Fatalf("azurerm_dns_zone.z = %s (%s), want CONCRETE %s", r, r.ImportID, want)
		}
		if got := azsweep.EnvSubscriptionID(os.Getenv); got != sub {
			t.Fatalf("the sweep's environment subscription is %q, identity used %q", got, sub)
		}
		return
	}
	t.Fatal("azurerm_dns_zone.z did not resolve")
}
