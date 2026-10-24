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

	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #2206: a data-plane id's host is one segment of a parent's
// ARM id plus the cloud's suffix, and an App Configuration key is escaped as
// azurerm's url.URL builder escapes it. The probe row has the components
// tools/azurerm-id-gen writes for azurerm_app_configuration_key.
func TestAzureDataPlaneComponentsResolveAndRefuse(t *testing.T) {
	const typ = "azurerm_dataplane_segment_probe"
	if _, had := DefaultTable[typ]; had {
		t.Fatalf("%s is already in the table", typ)
	}
	DefaultTable[typ] = TypeIdentity{
		Type:           typ,
		NonAWSProvider: true,
		Components: []Component{
			{Literal: "https://"},
			{Attrs: []string{"configuration_store_id"}, ARMSegmentIndex: 8, ARMSegmentAfter: "configurationStores"},
			{Literal: "."},
			{Cloud: CloudAppConfigurationDNSSuffix},
			{Literal: "/kv/"},
			{Attrs: []string{"key"}, URLEscape: "path"},
			{Literal: "?label="},
			{Attrs: []string{"label"}, URLEscape: "query", OmitIfAbsent: true},
		},
		ImportSyntax:  "https://{store}.{suffix}/kv/{key}?label={label}",
		IdentityAttrs: []string{"id"},
	}
	t.Cleanup(func() { delete(DefaultTable, typ) })

	const store = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.AppConfiguration/configurationStores/store1"
	resolve := func(cloud CloudContext, args string) (Resolution, string) {
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
resource "azurerm_dataplane_segment_probe" "p" {
`+args+`
}
`), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := loadConfig(t, dir, nil)
		schema := azurermSchema("configuration_store_id", "key", "label")
		schema.Block.Attributes["label"].Required = false
		result, diags := ResolveWith(context.Background(), cfg, Context{Cloud: cloud, Schemas: map[string]providers.Schema{typ: schema}})
		msg := ""
		if diags.HasErrors() {
			msg = diags.Err().Error()
		}
		if result != nil {
			for _, r := range result.All() {
				if r.Addr.String() == typ+".p" {
					return r, msg
				}
			}
		}
		return Resolution{}, msg
	}
	args := func(storeID, key, label string) string {
		s := "  configuration_store_id = \"" + storeID + "\"\n  key = \"" + key + "\"\n"
		if label != "" {
			s += "  label = \"" + label + "\"\n"
		}
		return s
	}
	for name, c := range map[string]struct {
		env, args, want string
	}{
		"public, no label":     {"public", args(store, "app/setting", ""), "https://store1.azconfig.io/kv/app%2Fsetting?label="},
		"china, label escaped": {"china", args(store, "a b", "blue green"), "https://store1.azconfig.azure.cn/kv/a%20b?label=blue+green"},
		"usgovernment":         {"usgovernment", args(store, "k", "l"), "https://store1.azconfig.azure.us/kv/k?label=l"},
		"store segment is case-insensitive about its marker": {"public", args(strings.Replace(store, "configurationStores", "CONFIGURATIONSTORES", 1), "k", ""), "https://store1.azconfig.io/kv/k?label="},
	} {
		t.Run(name, func(t *testing.T) {
			got, msg := resolve(CloudContext{Environment: c.env}, c.args)
			if got.Class != ClassConcrete || got.ImportID != c.want {
				t.Errorf("= %s (%s) %s, want CONCRETE %s", got, got.ImportID, msg, c.want)
			}
		})
	}

	// Red: an id that is not a configuration store's has no such segment, so
	// no host is read out of it.
	bad := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv1"
	got, msg := resolve(CloudContext{Environment: "public"}, args(bad, "k", ""))
	if got.Class == ClassConcrete || !strings.Contains(msg, "configurationStores") {
		t.Errorf("a vault id = %s (%s) %q, want a refusal naming configurationStores", got, got.ImportID, msg)
	}
}
