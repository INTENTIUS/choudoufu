// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	"github.com/intentius/choudoufu/internal/backend/remote-state/azure/auth"
	"github.com/intentius/choudoufu/internal/httpclient"
)

// Credentials is how a client was told to authenticate, so that a failure
// can be reported against what was actually used. It never holds the
// credential itself.
type Credentials struct {
	// Method is the credential chain's choice, in its own words: "Client
	// Secret Auth", "Azure CLI Auth", "Managed Service Identity Auth".
	// Empty for a [NewWith] client, which was handed a credential.
	Method string
	// TenantID and ClientID are the block's, when it names them.
	TenantID string
	ClientID string
}

// String is the one line a diagnostic names the credential with.
func (c Credentials) String() string {
	if c.Method == "" {
		return "the configured credential"
	}
	s := c.Method
	if c.ClientID != "" {
		s += " as client " + c.ClientID
	}
	if c.TenantID != "" {
		s += " in tenant " + c.TenantID
	}
	return s
}

// Endpoints are where a client sends its query and what it asks a token
// for.
type Endpoints struct {
	// ResourceManager is the ARM base URL: https://management.azure.com/
	// for the public cloud.
	ResourceManager string
	// Audience is the resource a token is requested for; the scope asked
	// for is Audience + "/.default".
	Audience string
}

// cloudConfig is the cloud the block names, in the precedence the
// providers apply: azurerm's metadata_host over environment, and azapi's
// endpoint block over environment, field by field.
func (a Attrs) cloudConfig(ctx context.Context) (cloud.Configuration, error) {
	var cfg cloud.Configuration
	var err error
	if a.MetadataHost != "" {
		cfg, err = auth.CloudConfigFromMetadataHost(ctx, a.MetadataHost)
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

// authConfig is the block's arguments as the azurerm state backend's
// credential chain takes them. That chain is the one this repository
// already ships for azurerm's connection arguments, and it tries what the
// provider tries, in nearly its order: client certificate, client secret,
// Azure DevOps OIDC, OIDC, managed identity, AKS workload identity, then
// the Azure CLI.
func (a Attrs) authConfig(cfg cloud.Configuration) *auth.Config {
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
	}
}

// New builds a client from a provider block's arguments, with the
// provider's environment fallbacks applied ([Attrs.WithEnv]). The
// credential chain picks its method now, so a configuration that names no
// usable credential fails here rather than per subscription; no token is
// requested until [Client.Search].
func New(ctx context.Context, a Attrs) (*Client, error) {
	a = a.WithEnv(os.Getenv)
	if refused := a.refusedMarks(); len(refused) > 0 {
		return nil, fmt.Errorf("the %s provider block sets %s from a sensitive value, which the estate sweep does not read; set it from a non-sensitive value so the sweep calls as and where the provider does", a.providerName(), strings.Join(refused, " and "))
	}
	cfg, err := a.cloudConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("the %s provider block's cloud: %w", a.providerName(), err)
	}
	ac := a.authConfig(cfg)
	method, err := auth.GetAuthMethod(ctx, ac)
	if err != nil {
		return nil, fmt.Errorf("the %s provider block names no credential the estate sweep can use: %w", a.providerName(), err)
	}
	cred, err := method.Construct(ctx, ac)
	if err != nil {
		return nil, fmt.Errorf("building the %s credential (%s): %w", a.providerName(), method.Name(), err)
	}
	rm := cfg.Services[cloud.ResourceManager]
	c := NewWith(Endpoints{ResourceManager: rm.Endpoint, Audience: rm.Audience}, cred, httpclient.New(ctx))
	c.subscription = a.SubscriptionID
	c.creds = Credentials{Method: method.Name(), TenantID: a.TenantID, ClientID: a.ClientID}
	return c, nil
}

func (a Attrs) providerName() string {
	if a.Provider == "" {
		return ProviderAzureRM
	}
	return a.Provider
}

// scope is the token scope for an audience: the audience with "/.default"
// appended, the v2 endpoint's form for "every permission this app has on
// that resource". A trailing slash is dropped first, so that the public
// cloud's "https://management.core.windows.net/" asks for
// "https://management.core.windows.net/.default".
func scope(audience string) string {
	return strings.TrimSuffix(audience, "/") + "/.default"
}
