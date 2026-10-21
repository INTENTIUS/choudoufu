// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #2138, the maintainer's option (a): the record rung holds an
// azurerm identity attribute the block does not declare, read from the
// source tools/azurerm-id-gen generated for it. azurerm_storage_container's
// identity is name, resource_group_name, storage_account_name and
// subscription_id, and its block states the last three only inside
// storage_account_id; azurerm_dns_cname_record's is dns_zone_name (the
// zone_name argument), record_type (the constant "CNAME"), name,
// resource_group_name and subscription_id (the provider's). The schemas
// below are the azurerm 5.10.0 shapes in the parts that matter. Each record
// is checked against the id azurerm gave the object, the external source:
// a record that rebuilds to anything else must not be written.

const (
	azContainerType = "azurerm_storage_container"
	azAccountID     = "/subscriptions/" + azRecordSub + "/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct"
	azContainerID   = azAccountID + "/blobServices/default/containers/logs"
)

func azContainerSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":                    {Type: cty.String, Optional: true, Computed: true},
			"name":                  {Type: cty.String, Required: true},
			"storage_account_id":    {Type: cty.String, Optional: true},
			"container_access_type": {Type: cty.String, Optional: true},
		}},
		IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
			"name":                 {Type: cty.String, Required: true},
			"resource_group_name":  {Type: cty.String, Required: true},
			"storage_account_name": {Type: cty.String, Required: true},
			"subscription_id":      {Type: cty.String, Required: true},
		}},
	}
}

func azContainerObject(id, accountID string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":                    cty.StringVal(id),
		"name":                  cty.StringVal("logs"),
		"storage_account_id":    cty.StringVal(accountID),
		"container_access_type": cty.StringVal("private"),
	})
}

// TestMarkersRecordAzurermRecordsAParentParsedOutOfItsID writes the record of
// a selected storage container the way an apply does, reads it back by
// value, and hands it to the removal leg's rebuild.
func TestMarkersRecordAzurermRecordsAParentParsedOutOfItsID(t *testing.T) {
	dir := t.TempDir()
	src := `
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
  }
  live {
    estate = "az-derived"
    record_store "local" {}
    strict {
      markers "record" {
        types = ["` + azContainerType + `"]
      }
    }
  }
}
provider "azurerm" {
  features {}
}
resource "` + azContainerType + `" "logs" {
  name               = "logs"
  storage_account_id = "` + azAccountID + `"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	addr := mustAddr(t, azContainerType+".logs")
	located := newTestLocatedStore(localHintStore(t), "az-derived")
	// The id azurerm returns spells two path keys in another case than the
	// parser does, which ARM ids are free to.
	applied := strings.Replace(strings.Replace(azContainerID, "resourceGroups", "resourcegroups", 1), "storageAccounts", "storageaccounts", 1)
	objSrc, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: azContainerObject(applied, azAccountID)}).Encode(azContainerSchema().Block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	final := states.NewState()
	final.EnsureModule(addrs.RootModuleInstance).SetResourceInstanceCurrent(addr.Resource, objSrc, azRecordProvider, addrs.NoKey)
	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		azRecordProvider.Provider: {ResourceTypes: map[string]providers.Schema{azContainerType: azContainerSchema()}},
	}}
	diags := WriteBack(context.Background(), WriteBackRequest{
		Store:      located.rs,
		FinalState: final,
		Schemas:    schemas,
		Config:     cfg,
		// A subscription the provider would answer, which the container's
		// record does not take: azurerm's identity test finds its
		// subscription_id inside storage_account_id.
		Cloud: identity.CloudContext{SubscriptionID: azRecordEnv},
	})
	assertNoErrors(t, diags)
	rec, _, exists, err := located.Get(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("nothing was recorded for a selected azurerm_storage_container")
	}
	want := map[string]string{"name": "logs", "resource_group_name": "rg", "storage_account_name": "acct", "subscription_id": azRecordSub}
	if len(rec.Components) != len(want) {
		t.Fatalf("recorded %v, want %v", rec.Components, want)
	}
	for k, v := range want {
		if rec.Components[k] != v {
			t.Errorf("recorded %s = %q, want %q", k, rec.Components[k], v)
		}
	}
	if rebuilt, ok := substrate.AzurermRecordImportID(azContainerType, rec.Components); !ok || !strings.EqualFold(rebuilt, applied) {
		t.Errorf("the removal leg rebuilds %q (%v) from the record, want %q", rebuilt, ok, applied)
	}
}

// TestLocatedRecordAzurermDerivedRefusesWhatDoesNotRebuild is the backstop:
// whatever the parse yields, a record that does not rebuild into the
// object's own id, or an object with no id to hold it to, writes nothing.
func TestLocatedRecordAzurermDerivedRefusesWhatDoesNotRebuild(t *testing.T) {
	if _, ok := LocatedRecordFromIn(azContainerType, azContainerSchema(), azContainerObject(azContainerID, azAccountID), nil); !ok {
		t.Fatal("the container as azurerm creates it is not recorded; the cases below prove nothing")
	}
	otherAccount := strings.Replace(azAccountID, "/acct", "/other", 1)
	for name, obj := range map[string]cty.Value{
		// storage_account_id names one account and the object sits in
		// another: the parse succeeds and names the wrong parent.
		"the object is not under the parent its argument names": azContainerObject(azContainerID, otherAccount),
		// A value of another shape than the parser's: nothing is read out
		// of it by position.
		"the argument is not a storage account id": azContainerObject(azContainerID, azAccountID+"/blobServices/default"),
		"the argument is another provider's id":    azContainerObject(azContainerID, strings.Replace(azAccountID, "Microsoft.Storage", "Microsoft.Compute", 1)),
		"the object carries no id":                 azContainerObject("", azAccountID),
	} {
		t.Run(name, func(t *testing.T) {
			if rec, ok := LocatedRecordFromIn(azContainerType, azContainerSchema(), obj, nil); ok {
				t.Errorf("recorded %+v", rec.Components)
			}
		})
	}
}

// TestLocatedRecordAzurermRenamedAndConstantAttributes: dns_zone_name read
// from zone_name, record_type the constant azurerm's identity test pins,
// and subscription_id the provider's.
func TestLocatedRecordAzurermRenamedAndConstantAttributes(t *testing.T) {
	schema := providers.Schema{
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":                  {Type: cty.String, Optional: true, Computed: true},
			"name":                {Type: cty.String, Required: true},
			"resource_group_name": {Type: cty.String, Required: true},
			"zone_name":           {Type: cty.String, Required: true},
			"ttl":                 {Type: cty.Number, Required: true},
			"record":              {Type: cty.String, Optional: true},
		}},
		IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
			"dns_zone_name":       {Type: cty.String, Required: true},
			"name":                {Type: cty.String, Required: true},
			"record_type":         {Type: cty.String, Required: true},
			"resource_group_name": {Type: cty.String, Required: true},
			"subscription_id":     {Type: cty.String, Required: true},
		}},
	}
	id := "/subscriptions/" + azRecordSub + "/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com/CNAME/www"
	obj := func(id string) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{
			"id":                  cty.StringVal(id),
			"name":                cty.StringVal("www"),
			"resource_group_name": cty.StringVal("rg"),
			"zone_name":           cty.StringVal("example.com"),
			"ttl":                 cty.NumberIntVal(300),
			"record":              cty.StringVal("target.example.com"),
		})
	}
	values := func(which identity.CloudValue) (string, bool) {
		if which == identity.CloudSubscriptionID {
			return azRecordSub, true
		}
		return "", false
	}
	rec, ok := LocatedRecordFromIn("azurerm_dns_cname_record", schema, obj(id), values)
	if !ok {
		t.Fatal("not recorded")
	}
	want := map[string]string{"dns_zone_name": "example.com", "name": "www", "record_type": "CNAME", "resource_group_name": "rg", "subscription_id": azRecordSub}
	if len(rec.Components) != len(want) {
		t.Fatalf("recorded %v, want %v", rec.Components, want)
	}
	for k, v := range want {
		if rec.Components[k] != v {
			t.Errorf("recorded %s = %q, want %q", k, rec.Components[k], v)
		}
	}
	// An object azurerm says is a TXT record set is not this type's: the
	// constant rebuilds another id.
	if rec, ok := LocatedRecordFromIn("azurerm_dns_cname_record", schema, obj(strings.Replace(id, "/CNAME/", "/TXT/", 1)), values); ok {
		t.Errorf("recorded %+v for an object at a TXT id", rec.Components)
	}
}
