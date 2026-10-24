// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1120 unit 17: the guards a template passes before it may
// stand in for an azurerm type's identity, and the route the generator
// records for every type.

func comp(segment string, attrs ...string) azurermIDComponent {
	return azurermIDComponent{Segment: segment, Attrs: attrs}
}

func lit(s string) azurermIDComponent { return azurermIDComponent{Literal: s} }

func armID(segment, attr string) azurermIDComponent {
	return azurermIDComponent{Segment: segment, Attrs: []string{attr}, ARMID: true}
}

func sub() azurermIDComponent {
	return azurermIDComponent{Segment: "subscriptionId", Cloud: azurermCloudSubscription}
}

// TestAzurermSegmentGuard is condition (b)'s structural half: every value
// segment is one argument, the provider block's subscription, or a whole
// leading ARM id; anything else is refused naming the segment.
func TestAzurermSegmentGuard(t *testing.T) {
	ok := []azurermIDType{
		{Type: "rg", Components: []azurermIDComponent{lit("/subscriptions/"), sub(), lit("/resourceGroups/"), comp("resourceGroupName", "name")}},
		{Type: "child", Components: []azurermIDComponent{armID("storageAccountName", "storage_account_id"), lit("/blobServices/default/containers/"), comp("containerName", "name")}},
	}
	for _, row := range ok {
		if why := azurermSegmentProblem(row); why != "" {
			t.Errorf("%s refused: %s", row.Type, why)
		}
	}
	for want, row := range map[string]azurermIDType{
		"segment {scopePath} is read from scope":                           {Components: []azurermIDComponent{armID("componentName", "application_insights_id"), armID("scopePath", "scope")}},
		"segment {name} reads whichever of a, b":                           {Components: []azurermIDComponent{lit("/x/"), comp("name", "a", "b")}},
		"segment {tenantId} is the provider configuration's \"tenant-id\"": {Components: []azurermIDComponent{lit("/t/"), {Segment: "tenantId", Cloud: "tenant-id"}, lit("/x/"), comp("name", "name")}},
		"segment {orphan} names no argument":                               {Components: []azurermIDComponent{lit("/x/"), {Segment: "orphan"}, lit("/y/"), comp("name", "name")}},
	} {
		if got := azurermSegmentProblem(row); !strings.Contains(got, want) {
			t.Errorf("azurermSegmentProblem = %q, want it to say %q", got, want)
		}
	}
}

// storageContainerRow is azurerm_storage_container at 5.10.0, template and
// identity test both.
func storageContainerRow() azurermIDType {
	return azurermIDType{
		Type:           "azurerm_storage_container",
		Class:          azurermClassIdentitySchema,
		TemplateClass:  azurermClassParentID,
		Template:       "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Storage/storageAccounts/{storageAccountName}/blobServices/default/containers/{containerName}",
		IdentitySchema: []string{"name", "resource_group_name", "storage_account_name", "subscription_id"},
		Components:     []azurermIDComponent{armID("storageAccountName", "storage_account_id"), lit("/blobServices/default/containers/"), comp("containerName", "name")},
		Contract: &azurermContract{Source: "storage_container_resource_identity_gen_test.go", Attributes: map[string]azurermContractAttr{
			"name":                 {Argument: "name"},
			"resource_group_name":  {Within: "storage_account_id"},
			"storage_account_name": {Within: "storage_account_id"},
			"subscription_id":      {Within: "storage_account_id"},
		}},
	}
}

// TestAzurermContractGuard is condition (b)'s exactness half: an
// identity-schema type's template reads each identity attribute from where
// azurerm's own identity test says it is, or is withheld by name.
func TestAzurermContractGuard(t *testing.T) {
	if why := azurermContractProblem(storageContainerRow()); why != "" {
		t.Errorf("azurerm_storage_container refused: %s", why)
	}
	// azurerm_mysql_flexible_database: identity flexible_server_name read
	// from the server_name argument, the subscription the provider's.
	mysql := azurermIDType{
		Type: "azurerm_mysql_flexible_database", Class: azurermClassIdentitySchema, TemplateClass: azurermClassTemplate,
		IdentitySchema: []string{"flexible_server_name", "name", "resource_group_name", "subscription_id"},
		Components: []azurermIDComponent{lit("/subscriptions/"), sub(), lit("/resourceGroups/"), comp("resourceGroupName", "resource_group_name"),
			lit("/providers/Microsoft.DBforMySQL/flexibleServers/"), comp("flexibleServerName", "server_name"), lit("/databases/"), comp("databaseName", "name")},
		Contract: &azurermContract{Source: "x", Attributes: map[string]azurermContractAttr{
			"flexible_server_name": {Argument: "server_name"}, "name": {Argument: "name"},
			"resource_group_name": {Argument: "resource_group_name"}, "subscription_id": {ProviderSubscription: true},
		}},
	}
	if why := azurermContractProblem(mysql); why != "" {
		t.Errorf("azurerm_mysql_flexible_database refused: %s", why)
	}
	// A DNS record type's record_type is a constant the template spells.
	dns := azurermIDType{
		Type: "azurerm_dns_a_record", Class: azurermClassIdentitySchema, IdentitySchema: []string{"name", "record_type", "zone_name"},
		Template:   "{zone}/A/{name}",
		Components: []azurermIDComponent{armID("dnsZoneName", "zone_id"), lit("/A/"), comp("relativeRecordSetName", "name")},
		Contract: &azurermContract{Source: "x", Attributes: map[string]azurermContractAttr{
			"name": {Argument: "name"}, "record_type": {Constant: "A"}, "zone_name": {Within: "zone_id"},
		}},
	}
	if why := azurermContractProblem(dns); why != "" {
		t.Errorf("a record type's constant refused: %s", why)
	}

	for want, mutate := range map[string]func(*azurermIDType){
		"publishes no resource identity test": func(r *azurermIDType) { r.Contract = nil },
		"does not say where identity attribute storage_account_name comes from": func(r *azurermIDType) {
			delete(r.Contract.Attributes, "storage_account_name")
		},
		"segments up to {storageAccountName} are read whole from storage_account_id, and azurerm's identity test": func(r *azurermIDType) {
			for k := range r.Contract.Attributes {
				if k != "name" {
					r.Contract.Attributes[k] = azurermContractAttr{Within: "storage_account_resource_id"}
				}
			}
		},
		"segment {containerName} reads name, and azurerm's identity test": func(r *azurermIDType) {
			r.Contract.Attributes["name"] = azurermContractAttr{Argument: "container_name"}
		},
		"identity attribute storage_account_name is read by no segment": func(r *azurermIDType) {
			r.Contract.Attributes["storage_account_name"] = azurermContractAttr{Argument: "storage_account_name"}
		},
		"segment {subscriptionId} is the provider configuration's subscription": func(r *azurermIDType) {
			r.Components = append([]azurermIDComponent{lit("/subscriptions/"), sub()}, r.Components[1:]...)
		},
	} {
		row := storageContainerRow()
		mutate(&row)
		if got := azurermContractProblem(row); !strings.Contains(got, want) {
			t.Errorf("azurermContractProblem = %q, want it to say %q", got, want)
		}
	}
}

// TestAzurermServedSchemaGuard: a template whose argument the provider in
// use does not let a configuration set is withheld, by segment, and the
// reason reaches the route.
func TestAzurermServedSchemaGuard(t *testing.T) {
	block := azurermArgsBlock("name")
	block.Attributes["storage_account_id"] = &configschema.Attribute{Type: cty.String, Computed: true}
	got, ok := Azure.SynthesizeIdentity("azurerm_storage_container", providers.Schema{Block: block})
	if !ok || got.ARMTemplate != nil || !strings.Contains(got.TemplateWithheld, "segment {storageAccountName} reads storage_account_id") {
		t.Errorf("SynthesizeIdentity = %+v, %v; want the template withheld naming storage_account_id", got, ok)
	}
	block.Attributes["storage_account_id"] = &configschema.Attribute{Type: cty.String, Required: true}
	if got, _ := Azure.SynthesizeIdentity("azurerm_storage_container", providers.Schema{Block: block}); got.ARMTemplate == nil {
		t.Errorf("with storage_account_id settable the template is still withheld: %s", got.TemplateWithheld)
	}
	if got, _ := Azure.SynthesizeIdentity("azurerm_dns_zone", providers.Schema{}); got.TemplateWithheld == "" || !got.FromIdentitySchema {
		t.Errorf("a schema with no block: %+v, want the template withheld", got)
	}
}

// azurermRoutesPinned is how many azurerm 5.10.0 types the artifact
// records on each route. TO CHANGE A NUMBER, regenerate
// live/azurerm-ids.json and say in the commit which types moved and why.
//
// GitHub issue #2204 moved the 14 composite and subnet association types
// from unmapped to composite (TestAzurermCompositeIDsAreAzurermsFormat).
//
// GitHub issue #2138 moved six identity-schema types from unmapped to
// template-fallback, their templates taken from azurerm's identity test
// where segment names found no argument; TestAzurermAdmissionCounts names
// them.
//
// GitHub issue #2201 item 1 (the Create-call rule) moved 99 types out of
// unmapped: 52 to parent-id, 44 to template and 3 to template-fallback
// (azurerm_private_dns_a_record and _cname_record, whose record type is
// the constant azurerm's Create passes, and the two netapp bucket types
// that left withheld: azurerm ships no identity test for them and the
// Create call stands in). azurerm_api_management_api_policy came out
// withheld by the shared-template guard.
//
// GitHub issue #2201 item 1 (the Create-call rule) moved 99 types out of
// unmapped: 52 to parent-id, 44 to template and 3 to template-fallback
// (azurerm_private_dns_a_record and _cname_record, whose record type is
// the constant azurerm's Create passes), and the two netapp bucket types
// left withheld for template-fallback: azurerm ships no identity test for
// them and the Create call stands in. azurerm_api_management_api_policy
// came out withheld by the shared-template guard.
var azurermRoutesPinned = map[string]int{
	AzurermRouteIdentitySchema:   103,
	AzurermRouteTemplateFallback: 82,
	AzurermRouteTemplate:         361,
	AzurermRouteParentID:         403,
	AzurermRouteComposite:        14,
	AzurermRouteWithheld:         8,
	AzurermRouteUnmapped:         135,
}

// TestAzurermRoutesAreTheGuardsVerdicts is condition (c)'s guard on this
// side: the route recorded on every row of the embedded artifact is what
// the guards here decide today. (Which identity-schema types the route
// admits is internal/live/identity's TestAzurermRoutesMatchTheRecord.) A
// guard changed without regenerating the artifact fails here by type.
func TestAzurermRoutesAreTheGuardsVerdicts(t *testing.T) {
	var f azurermIDsFile
	if err := json.Unmarshal(embeddedAzurermIDs, &f); err != nil {
		t.Fatal(err)
	}
	tab, err := loadAzurermIDs()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, row := range f.Types {
		counts[row.Route]++
		_, withheld := tab.withheld[row.Type]
		bearing := row.templateBearing()
		identity := row.Class == azurermClassIdentitySchema
		var consistent bool
		switch row.Route {
		case AzurermRouteIdentitySchema:
			consistent = identity
		case AzurermRouteTemplateFallback:
			consistent = identity && bearing && !withheld
		case AzurermRouteTemplate, AzurermRouteParentID, AzurermRouteComposite:
			consistent = !identity && row.Class == row.Route && !withheld
		case AzurermRouteWithheld:
			consistent = bearing && withheld
		case AzurermRouteUnmapped:
			consistent = !bearing
		}
		if !consistent {
			t.Errorf("%s: recorded route %q, and the row is class %q/%q, withheld %v", row.Type, row.Route, row.Class, row.TemplateClass, withheld)
		}
	}
	for route, n := range azurermRoutesPinned {
		if counts[route] != n {
			t.Errorf("route %s: %d types, pinned %d", route, counts[route], n)
		}
	}
	if len(counts) != len(azurermRoutesPinned) {
		t.Errorf("routes %v, pinned %v", counts, azurermRoutesPinned)
	}
}
