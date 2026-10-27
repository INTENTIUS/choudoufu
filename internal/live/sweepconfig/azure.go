// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package sweepconfig

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	"github.com/intentius/choudoufu/internal/backend/remote-state/azure/auth"
	"github.com/intentius/choudoufu/internal/httpclient"
	"github.com/intentius/choudoufu/pkg/azsweep"
	"github.com/intentius/choudoufu/pkg/gcpsweep"
)

// AzureSweep builds the Azure estate sweep's client from a provider
// block's arguments, with the provider's environment fallbacks applied
// ([azsweep.Attrs.WithEnv]). The credential chain picks its method now, so
// a configuration that names no usable credential fails here rather than
// per subscription; no token is requested until a search.
func AzureSweep(ctx context.Context, a azsweep.Attrs) (*azsweep.Client, error) {
	cfg, err := azureConfig(ctx, a, "the estate sweep")
	if err != nil {
		return nil, err
	}
	return azsweep.New(cfg)
}

// AzureTagger builds the post-create marker write's Tagger from a provider
// block's arguments, through the same credential chain [AzureSweep] uses.
func AzureTagger(ctx context.Context, a azsweep.Attrs) (*azsweep.Tagger, error) {
	cfg, err := azureConfig(ctx, a, "the post-create marker write")
	if err != nil {
		return nil, err
	}
	return azsweep.NewTagger(cfg), nil
}

// AzureKeyVault builds the Key Vault listing's client from a provider
// block's arguments, through the credential chain [AzureSweep] uses, so
// the listing calls as the provider does.
func AzureKeyVault(ctx context.Context, a azsweep.Attrs) (*azsweep.KeyVaultClient, error) {
	cfg, err := azureConfig(ctx, a, "the Key Vault listing")
	if err != nil {
		return nil, err
	}
	return azsweep.NewKeyVault(cfg), nil
}

func azureConfig(ctx context.Context, a azsweep.Attrs, who string) (azsweep.Config, error) {
	a = a.WithEnv(os.Getenv)
	cred, ep, creds, err := AzureCredential(ctx, a, who)
	if err != nil {
		return azsweep.Config{}, err
	}
	return azsweep.Config{
		Markers:      Markers,
		Credential:   cred,
		Endpoints:    ep,
		HTTPClient:   httpclient.New(ctx),
		Credentials:  creds,
		Subscription: a.SubscriptionID,
	}, nil
}

// AzureCredential is the credential chain the Azure sweep, tagger and Key
// Vault listing share, for arguments that already have the environment
// applied ([azsweep.Attrs.WithEnv]); who names the caller in the errors.
//
// It is OpenTofu's azurerm state backend chain, which tries what the
// provider tries, in nearly its order: client certificate, client secret,
// Azure DevOps OIDC, OIDC, managed identity, AKS workload identity, then the
// Azure CLI. For a block whose cloud is local (an emulator: its metadata
// host or Resource Manager endpoint on a loopback host) the chain keeps
// only the credentials that ask the block's own authority host for a token,
// and that host must be local too: the Azure CLI, managed identity, AKS
// workload identity and Azure DevOps OIDC are turned off, since each would
// fetch a token from Azure itself or a metadata server, and a local cloud
// with a public authority host is refused.
func AzureCredential(ctx context.Context, a azsweep.Attrs, who string) (azcore.TokenCredential, azsweep.Endpoints, azsweep.Credentials, error) {
	if refused := a.RefusedMarks(); len(refused) > 0 {
		return nil, azsweep.Endpoints{}, azsweep.Credentials{}, fmt.Errorf("the %s provider block sets %s from a sensitive value, which %s does not read; set it from a non-sensitive value so %s calls as and where the provider does", a.ProviderName(), strings.Join(refused, " and "), who, who)
	}
	cfg, err := azureCloudConfig(ctx, a)
	if err != nil {
		return nil, azsweep.Endpoints{}, azsweep.Credentials{}, fmt.Errorf("the %s provider block's cloud: %w", a.ProviderName(), err)
	}
	rm := cfg.Services[cloud.ResourceManager]
	ac := azureAuthConfig(a, cfg)
	if azureLocal(a, rm.Endpoint) {
		if !gcpsweep.IsLoopback(cfg.ActiveDirectoryAuthorityHost) {
			return nil, azsweep.Endpoints{}, azsweep.Credentials{}, fmt.Errorf("the %s provider block points Resource Manager at a local endpoint (%s) but its authority host %q is not local, so %s's token would be requested from a public cloud; point active_directory_authority_host (or metadata_host) at the emulator too", a.ProviderName(), rm.Endpoint, cfg.ActiveDirectoryAuthorityHost, who)
		}
		ac.AzureCLIAuthConfig.CLIAuthEnabled = false
		ac.MSIAuthConfig.UseMsi = false
		ac.WorkloadIdentityAuthConfig.UseAKSWorkloadIdentity = false
		ac.ADOAuthConfig.ADOServiceConnectionId = ""
		ac.OIDCAuthConfig.OIDCRequestURL = ""
		ac.OIDCAuthConfig.OIDCRequestToken = ""
	}
	method, err := auth.GetAuthMethod(ctx, ac)
	if err != nil {
		return nil, azsweep.Endpoints{}, azsweep.Credentials{}, fmt.Errorf("the %s provider block names no credential %s can use: %w", a.ProviderName(), who, err)
	}
	cred, err := method.Construct(ctx, ac)
	if err != nil {
		return nil, azsweep.Endpoints{}, azsweep.Credentials{}, fmt.Errorf("building the %s credential (%s): %w", a.ProviderName(), method.Name(), err)
	}
	return cred, azsweep.Endpoints{ResourceManager: rm.Endpoint, Audience: rm.Audience}, azsweep.Credentials{Method: method.Name(), TenantID: a.TenantID, ClientID: a.ClientID}, nil
}

// azureLocal reports whether a's cloud is an emulator: its metadata host,
// or the Resource Manager endpoint it resolved to, is a loopback host.
func azureLocal(a azsweep.Attrs, resourceManager string) bool {
	return (a.MetadataHost != "" && gcpsweep.IsLoopback(a.MetadataHost)) ||
		(resourceManager != "" && gcpsweep.IsLoopback(resourceManager))
}

// azureCloudConfig is the cloud the block names, in the precedence the
// providers apply: azurerm's metadata_host over environment, and azapi's
// endpoint block over environment, field by field. azapi's environment
// "custom" is a cloud of its three endpoint values alone, each required
// (azapi 2.13.0 provider.go:635-658); azurerm has no "custom" and refuses
// it, as go-azure-sdk's environments.FromName does.
func azureCloudConfig(ctx context.Context, a azsweep.Attrs) (cloud.Configuration, error) {
	var cfg cloud.Configuration
	var err error
	if a.MetadataHost != "" {
		cfg, err = auth.CloudConfigFromMetadataHost(ctx, a.MetadataHost)
	} else if a.ProviderName() == azsweep.ProviderAzAPI && strings.EqualFold(a.Environment, "custom") {
		var missing []string
		for _, f := range []struct{ name, val string }{
			{"active_directory_authority_host", a.ActiveDirectoryAuthorityHost},
			{"resource_manager_endpoint", a.ResourceManagerEndpoint},
			{"resource_manager_audience", a.ResourceManagerAudience},
		} {
			if f.val == "" {
				missing = append(missing, f.name)
			}
		}
		if len(missing) > 0 {
			return cloud.Configuration{}, fmt.Errorf("environment \"custom\" needs %s", strings.Join(missing, ", "))
		}
		cfg = cloud.Configuration{
			ActiveDirectoryAuthorityHost: a.ActiveDirectoryAuthorityHost,
			Services:                     map[cloud.ServiceName]cloud.ServiceConfiguration{cloud.ResourceManager: {}},
		}
	} else {
		cfg, _, err = auth.CloudConfigFromAddresses(ctx, a.Environment, "")
	}
	if err != nil {
		return cloud.Configuration{}, err
	}
	rm := cfg.Services[cloud.ResourceManager]
	if a.ResourceManagerEndpoint != "" {
		rm.Endpoint = a.ResourceManagerEndpoint
	}
	if a.ResourceManagerAudience != "" {
		rm.Audience = a.ResourceManagerAudience
	}
	services := map[cloud.ServiceName]cloud.ServiceConfiguration{}
	for k, v := range cfg.Services {
		services[k] = v
	}
	services[cloud.ResourceManager] = rm
	cfg.Services = services
	if a.ActiveDirectoryAuthorityHost != "" {
		cfg.ActiveDirectoryAuthorityHost = a.ActiveDirectoryAuthorityHost
	}
	return cfg, nil
}

// azureAuthConfig is the block's arguments as the azurerm state backend's
// credential chain takes them. That chain is the one this repository
// already ships for azurerm's connection arguments.
func azureAuthConfig(a azsweep.Attrs, cfg cloud.Configuration) *auth.Config {
	return &auth.Config{
		ADOAuthConfig:      auth.ADOAuthConfig{ADOServiceConnectionId: a.ADOServiceConnectionID},
		AzureCLIAuthConfig: auth.AzureCLIAuthConfig{CLIAuthEnabled: on(a.UseCLI, true)},
		ClientSecretCredentialAuthConfig: auth.ClientSecretCredentialAuthConfig{
			ClientID:             a.ClientID,
			ClientIDFilePath:     a.ClientIDFilePath,
			ClientSecret:         a.ClientSecret,
			ClientSecretFilePath: a.ClientSecretFilePath,
		},
		ClientCertificateAuthConfig: auth.ClientCertificateAuthConfig{
			ClientCertificate:         a.ClientCertificate,
			ClientCertificatePassword: a.ClientCertificatePassword,
			ClientCertificatePath:     a.ClientCertificatePath,
		},
		OIDCAuthConfig: auth.OIDCAuthConfig{
			UseOIDC:           on(a.UseOIDC, false),
			OIDCToken:         a.OIDCToken,
			OIDCTokenFilePath: a.OIDCTokenFilePath,
			OIDCRequestURL:    a.OIDCRequestURL,
			OIDCRequestToken:  a.OIDCRequestToken,
		},
		MSIAuthConfig: auth.MSIAuthConfig{UseMsi: on(a.UseMSI, false), Endpoint: a.MSIEndpoint},
		StorageAddresses: auth.StorageAddresses{
			CloudConfig:    cfg,
			SubscriptionID: a.SubscriptionID,
			TenantID:       a.TenantID,
		},
		WorkloadIdentityAuthConfig: auth.WorkloadIdentityAuthConfig{UseAKSWorkloadIdentity: on(a.UseAKSWorkloadIdentity, false)},
		DisableInstanceDiscovery:   on(a.DisableInstanceDiscovery, false),
	}
}

func on(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}
