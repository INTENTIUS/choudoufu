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
		"googleapi 404":         {"Error when reading or editing ComputeNetwork", "googleapi: Error 404: The resource 'projects/p/global/networks/n' was not found, notFound", true},
		"googleapi bare 404":    {"Error reading bucket", "googleapi: got HTTP response code 404 with body: Not Found", true},
		"googleapi 403":         {"Error when reading or editing ComputeNetwork", "googleapi: Error 403: Required 'compute.networks.get' permission, forbidden", false},
		"googleapi 4040 is not": {"x", "googleapi: Error 4040: y", false},
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
		tc.object.Provider = "azurerm"
		if got := Matches(tc.summary, "", tc.object); got != tc.want {
			t.Errorf("%s: Matches(summary) = %v, want %v", name, got, tc.want)
		}
		if got := Matches("Error", tc.summary, tc.object); got != tc.want {
			t.Errorf("%s: Matches(detail) = %v, want %v", name, got, tc.want)
		}
	}
}

// GitHub issue #2140, finding 1: the Azure shapes are read only for an
// object asked of an Azure provider. Both inputs below name a segment of
// the object being imported, so the azurerm sentence shape alone would
// read them as absences and the callers would plan a create in place of a
// fatal import error. Main never read them as absent.
func TestMatchesAzureShapesNeedAnAzureProvider(t *testing.T) {
	for name, tc := range map[string]struct {
		summary string
		object  Object
	}{
		"kubernetes namespace": {`namespace "team-a" was not found`, Object{ID: "team-a/app", Provider: "kubernetes"}},
		"aws subnet":           {`subnet "subnet-0abc" was not found`, Object{ID: "subnet-0abc/rtb-0def", Provider: "aws"}},
		"no provider":          {`namespace "team-a" was not found`, Object{ID: "team-a/app"}},
	} {
		if Matches(tc.summary, "", tc.object) || Matches("Error", tc.summary, tc.object) {
			t.Errorf("%s: %q reads as an absence of %+v", name, tc.summary, tc.object)
		}
	}

	// The same Azure answers that are absences from azurerm and azapi are
	// not absences from any other provider.
	const aksID = "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks"
	sdk := "retrieving Kubernetes Cluster: unexpected status 404 (404 Not Found) with error: ResourceNotFound: The Resource 'Microsoft.ContainerService/managedClusters/aks' under resource group 'rg' was not found."
	sentence := "the Bot Channels Registration \"aks\" was not found in Resource Group \"rg\""
	for _, text := range []string{sdk, sentence} {
		for _, provider := range []string{"azurerm", "azapi"} {
			if !Matches("Error", text, Object{ID: aksID, Provider: provider}) {
				t.Errorf("%s: %q does not read as an absence", provider, text)
			}
		}
		for _, provider := range []string{"aws", "kubernetes", "google", ""} {
			if Matches("Error", text, Object{ID: aksID, Provider: provider}) {
				t.Errorf("%s: %q reads as an absence", provider, text)
			}
		}
	}
}

// mainCases are origin/main's TestMatches cases at the time of #2140,
// verbatim: main's Matches took no object, so an AWS or Kubernetes
// caller's answer must not depend on the object it now passes.
var mainCases = map[string]struct {
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
}

func TestMatchesKeepsMainsAnswerOffAzure(t *testing.T) {
	objects := []Object{
		{},
		{ID: "smoke-k8s/app", Provider: "kubernetes"},
		{ID: "app-config", Identity: map[string]string{"name": "web", "namespace": "region"}, Provider: "kubernetes"},
		{ID: "arn:aws:iam::123456789012:policy/thing", Provider: "aws"},
		{ID: "endpoint/region", Provider: "aws"},
	}
	for name, tc := range mainCases {
		for _, object := range objects {
			if got := Matches(tc.summary, tc.detail, object); got != tc.want {
				t.Errorf("%s, %+v: Matches = %v, main's answer %v", name, object, got, tc.want)
			}
		}
	}
}

// The Azure greenfield stage of live/e2e/reference-azure (2026-10-21): on
// an empty subscription azapi_resource's ImportState answers the 404 with
// RemoveResource, and terraform-plugin-framework reports that as "Missing
// Resource Import State" for an import by ID. That is the absence of the
// object, so a resolver-derived import must fall through to a create. The
// same error from any other provider, or for an import by identity, is not.
func TestMatchesAzapiRemovedOnImport(t *testing.T) {
	const (
		workerID = "/subscriptions/s/resourceGroups/refaz-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/refaz-worker"
		detail   = "An unexpected error was encountered when importing the resource. This is always a problem with the provider. Please give the following information to the provider developer:\n\nResource ImportState method returned no State in response. If import is intentionally not supported, remove the Resource type ImportState method or return an error."
	)
	for name, tc := range map[string]struct {
		summary, detail string
		object          Object
		want            bool
	}{
		"azapi by id":              {FrameworkMissingImportState, detail, Object{ID: workerID, Provider: "azapi"}, true},
		"azapi by identity":        {FrameworkMissingImportState, detail, Object{Identity: map[string]string{"id": workerID}, Provider: "azapi"}, false},
		"azurerm":                  {FrameworkMissingImportState, detail, Object{ID: workerID, Provider: "azurerm"}, false},
		"kubernetes":               {FrameworkMissingImportState, detail, Object{ID: "ns/app", Provider: "kubernetes"}, false},
		"aws":                      {FrameworkMissingImportState, detail, Object{ID: "i-0abc", Provider: "aws"}, false},
		"no provider":              {FrameworkMissingImportState, detail, Object{ID: workerID}, false},
		"azapi, other detail":      {FrameworkMissingImportState, "something else", Object{ID: workerID, Provider: "azapi"}, false},
		"azapi, real failure":      {"Failed to retrieve resource", "unexpected status 403 (403 Forbidden)", Object{ID: workerID, Provider: "azapi"}, false},
		"azapi, detail as summary": {"Error", detail, Object{ID: workerID, Provider: "azapi"}, false},
	} {
		if got := Matches(tc.summary, tc.detail, tc.object); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", name, got, tc.want)
		}
	}
}
