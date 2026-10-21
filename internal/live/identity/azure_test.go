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

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1120, unit 10: Azure admission. azurerm types with an id
// template generated from azurerm's parsers resolve to their ARM id, the
// subscription read from the provider block; azapi_resource resolves to
// the id azapi builds from type, parent_id and name; one ARM object
// declared twice, in any letter case and through either provider, is the
// duplicate-identity refusal.
//
// The schemas are the real providers' shapes at azurerm 5.10.0 and azapi
// 2.13.0 (`tofu providers schema -json`), reduced to the arguments the
// identity reads plus the tags map that makes each an arm-tags carrier.

func azureTagsAttr() *configschema.Attribute {
	return &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
}

// azurermSchema is an azurerm type with the given required string
// arguments and a tags map, no identity schema (the template and parent-id
// classes have none at 5.10.0).
func azurermSchema(args ...string) providers.Schema {
	attrs := map[string]*configschema.Attribute{
		"id":   {Type: cty.String, Computed: true},
		"tags": azureTagsAttr(),
	}
	for _, a := range args {
		attrs[a] = &configschema.Attribute{Type: cty.String, Required: true}
	}
	return providers.Schema{Block: &configschema.Block{Attributes: attrs}}
}

// azapiResourceSchema is azapi_resource at 2.13.0: type required, parent_id
// and name optional+computed, tags and location optional+computed, and the
// identity schema the provider serves (id required, type optional).
func azapiResourceSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":        {Type: cty.String, Computed: true},
			"type":      {Type: cty.String, Required: true},
			"parent_id": {Type: cty.String, Optional: true, Computed: true},
			"name":      {Type: cty.String, Optional: true, Computed: true},
			"location":  {Type: cty.String, Optional: true, Computed: true},
			"tags":      {Type: cty.Map(cty.String), Optional: true, Computed: true},
			"body":      {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
		}},
		IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Required: true},
			"type": {Type: cty.String, Optional: true},
		}},
		IdentitySchemaVersion: 0,
	}
}

func azureSchemas() map[string]providers.Schema {
	return map[string]providers.Schema{
		// template class: /subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Network/dnsZones/{name}
		"azurerm_dns_zone": azurermSchema("name", "resource_group_name"),
		// parent-id class: {dnsZoneId}/A/{name}... is identity-schema at 5.10.0,
		// so the parent-id case is a type whose parent arrives as an ARM id:
		// azurerm_active_directory_domain_service_trust, {domain_service_id}/trusts/{name}.
		"azurerm_active_directory_domain_service_trust": azurermSchema("name", "domain_service_id"),
		// identity-schema class: keeps the identity-schema route.
		"azurerm_virtual_network": {
			Block: azurermSchema("name", "resource_group_name", "location").Block,
			IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
				"name":                {Type: cty.String, Required: true},
				"resource_group_name": {Type: cty.String, Required: true},
				"subscription_id":     {Type: cty.String, Required: true},
			}},
		},
		// identity-schema class with a parent-id template: the identity
		// schema names storage_account_name, which no argument supplies.
		"azurerm_storage_container": {
			Block: azurermSchema("name", "storage_account_id").Block,
			IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
				"name":                 {Type: cty.String, Required: true},
				"resource_group_name":  {Type: cty.String, Required: true},
				"storage_account_name": {Type: cty.String, Required: true},
				"subscription_id":      {Type: cty.String, Required: true},
			}},
		},
		// No row at all, an identity schema the configuration supplies
		// but for subscription_id: the route's identity object.
		"azurerm_widget": {
			Block: azurermSchema("name", "resource_group_name").Block,
			IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
				"name":                {Type: cty.String, Required: true},
				"resource_group_name": {Type: cty.String, Required: true},
				"subscription_id":     {Type: cty.String, Required: true},
			}},
		},
		// unmapped class: a parser with its own grammar.
		"azurerm_role_assignment": azurermSchema("scope", "role_definition_name", "principal_id", "name"),
		// withheld at unit 10 (it recorded azurerm_lb's id); the regenerated
		// row is the rule's own, parent-id class.
		"azurerm_lb_nat_rule": azurermSchema("name", "loadbalancer_id"),
		"azapi_resource":      azapiResourceSchema(),
	}
}

func resolveAzure(t *testing.T, body string) (*Result, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
    azapi   = { source = "azure/azapi" }
  }
}
`+body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir, nil)
	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: azureSchemas()})
	if diags.HasErrors() {
		return result, diags.Err().Error()
	}
	return result, ""
}

func azureResolution(t *testing.T, result *Result, addr string) Resolution {
	t.Helper()
	if result == nil {
		t.Fatalf("no result for %s", addr)
	}
	for _, r := range result.All() {
		if r.Addr.String() == addr {
			return r
		}
	}
	t.Fatalf("%s did not resolve", addr)
	return Resolution{}
}

const azureSub = "00000000-0000-0000-0000-000000000001"

const azureProviders = `
provider "azurerm" {
  features {}
  subscription_id = "` + azureSub + `"
}
provider "azapi" {
  subscription_id = "` + azureSub + `"
}
`

func TestAzurermTemplateResolvesToItsARMID(t *testing.T) {
	result, errs := resolveAzure(t, azureProviders+`
resource "azurerm_dns_zone" "z" {
  name                = "example.com"
  resource_group_name = "rg-prod"
}
`)
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got := azureResolution(t, result, "azurerm_dns_zone.z")
	want := "/subscriptions/" + azureSub + "/resourceGroups/rg-prod/providers/Microsoft.Network/dnsZones/example.com"
	if got.Class != ClassConcrete || got.ImportID != want {
		t.Errorf("azurerm_dns_zone.z = %s, want CONCRETE %s", got, want)
	}
}

// The subscription is the provider block's, or ARM_SUBSCRIPTION_ID's when
// the block sets none, never a guess: a block that does not state it and a
// run whose environment names none (this test's zero CloudContext) leave
// the identity unbuildable, the same answer an AWS account-embedded
// identity gets. azure_subscription_env_test.go has the environment's
// cases (GitHub issue #2133, finding 4).
func TestAzurermSubscriptionUnstatedIsCloudUnknown(t *testing.T) {
	result, errs := resolveAzure(t, `
provider "azurerm" {
  features {}
}
resource "azurerm_dns_zone" "z" {
  name                = "example.com"
  resource_group_name = "rg-prod"
}
`)
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got := azureResolution(t, result, "azurerm_dns_zone.z")
	if got.Class != ClassNeedsDiscovery || got.Cause != DiscoveryCloudUnknown || len(got.CauseArgs) == 0 || got.CauseArgs[0] != string(CloudSubscriptionID) {
		t.Errorf("azurerm_dns_zone.z = %s cause %s %v, want NEEDS_DISCOVERY for the unknown subscription", got, got.Cause, got.CauseArgs)
	}
	if !strings.Contains(got.Reason, "Azure subscription ID") {
		t.Errorf("reason %q does not name the subscription", got.Reason)
	}
}

func TestAzurermParentIDTypeReadsTheParentsID(t *testing.T) {
	parent := "/subscriptions/" + azureSub + "/resourceGroups/rg/providers/Microsoft.AAD/domainServices/ds"
	result, errs := resolveAzure(t, azureProviders+`
resource "azurerm_active_directory_domain_service_trust" "t" {
  name              = "trust1"
  domain_service_id = "`+parent+`"
}
`)
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got := azureResolution(t, result, "azurerm_active_directory_domain_service_trust.t")
	if want := parent + "/trusts/trust1"; got.Class != ClassConcrete || got.ImportID != want {
		t.Errorf("= %s, want CONCRETE %s", got, want)
	}
}

// An unmapped type gets the identity-schema route's no-identity-schema
// refusal, never a binding from resource_group_name and name.
func TestAzurermUnmappedKeepsTheSchemaRouteRefusal(t *testing.T) {
	schemas := azureSchemas()
	if ti, ok := SynthesizeTypeIdentity("azurerm_role_assignment", schemas, nil); ok {
		t.Errorf("azurerm_role_assignment synthesized %+v", ti)
	}
	if got := SchemaRefusal("azurerm_role_assignment", schemas, nil); !strings.Contains(got, "serves no resource identity schema for azurerm_role_assignment") {
		t.Errorf("SchemaRefusal = %q", got)
	}
	_, errs := resolveAzure(t, azureProviders+`
resource "azurerm_role_assignment" "r" {
  scope                = "/subscriptions/`+azureSub+`/resourceGroups/rg"
  role_definition_name = "Reader"
  principal_id         = "p"
  name                 = "a"
}
`)
	if !strings.Contains(errs, "Resource type outside the live-markers subset") {
		t.Errorf("azurerm_role_assignment: %q, want the unadmitted-type refusal", errs)
	}
}

// #1120 unit 13: the identity-schema route answers subscription_id from the
// provider block. Where the generated template supplies exactly the
// identity schema's attributes, the identity is that ARM id with the
// identity object's values beside it.
func TestAzurermIdentitySchemaRouteRendersTheTemplate(t *testing.T) {
	result, errs := resolveAzure(t, azureProviders+`
resource "azurerm_virtual_network" "v" {
  name                = "vnet1"
  resource_group_name = "rg"
  location            = "westeurope"
}
`)
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got := azureResolution(t, result, "azurerm_virtual_network.v")
	want := "/subscriptions/" + azureSub + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet1"
	if got.Class != ClassConcrete || got.ImportID != want {
		t.Errorf("= %s, want CONCRETE %s", got, want)
	}
	if v := got.IdentityValues; v["subscription_id"] != azureSub || v["resource_group_name"] != "rg" || v["name"] != "vnet1" || len(v) != 3 {
		t.Errorf("identity values %v, want the three identity schema attributes", v)
	}
}

// Where the identity schema names something no argument supplies, the
// template built from the arguments the type has is the route's answer.
func TestAzurermIdentitySchemaRouteFallsToAParentIDTemplate(t *testing.T) {
	account := "/subscriptions/" + azureSub + "/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa1"
	result, errs := resolveAzure(t, azureProviders+`
resource "azurerm_storage_container" "c" {
  name               = "logs"
  storage_account_id = "`+account+`"
}
`)
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got := azureResolution(t, result, "azurerm_storage_container.c")
	if want := account + "/blobServices/default/containers/logs"; got.Class != ClassConcrete || got.ImportID != want {
		t.Errorf("= %s, want CONCRETE %s", got, want)
	}
}

// A type with no template: the identity object, the subscription read from
// the provider block, or the unknown-account answer when it is not stated.
func TestAzurermIdentitySchemaRouteWithoutATemplate(t *testing.T) {
	ti, ok := SynthesizeTypeIdentity("azurerm_widget", azureSchemas(), nil)
	if !ok || !ti.IdentityObjectOnly || ti.ARMResourceID {
		t.Fatalf("azurerm_widget = %+v, %v; want an identity-object entry", ti, ok)
	}
	body := `
resource "azurerm_widget" "w" {
  name                = "w1"
  resource_group_name = "rg"
}
`
	result, errs := resolveAzure(t, azureProviders+body)
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got := azureResolution(t, result, "azurerm_widget.w")
	if got.Class != ClassConcrete || got.ImportID != "" || got.IdentityValues["subscription_id"] != azureSub || got.IdentityValues["name"] != "w1" {
		t.Errorf("= %s values %v, want the identity object with the provider's subscription", got, got.IdentityValues)
	}
	result, errs = resolveAzure(t, "provider \"azurerm\" {\n  features {}\n}\n"+body)
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got = azureResolution(t, result, "azurerm_widget.w")
	if got.Class != ClassNeedsDiscovery || got.Cause != DiscoveryCloudUnknown || got.CauseArgs[0] != string(CloudSubscriptionID) {
		t.Errorf("= %s cause %s %v, want NEEDS_DISCOVERY for the unknown subscription", got, got.Cause, got.CauseArgs)
	}
}

func TestAzapiResourceIdentity(t *testing.T) {
	rg := "/subscriptions/" + azureSub + "/resourceGroups/rg"
	vnet := rg + "/providers/Microsoft.Network/virtualNetworks/vnet1"
	for _, tc := range []struct {
		name, typ, parent, resName, want string
	}{
		{"top-level", "Microsoft.Network/virtualNetworks@2024-05-01", rg, "vnet1", vnet},
		{"child", "Microsoft.Network/virtualNetworks/subnets@2024-05-01", vnet, "snet", vnet + "/subnets/snet"},
		{"grandchild", "Microsoft.Sql/servers/databases/backupShortTermRetentionPolicies@2023-08-01", rg + "/providers/Microsoft.Sql/servers/s/databases/d", "default", rg + "/providers/Microsoft.Sql/servers/s/databases/d/backupShortTermRetentionPolicies/default"},
		{"resource group", "Microsoft.Resources/resourceGroups@2024-03-01", "/subscriptions/" + azureSub, "rg", rg},
		{"subscription scope", "Microsoft.Authorization/policyDefinitions@2023-04-01", "/subscriptions/" + azureSub, "pd", "/subscriptions/" + azureSub + "/providers/Microsoft.Authorization/policyDefinitions/pd"},
		{"extension", "Microsoft.Authorization/locks@2020-05-01", vnet, "nodelete", vnet + "/providers/Microsoft.Authorization/locks/nodelete"},
		{"tenant root", "Microsoft.Management/managementGroups@2023-04-01", "/", "mg1", "/providers/Microsoft.Management/managementGroups/mg1"},
		{"provider namespace", "Microsoft.Network@2021-04-01", "/subscriptions/" + azureSub, "Microsoft.Network", "/subscriptions/" + azureSub + "/providers/Microsoft.Network"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, errs := resolveAzure(t, azureProviders+`
resource "azapi_resource" "x" {
  type      = "`+tc.typ+`"
  parent_id = "`+tc.parent+`"
  name      = "`+tc.resName+`"
}
`)
			if errs != "" {
				t.Fatalf("refused: %s", errs)
			}
			got := azureResolution(t, result, "azapi_resource.x")
			if got.Class != ClassConcrete || got.ImportID != tc.want {
				t.Errorf("= %s, want CONCRETE %s", got, tc.want)
			}
		})
	}
}

// A parent_id written as a reference to its parent's id renders once the
// parent is known; the type, which decides the joining text, does not
// wait.
func TestAzapiChildOfAParentIsParentDerived(t *testing.T) {
	result, errs := resolveAzure(t, azureProviders+`
resource "azapi_resource" "vnet" {
  type      = "Microsoft.Network/virtualNetworks@2024-05-01"
  parent_id = "/subscriptions/`+azureSub+`/resourceGroups/rg"
  name      = "vnet1"
}
resource "azapi_resource" "snet" {
  type      = "Microsoft.Network/virtualNetworks/subnets@2024-05-01"
  parent_id = azapi_resource.vnet.id
  name      = "snet"
}
`)
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got := azureResolution(t, result, "azapi_resource.snet")
	want := "/subscriptions/" + azureSub + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet1/subnets/snet"
	switch got.Class {
	case ClassConcrete:
		if got.ImportID != want {
			t.Errorf("= %s, want %s", got, want)
		}
	case ClassParentDerived:
		if f := got.Formula.String(); f != "${azapi_resource.vnet.id}/subnets/snet" {
			t.Errorf("formula %q", f)
		}
	default:
		t.Errorf("= %s, want CONCRETE or PARENT_DERIVED", got)
	}
}

// A type this configuration does not state is refused, never rendered:
// the joining text depends on its value. That is what the manifest path
// does with a key it cannot read.
func TestAzapiTypeMustBeKnown(t *testing.T) {
	for name, body := range map[string]string{
		"variable": `
variable "t" { type = string }
resource "azapi_resource" "x" {
  type      = var.t
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
  name      = "n"
}
`,
		"no api version": `
resource "azapi_resource" "x" {
  type      = "Microsoft.Network/virtualNetworks"
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
  name      = "n"
}
`,
		"tenant": `
resource "azapi_resource" "x" {
  type      = "Microsoft.Resources/tenants@2020-01-01"
  parent_id = "/"
  name      = "n"
}
`,
	} {
		t.Run(name, func(t *testing.T) {
			result, errs := resolveAzure(t, azureProviders+body)
			if errs == "" {
				t.Fatalf("resolved as %s; want a refusal", azureResolution(t, result, "azapi_resource.x"))
			}
		})
	}
}

// One ARM object declared through azurerm and through azapi, in different
// letter case, is the duplicate-identity refusal.
func TestAzureDuplicateAcrossProvidersAndCase(t *testing.T) {
	_, errs := resolveAzure(t, azureProviders+`
resource "azurerm_dns_zone" "z" {
  name                = "Example.com"
  resource_group_name = "RG-Prod"
}
resource "azapi_resource" "z" {
  type      = "Microsoft.Network/dnszones@2018-05-01"
  parent_id = "/subscriptions/`+azureSub+`/resourcegroups/rg-prod"
  name      = "example.COM"
}
`)
	if !strings.Contains(errs, "Two resources with the same identity") || !strings.Contains(errs, "azurerm_dns_zone.z") || !strings.Contains(errs, "azapi_resource.z") {
		t.Errorf("errors = %q, want the duplicate-identity refusal naming both blocks", errs)
	}
}

// Two azapi blocks naming one virtual network in different case are the
// same refusal; two distinct networks are not.
func TestAzapiDuplicateVNetIsCaseInsensitive(t *testing.T) {
	block := func(label, parent, name string) string {
		return `
resource "azapi_resource" "` + label + `" {
  type      = "Microsoft.Network/virtualNetworks@2024-05-01"
  parent_id = "` + parent + `"
  name      = "` + name + `"
}
`
	}
	rg := "/subscriptions/" + azureSub + "/resourceGroups/rg"
	_, errs := resolveAzure(t, azureProviders+block("a", rg, "vnet1")+block("b", strings.ToUpper(rg), "VNET1"))
	if !strings.Contains(errs, "Two resources with the same identity") {
		t.Errorf("errors = %q, want the duplicate-identity refusal", errs)
	}
	if _, errs := resolveAzure(t, azureProviders+block("a", rg, "vnet1")+block("b", rg, "vnet2")); errs != "" {
		t.Errorf("two distinct networks refused: %s", errs)
	}
}

// An azurerm_virtual_network (identity-schema route, #1120 unit 13) and an
// azapi_resource naming the same network in other letter case are one
// object declared twice.
func TestAzurermVNetAndAzapiVNetAreOneIdentity(t *testing.T) {
	_, errs := resolveAzure(t, azureProviders+`
resource "azurerm_virtual_network" "v" {
  name                = "VNet1"
  resource_group_name = "RG"
  location            = "westeurope"
}
resource "azapi_resource" "v" {
  type      = "Microsoft.Network/virtualNetworks@2024-05-01"
  parent_id = "/subscriptions/`+azureSub+`/resourceGroups/rg"
  name      = "vnet1"
}
`)
	if !strings.Contains(errs, "Two resources with the same identity") || !strings.Contains(errs, "azurerm_virtual_network.v") || !strings.Contains(errs, "azapi_resource.v") {
		t.Errorf("errors = %q, want the duplicate-identity refusal naming both blocks", errs)
	}
}

// #1120 unit 13: azapi_resource's identity values are azapi's identity
// schema, {id, type}, so an import by identity object reads the object at
// the block's own API version.
func TestAzapiIdentityCarriesTheAPIVersion(t *testing.T) {
	result, errs := resolveAzure(t, azureProviders+`
resource "azapi_resource" "v" {
  type      = "Microsoft.Network/virtualNetworks@2024-05-01"
  parent_id = "/subscriptions/`+azureSub+`/resourceGroups/rg"
  name      = "vnet1"
}
`)
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got := azureResolution(t, result, "azapi_resource.v")
	id := "/subscriptions/" + azureSub + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet1"
	if got.ImportID != id || got.IdentityValues["id"] != id || got.IdentityValues["type"] != "Microsoft.Network/virtualNetworks@2024-05-01" || len(got.IdentityValues) != 2 {
		t.Errorf("= %s values %v, want id and type@api-version", got, got.IdentityValues)
	}
}

// #1120 unit 13: a resource group with no parent_id is under the
// provider's subscription, as azapi plans it; any other type without one is
// refused, as azapi refuses it; and an unstated subscription is the
// unknown-account answer.
func TestAzapiResourceGroupDefaultsItsParent(t *testing.T) {
	rg := func(typ string) string {
		return `
resource "azapi_resource" "rg" {
  type = "` + typ + `"
  name = "rg1"
}
`
	}
	for _, typ := range []string{"Microsoft.Resources/resourceGroups@2024-03-01", "microsoft.resources/RESOURCEGROUPS@2024-03-01"} {
		result, errs := resolveAzure(t, azureProviders+rg(typ))
		if errs != "" {
			t.Fatalf("%s refused: %s", typ, errs)
		}
		got := azureResolution(t, result, "azapi_resource.rg")
		// azapi's default compares the type case-insensitively; its id
		// builder does not, so the second spelling takes the default branch.
		want := "/subscriptions/" + azureSub + "/resourceGroups/rg1"
		if strings.HasPrefix(typ, "microsoft") {
			want = "/subscriptions/" + azureSub + "/providers/microsoft.resources/RESOURCEGROUPS/rg1"
		}
		if got.Class != ClassConcrete || got.ImportID != want {
			t.Errorf("%s = %s, want CONCRETE %s", typ, got, want)
		}
	}
	if _, errs := resolveAzure(t, azureProviders+`
resource "azapi_resource" "v" {
  type = "Microsoft.Network/virtualNetworks@2024-05-01"
  name = "vnet1"
}
`); !strings.Contains(errs, "Identity argument not set") {
		t.Errorf("a virtual network with no parent_id: %q, want the missing-argument refusal", errs)
	}
	result, errs := resolveAzure(t, "provider \"azapi\" {}\n"+rg("Microsoft.Resources/resourceGroups@2024-03-01"))
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got := azureResolution(t, result, "azapi_resource.rg")
	if got.Class != ClassNeedsDiscovery || got.Cause != DiscoveryCloudUnknown || len(got.CauseArgs) != 2 || got.CauseArgs[1] != "parent_id" {
		t.Errorf("= %s cause %s %v, want NEEDS_DISCOVERY naming the subscription and parent_id", got, got.Cause, got.CauseArgs)
	}
}

func TestARMTypeSegment(t *testing.T) {
	for typ, want := range map[string]struct {
		seg string
		use armParentUse
	}{
		"Microsoft.Network/virtualNetworks@1":         {"/providers/Microsoft.Network/virtualNetworks/", armParentRootDropped},
		"Microsoft.Network/virtualNetworks/subnets@1": {"/subnets/", armParentKept},
		"Microsoft.Resources/resourceGroups@1":        {"/resourceGroups/", armParentKept},
		// azapi's switch is case-sensitive, so this is the default branch.
		"microsoft.resources/resourcegroups@1": {"/providers/microsoft.resources/resourcegroups/", armParentRootDropped},
		"Microsoft.Resources/subscriptions@1":  {"/subscriptions/", armParentDropped},
		"Microsoft.Resources/providers@1":      {"/providers/", armParentRootDropped},
		"Microsoft.Network@1":                  {"/providers/", armParentRootDropped},
	} {
		seg, use, problem := armTypeSegment(typ)
		if problem != "" || seg != want.seg || use != want.use {
			t.Errorf("armTypeSegment(%q) = %q, %d, %q; want %q, %d", typ, seg, use, problem, want.seg, want.use)
		}
	}
	for _, typ := range []string{"Microsoft.Network/virtualNetworks", "a@b@c", "Microsoft.Resources/tenants@1"} {
		if _, _, problem := armTypeSegment(typ); problem == "" {
			t.Errorf("armTypeSegment(%q) answered; want a refusal", typ)
		}
	}
}

// GitHub issue #2142's Azure audit: azapi 2.13.0 names an azapi_resource
// that omits `name` with its provider block's default_name, and refuses the
// block only when the provider sets none (nameWithDefaultNaming). So a
// static default_name is the name, read from the block's own provider
// configuration as its subscription_id is; another provider configuration's
// default_name is not, and with none the missing argument stays refused.
func TestAzapiOmittedNameIsTheProvidersDefaultName(t *testing.T) {
	result, errs := resolveAzure(t, azureProviders+`
provider "azapi" {
  alias           = "named"
  subscription_id = "`+azureSub+`"
  default_name    = "fixed"
}
resource "azapi_resource" "x" {
  provider  = azapi.named
  type      = "Microsoft.Network/virtualNetworks@2024-01-01"
  parent_id = "/subscriptions/`+azureSub+`/resourceGroups/rg"
}
`)
	if errs != "" {
		t.Fatalf("refused: %s", errs)
	}
	got := azureResolution(t, result, "azapi_resource.x")
	want := "/subscriptions/" + azureSub + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/fixed"
	if got.Class != ClassConcrete || got.ImportID != want {
		t.Errorf("azapi_resource.x = %s, want CONCRETE %s", got, want)
	}

	// The default provider configuration sets no default_name: the aliased
	// one's is not this block's, and azapi refuses the block itself.
	_, errs = resolveAzure(t, azureProviders+`
provider "azapi" {
  alias           = "named"
  subscription_id = "`+azureSub+`"
  default_name    = "fixed"
}
resource "azapi_resource" "x" {
  type      = "Microsoft.Network/virtualNetworks@2024-01-01"
  parent_id = "/subscriptions/`+azureSub+`/resourceGroups/rg"
}
`)
	if !strings.Contains(errs, `has no value for "name"`) {
		t.Errorf("no default_name on the block's provider: %q, want the missing-name refusal", errs)
	}
}

// GitHub issue #2142's Azure audit: a type the configuration states
// through a variable's default, a local, or a sibling azapi_resource's own
// type argument is known before apply, as stock knows it at plan, and
// resolves; only one this configuration cannot state is refused
// ([TestAzapiTypeMustBeKnown]).
func TestAzapiTypeReadFromConfiguration(t *testing.T) {
	const vnet = "/subscriptions/" + azureSub + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/n"
	for name, body := range map[string]string{
		"variable default": `
variable "t" {
  type    = string
  default = "Microsoft.Network/virtualNetworks@2024-01-01"
}
resource "azapi_resource" "x" {
  type      = var.t
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
  name      = "n"
}`,
		"local in a template": `
locals {
  v = "2024-01-01"
}
resource "azapi_resource" "x" {
  type      = "Microsoft.Network/virtualNetworks@${local.v}"
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
  name      = "n"
}`,
		"a sibling's type argument": `
resource "azapi_resource" "a" {
  type      = "Microsoft.Network/virtualNetworks@2024-01-01"
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
  name      = "a"
}
resource "azapi_resource" "x" {
  type      = azapi_resource.a.type
  parent_id = "/subscriptions/` + azureSub + `/resourceGroups/rg"
  name      = "n"
}`,
	} {
		t.Run(name, func(t *testing.T) {
			result, errs := resolveAzure(t, azureProviders+body)
			if errs != "" {
				t.Fatalf("refused: %s", errs)
			}
			if got := azureResolution(t, result, "azapi_resource.x"); got.Class != ClassConcrete || got.ImportID != vnet {
				t.Errorf("azapi_resource.x = %s, want CONCRETE %s", got, vnet)
			}
		})
	}
}
