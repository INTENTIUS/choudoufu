// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/intentius/choudoufu/internal/backend/remote-state/azure/auth"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/httpclient"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// azureRMEnv is, per argument, the environment variables stock's azurerm
// backend schema falls back to when the argument is unset, in its order
// (internal/backend/remote-state/azure/backend.go's DefaultFunc entries). A
// record_store "azurerm" block resolves the same way, so a CI job that
// authenticates stock's backend through ARM_* authenticates this store too.
var azureRMEnv = map[string][]string{
	"subscription_id":             {"ARM_SUBSCRIPTION_ID"},
	"tenant_id":                   {"ARM_TENANT_ID"},
	"environment":                 {"ARM_ENVIRONMENT"},
	"metadata_host":               {"ARM_METADATA_HOST"},
	"access_key":                  {"ARM_ACCESS_KEY"},
	"sas_token":                   {"ARM_SAS_TOKEN"},
	"client_id":                   {"ARM_CLIENT_ID"},
	"client_id_file_path":         {"ARM_CLIENT_ID_FILE_PATH"},
	"client_secret":               {"ARM_CLIENT_SECRET"},
	"client_secret_file_path":     {"ARM_CLIENT_SECRET_FILE_PATH"},
	"client_certificate":          {"ARM_CLIENT_CERTIFICATE"},
	"client_certificate_password": {"ARM_CLIENT_CERTIFICATE_PASSWORD"},
	"client_certificate_path":     {"ARM_CLIENT_CERTIFICATE_PATH"},
	"oidc_token":                  {"ARM_OIDC_TOKEN"},
	"oidc_token_file_path":        {"ARM_OIDC_TOKEN_FILE_PATH", "AZURE_FEDERATED_TOKEN_FILE"},
	"oidc_request_url":            {"ARM_OIDC_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_URL", "SYSTEM_OIDCREQUESTURI"},
	"oidc_request_token":          {"ARM_OIDC_REQUEST_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN", "SYSTEM_ACCESSTOKEN"},
	"ado_service_connection_id":   {"ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID", "ARM_OIDC_AZURE_SERVICE_CONNECTION_ID", "AZURESUBSCRIPTION_SERVICE_CONNECTION_ID"},
	"use_azuread_auth":            {"ARM_USE_AZUREAD"},
	"use_cli":                     {"ARM_USE_CLI"},
	"use_msi":                     {"ARM_USE_MSI"},
	"use_oidc":                    {"ARM_USE_OIDC"},
	"use_aks_workload_identity":   {"ARM_USE_AKS_WORKLOAD_IDENTITY"},
}

// azureRMBoolDefaults are stock's defaults for the booleans when neither the
// argument nor its environment variable is set. use_cli alone is true.
var azureRMBoolDefaults = map[string]bool{"use_cli": true}

// resolveAzureRM fills every unset argument of a from its environment
// variables, the way stock's schema does. getenv is os.Getenv outside tests.
func resolveAzureRM(a configs.LiveRecordStoreAzureRM, getenv func(string) string) (configs.LiveRecordStoreAzureRM, map[string]bool, error) {
	str := func(name string, v *string) {
		if *v != "" {
			return
		}
		for _, env := range azureRMEnv[name] {
			if got := getenv(env); got != "" {
				*v = got
				return
			}
		}
	}
	for name, v := range map[string]*string{
		"subscription_id": &a.SubscriptionID, "tenant_id": &a.TenantID,
		"environment": &a.Environment, "metadata_host": &a.MetadataHost,
		"access_key": &a.AccessKey, "sas_token": &a.SASToken,
		"client_id": &a.ClientID, "client_id_file_path": &a.ClientIDFilePath,
		"client_secret": &a.ClientSecret, "client_secret_file_path": &a.ClientSecretFilePath,
		"client_certificate": &a.ClientCertificate, "client_certificate_password": &a.ClientCertificatePassword,
		"client_certificate_path": &a.ClientCertificatePath,
		"oidc_token":              &a.OIDCToken, "oidc_token_file_path": &a.OIDCTokenFilePath,
		"oidc_request_url": &a.OIDCRequestURL, "oidc_request_token": &a.OIDCRequestToken,
		"ado_service_connection_id": &a.ADOServiceConnectionID,
	} {
		str(name, v)
	}
	bools := map[string]bool{}
	for name, v := range map[string]*bool{
		"use_azuread_auth": a.UseAzureADAuth, "use_cli": a.UseCLI, "use_msi": a.UseMSI,
		"use_oidc": a.UseOIDC, "use_aks_workload_identity": a.UseAKSWorkloadIdentity,
	} {
		if v != nil {
			bools[name] = *v
			continue
		}
		bools[name] = azureRMBoolDefaults[name]
		for _, env := range azureRMEnv[name] {
			raw := getenv(env)
			if raw == "" {
				continue
			}
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return a, nil, fmt.Errorf("%s=%q is not a boolean", env, raw)
			}
			bools[name] = b
			break
		}
	}
	return a, bools, nil
}

// newAzureRMStore builds the "azurerm" backend's store (the Azure epic,
// GitHub issue #1120). Every credential goes through stock's azurerm backend
// auth package, in stock's order: an access key, then a SAS token, then
// Azure AD for the blobs when use_azuread_auth is set, and otherwise an Azure
// AD credential used to list the account's access key. This file adds no
// authentication of its own.
//
// The opening checks need one more thing stock does not: a credential for
// Azure Resource Manager, because blob versioning, the lifecycle policy and
// public access are account properties ARM serves and the Blob REST API does
// not. It is the credential stock's auth method selection picks. When none can
// be picked - an access key or SAS token with no Azure AD login beside it -
// the store still opens, and each setting is reported unreadable with the
// reason, which a run refuses and allow_insecure can waive by name.
func newAzureRMStore(ctx context.Context, rs *configs.LiveRecordStore, estate string, o recordStoreOptions) (staterecord.Store, error) {
	a, bools, err := resolveAzureRM(rs.AzureRM, os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("record_store \"azurerm\": %w", err)
	}
	cloudConfig, storageSuffix, err := auth.CloudConfigFromAddresses(ctx, a.Environment, a.MetadataHost)
	if err != nil {
		return nil, fmt.Errorf("record_store \"azurerm\": %w", err)
	}
	config := &auth.Config{
		ADOAuthConfig:      auth.ADOAuthConfig{ADOServiceConnectionId: a.ADOServiceConnectionID},
		AzureCLIAuthConfig: auth.AzureCLIAuthConfig{CLIAuthEnabled: bools["use_cli"]},
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
			UseOIDC:           bools["use_oidc"],
			OIDCToken:         a.OIDCToken,
			OIDCTokenFilePath: a.OIDCTokenFilePath,
			OIDCRequestURL:    a.OIDCRequestURL,
			OIDCRequestToken:  a.OIDCRequestToken,
		},
		MSIAuthConfig: auth.MSIAuthConfig{UseMsi: bools["use_msi"]},
		StorageAddresses: auth.StorageAddresses{
			CloudConfig:      cloudConfig,
			ResourceGroup:    a.ResourceGroupName,
			StorageAccount:   a.StorageAccountName,
			StorageContainer: a.ContainerName,
			StorageSuffix:    storageSuffix,
			SubscriptionID:   a.SubscriptionID,
			TenantID:         a.TenantID,
		},
		WorkloadIdentityAuthConfig: auth.WorkloadIdentityAuthConfig{UseAKSWorkloadIdentity: bools["use_aks_workload_identity"]},
	}

	// The Azure AD credential, picked the way stock picks one. Its absence is
	// fatal only on the paths that need it for the blobs.
	var cred azcore.TokenCredential
	var method auth.AuthMethod
	method, credErr := auth.GetAuthMethod(ctx, config)
	if credErr == nil {
		cred, credErr = method.Construct(ctx, config)
	}
	// AugmentConfig fills a subscription from the az CLI's profile and
	// checks the resource group, account and subscription ARM needs. Stock
	// calls it only on the shared-key path; the opening checks need the same
	// three names on every path.
	if credErr == nil {
		credErr = method.AugmentConfig(ctx, config)
	}

	var client *container.Client
	switch {
	case a.AccessKey != "":
		client, err = auth.NewContainerClientFromStorageAccessKey(ctx, config.StorageAddresses, a.AccessKey)
	case a.SASToken != "":
		client, err = auth.NewContainerClientFromSAS(ctx, config.StorageAddresses, a.SASToken)
	case credErr != nil:
		err = credErr
	case bools["use_azuread_auth"]:
		client, err = auth.NewContainerClient(ctx, config.StorageAddresses, cred)
	default:
		client, err = auth.NewContainerClientWithSharedKeyCredential(ctx, config.StorageAddresses, cred)
	}
	if err != nil {
		return nil, fmt.Errorf("record_store \"azurerm\": %w", err)
	}

	cfg := staterecord.AzureBlobConfig{
		Container:     client,
		Account:       a.StorageAccountName,
		ContainerName: a.ContainerName,
		ResourceGroup: a.ResourceGroupName,
		// Empty on purpose, as every backend's: the namespace a record lives
		// under is carried by the KEY. See backendKeyPrefix.
		KeyPrefix:         backendKeyPrefix,
		GetAllParallelism: o.bulkReadParallelism,
		BaseTags:          map[string]string{markers.TagEstate: estate},
	}
	if credErr != nil {
		cfg.ContractUnavailable = fmt.Sprintf("no Azure AD credential could be built to read it through Azure Resource Manager (%s); an access key or a SAS token reaches the blobs and not the storage account's settings", credErr)
	} else {
		factory, err := armstorage.NewClientFactory(config.SubscriptionID, cred, &arm.ClientOptions{
			ClientOptions: policy.ClientOptions{
				Cloud:     cloudConfig,
				Transport: httpclient.New(ctx),
				Telemetry: policy.TelemetryOptions{Disabled: true},
			},
			// A read of three settings registers nothing.
			DisableRPRegistration: true,
		})
		if err != nil {
			return nil, fmt.Errorf("record_store \"azurerm\": building the Azure Resource Manager client for the opening checks: %w", err)
		}
		cfg.Contract = staterecord.NewAzureContractAPI(factory, a.ResourceGroupName, a.StorageAccountName, a.ContainerName)
	}
	store, err := staterecord.NewAzureBlobStore(cfg)
	if err != nil {
		return nil, fmt.Errorf("record_store \"azurerm\": %w", err)
	}
	return store, nil
}
