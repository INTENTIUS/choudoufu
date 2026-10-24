// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #2206 (ruling of 2026-10-23 on #2201 item 5): the Azure
// environment, and the data-plane DNS suffixes read from it, are provider
// block values answered the way the subscription is: the block's own
// `environment` wins, a silent block takes the run's, and a block this run
// cannot read is not answered from the run.

func TestAzureDNSSuffixTable(t *testing.T) {
	for _, c := range []struct {
		env   string
		which CloudValue
		want  string
		ok    bool
	}{
		{"public", CloudKeyVaultDNSSuffix, "vault.azure.net", true},
		{"public", CloudManagedHSMDNSSuffix, "managedhsm.azure.net", true},
		{"public", CloudAppConfigurationDNSSuffix, "azconfig.io", true},
		{"public", CloudStorageDNSSuffix, "core.windows.net", true},
		{"usgovernment", CloudKeyVaultDNSSuffix, "vault.usgovcloudapi.net", true},
		{"UsGovernment", CloudStorageDNSSuffix, "core.usgovcloudapi.net", true},
		{"china", CloudAppConfigurationDNSSuffix, "azconfig.azure.cn", true},
		{"china", CloudManagedHSMDNSSuffix, "managedhsm.azure.cn", true},
		{"custom", CloudKeyVaultDNSSuffix, "", false},
		{"german", CloudKeyVaultDNSSuffix, "", false},
		{"", CloudKeyVaultDNSSuffix, "", false},
		{"public", CloudSubscriptionID, "", false},
	} {
		got, ok := AzureDNSSuffix(c.env, c.which)
		if got != c.want || ok != c.ok {
			t.Errorf("AzureDNSSuffix(%q, %s) = %q, %v; want %q, %v", c.env, c.which, got, ok, c.want, c.ok)
		}
	}
}

func azurermProvider() addrs.AbsProviderConfig {
	return addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}
}

func TestAzureEnvironmentFollowsTheSubscriptionsRules(t *testing.T) {
	const run = "usgovernment"
	for name, c := range map[string]struct {
		cloud  CloudContext
		block  string
		want   string
		wantOK bool
	}{
		"block states it, run ignored":        {CloudContext{Environment: run}, "provider \"azurerm\" {\n  features {}\n  environment = \"china\"\n}\n", "china", true},
		"block spelling is case-insensitive":  {CloudContext{}, "provider \"azurerm\" {\n  features {}\n  environment = \"China\"\n}\n", "china", true},
		"silent block takes the run":          {CloudContext{Environment: run}, "provider \"azurerm\" {\n  features {}\n}\n", run, true},
		"no block takes the run":              {CloudContext{Environment: run}, "", run, true},
		"run knows nothing, block silent":     {CloudContext{}, "provider \"azurerm\" {\n  features {}\n}\n", "", false},
		"unreadable block value":              {CloudContext{Environment: run}, "variable \"env\" {\n  type = string\n}\nprovider \"azurerm\" {\n  features {}\n  environment = var.env\n}\n", "", false},
		"custom is not a cloud with suffixes": {CloudContext{}, "provider \"azurerm\" {\n  features {}\n  environment = \"custom\"\n}\n", "", false},
		"metadata host decides the cloud":     {CloudContext{Environment: run}, "provider \"azurerm\" {\n  features {}\n  metadata_host = \"management.example.test\"\n}\n", "", false},
		"run names an unknown cloud":          {CloudContext{Environment: "german"}, "", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, _ := resolveAzureCfg(t, c.cloud, c.block+dnsZone)
			got, ok := ProviderCloudValue(cfg, azurermProvider(), CloudEnvironment, c.cloud)
			if got != c.want || ok != c.wantOK {
				t.Errorf("environment = %q, %v; want %q, %v", got, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestAzureDNSSuffixIsAnsweredFromTheProviderBlock(t *testing.T) {
	cloud := CloudContext{Environment: "usgovernment"}
	cfg, _ := resolveAzureCfg(t, cloud, `
provider "azurerm" {
  features {}
}
provider "azurerm" {
  alias       = "cn"
  features {}
  environment = "china"
}
`+dnsZone)
	cn := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm"), Alias: "cn"}
	for _, c := range []struct {
		provider addrs.AbsProviderConfig
		which    CloudValue
		want     string
	}{
		{azurermProvider(), CloudKeyVaultDNSSuffix, "vault.usgovcloudapi.net"},
		{azurermProvider(), CloudStorageDNSSuffix, "core.usgovcloudapi.net"},
		{cn, CloudKeyVaultDNSSuffix, "vault.azure.cn"},
		{cn, CloudAppConfigurationDNSSuffix, "azconfig.azure.cn"},
	} {
		got, ok := ProviderCloudValue(cfg, c.provider, c.which, cloud)
		if !ok || got != c.want {
			t.Errorf("%s alias %q: %s = %q, %v; want %q", c.provider.Provider, c.provider.Alias, c.which, got, ok, c.want)
		}
	}
	// Nothing told the run which cloud: no suffix, so an id that embeds one
	// needs discovery rather than a guess at the public cloud.
	cfg, _ = resolveAzureCfg(t, CloudContext{}, "provider \"azurerm\" {\n  features {}\n}\n"+dnsZone)
	if got, ok := ProviderCloudValue(cfg, azurermProvider(), CloudKeyVaultDNSSuffix, CloudContext{}); ok {
		t.Errorf("suffix with no environment = %q, want no answer", got)
	}
}

// A data-plane id component reads its host's suffix through the same
// machinery as the subscription: unanswered, the instance needs discovery
// and names the suffix; answered, it joins the id. The type is a made-up row
// so no generated entry is disturbed.
func TestAzureDNSSuffixComponentResolvesAndRefuses(t *testing.T) {
	const typ = "azurerm_dataplane_probe"
	if _, had := DefaultTable[typ]; had {
		t.Fatalf("%s is already in the table", typ)
	}
	DefaultTable[typ] = TypeIdentity{
		Type:           typ,
		NonAWSProvider: true,
		Components: []Component{
			{Literal: "https://"},
			{Attrs: []string{"name"}, IdentityAttr: SameNameIdentity},
			{Literal: "."},
			{Cloud: CloudKeyVaultDNSSuffix},
		},
		ImportSyntax:  "https://{name}.{suffix}",
		IdentityAttrs: []string{"id"},
	}
	t.Cleanup(func() { delete(DefaultTable, typ) })
	const probe = `
resource "azurerm_dataplane_probe" "p" {
  name = "vault1"
}
`
	resolve := func(cloud CloudContext, body string) Resolution {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
  }
}
`+body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := loadConfig(t, dir, nil)
		result, diags := ResolveWith(context.Background(), cfg, Context{Cloud: cloud, Schemas: map[string]providers.Schema{typ: azurermSchema("name")}})
		if diags.HasErrors() {
			t.Fatalf("refused: %s", diags.Err())
		}
		return azureResolution(t, result, "azurerm_dataplane_probe.p")
	}

	got := resolve(CloudContext{Environment: "china"}, "provider \"azurerm\" {\n  features {}\n}\n"+probe)
	if got.Class != ClassConcrete || got.ImportID != "https://vault1.vault.azure.cn" {
		t.Errorf("china = %s (%s), want CONCRETE https://vault1.vault.azure.cn", got, got.ImportID)
	}
	got = resolve(CloudContext{Environment: "china"}, "provider \"azurerm\" {\n  features {}\n  environment = \"public\"\n}\n"+probe)
	if got.Class != ClassConcrete || got.ImportID != "https://vault1.vault.azure.net" {
		t.Errorf("block wins = %s (%s), want CONCRETE https://vault1.vault.azure.net", got, got.ImportID)
	}
	got = resolve(CloudContext{}, "provider \"azurerm\" {\n  features {}\n}\n"+probe)
	if got.Class != ClassNeedsDiscovery || got.Cause != DiscoveryCloudUnknown || len(got.CauseArgs) == 0 || got.CauseArgs[0] != string(CloudKeyVaultDNSSuffix) {
		t.Errorf("no environment = %s cause %s %v, want NEEDS_DISCOVERY naming the suffix", got, got.Cause, got.CauseArgs)
	}
}
