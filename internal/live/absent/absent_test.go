// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package absent

import "testing"

func TestMatches(t *testing.T) {
	for name, tc := range map[string]struct {
		summary, detail string
		want            bool
	}{
		"sdk finder":            {"couldn't find resource", "", true},
		"aws api code":          {"error reading thing", "ResourceNotFoundException: no such policy", true},
		"kubernetes":            {"Unable to fetch service account \"smoke-k8s/app\" from Kubernetes", "serviceaccounts \"app\" not found", true},
		"kubernetes grouped":    {"Cannot import", "deployments.apps \"web\" not found", true},
		"kubernetes in summary": {"configmaps \"app-config\" not found", "", true},
		"english not found":     {"Provider produced invalid response", "the endpoint was not found in the region", false},

		// hashicorp/azurerm 5.10.0, through go-azure-sdk's 404 rendering
		// (sdk/client/client.go) under an importer's "retrieving %s: %+v".
		"azurerm go-azure-sdk 404": {"retrieving Kubernetes Cluster (Subscription: \"s\"\nResource Group Name: \"rg\"\nKubernetes Cluster Name: \"aks\"): unexpected status 404 (404 Not Found) with error: ResourceNotFound: The Resource 'Microsoft.ContainerService/managedClusters/aks' under resource group 'rg' was not found.", "", true},
		"azurerm missing group":    {"retrieving Rule Set (Subscription: \"s\"): unexpected status 404 (404 Not Found) with error: ResourceGroupNotFound: Resource group 'rg' could not be found.", "", true},
		// go-autorest's DetailedError, keyvault's nested-item importer.
		"azurerm autorest 404": {"retrieving the Resource ID the Key Vault at URL \"https://kv.vault.azure.net/\": keyvault.BaseClient#GetKey: Failure responding to request: StatusCode=404 -- Original Error: autorest/azure: Service returned an error. Status=404 Code=\"KeyNotFound\"", "", true},
		// ARM's code alone, as a whole word.
		"arm code":        {"Error", "Code=\"ParentResourceNotFound\" Message=\"Can not perform requested operation on nested resource.\"", true},
		"arm code inside": {"Error", "MyResourceNotFoundHandler failed", false},
		// azurerm importers' own sentence over an id or a quoted name.
		"azurerm id was not found":   {"Virtual Machine (Subscription: \"s\"\nResource Group Name: \"rg\"\nVirtual Machine Name: \"vm\") was not found therefore Data Disk Attachment cannot be imported", "", true},
		"azurerm name was not found": {"the Bot Channels Registration \"bot\" was not found in Resource Group \"rg\"", "", true},
		// Not absences: azapi's non-404 read failure, a throttled call.
		"azapi read failure": {"Failed to retrieve resource", "reading Resource: (ResourceId \"/subscriptions/s/resourceGroups/rg\" / Api Version \"2024-03-01\"): GET https://management.azure.com/subscriptions/s/resourceGroups/rg\nRESPONSE 403: 403 Forbidden\nERROR CODE: AuthorizationFailed", false},
		"azurerm throttled":  {"retrieving Virtual Network: unexpected status 429 (429 Too Many Requests) with error: TooManyRequests: Rate limit exceeded", "", false},
		"forbidden":          {"Unauthorized", "serviceaccounts is forbidden: User \"x\" cannot get resource", false},
		"empty":              {"", "", false},
	} {
		if got := Matches(tc.summary, tc.detail); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", name, got, tc.want)
		}
	}
}
