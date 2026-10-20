// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// GitHub issue #2119, the record leg for azapi_resource. azapi declares
// every ARM type through one Terraform type, and whether an instance took a
// marker is a property of the ARM type and API version its `type` names
// (internal/live/armtypes): a virtual network does, a subnet does not. So a
// removed azapi subnet is as invisible to the Resource Graph sweep as a
// removed azurerm_subnet (recordorphan_azure_test.go), and only its record
// still names it. Before this, the leg read no azapi record at all:
// azapi_resource has no ratified row and no azurerm id template, so its key
// was a stray one, and its schema carries tags, so it would have been
// skipped as swept even past that.

const (
	azapiVNetID     = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-app/providers/Microsoft.Network/virtualNetworks/vnet-app"
	azapiSubnetID   = azapiVNetID + "/subnets/snet-web"
	azapiSubnetType = "Microsoft.Network/virtualNetworks/subnets@2024-05-01"
	azapiVNetType   = "Microsoft.Network/virtualNetworks@2024-05-01"
)

var azapiProviderAddr = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azapi")}

// azapiCloud is the fake provider with azapi_resource's shape (type,
// parent_id and name as arguments, a settable tags map, no resource_id; see
// substrate.AzapiResourceShape) and no list resource.
func azapiCloud() *fakeCloud {
	cloud := newFakeCloud()
	cloud.types = append(cloud.types, "azapi_resource")
	cloud.missing["azapi_resource"] = true
	cloud.requiredAttr["azapi_resource"] = map[string]bool{"type": true, "parent_id": true, "name": true}
	return cloud
}

// seedAzapiRecord writes rec under azapi_resource.<name> in an estate whose
// configuration no longer declares it.
func seedAzapiRecord(t *testing.T, name string, rec projection.LocatedRecord) (Request, addrs.AbsResourceInstance) {
	t.Helper()
	dir := t.TempDir()
	src := `
terraform {
  required_providers {
    azapi = { source = "azure/azapi" }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	rawStore, seedStore := recordOrphanHintStore(t)
	addr := mustAddr(t, "azapi_resource."+name)
	if _, err := projection.SeedLocatedForInstance(t.Context(), seedStore, addr, azapiProviderAddr, rec); err != nil {
		t.Fatalf("seeding %s's record: %s", addr, err)
	}
	return Request{
		Estate:        estateName,
		Config:        cfg,
		Provider:      azapiCloud(),
		HintStore:     rawStore,
		RecordRemoval: true,
	}, addr
}

// azapiSubnetRecord is what an apply or a live-import leaves for the subnet
// (projection.LocatedRecordFrom): the id the provider read back, with the
// three arguments azapi built it from beside it.
func azapiSubnetRecord() projection.LocatedRecord {
	return projection.LocatedRecord{
		ImportID: azapiSubnetID,
		Components: map[string]string{
			"type":      azapiSubnetType,
			"parent_id": azapiVNetID,
			"name":      "snet-web",
		},
	}
}

func azapiResolutionAt(res *Result, addr addrs.AbsResourceInstance) (identity.Resolution, bool) {
	for _, r := range res.Resolutions {
		if r.Addr.String() == addr.String() {
			return r, true
		}
	}
	return identity.Resolution{}, false
}

// TestRemovedAzapiSubnetIsCaughtByItsRecord: the subnet's ARM type takes no
// tags, so its record is read, and the removal is proposed as an undeclared,
// record-rooted resolution of the subnet's ARM id, imported through azapi's
// identity (id and type) so the read happens at the API version the block
// declared.
func TestRemovedAzapiSubnetIsCaughtByItsRecord(t *testing.T) {
	req, addr := seedAzapiRecord(t, "subnet", azapiSubnetRecord())

	res, diags := Discover(t.Context(), req)
	assertNoErrors(t, diags)

	r, found := azapiResolutionAt(res, addr)
	if !found {
		t.Fatalf("the removed %s was not proposed; its record was not read:\n%s", addr, res)
	}
	if !r.Undeclared || !r.RecordRooted || r.Class != identity.ClassConcrete {
		t.Errorf("%s: want an undeclared, record-rooted, concrete resolution, got %+v", addr, r)
	}
	if r.ImportID != azapiSubnetID {
		t.Errorf("%s: ImportID %q, want the subnet's ARM id %q", addr, r.ImportID, azapiSubnetID)
	}
	if r.IdentityValues["id"] != azapiSubnetID || r.IdentityValues["type"] != azapiSubnetType {
		t.Errorf("%s: identity values %v, want azapi's identity {id, type} at the declared API version", addr, r.IdentityValues)
	}
}

// TestRemovedAzapiSubnetIDIsRebuiltFromItsArguments: a record holding the
// three arguments and no id string gets the id azapi itself would build
// (identity.AzapiResourceID): parent_id, the child type's last segment,
// name.
func TestRemovedAzapiSubnetIDIsRebuiltFromItsArguments(t *testing.T) {
	rec := azapiSubnetRecord()
	rec.ImportID = ""
	req, addr := seedAzapiRecord(t, "subnet", rec)

	res, diags := Discover(t.Context(), req)
	assertNoErrors(t, diags)

	r, found := azapiResolutionAt(res, addr)
	if !found {
		t.Fatalf("the removed %s was not proposed:\n%s\n%s", addr, res, renderDiags(diags))
	}
	if r.ImportID != azapiSubnetID {
		t.Errorf("%s: ImportID %q, want %q rebuilt from parent_id, type and name", addr, r.ImportID, azapiSubnetID)
	}
}

// TestRemovedAzapiVNetIsLeftToTheSweep: a virtual network's ARM type takes
// tags, so the stamp marked it and the Resource Graph sweep is what finds it
// (or, if it now carries another estate's marker, rightly does not). The
// record leg proposes nothing for it, exactly as it proposes nothing for a
// taggable AWS type.
func TestRemovedAzapiVNetIsLeftToTheSweep(t *testing.T) {
	req, addr := seedAzapiRecord(t, "vnet", projection.LocatedRecord{
		ImportID: azapiVNetID,
		Components: map[string]string{
			"type":      azapiVNetType,
			"parent_id": "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-app",
			"name":      "vnet-app",
		},
	})

	res, diags := Discover(t.Context(), req)
	assertNoErrors(t, diags)
	if r, found := azapiResolutionAt(res, addr); found {
		t.Fatalf("%s takes tags and is the sweep's to find, but the record leg proposed it: %+v", addr, r)
	}
}

// TestAzapiRecordWithoutItsTypeWarnsByName: a record that cannot say which
// ARM type it holds cannot say whether the sweep covers the object. It is
// not guessed at and not dropped silently: the plan warns, naming the type
// and the estate, as it does for an azurerm record missing a segment.
func TestAzapiRecordWithoutItsTypeWarnsByName(t *testing.T) {
	req, addr := seedAzapiRecord(t, "subnet", projection.LocatedRecord{ImportID: azapiSubnetID})

	res, diags := Discover(t.Context(), req)
	assertNoErrors(t, diags)
	if r, found := azapiResolutionAt(res, addr); found {
		t.Fatalf("%s was proposed from a record with no type: %+v", addr, r)
	}
	want := `Estate "` + estateName + `" holds a record of an azapi_resource`
	if !hasDiag(diags, problemSummaries[ProblemUnsweepableOwnedType], want) {
		t.Errorf("no warning names the type and the estate (%q):\n%s", want, renderDiags(diags))
	}
}
