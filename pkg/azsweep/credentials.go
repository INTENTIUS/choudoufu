// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/intentius/choudoufu/pkg/markerkeys"
)

// Credentials is how a client was told to authenticate, so that a failure
// can be reported against what was actually used. It never holds the
// credential itself.
type Credentials struct {
	// Method is the credential chain's choice, in its own words: "Client
	// Secret Auth", "Azure CLI Auth", "Managed Service Identity Auth".
	// Empty when the caller names none ([Config.Credentials]).
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

// Config is what the caller supplies a [Client], [Tagger] or
// [KeyVaultClient] (GitHub issue #2281): the marker vocabulary, the
// credential, where it calls and the HTTP client. The package builds no
// credential of its own and reads no environment to find one; choudoufu
// passes what OpenTofu's azurerm backend credential chain builds, plunge an
// azidentity credential.
type Config struct {
	// Markers names the owner tag key a search matches. Only [New] reads
	// it.
	Markers markerkeys.Keys
	// Credential is what every token is requested from.
	Credential azcore.TokenCredential
	// Endpoints are the ARM base URL and token audience; the Key Vault
	// listing reads neither, since a vault's URL names its own cloud.
	Endpoints Endpoints
	// HTTPClient carries every call; nil is http.DefaultClient.
	HTTPClient *http.Client
	// Credentials is what diagnostics name the credential as.
	Credentials Credentials
	// Subscription is the provider block's subscription_id, the one a
	// [Coverage] built from provider blocks keys the client by.
	Subscription string
}

func (c Config) httpClient() *http.Client {
	if c.HTTPClient == nil {
		return http.DefaultClient
	}
	return c.HTTPClient
}

// ProviderName is the block's provider, azurerm when unset: what errors
// about the block name it as.
func (a Attrs) ProviderName() string { return a.providerName() }

// RefusedMarks are the arguments a credential built from a is refused
// over: set from a marked value the caller did not read.
func (a Attrs) RefusedMarks() []string { return a.refusedMarks() }

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
