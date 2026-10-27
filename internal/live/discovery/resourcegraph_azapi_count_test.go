// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/pkg/azsweep"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// corpus-azapi-role-assignment's day2_count, the discovery half: a count of
// azapi user-assigned identities (an ARM type that takes tags, so the stamp
// marked each shard) scales 2 -> 1. The shard that leaves the count is found
// by its marker through the Resource Graph sweep and by nothing else (the
// record leg skips an azapi instance whose ARM type takes tags), and it must
// be shard[1], the highest index, with shard[0] bound at its own ARM id. On
// the way back up (1 -> 2) the sweep sees one shard, declared, and proposes
// nothing.

const azapiShardRG = "/subscriptions/" + rgSub1 + "/resourceGroups/rg"

func azapiShardID(i string) string {
	return azapiShardRG + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/ra-shard-" + i
}

func azapiShardHit(i string) azsweep.Resource {
	return azsweep.Resource{
		// Resource Graph's casing.
		ID:             strings.ToLower(azapiShardID(i)),
		Type:           "microsoft.managedidentity/userassignedidentities",
		Name:           "ra-shard-" + i,
		ResourceGroup:  "rg",
		SubscriptionID: rgSub1,
		Tags: map[string]string{
			markers.TagEstate:  rgEstate,
			markers.TagAddress: markers.EscapeAddress("azapi_resource.shard[" + i + "]"),
		},
	}
}

func azapiShardConfig(t *testing.T, count int) string {
	t.Helper()
	dir := t.TempDir()
	src := `
terraform {
  required_providers {
    azapi = { source = "azure/azapi" }
  }
}
resource "azapi_resource" "shard" {
  count     = ` + string(rune('0'+count)) + `
  type      = "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31"
  name      = "ra-shard-${count.index}"
  parent_id = "` + azapiShardRG + `"
  location  = "westeurope"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func azapiShardResolution(i int) identity.Resolution {
	return identity.Resolution{
		Addr:     addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "azapi_resource", Name: "shard"}.Instance(addrs.IntKey(i)).Absolute(addrs.RootModuleInstance),
		Class:    identity.ClassConcrete,
		ImportID: azapiShardID(string(rune('0' + i))),
	}
}

func TestResourceGraphLegScalesAnAzapiCountDown(t *testing.T) {
	azapiSelf := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewProvider(addrs.DefaultProviderRegistryHost, "azure", "azapi")}
	leg := func(hits ...azsweep.Resource) ResourceGraphSweep {
		graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: hits}}
		return ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}, Self: azapiSelf}
	}
	discover := func(t *testing.T, count int, sweep ResourceGraphSweep, recorded ...string) (*Result, map[string]OwnedResource) {
		t.Helper()
		// Each applied shard's record, as write-back leaves it: the record
		// leg must leave a shard whose ARM type takes tags to the sweep.
		raw, err := staterecord.NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(rgEstate))
		for _, i := range recorded {
			addr := mustAddr(t, "azapi_resource.shard["+i+"]")
			if _, err := projection.SeedLocatedForInstance(t.Context(), store, addr, azapiSelf, projection.LocatedRecord{
				ImportID: azapiShardID(i),
				Components: map[string]string{
					"type":      "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31",
					"parent_id": azapiShardRG,
					"name":      "ra-shard-" + i,
				},
			}); err != nil {
				t.Fatalf("seeding %s: %s", addr, err)
			}
		}
		var resolutions []identity.Resolution
		for i := 0; i < count; i++ {
			resolutions = append(resolutions, azapiShardResolution(i))
		}
		res, diags := Discover(context.Background(), Request{
			Estate:        rgEstate,
			Config:        loadConfig(t, azapiShardConfig(t, count)),
			Provider:      azapiCloud(),
			Resolutions:   resolutions,
			Sweepers:      []Sweeper{sweep},
			HintStore:     raw,
			RecordRemoval: true,
		})
		assertNoErrors(t, diags)
		return res, removalsByAddr(res)
	}

	t.Run("2 -> 1 proposes exactly shard[1]", func(t *testing.T) {
		res, removals := discover(t, 1, leg(azapiShardHit("0"), azapiShardHit("1")), "0", "1")
		o, ok := removals["azapi_resource.shard[1]"]
		if !ok || len(removals) != 1 {
			t.Fatalf("removals %v, want exactly azapi_resource.shard[1]; orphans %+v, problems %+v", removals, res.Orphans, res.Problems)
		}
		if !strings.EqualFold(o.ImportID, azapiShardID("1")) || !o.Swept {
			t.Errorf("shard[1]'s removal imports %q (swept %v), want its ARM id, found by the sweep", o.ImportID, o.Swept)
		}
		if len(res.Problems) != 0 {
			t.Errorf("problems %+v", res.Problems)
		}
	})

	t.Run("1 -> 2 proposes nothing", func(t *testing.T) {
		res, removals := discover(t, 2, leg(azapiShardHit("0")), "0")
		if len(removals) != 0 || len(res.Problems) != 0 {
			t.Errorf("removals %v, problems %+v; the new shard[1] is a create, the standing shard[0] is declared", removals, res.Problems)
		}
	})
}
