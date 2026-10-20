// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package armtypes

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// azurermIDsPath is the committed id table the join is generated from.
var azurermIDsPath = filepath.Join("..", "..", "..", "live", "azurerm-ids.json")

// TestRMARMTypesMatchTheIDTable regenerates the join from the committed id
// table and requires it byte-identical to the committed file. Both inputs
// are in the repository, so unlike the provider-source legs in pin_test.go
// this one always runs: a regenerated live/azurerm-ids.json that moves an
// ARM type fails here until the join is regenerated beside it.
func TestRMARMTypesMatchTheIDTable(t *testing.T) {
	data, err := os.ReadFile(azurermIDsPath)
	if err != nil {
		t.Fatal(err)
	}
	join, err := GenerateAzurermARMTypes(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := join.Render(); !bytes.Equal(got, committedRMARMTypes) {
		t.Fatalf("regenerating from %s does not reproduce %s; run go run ./tools/azurerm-tags-gen -ids live/azurerm-ids.json", azurermIDsPath, RMARMTypesPath)
	}
}

func TestCommittedRMARMTypesParse(t *testing.T) {
	join, err := ParseRMARMTypes(committedRMARMTypes)
	if err != nil {
		t.Fatal(err)
	}
	if join.Version != pinnedAzurermVersion || join.Commit != pinnedAzurermCommit {
		t.Errorf("join is azurerm %s (%s), want %s (%s)", join.Version, join.Commit, pinnedAzurermVersion, pinnedAzurermCommit)
	}
	if join.Tagged != pinnedRMTagCounts.Tagged {
		t.Errorf("join was asked about %d types, the tag facts hold %d", join.Tagged, pinnedRMTagCounts.Tagged)
	}
	if !bytes.Equal(join.Render(), committedRMARMTypes) {
		t.Error("parse then render does not reproduce the committed file")
	}
	for tf := range join.Rows() {
		if _, ok := AzurermTagRules(tf); !ok {
			t.Errorf("%s has an ARM type row and no tag facts row", tf)
		}
	}
}

// TestAzurermARMTypeKnownRows pins the rows the tag-limit lint depends on:
// the azurerm types whose ARM type documents fewer than 50 tags, and the
// after-create type the boundary template exempts.
func TestAzurermARMTypeKnownRows(t *testing.T) {
	for tf, want := range map[string]string{
		"azurerm_cosmosdb_account":                "Microsoft.DocumentDB/databaseAccounts",
		"azurerm_api_management":                  "Microsoft.ApiManagement/service",
		"azurerm_storage_account":                 "Microsoft.Storage/storageAccounts",
		"azurerm_resource_group":                  "Microsoft.Resources/resourceGroups",
		"azurerm_app_service_managed_certificate": "Microsoft.Web/certificates",
		// The id generator now reads azurerm_subscription's own alias id
		// (unit 13's parser fix), so it names the alias type. Its tags
		// still go on the subscription scope (substrate.ARMTagScope).
		"azurerm_subscription": "Microsoft.Subscription/aliases",
	} {
		if got, ok := AzurermARMType(tf); !ok || got != want {
			t.Errorf("AzurermARMType(%s) = %q, %v; want %q", tf, got, ok, want)
		}
	}
	// A record set whose type segment is a placeholder, and a type with
	// no ARM tags map.
	for _, tf := range []string{"azurerm_dns_a_record", "azurerm_api_management_named_value"} {
		if got, ok := AzurermARMType(tf); ok {
			t.Errorf("AzurermARMType(%s) = %q; want no row", tf, got)
		}
	}
}

func TestARMTemplateType(t *testing.T) {
	for template, want := range map[string]string{
		"/subscriptions/{s}/resourceGroups/{rg}":                                                             "Microsoft.Resources/resourceGroups",
		"/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Network/virtualNetworks/{v}":             "Microsoft.Network/virtualNetworks",
		"/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Network/virtualNetworks/{v}/subnets/{n}": "Microsoft.Network/virtualNetworks/subnets",
		"{scope}/providers/Microsoft.Insights/diagnosticSettings/{n}":                                        "",
		"/subscriptions/{s}/providers/Microsoft.X/a/{a}/providers/Microsoft.Insights/diagnosticSettings/{n}": "Microsoft.Insights/diagnosticSettings",
		"/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Network/dnsZones/{z}/{recordType}/{n}":   "",
		"{keyVaultBaseUrl}/storage/{name}/sas/{n}":                                                           "",
		"/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Web/sites/{n}/config":                    "",
	} {
		got, ok := armTemplateType(template)
		if want == "" {
			if ok {
				t.Errorf("armTemplateType(%q) = %q; want none", template, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("armTemplateType(%q) = %q, %v; want %q", template, got, ok, want)
		}
	}
}

func TestParseRMARMTypesRefusesDamage(t *testing.T) {
	good := string(committedRMARMTypes)
	for name, bad := range map[string]string{
		"count":    replaceOnce(good, "mapped=", "mapped=1"),
		"no pin":   replaceOnce(good, "# source: ", "# nothing: "),
		"unsorted": good + "azurerm_aaa Microsoft.X/y\n",
		"shape":    good + "azurerm_zzzzzz\n",
	} {
		if _, err := ParseRMARMTypes([]byte(bad)); err == nil {
			t.Errorf("%s: damaged join parsed", name)
		}
	}
}

func replaceOnce(s, old, new string) string {
	i := bytes.Index([]byte(s), []byte(old))
	if i < 0 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}
