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
		// unmapped class: a parser with its own grammar.
		"azurerm_role_assignment": azurermSchema("scope", "role_definition_name", "principal_id", "name"),
		// withheld: shares azurerm_lb's LoadBalancerId template.
		"azurerm_lb_nat_rule": azurermSchema("name", "resource_group_name", "loadbalancer_id"),
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

// The subscription is the provider block's, never a guess: a block that
// does not state it statically (ARM_SUBSCRIPTION_ID) leaves the identity
// unbuildable, the same answer an AWS account-embedded identity gets.
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

// The two classes this unit does not admit get what they got before it:
// the identity-schema route (whose refusal names the subscription_id no
// azurerm argument supplies), and for an unmapped type the route's
// no-identity-schema refusal. Neither is ever bound from
// resource_group_name and name.
func TestAzurermUnadmittedClassesKeepTheSchemaRoute(t *testing.T) {
	schemas := azureSchemas()
	for typ, wantClause := range map[string]string{
		"azurerm_virtual_network": `requires "name", "resource_group_name", or "subscription_id"`,
		"azurerm_role_assignment": "serves no resource identity schema for azurerm_role_assignment",
		"azurerm_lb_nat_rule":     "serves no resource identity schema for azurerm_lb_nat_rule",
	} {
		if ti, ok := SynthesizeTypeIdentity(typ, schemas, nil); ok {
			t.Errorf("%s synthesized %+v; it must not be admitted by this unit", typ, ti)
			continue
		}
		if got := SchemaRefusal(typ, schemas, nil); !strings.Contains(got, wantClause) {
			t.Errorf("SchemaRefusal(%s) = %q, want it to contain %q", typ, got, wantClause)
		}
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

// An azurerm_virtual_network (identity-schema route) and an azapi_resource
// naming the same network: the brief's own duplicate case. The
// identity-schema route cannot build the network's identity (its identity
// schema requires subscription_id, which no azurerm argument supplies), so
// there is no id to compare and no collision is found. Pinned so the gap
// is visible and moves the day that route is changed.
func TestAzurermVNetOnTheSchemaRouteCannotCollide(t *testing.T) {
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
	if strings.Contains(errs, "Two resources with the same identity") {
		t.Errorf("the identity-schema route now builds a VNet id (%q); update this pin and the report", errs)
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
