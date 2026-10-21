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
	"github.com/intentius/choudoufu/internal/configs"
)

// resolveAzureCfg is resolveAzureIn returning the configuration too, for
// asking [ProviderCloudValue] about the same provider blocks.
func resolveAzureCfg(t *testing.T, cloud CloudContext, body string) (*configs.Config, *Result) {
	t.Helper()
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
	result, diags := ResolveWith(context.Background(), cfg, Context{Cloud: cloud, Schemas: azureSchemas()})
	if diags.HasErrors() {
		t.Fatalf("refused: %s", diags.Err())
	}
	return cfg, result
}

// GitHub issue #2135: a record's provider components are answered by
// [ProviderCloudValue], which has to give the answer the identity route
// gives for the same provider configuration, or a record would name an
// object under another subscription than the plan computed. The external
// source each case is checked against is the identity route's own
// resolution of a resource under that provider block: the subscription
// segment of the ARM id it renders.
func TestProviderCloudValueAgreesWithTheIdentityRoute(t *testing.T) {
	for name, c := range map[string]struct {
		cloud CloudContext
		body  string
		addr  string
	}{
		"block states it, environment ignored": {CloudContext{SubscriptionID: azureEnvSub}, azureProviders + dnsZone, "azurerm_dns_zone.z"},
		"silent block, environment":            {CloudContext{SubscriptionID: azureEnvSub}, "provider \"azurerm\" {\n  features {}\n}\n" + dnsZone, "azurerm_dns_zone.z"},
		"no block, environment":                {CloudContext{SubscriptionID: azureEnvSub}, dnsZone, "azurerm_dns_zone.z"},
		"aliased block keeps its own": {CloudContext{SubscriptionID: azureEnvSub}, `
provider "azurerm" {
  features {}
}
provider "azurerm" {
  alias           = "other"
  features {}
  subscription_id = "00000000-0000-0000-0000-0000000000aa"
}
resource "azurerm_dns_zone" "o" {
  provider            = azurerm.other
  name                = "example.com"
  resource_group_name = "rg-prod"
}
`, "azurerm_dns_zone.o"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, result := resolveAzureCfg(t, c.cloud, c.body)
			res := azureResolution(t, result, c.addr)
			if res.Class != ClassConcrete {
				t.Fatalf("%s = %s, want CONCRETE", c.addr, res)
			}
			want := strings.Split(res.ImportID, "/")[2]
			got, ok := ProviderCloudValue(cfg, res.cloudScope.provider, CloudSubscriptionID, c.cloud)
			if !ok || got != want {
				t.Errorf("ProviderCloudValue = %q, %v; the identity route named %q (%s)", got, ok, want, res.ImportID)
			}
		})
	}
}

// A block that sets subscription_id to something this run cannot evaluate
// leaves the identity route unable to name the object, and the record rung
// with nothing to record: neither answers from the environment.
func TestProviderCloudValueUnresolvableBlockAnswersNothing(t *testing.T) {
	cloud := CloudContext{SubscriptionID: azureEnvSub}
	cfg, result := resolveAzureCfg(t, cloud, `
variable "sub" {
  type = string
}
provider "azurerm" {
  features {}
  subscription_id = var.sub
}
`+dnsZone)
	wantSubscriptionUnknown(t, result, "azurerm_dns_zone.z")
	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}
	if got, ok := ProviderCloudValue(cfg, provider, CloudSubscriptionID, cloud); ok {
		t.Errorf("ProviderCloudValue = %q, want no answer", got)
	}
	// A zero provider address is no provider configuration at all, not
	// the implied empty one, and is not answered from the environment.
	if got, ok := ProviderCloudValue(cfg, addrs.AbsProviderConfig{}, CloudSubscriptionID, cloud); ok {
		t.Errorf("ProviderCloudValue for no provider = %q, want no answer", got)
	}
}

// LocatedProviderIdentity is all or nothing, as LocatedIdentity is.
func TestLocatedProviderIdentityIsAllOrNothing(t *testing.T) {
	plan := LocatedIdentityPlan{Components: []string{"name"}, ProviderComponents: map[string]CloudValue{"subscription_id": CloudSubscriptionID}}
	if _, ok := LocatedProviderIdentity(plan, nil); ok {
		t.Error("no provider values recorded a partial identity")
	}
	none := func(CloudValue) (string, bool) { return "", false }
	if _, ok := LocatedProviderIdentity(plan, none); ok {
		t.Error("an unknown subscription recorded a partial identity")
	}
	sub := func(which CloudValue) (string, bool) { return azureSub, which == CloudSubscriptionID }
	got, ok := LocatedProviderIdentity(plan, sub)
	if !ok || got["subscription_id"] != azureSub || len(got) != 1 {
		t.Errorf("got %v, %v", got, ok)
	}
	if got, ok := LocatedProviderIdentity(LocatedIdentityPlan{Components: []string{"name"}}, nil); !ok || got != nil {
		t.Errorf("a plan with no provider components: %v, %v", got, ok)
	}
}
