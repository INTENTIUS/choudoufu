// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package managedk8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// aksReadAction is the Azure RBAC action a managed cluster GET needs.
const aksReadAction = "Microsoft.ContainerService/managedClusters/read"

// aksAPIVersion is the ARM api-version asked for. securityProfile's
// azureKeyVaultKms is generally available in it.
const aksAPIVersion = "2024-09-01"

// aksDefaultEndpoint is Azure public cloud's Resource Manager.
const aksDefaultEndpoint = "https://management.azure.com"

// aksManagedCluster is the part of a managed cluster's ARM resource this
// reads.
type aksManagedCluster struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Properties struct {
		KubernetesVersion string `json:"kubernetesVersion"`
		FQDN              string `json:"fqdn"`
		PrivateFQDN       string `json:"privateFQDN"`
		AzurePortalFQDN   string `json:"azurePortalFQDN"`
		SecurityProfile   *struct {
			AzureKeyVaultKms *struct {
				Enabled               *bool  `json:"enabled"`
				KeyID                 string `json:"keyId"`
				KeyVaultNetworkAccess string `json:"keyVaultNetworkAccess"`
			} `json:"azureKeyVaultKms"`
		} `json:"securityProfile"`
	} `json:"properties"`
}

// aks GETs the managed cluster from Resource Manager with
// DefaultAzureCredential, the chain the azurerm provider and the azure
// backend fall back to. One GET does not need the containerservice SDK
// module, which this module does not depend on.
func (r *Reader) aks(ctx context.Context, cp staterecord.ManagedControlPlane) (staterecord.ControlPlaneEncryption, error) {
	var out staterecord.ControlPlaneEncryption
	token, err := r.aksBearer(ctx)
	if err != nil {
		return out, err
	}
	base := r.AKSEndpoint
	if base == "" {
		base = aksDefaultEndpoint
	}
	u := fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s?api-version=%s",
		strings.TrimSuffix(base, "/"), url.PathEscape(cp.SubscriptionID), url.PathEscape(cp.ResourceGroup), url.PathEscape(cp.Name), aksAPIVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return out, fmt.Errorf("building the managed cluster request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := r.httpClient().Do(req)
	if err != nil {
		return out, fmt.Errorf("managedClusters GET: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return out, fmt.Errorf("managedClusters GET: reading the response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return out, aksError(resp.StatusCode, body)
	}
	var mc aksManagedCluster
	if err := json.Unmarshal(body, &mc); err != nil {
		return out, fmt.Errorf("managedClusters GET: decoding the response: %w", err)
	}
	return aksEncryption(mc), nil
}

// aksEncryption is the verdict a managed cluster's securityProfile carries.
func aksEncryption(mc aksManagedCluster) staterecord.ControlPlaneEncryption {
	p := mc.Properties
	out := staterecord.ControlPlaneEncryption{
		Cluster:   mc.ID,
		Endpoints: appendNonEmpty(nil, p.FQDN, p.PrivateFQDN, p.AzurePortalFQDN),
	}
	if out.Cluster == "" {
		out.Cluster = mc.Name
	}
	if sp := p.SecurityProfile; sp != nil && sp.AzureKeyVaultKms != nil && sp.AzureKeyVaultKms.Enabled != nil && *sp.AzureKeyVaultKms.Enabled {
		kms := sp.AzureKeyVaultKms
		out.Verdict = staterecord.EncryptionOn
		out.Detail = fmt.Sprintf("securityProfile.azureKeyVaultKms is enabled with Key Vault key %s", orUnsetKey(kms.KeyID))
		if kms.KeyVaultNetworkAccess != "" {
			out.Detail += fmt.Sprintf(" (key vault network access %s)", kms.KeyVaultNetworkAccess)
		}
		return out
	}
	out.Verdict = staterecord.EncryptionOff
	out.Detail = "securityProfile.azureKeyVaultKms is not enabled, so KMS etcd encryption is off; turn it on with `az aks update --enable-azure-keyvault-kms --azure-keyvault-kms-key-id <key>`"
	return out
}

func orUnsetKey(s string) string {
	if s == "" {
		return "(no key named)"
	}
	return s
}

// aksError turns a non-200 Resource Manager answer into an error.
func aksError(code int, body []byte) error {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	msg := e.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	err := fmt.Errorf("managedClusters GET: %s (HTTP %d): %s", orStatus(e.Error.Code, code), code, msg)
	switch {
	case code == http.StatusForbidden || e.Error.Code == "AuthorizationFailed":
		return &staterecord.ControlPlaneDeniedError{Action: aksReadAction, Err: err}
	case code == http.StatusNotFound:
		return &staterecord.ControlPlaneNotFoundError{Err: err}
	}
	return err
}

func (r *Reader) aksBearer(ctx context.Context) (string, error) {
	if r.aksToken != nil {
		return r.aksToken(ctx)
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", fmt.Errorf("resolving Azure credentials: %w", err)
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{aksDefaultEndpoint + "/.default"}})
	if err != nil {
		return "", fmt.Errorf("getting an Azure Resource Manager token: %w", err)
	}
	return tok.Token, nil
}
