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

// GitHub issue #2119, the Azure column's "removing a derived block is
// caught by its record". azurerm_subnet takes no tags, so the Resource
// Graph sweep never lists it, and the Azure pass runs with Sweep off (its
// leg is the whole sweep). Before RecordRemoval, deleting a subnet's block
// left the subnet in the cloud and out of the plan; an AWS estate proposes
// destroying an untaggable aws_iam_role_policy from the same record.

const azureSubnetID = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-app/providers/Microsoft.Network/virtualNetworks/vnet-app/subnets/snet-web"

var azurermProviderAddr = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}

// azureSubnetCloud is the fake provider with azurerm_subnet's 5.10.0 shape:
// no tags, the three arguments its id is built from, the identity schema
// the provider serves (name, resource_group_name, subscription_id,
// virtual_network_name), and no list resource.
func azureSubnetCloud() *fakeCloud {
	cloud := newFakeCloud()
	cloud.types = append(cloud.types, "azurerm_subnet")
	cloud.untagged["azurerm_subnet"] = true
	cloud.missing["azurerm_subnet"] = true
	cloud.requiredAttr["azurerm_subnet"] = map[string]bool{"name": true, "resource_group_name": true, "virtual_network_name": true}
	cloud.identityAttrs["azurerm_subnet"] = []string{"name", "resource_group_name", "subscription_id", "virtual_network_name"}
	return cloud
}

// azureSubnetRemovedConfig is the configuration after the block went: the
// provider and nothing else.
func azureSubnetRemovedConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// seedSubnetRecord writes the record an apply leaves for the subnet: the
// identity schema's attributes, the shape projection.LocatedRecordFrom
// writes for a type with an identity schema, with no string in it.
func seedSubnetRecord(t *testing.T, components map[string]string) (*fakeCloud, Request, addrs.AbsResourceInstance) {
	t.Helper()
	cfg := loadConfig(t, azureSubnetRemovedConfig(t))
	rawStore, seedStore := recordOrphanHintStore(t)
	addr := mustAddr(t, "azurerm_subnet.web")
	if _, err := projection.SeedLocatedForInstance(t.Context(), seedStore, addr, azurermProviderAddr, projection.LocatedRecord{Components: components}); err != nil {
		t.Fatalf("seeding the subnet's record: %s", err)
	}
	cloud := azureSubnetCloud()
	return cloud, Request{
		Estate:    estateName,
		Config:    cfg,
		Provider:  cloud,
		HintStore: rawStore,
	}, addr
}

func subnetComponents() map[string]string {
	return map[string]string{
		"name":                 "snet-web",
		"resource_group_name":  "rg-app",
		"subscription_id":      "00000000-0000-0000-0000-000000000001",
		"virtual_network_name": "vnet-app",
	}
}

// TestRemovedAzureSubnetIsCaughtByItsRecord: with the sweep off and the
// record leg on, the removed subnet comes back as an undeclared, record-
// rooted resolution whose import id is the subnet's ARM id, which the plan
// then reads and proposes destroying the way it does any orphan.
func TestRemovedAzureSubnetIsCaughtByItsRecord(t *testing.T) {
	_, req, addr := seedSubnetRecord(t, subnetComponents())
	req.RecordRemoval = true

	res, diags := Discover(t.Context(), req)
	assertNoErrors(t, diags)

	var found bool
	for _, r := range res.Resolutions {
		if r.Addr.String() != addr.String() {
			continue
		}
		found = true
		if !r.Undeclared || !r.RecordRooted || r.Class != identity.ClassConcrete {
			t.Errorf("%s: want an undeclared, record-rooted, concrete resolution, got %+v", addr, r)
		}
		if r.ImportID != azureSubnetID {
			t.Errorf("%s: ImportID %q, want the subnet's ARM id %q", addr, r.ImportID, azureSubnetID)
		}
	}
	if !found {
		t.Fatalf("the removed %s was not proposed; its record was not read:\n%s", addr, res)
	}
}

// TestRemovedAzureSubnetIsForgottenWithoutTheRecordLeg is the control: the
// same record, the pass as it ran before #2119 (Sweep off, no record leg),
// and the subnet is not in the plan at all. It is what makes the test above
// about RecordRemoval rather than about the fixture.
func TestRemovedAzureSubnetIsForgottenWithoutTheRecordLeg(t *testing.T) {
	_, req, addr := seedSubnetRecord(t, subnetComponents())

	res, diags := Discover(t.Context(), req)
	assertNoErrors(t, diags)
	for _, r := range res.Resolutions {
		if r.Addr.String() == addr.String() {
			t.Fatalf("%s was proposed with no record leg running: %+v", addr, r)
		}
	}
}

// TestAzureRecordMissingASegmentWarnsByName: a record that does not hold
// every segment of the ARM id is not guessed at, and it is not dropped
// silently either. The plan warns, naming the type and the estate (#2119's
// D3: the address is what the record no longer vouches for), and proposes
// nothing for it.
func TestAzureRecordMissingASegmentWarnsByName(t *testing.T) {
	comps := subnetComponents()
	delete(comps, "subscription_id")
	_, req, addr := seedSubnetRecord(t, comps)
	req.RecordRemoval = true

	res, diags := Discover(t.Context(), req)
	assertNoErrors(t, diags)
	for _, r := range res.Resolutions {
		if r.Addr.String() == addr.String() {
			t.Fatalf("%s was proposed from a record missing its subscription: %+v", addr, r)
		}
	}
	want := `Estate "` + estateName + `" holds a record of an azurerm_subnet`
	if !hasDiag(diags, problemSummaries[ProblemUnsweepableOwnedType], want) {
		t.Errorf("no warning names the type and the estate (%q):\n%s", want, renderDiags(diags))
	}
}
