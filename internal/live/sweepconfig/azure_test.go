// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package sweepconfig

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/pkg/azsweep"
)

// TestAzureMarkedIdentityIsRefused: a block that sets subscription_id from
// a sensitive value builds no credential, and the refusal names it.
func TestAzureMarkedIdentityIsRefused(t *testing.T) {
	a := azsweep.ProviderAttrs(azsweep.ProviderAzureRM, cty.ObjectVal(map[string]cty.Value{
		"subscription_id": cty.StringVal("11111111-1111-1111-1111-111111111111").Mark(marks.Sensitive),
	}), true)
	if _, err := AzureSweep(context.Background(), a); err == nil || !strings.Contains(err.Error(), "subscription_id") {
		t.Errorf("AzureSweep = %v, want a refusal naming subscription_id", err)
	}
}

// TestAzureEndpointBlockCloud: azapi's endpoint block overrides the
// public cloud field by field, without writing through to azcore's preset.
func TestAzureEndpointBlockCloud(t *testing.T) {
	a := azsweep.Attrs{Provider: azsweep.ProviderAzAPI, ResourceManagerEndpoint: "https://arm.example/", ResourceManagerAudience: "https://arm.example/"}
	cfg, err := azureCloudConfig(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	rm := cfg.Services[cloud.ResourceManager]
	if rm.Endpoint != "https://arm.example/" || rm.Audience != "https://arm.example/" || cfg.ActiveDirectoryAuthorityHost != cloud.AzurePublic.ActiveDirectoryAuthorityHost {
		t.Errorf("cloud = %+v", cfg)
	}
	if cloud.AzurePublic.Services[cloud.ResourceManager].Endpoint == "https://arm.example/" {
		t.Errorf("the override wrote through to azcore's AzurePublic")
	}
}

// TestAzureLocalCloudNeverReachesAPublicAuthority is the never-public-cloud
// rule (GitHub issue #2281): a block whose Resource Manager endpoint is
// local and whose authority host is Azure's builds no credential, and one
// whose cloud is local throughout never falls back to the Azure CLI or a
// managed identity when it names no client secret.
func TestAzureLocalCloudNeverReachesAPublicAuthority(t *testing.T) {
	yes := true
	public := azsweep.Attrs{Provider: azsweep.ProviderAzAPI, ResourceManagerEndpoint: "https://localhost:4577/", ResourceManagerAudience: "https://localhost:4577/",
		TenantID: "t", ClientID: "c", ClientSecret: "s"}
	if _, _, _, err := AzureCredential(context.Background(), public, "the test"); err == nil || !strings.Contains(err.Error(), "authority host") {
		t.Errorf("a local Resource Manager with Azure's authority host: err = %v, want a refusal naming the authority host", err)
	}
	local := azsweep.Attrs{Provider: azsweep.ProviderAzAPI, Environment: "custom", ActiveDirectoryAuthorityHost: "https://localhost:4577/",
		ResourceManagerEndpoint: "https://localhost:4577/", ResourceManagerAudience: "https://localhost:4577/", UseCLI: &yes, UseMSI: &yes}
	if _, _, creds, err := AzureCredential(context.Background(), local, "the test"); err == nil {
		t.Errorf("a local cloud with no client credential built %s; the Azure CLI and managed identity fetch tokens from Azure and must stay off", creds)
	}
	local.TenantID, local.ClientID, local.ClientSecret = "t", "c", "s"
	if _, ep, creds, err := AzureCredential(context.Background(), local, "the test"); err != nil || ep.ResourceManager != "https://localhost:4577/" || !strings.Contains(strings.ToLower(creds.Method), "secret") {
		t.Errorf("a local cloud with a client secret: %v, %+v, %s; want the client secret credential on the local endpoint", err, ep, creds)
	}
}

// TestAzAPICustomEnvironment: azapi's environment = "custom" is a cloud of
// its three endpoint values alone, as azapi 2.13.0 builds it
// (provider.go:635-658), and a missing one is refused by name rather than
// filled from the public cloud (GitHub issue #2133, the endpoint lock).
func TestAzAPICustomEnvironment(t *testing.T) {
	env := map[string]string{
		"ARM_ENVIRONMENT":                     "custom",
		"ARM_RESOURCE_MANAGER_ENDPOINT":       "https://localhost:45770",
		"ARM_RESOURCE_MANAGER_AUDIENCE":       "https://localhost:45770/",
		"ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST": "https://localhost:45770/",
		"ARM_DISABLE_INSTANCE_DISCOVERY":      "TRUE",
	}
	a := azsweep.Attrs{Provider: azsweep.ProviderAzAPI}.WithEnv(func(k string) string { return env[k] })
	cfg, err := azureCloudConfig(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	rm := cfg.Services[cloud.ResourceManager]
	if rm.Endpoint != "https://localhost:45770" || rm.Audience != "https://localhost:45770/" || cfg.ActiveDirectoryAuthorityHost != "https://localhost:45770/" || len(cfg.Services) != 1 {
		t.Errorf("cloud = %+v", cfg)
	}
	if !on(a.DisableInstanceDiscovery, false) || !azureAuthConfig(a, cfg).DisableInstanceDiscovery {
		t.Errorf("ARM_DISABLE_INSTANCE_DISCOVERY=TRUE did not turn discovery off: %v", a.DisableInstanceDiscovery)
	}

	delete(env, "ARM_ACTIVE_DIRECTORY_AUTHORITY_HOST")
	a = azsweep.Attrs{Provider: azsweep.ProviderAzAPI}.WithEnv(func(k string) string { return env[k] })
	if _, err := azureCloudConfig(context.Background(), a); err == nil || !strings.Contains(err.Error(), "active_directory_authority_host") {
		t.Errorf("custom without its authority = %v, want a refusal naming active_directory_authority_host", err)
	}
	// azurerm has no "custom": go-azure-sdk's environments.FromName refuses it.
	rmAttrs := azsweep.Attrs{Provider: azsweep.ProviderAzureRM}.WithEnv(func(k string) string { return env[k] })
	if _, err := azureCloudConfig(context.Background(), rmAttrs); err == nil {
		t.Error(`azurerm with environment "custom" and no metadata_host built a cloud`)
	}
	// The block's own disable_instance_discovery wins over the environment.
	val := cty.ObjectVal(map[string]cty.Value{"disable_instance_discovery": cty.False})
	if b := azsweep.ProviderAttrs(azsweep.ProviderAzAPI, val, true).WithEnv(func(k string) string { return env[k] }); on(b.DisableInstanceDiscovery, true) {
		t.Error("ARM_DISABLE_INSTANCE_DISCOVERY overrode the block's disable_instance_discovery = false")
	}
}

