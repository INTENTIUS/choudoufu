// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

// GitHub issue #1120's first Azure lane run. Both estates' migrate stage
// reported every azapi_resource as skipped: "8 newly stamped ... 3 skipped"
// on reference-azure, "1 newly stamped ... 2 skipped" and no role
// assignment record on corpus-azapi-role-assignment. The cause was the
// provider address live-import reached each instance through: the type's
// implied provider in the hashicorp namespace, registry.opentofu.org/
// hashicorp/azapi, which nobody publishes and init never installed. The
// failure to reach it was swallowed by admission, and the instance was
// filed UNADMITTED_TYPE, "no identity knowledge for resource type
// azapi_resource". The tests below serve providers by address, as the
// command layer's projectionProviders does, and record the provider
// addresses stock terraform writes into a state file.

// Stock terraform's spellings of the two providers in a state file.
var (
	stockAzapi = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.Provider{
		Hostname: "registry.terraform.io", Namespace: "azure", Type: "azapi",
	}}
	stockAzurerm = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.Provider{
		Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "azurerm",
	}}
)

// installedAzure serves cloud only at the addresses "choudoufu init" would
// have installed for a configuration requiring azure/azapi and
// hashicorp/azurerm, and refuses every other, as a plugin cache does.
type installedAzure struct {
	cloud *azureCloud
	asked []string
}

func (p *installedAzure) ConfiguredProvider(_ context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error) {
	p.asked = append(p.asked, addr.Provider.String())
	switch addr.Provider.String() {
	case "registry.opentofu.org/azure/azapi", "registry.opentofu.org/hashicorp/azurerm":
		return p.cloud, nil
	}
	return nil, fmt.Errorf("provider %s is not installed", addr.Provider)
}

const (
	refazRGID       = "/subscriptions/" + azSub + "/resourceGroups/refaz-rg"
	refazWorkerID   = refazRGID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/refaz-worker"
	refazRoleID     = refazRGID + "/providers/Microsoft.Authorization/roleAssignments/6faae21a-0cd6-4536-8c23-a278823d12ed"
	refazConfigTmpl = `
terraform {
  required_providers {
    azapi   = { source = "Azure/azapi" }
    azurerm = { source = "hashicorp/azurerm" }
  }
}

provider "azurerm" {
  features {}
  subscription_id = "%s"
}
`
)

// azureStockState is [azureState] with the provider addresses stock
// terraform records.
func azureStockState(t *testing.T, objs map[string]cty.Value) *states.State {
	t.Helper()
	state := states.NewState()
	schemas := azureSchemas()
	for addrStr, obj := range objs {
		addr := mustAddr(t, addrStr)
		typ := addr.Resource.Resource.Type
		raw, err := ctyjson.Marshal(obj, schemas[typ].Block.ImpliedType())
		if err != nil {
			t.Fatalf("encoding %s: %s", addrStr, err)
		}
		prov := stockAzurerm
		if strings.HasPrefix(typ, "azapi_") {
			prov = stockAzapi
		}
		state.RootModule().SetResourceInstanceCurrent(addr.Resource,
			&states.ResourceInstanceObjectSrc{AttrsJSON: raw, Status: states.ObjectReady}, prov, addrs.NoKey)
	}
	return state
}

func azureRecordStore(t *testing.T) *projection.RecordStore {
	t.Helper()
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(testEstate))
}

func azureTestConfigDir(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestAzapiLiveImportReachesAzureAzapi: the shapes both estates migrated.
// A taggable azapi type at its API version (a user-assigned identity, and a
// resource group) is stamped through its top-level tags; an untaggable one
// (a role assignment, which takes no tags at any API version) is UNTAGGABLE,
// written to nothing, and its identity recorded, the way an untaggable AWS
// type's is (TestUntaggableAzureTypesTakeTheRecordPath). None is
// UNADMITTED_TYPE, and the run asks for registry.opentofu.org/azure/azapi,
// the provider the configuration requires, never hashicorp/azapi.
func TestAzapiLiveImportReachesAzureAzapi(t *testing.T) {
	cloud := newAzureCloud()
	worker := azapiObject(refazWorkerID, "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31", refazRGID, "refaz-worker", cty.EmptyObjectVal, nil)
	rg := azapiObject(refazRGID, "Microsoft.Resources/resourceGroups@2020-06-01", "/subscriptions/"+azSub, "refaz-rg", cty.EmptyObjectVal, nil)
	role := azapiObject(refazRoleID, "Microsoft.Authorization/roleAssignments@2022-04-01", refazRGID, "6faae21a-0cd6-4536-8c23-a278823d12ed",
		cty.ObjectVal(map[string]cty.Value{"properties": cty.ObjectVal(map[string]cty.Value{"principalType": cty.StringVal("ServicePrincipal")})}), nil)
	for id, obj := range map[string]cty.Value{refazWorkerID: worker, refazRGID: rg, refazRoleID: role} {
		cloud.objects[id] = obj
	}
	provs := &installedAzure{cloud: cloud}
	store := azureRecordStore(t)
	cfg := loadSlotTestConfig(t, azureTestConfigDir(t, fmt.Sprintf(refazConfigTmpl, azSub)))
	state := azureStockState(t, map[string]cty.Value{
		"azapi_resource.worker":          worker,
		"azapi_resource.resourceGroup":   rg,
		"azapi_resource.roleAssignments": role,
	})

	rat, diags := Ratify(context.Background(), Request{Estate: testEstate, State: state, Providers: provs, Config: cfg, RecordStore: store})
	if diags.HasErrors() {
		t.Fatalf("Ratify: %s", diags.Err())
	}
	for _, e := range rat.Entries {
		if e.Status == StatusUnadmittedType || e.Status == StatusMissing {
			t.Errorf("%s ratified %s (%s)", e.Addr, e.Status, e.Detail)
		}
	}
	for _, asked := range provs.asked {
		if asked != "registry.opentofu.org/azure/azapi" {
			t.Errorf("asked for provider %s; every instance here is azure/azapi's", asked)
		}
	}
	if e, _ := entryFor(rat, "azapi_resource.roleAssignments"); e.Status != StatusUntaggable || !strings.Contains(e.Detail, "Microsoft.Authorization/roleAssignments") {
		t.Errorf("the role assignment ratified %s (%s), want UNTAGGABLE naming its ARM type", e.Status, e.Detail)
	}

	out := approveAll(t, rat)
	for _, addr := range []string{"azapi_resource.worker", "azapi_resource.resourceGroup"} {
		if out[addr].Outcome != OutcomeStamped {
			t.Errorf("%s: outcome %s (%s), want STAMPED", addr, out[addr].Outcome, out[addr].Detail)
		}
	}
	for id, addr := range map[string]string{refazWorkerID: "azapi_resource.worker", refazRGID: "azapi_resource.resourceGroup"} {
		want := map[string]string{"tofu-estate": testEstate, "tofu-address": addr}
		if got := cloud.tags(t, id); !mapsEqual(got, want) {
			t.Errorf("%s: live tags %v, want %v", addr, got, want)
		}
	}
	if o := out["azapi_resource.roleAssignments"]; o.Outcome != OutcomeSkipped || !strings.Contains(o.Detail, "takes no tags") {
		t.Errorf("the role assignment: outcome %s (%s), want SKIPPED saying its ARM type takes no tags", o.Outcome, o.Detail)
	}
	for _, id := range cloud.applied {
		if id == refazRoleID {
			t.Errorf("a write was applied to the role assignment, which takes no tags")
		}
	}
	rec, _, _, found, err := store.GetIdentity(context.Background(), mustAddr(t, "azapi_resource.roleAssignments"))
	if err != nil || !found {
		t.Fatalf("no identity record for the role assignment (found %v, err %v)", found, err)
	}
	if !strings.EqualFold(rec.ImportID, refazRoleID) {
		t.Errorf("the role assignment's record names %q, want %q", rec.ImportID, refazRoleID)
	}
}

// TestUnreachableProviderIsNamedNotUnadmitted: a provider live-import cannot
// reach is reported as that, by its address, as MISSING. Before the fix the
// failure was swallowed by the schema admission doors and the instance read
// "There is no identity knowledge for resource type azapi_resource", which
// sends an operator to the admitted-type list for a type that is admitted.
// Here the configuration names no azapi requirement, so the implied
// hashicorp/azapi is asked for and is not installed.
func TestUnreachableProviderIsNamedNotUnadmitted(t *testing.T) {
	cloud := newAzureCloud()
	worker := azapiObject(refazWorkerID, "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31", refazRGID, "refaz-worker", cty.EmptyObjectVal, nil)
	cloud.objects[refazWorkerID] = worker
	cfg := loadSlotTestConfig(t, azureTestConfigDir(t, `terraform {}`))
	state := azureStockState(t, map[string]cty.Value{"azapi_resource.worker": worker})

	rat, diags := Ratify(context.Background(), Request{Estate: testEstate, State: state, Providers: &installedAzure{cloud: cloud}, Config: cfg})
	if diags.HasErrors() {
		t.Fatalf("Ratify: %s", diags.Err())
	}
	e, _ := entryFor(rat, "azapi_resource.worker")
	if e.Status != StatusMissing || !strings.Contains(e.Detail, "registry.opentofu.org/hashicorp/azapi") || !strings.Contains(e.Detail, "is not installed") {
		t.Errorf("ratified %s (%s), want MISSING naming the provider and its error", e.Status, e.Detail)
	}
	if strings.Contains(e.Detail, "no identity knowledge") {
		t.Errorf("a provider failure reported as an unadmitted type: %s", e.Detail)
	}
	if o := approveAll(t, rat)["azapi_resource.worker"]; o.Outcome != OutcomeSkipped || !strings.Contains(o.Detail, "registry.opentofu.org/hashicorp/azapi") {
		t.Errorf("outcome %s (%s), want SKIPPED repeating the provider failure", o.Outcome, o.Detail)
	}
}

// TestUntaggableRecordReadsTheProviderBlocksSubscription: an untaggable
// azurerm type's identity record takes its subscription from the provider
// block (GitHub issue #2135), and the block is found under the address this
// run reached the provider through. Looked up under the address stock's
// state file recorded, registry.terraform.io/hashicorp/azurerm, no block
// matched and the environment's subscription was used instead. The record
// is checked against the object's own id (projection.withProviderComponents),
// so where the two subscriptions differ the record was not written at all:
// silently, since seedIdentityFor treats "not recordable" as nothing to do.
func TestUntaggableRecordReadsTheProviderBlocksSubscription(t *testing.T) {
	const blockSub = azSub // the subscription the subnet's id names
	const envSub = "22222222-2222-2222-2222-222222222222"
	cloud := newAzureCloud()
	store := azureRecordStore(t)
	cfg := loadSlotTestConfig(t, azureTestConfigDir(t, fmt.Sprintf(refazConfigTmpl, blockSub)))
	state := azureStockState(t, map[string]cty.Value{"azurerm_subnet.web": subnet()})

	rat, diags := Ratify(context.Background(), Request{
		Estate: testEstate, State: state, Providers: &installedAzure{cloud: cloud}, Config: cfg, RecordStore: store,
		Cloud: identity.CloudContext{SubscriptionID: envSub},
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify: %s", diags.Err())
	}
	if e, _ := entryFor(rat, "azurerm_subnet.web"); e.Status != StatusUntaggable {
		t.Fatalf("ratified %s (%s), want UNTAGGABLE", e.Status, e.Detail)
	}
	approveAll(t, rat)
	rec, _, _, found, err := store.GetIdentity(context.Background(), mustAddr(t, "azurerm_subnet.web"))
	if err != nil || !found {
		t.Fatalf("no identity record for azurerm_subnet.web (found %v, err %v)", found, err)
	}
	if got := rec.Components["subscription_id"]; got != blockSub {
		t.Errorf("the record's subscription_id = %q, want the provider block's %q (the environment's is %q)", got, blockSub, envSub)
	}
}
