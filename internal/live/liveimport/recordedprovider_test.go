// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1120: the provider an identity record names. A live plan
// over a root with more than one provider runs discovery once per provider
// configuration ([discovery.Request.ScopeProvider], set to the address the
// configuration resolves, registry.opentofu.org/azure/azapi), and the
// record leg proposes a removed block's record only in the pass whose
// configuration the record names. live-import wrote the address the stock
// state file recorded, registry.terraform.io/azure/azapi, so in both Azure
// estates (azurerm + azapi) the role assignment's record read as another
// configuration's in its own pass, and its removal (day2_remove) was never
// planned as a destroy: the merge refused it as "provider configuration is
// gone" instead.

// configAzapi is the provider configuration a live plan's azapi pass is
// scoped to: the root's required_providers entry for azapi.
var configAzapi = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.Provider{
	Hostname: "registry.opentofu.org", Namespace: "azure", Type: "azapi",
}}

// listingAzureCloud is azureCloud as discovery takes a provider: a handle
// that speaks the list protocol, here with no list resource types, as azapi
// and azurerm have none.
type listingAzureCloud struct{ *azureCloud }

func (listingAzureCloud) ListResourceStream(context.Context, providers.ListResourceRequest, func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	return nil
}

// migrateRoleAssignment runs live-import over a stock state holding the
// untaggable role assignment, recorded under stateProvider, and returns the
// raw record store it seeded.
func migrateRoleAssignment(t *testing.T, stateProvider addrs.AbsProviderConfig) (staterecord.Store, *projection.RecordStore) {
	t.Helper()
	cloud := newAzureCloud()
	role := azapiObject(refazRoleID, "Microsoft.Authorization/roleAssignments@2022-04-01", refazRGID, "6faae21a-0cd6-4536-8c23-a278823d12ed",
		cty.ObjectVal(map[string]cty.Value{"properties": cty.ObjectVal(map[string]cty.Value{"principalType": cty.StringVal("ServicePrincipal")})}), nil)
	cloud.objects[refazRoleID] = role

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(testEstate))
	cfg := loadSlotTestConfig(t, azureTestConfigDir(t, fmt.Sprintf(refazConfigTmpl, azSub)))
	state := azureStockState(t, map[string]cty.Value{"azapi_resource.roleAssignments": role})
	if stateProvider.String() != stockAzapi.String() {
		rs := state.RootModule().Resources["azapi_resource.roleAssignments"]
		rs.ProviderConfig = stateProvider
	}

	rat, diags := Ratify(context.Background(), Request{Estate: testEstate, State: state, Providers: &installedAzure{cloud: cloud}, Config: cfg, RecordStore: store})
	if diags.HasErrors() {
		t.Fatalf("Ratify: %s", diags.Err())
	}
	approveAll(t, rat)
	if _, _, _, found, err := store.GetIdentity(context.Background(), mustAddr(t, "azapi_resource.roleAssignments")); err != nil || !found {
		t.Fatalf("live-import recorded no identity for the role assignment (found %v, err %v)", found, err)
	}
	return raw, store
}

// azapiPassAfterRemoval runs the azapi pass of a multi-provider live plan
// over the configuration with the role assignment's block removed, and
// merges it the way live-plan does.
func azapiPassAfterRemoval(t *testing.T, raw staterecord.Store) (*discovery.Result, *discovery.Result, tfdiags.Diagnostics) {
	t.Helper()
	// refazConfigTmpl declares no resource: after day2_remove the providers
	// are still required and configured, and the role assignment's block is
	// gone.
	cfg := loadSlotTestConfig(t, azureTestConfigDir(t, fmt.Sprintf(refazConfigTmpl, azSub)))
	res, diags := discovery.Discover(context.Background(), discovery.Request{
		Estate:        testEstate,
		Config:        cfg,
		Provider:      listingAzureCloud{newAzureCloud()},
		HintStore:     raw,
		RecordRemoval: true,
		ScopeProvider: configAzapi,
		VouchProvider: configAzapi,
	})
	if diags.HasErrors() {
		t.Fatalf("Discover: %s", diags.Err())
	}
	merged, _, mdiags := discovery.Merge(testEstate, []discovery.Pass{{Provider: configAzapi, Result: res}}, false)
	return res, merged, mdiags
}

// TestStockStateRecordIsRemovedThroughTheConfiguredProvider is the red: a
// role assignment migrated out of a stock state, then its block removed.
// The azapi pass must propose its destroy, not leave it to a configuration
// that does not exist.
func TestStockStateRecordIsRemovedThroughTheConfiguredProvider(t *testing.T) {
	raw, store := migrateRoleAssignment(t, stockAzapi)
	addr := mustAddr(t, "azapi_resource.roleAssignments")
	if _, recorded, _, _, err := store.GetIdentityWithProvider(context.Background(), addr); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("the record names provider %s", recorded)
	}

	res, merged, mdiags := azapiPassAfterRemoval(t, raw)
	for _, e := range res.RecordedElsewhere {
		t.Errorf("the azapi pass left %s to provider %s (RecordedElsewhere), want its destroy proposed", e.Addr, e.Provider)
	}
	if mdiags.HasErrors() {
		t.Errorf("Merge: %s", mdiags.Err())
	}
	var removals int
	for _, r := range merged.Resolutions {
		if r.Addr.String() == addr.String() && r.Undeclared {
			removals++
		}
	}
	if removals != 1 {
		t.Errorf("the merge proposes %d removals of %s, want 1:\n%s", removals, addr, merged)
	}
}

// TestRecordNamingAnotherAliasStaysThatAliassRemoval is the honest
// "recorded elsewhere" control: the stock state recorded the role
// assignment under azapi.west, so the record names the west configuration
// (on the configuration's host), the default azapi pass leaves it to that
// configuration, and the west pass proposes the destroy.
func TestRecordNamingAnotherAliasStaysThatAliassRemoval(t *testing.T) {
	stockWest := stockAzapi
	stockWest.Alias = "west"
	raw, store := migrateRoleAssignment(t, stockWest)
	addr := mustAddr(t, "azapi_resource.roleAssignments")
	configWest := configAzapi
	configWest.Alias = "west"
	if _, recorded, _, _, err := store.GetIdentityWithProvider(context.Background(), addr); err != nil {
		t.Fatal(err)
	} else if recorded != configWest.String() {
		t.Errorf("the record names provider %s, want %s", recorded, configWest)
	}

	cfg := loadSlotTestConfig(t, azureTestConfigDir(t, fmt.Sprintf(refazConfigTmpl, azSub)+`
provider "azapi" {
  alias = "west"
}
`))
	var passes []discovery.Pass
	for _, scope := range []addrs.AbsProviderConfig{configAzapi, configWest} {
		res, diags := discovery.Discover(context.Background(), discovery.Request{
			Estate:        testEstate,
			Config:        cfg,
			Provider:      listingAzureCloud{newAzureCloud()},
			HintStore:     raw,
			RecordRemoval: true,
			ScopeProvider: scope,
			VouchProvider: scope,
		})
		if diags.HasErrors() {
			t.Fatalf("Discover through %s: %s", scope, diags.Err())
		}
		passes = append(passes, discovery.Pass{Provider: scope, Result: res})
	}
	if got := passes[0].Result.RecordedElsewhere; len(got) != 1 || got[0].Provider != configWest.String() {
		t.Errorf("the default azapi pass's RecordedElsewhere is %+v, want the role assignment left to %s", got, configWest)
	}
	merged, providerOf, mdiags := discovery.Merge(testEstate, passes, false)
	if mdiags.HasErrors() {
		t.Fatalf("Merge: %s", mdiags.Err())
	}
	var removals int
	for _, r := range merged.Resolutions {
		if r.Addr.String() == addr.String() && r.Undeclared {
			removals++
		}
	}
	if removals != 1 {
		t.Errorf("the merge proposes %d removals of %s, want 1:\n%s", removals, addr, merged)
	}
	if got := providerOf[addr.String()]; got.String() != configWest.String() {
		t.Errorf("the removal is read through %s, want %s", got, configWest)
	}
}

// TestRecordedProviderAddrKeepsAnUnrecognisedSource: only the host is ever
// taken from the configuration. A state provider whose namespace or type
// the configuration does not resolve the type to (here no required_providers
// entry, so the hashicorp default) is recorded as the state wrote it, and
// the module path and alias always are.
func TestRecordedProviderAddrKeepsAnUnrecognisedSource(t *testing.T) {
	bare := loadSlotTestConfig(t, azureTestConfigDir(t, `terraform {}`))
	if got := recordedProviderAddr(bare, "azapi_resource", stockAzapi); got.String() != stockAzapi.String() {
		t.Errorf("with no azapi requirement: %s, want the state's %s", got, stockAzapi)
	}
	stockAWS := addrs.AbsProviderConfig{Module: addrs.RootModule.Child("net"), Alias: "east", Provider: addrs.Provider{
		Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "aws",
	}}
	want := `module.net.provider["registry.opentofu.org/hashicorp/aws"].east`
	if got := recordedProviderAddr(bare, "aws_iam_role", stockAWS); got.String() != want {
		t.Errorf("a stock aws address: %s, want %s", got, want)
	}
}
