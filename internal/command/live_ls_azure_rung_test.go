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

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestLiveLsRungAsksTheStampForAzure is GitHub issue #1120 unit 22: an
// azapi_resource's schema always has a tags argument, so live-ls's rung,
// asking markers.Taggable of the schema, called every azapi block
// marker-carried. It now asks what the stamp asks: an azapi subnet at
// 2024-05-01, which takes no tags at that API version, is
// declaration-carried, per instance of a for_each block too; an azapi
// virtual network at the same version is marker-carried (no rung); an
// azapi type azapi's data does not know reads as untaggable, as the stamp
// leaves it unstamped; and azurerm answers from the arm-tags predicate.
func TestLiveLsRungAsksTheStampForAzure(t *testing.T) {
	dir := t.TempDir()
	src := `
terraform {
  required_providers {
    azapi = {
      source = "azure/azapi"
    }
  }
}
resource "azapi_resource" "subnet" {
  type = "Microsoft.Network/virtualNetworks/subnets@2024-05-01"
  name = "a"
}
resource "azapi_resource" "net" {
  type = "Microsoft.Network/virtualNetworks@2024-05-01"
  name = "b"
}
resource "azapi_resource" "each" {
  for_each = {
    sub = "Microsoft.Network/virtualNetworks/subnets@2024-05-01"
    net = "Microsoft.Network/virtualNetworks@2024-05-01"
  }
  type = each.value
  name = each.key
}
resource "azapi_resource" "unknown" {
  type = "Microsoft.Nothing/nowhere@2024-05-01"
  name = "c"
}
resource "azurerm_subnet" "s" {
  name = "s"
}
resource "azurerm_virtual_network" "v" {
  name = "v"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	config := liveTestLoadConfig(t, dir)

	tags := &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
	name := &configschema.Attribute{Type: cty.String, Required: true}
	schemas := map[string]providers.Schema{
		"azapi_resource": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"name": name,
			"type": {Type: cty.String, Required: true},
			"body": {Type: cty.DynamicPseudoType, Optional: true},
			"tags": tags,
		}}},
		"azurerm_subnet":          {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{"name": name}}},
		"azurerm_virtual_network": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{"name": name, "tags": tags}}},
	}

	for _, tc := range []struct {
		addr string
		want string // "" is marker-carried: no rung
		in   string // a phrase the detail must carry
	}{
		{"azapi_resource.subnet", "declaration-carried", "takes no tags"},
		{"azapi_resource.net", "", ""},
		{`azapi_resource.each["sub"]`, "declaration-carried", "takes no tags"},
		{`azapi_resource.each["net"]`, "", ""},
		{"azapi_resource.unknown", "declaration-carried", "does not know"},
		{"azurerm_subnet.s", "declaration-carried", "no settable tags argument"},
		{"azurerm_virtual_network.v", "", ""},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			res := liveLsTestResolution(t, tc.addr, identity.ClassConcrete)
			inst := liveLsInstanceOf(t.Context(), config, res.Addr, schemas)
			rung, detail, ok := liveLsRung(res, schemas, inst)
			if tc.want == "" {
				if ok {
					t.Fatalf("got rung=%q detail=%q, want marker-carried (no rung)", rung, detail)
				}
				return
			}
			if !ok || rung != tc.want || !strings.Contains(detail, tc.in) {
				t.Fatalf("got rung=%q detail=%q ok=%v, want %s carrying %q", rung, detail, ok, tc.want, tc.in)
			}
		})
	}
}
