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
		"forbidden":             {"Unauthorized", "serviceaccounts is forbidden: User \"x\" cannot get resource", false},
		"empty":                 {"", "", false},
	} {
		if got := Matches(tc.summary, tc.detail, Object{}); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", name, got, tc.want)
		}
	}
}

// GitHub issue #2133, finding 7: an Azure 404 reads as absence only when
// ARM's not-found code names the object being imported, its parent or its
// resource group. The shapes are hashicorp/azurerm 5.10.0's, rendered
// through go-azure-sdk (sdk/client/client.go:596) or go-autorest
// (autorest/error.go:97), under an importer's "retrieving %s: %+v".
func TestMatchesAzureOnlyAboutTheImportedObject(t *testing.T) {
	const (
		aksID  = "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks"
		diskID = "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm/dataDisks/disk1"
		dbID   = "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/databases/db"
		botID  = "/subscriptions/s/resourceGroups/rg/providers/Microsoft.BotService/botServices/bot"
		sdk    = "retrieving Kubernetes Cluster (Subscription: \"s\"\nResource Group Name: \"rg\"\nKubernetes Cluster Name: \"aks\"): unexpected status 404 (404 Not Found) with error: "
	)
	aks := Object{ID: aksID}
	for name, tc := range map[string]struct {
		summary string
		object  Object
		want    bool
	}{
		// Absences: the object, its group, its parent.
		"sdk resource":          {sdk + "ResourceNotFound: The Resource 'Microsoft.ContainerService/managedClusters/aks' under resource group 'rg' was not found. For more details please go to https://aka.ms/ARMResourceNotFoundFix", aks, true},
		"sdk resource, case":    {sdk + "ResourceNotFound: The Resource 'microsoft.containerservice/managedclusters/AKS' under resource group 'RG' was not found.", aks, true},
		"sdk group":             {"retrieving Rule Set (Subscription: \"s\"): unexpected status 404 (404 Not Found) with error: ResourceGroupNotFound: Resource group 'rg' could not be found.", aks, true},
		"sdk parent":            {"retrieving Database: unexpected status 404 (404 Not Found) with error: ParentResourceNotFound: Can not perform requested operation on nested resource. Parent resource 'srv' not found.", Object{ID: dbID}, true},
		"autorest resource":     {"retrieving thing: web.AppsClient#Get: Failure responding to request: StatusCode=404 -- Original Error: autorest/azure: Service returned an error. Status=404 Code=\"ResourceNotFound\" Message=\"The Resource 'Microsoft.ContainerService/managedClusters/aks' under resource group 'rg' was not found.\"", aks, true},
		"identity only":         {sdk + "ResourceNotFound: The Resource 'Microsoft.ContainerService/managedClusters/aks' under resource group 'rg' was not found.", Object{Identity: map[string]string{"subscription_id": "s", "resource_group_name": "rg", "name": "aks"}}, true},
		"azurerm id sentence":   {"Virtual Machine (Subscription: \"s\"\nResource Group Name: \"rg\"\nVirtual Machine Name: \"vm\") was not found therefore Data Disk Attachment cannot be imported", Object{ID: diskID}, true},
		"azurerm name sentence": {"the Bot Channels Registration \"bot\" was not found in Resource Group \"rg\"", Object{ID: botID}, true},

		// Not absences: a 404 about something else.
		"parent of a different id": {"retrieving Database: unexpected status 404 (404 Not Found) with error: ParentResourceNotFound: Can not perform requested operation on nested resource. Parent resource 'other-srv' not found.", Object{ID: dbID}, false},
		"different resource":       {sdk + "ResourceNotFound: The Resource 'Microsoft.Network/virtualNetworks/vnet' under resource group 'rg' was not found.", aks, false},
		"same name, other type":    {sdk + "ResourceNotFound: The Resource 'Microsoft.Network/virtualNetworks/aks' under resource group 'rg' was not found.", aks, false},
		"different group":          {"retrieving Rule Set: unexpected status 404 (404 Not Found) with error: ResourceGroupNotFound: Resource group 'other-rg' could not be found.", aks, false},
		"no registered provider":   {sdk + "NoRegisteredProviderFound: No registered resource provider found for location 'westeurope' and API version '2099-01-01' for type 'managedClusters'.", aks, false},
		"invalid resource type":    {sdk + "InvalidResourceType: The resource type 'managedClusters' could not be found in the namespace 'Microsoft.ContainerService' for api version '2099-01-01'.", aks, false},
		"404 with no body":         {"retrieving Kubernetes Cluster: unexpected status 404 (404 Not Found) received with no body", aks, false},
		"data-plane 404":           {"retrieving the Resource ID the Key Vault at URL \"https://kv.vault.azure.net/\": keyvault.BaseClient#GetKey: Failure responding to request: StatusCode=404 -- Original Error: autorest/azure: Service returned an error. Status=404 Code=\"KeyNotFound\" Message=\"A key with (name/id) k was not found in this key vault.\"", Object{ID: "https://kv.vault.azure.net/keys/k/v1"}, false},
		"code without names":       {sdk + "ResourceNotFound: The resource was not found.", Object{ID: dbID}, false},
		"autorest code, no 404":    {"Code=\"ParentResourceNotFound\" Message=\"Can not perform requested operation on nested resource. Parent resource 'srv' not found.\"", Object{ID: dbID}, false},
		"unknown object":           {sdk + "ResourceNotFound: The Resource 'Microsoft.ContainerService/managedClusters/aks' under resource group 'rg' was not found.", Object{}, false},
		"sentence, other object":   {"Virtual Machine (Subscription: \"s\"\nResource Group Name: \"rg\"\nVirtual Machine Name: \"other-vm\") was not found therefore Data Disk Attachment cannot be imported", Object{ID: diskID}, false},
		"azapi read failure":       {"Failed to retrieve resource: reading Resource: (ResourceId \"/subscriptions/s/resourceGroups/rg\" / Api Version \"2024-03-01\"): GET https://management.azure.com/subscriptions/s/resourceGroups/rg\nRESPONSE 403: 403 Forbidden\nERROR CODE: AuthorizationFailed", aks, false},
		"azurerm throttled":        {"retrieving Virtual Network: unexpected status 429 (429 Too Many Requests) with error: TooManyRequests: Rate limit exceeded", aks, false},
		"arm code inside a word":   {"MyResourceNotFoundHandler failed for 'aks'", aks, false},
	} {
		if got := Matches(tc.summary, "", tc.object); got != tc.want {
			t.Errorf("%s: Matches(summary) = %v, want %v", name, got, tc.want)
		}
		if got := Matches("Error", tc.summary, tc.object); got != tc.want {
			t.Errorf("%s: Matches(detail) = %v, want %v", name, got, tc.want)
		}
	}
}
