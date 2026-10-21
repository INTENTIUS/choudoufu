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
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #2135, the maintainer's second ruling: the record rung reads
// an azurerm identity's subscription_id from the provider block, as the
// identity route does, so strict { markers "record" } works for an azurerm
// type with a resource identity schema. These tests write the record the
// way an apply does (WriteBack over a final state) and read it back BY
// VALUE, then hand it to the removal leg's own rebuild
// (substrate.AzurermRecordImportID) and compare with the id azurerm gave
// the object, which is the external source here: a record that exists and
// names another subscription is the failure this must not have.
//
// azurerm_route_table is the type AKS retags (ownership_aks_test.go), and
// the schema below is its azurerm 5.10.0 shape in the parts that matter:
// no subscription_id argument, and an identity schema requiring name,
// resource_group_name and subscription_id.

const (
	azRecordType = "azurerm_route_table"
	azRecordSub  = "00000000-0000-0000-0000-000000000001"
	azRecordEnv  = "00000000-0000-0000-0000-0000000000ee"
)

var azRecordProvider = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}

func azRecordSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":                  {Type: cty.String, Computed: true},
				"name":                {Type: cty.String, Required: true},
				"resource_group_name": {Type: cty.String, Required: true},
				"location":            {Type: cty.String, Required: true},
				"tags":                {Type: cty.Map(cty.String), Optional: true},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"name":                {Type: cty.String, Required: true},
				"resource_group_name": {Type: cty.String, Required: true},
				"subscription_id":     {Type: cty.String, Required: true},
			},
		},
	}
}

func azRecordID(sub string) string {
	return "/subscriptions/" + sub + "/resourceGroups/rg/providers/Microsoft.Network/routeTables/rt"
}

// azRecordWriteBack writes the record of one applied route table whose id
// azurerm set to appliedID, under a configuration whose provider block is
// providerBlock, and returns the diagnostics and the record read back.
func azRecordWriteBack(t *testing.T, providerBlock, appliedID string, cloud identity.CloudContext) (tfdiags.Diagnostics, LocatedRecord, bool) {
	t.Helper()
	dir := t.TempDir()
	src := `
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
  }
  live {
    estate = "az-aks"
    record_store "local" {}
    strict {
      markers "record" {
        types = ["` + azRecordType + `"]
      }
    }
  }
}
` + providerBlock + `
resource "` + azRecordType + `" "rt" {
  name                = "rt"
  resource_group_name = "rg"
  location            = "westeurope"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	addr := mustAddr(t, azRecordType+".rt")
	located := newTestLocatedStore(localHintStore(t), "az-aks")

	applied := cty.ObjectVal(map[string]cty.Value{
		"id":                  cty.StringVal(appliedID),
		"name":                cty.StringVal("rt"),
		"resource_group_name": cty.StringVal("rg"),
		"location":            cty.StringVal("westeurope"),
		"tags":                cty.NullVal(cty.Map(cty.String)),
	})
	objSrc, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: applied}).Encode(azRecordSchema().Block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	final := states.NewState()
	final.EnsureModule(addrs.RootModuleInstance).SetResourceInstanceCurrent(addr.Resource, objSrc, azRecordProvider, addrs.NoKey)
	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		azRecordProvider.Provider: {ResourceTypes: map[string]providers.Schema{azRecordType: azRecordSchema()}},
	}}

	diags := WriteBack(context.Background(), WriteBackRequest{
		Store:      located.rs,
		FinalState: final,
		Schemas:    schemas,
		Config:     cfg,
		Cloud:      cloud,
	})
	rec, _, exists, err := located.Get(context.Background(), addr)
	if err != nil {
		t.Fatalf("reading back the record: %s", err)
	}
	return diags, rec, exists
}

func TestMarkersRecordAzurermRecordsTheProviderSubscription(t *testing.T) {
	for name, c := range map[string]struct {
		block string
		cloud identity.CloudContext
		sub   string
	}{
		"the block states it": {"provider \"azurerm\" {\n  features {}\n  subscription_id = \"" + azRecordSub + "\"\n}\n", identity.CloudContext{SubscriptionID: azRecordEnv}, azRecordSub},
		"the block is silent": {"provider \"azurerm\" {\n  features {}\n}\n", identity.CloudContext{SubscriptionID: azRecordEnv}, azRecordEnv},
	} {
		t.Run(name, func(t *testing.T) {
			// The id azurerm returns spells resourceGroups in another case
			// than the template does, which ARM ids are free to.
			applied := strings.Replace(azRecordID(c.sub), "resourceGroups", "resourcegroups", 1)
			diags, rec, exists := azRecordWriteBack(t, c.block, applied, c.cloud)
			assertNoErrors(t, diags)
			if !exists {
				t.Fatal("nothing was recorded for a selected azurerm_route_table; the next plan would propose creating a second one")
			}
			want := map[string]string{"name": "rt", "resource_group_name": "rg", "subscription_id": c.sub}
			if len(rec.Components) != len(want) {
				t.Fatalf("recorded %v, want %v", rec.Components, want)
			}
			for k, v := range want {
				if rec.Components[k] != v {
					t.Errorf("recorded %s = %q, want %q", k, rec.Components[k], v)
				}
			}
			// The removal leg's rebuild of the record names the object
			// azurerm created.
			rebuilt, ok := substrate.AzurermRecordImportID(azRecordType, rec.Components)
			if !ok || !strings.EqualFold(rebuilt, applied) {
				t.Errorf("the removal leg rebuilds %q (%v) from the record, want %q", rebuilt, ok, applied)
			}
		})
	}
}

// A selected instance whose subscription this run cannot answer, or whose
// answer is not the subscription the object lives in, is refused loudly:
// the record would be its only identity carrier, and a record with no
// subscription is partial and one with the wrong one is a wrong identity.
func TestMarkersRecordAzurermRefusesAnUnanswerableOrWrongSubscription(t *testing.T) {
	for name, c := range map[string]struct {
		block   string
		cloud   identity.CloudContext
		applied string
	}{
		"silent block, no environment": {"provider \"azurerm\" {\n  features {}\n}\n", identity.CloudContext{}, azRecordID(azRecordSub)},
		"the object lives elsewhere":   {"provider \"azurerm\" {\n  features {}\n  subscription_id = \"" + azRecordSub + "\"\n}\n", identity.CloudContext{}, azRecordID(azRecordEnv)},
	} {
		t.Run(name, func(t *testing.T) {
			diags, _, exists := azRecordWriteBack(t, c.block, c.applied, c.cloud)
			if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), "Cannot record a located identity") {
				t.Errorf("want the loud record failure, got: %v", diags.Err())
			}
			if exists {
				t.Error("a record was written")
			}
		})
	}
}

// LocatedRecordFrom, with no provider configuration to ask, records no
// azurerm identity-schema identity at all rather than one missing its
// subscription.
func TestLocatedRecordFromWithoutProviderValuesRecordsNothing(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{
		"id":                  cty.StringVal(azRecordID(azRecordSub)),
		"name":                cty.StringVal("rt"),
		"resource_group_name": cty.StringVal("rg"),
		"location":            cty.StringVal("westeurope"),
		"tags":                cty.NullVal(cty.Map(cty.String)),
	})
	if rec, ok := LocatedRecordFrom(azRecordType, azRecordSchema(), obj); ok {
		t.Errorf("recorded %+v with no provider values", rec)
	}
}
