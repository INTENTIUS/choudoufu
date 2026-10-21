// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/azsweep"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1120, the Azure lane's fourth run: the first in which the
// Resource Graph sweep answered (floci-az e1def56). Both estates declare
// azurerm and azapi, so a plan runs one discovery pass per provider
// configuration, and both failed test_plan with "Marked resource outside
// its address's provider configuration" for an azurerm object the azapi
// pass had listed. These drive the passes the way liveDiscover does: legs
// built by liveSweepLegs over the configuration, one Discover per provider
// configuration scoped to it, and discovery.Merge over the lot.

const (
	famEstate = "reference-azure"
	famSub    = "00000000-0000-0000-0000-00000000f001"
	famOther  = "00000000-0000-0000-0000-00000000f002"
)

func famRG(sub string) string { return "/subscriptions/" + sub + "/resourceGroups/refaz-rg" }

func famID(sub, provider, name string) string {
	return famRG(sub) + "/providers/" + provider + "/" + name
}

// famGraph is a Resource Graph answering per subscription, counting the
// queries each subscription was sent.
type famGraph struct {
	mu    sync.Mutex
	bySub map[string][]azsweep.Resource
	asked map[string]int
}

func (g *famGraph) Search(_ context.Context, sub, estate string) ([]azsweep.Resource, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.asked == nil {
		g.asked = map[string]int{}
	}
	g.asked[strings.ToLower(sub)]++
	var out []azsweep.Resource
	for _, r := range g.bySub[strings.ToLower(sub)] {
		if v, ok := azsweep.EstateTag(r.Tags); ok && v == estate {
			out = append(out, r)
		}
	}
	return out, nil
}

// famClient is one provider block's client over the shared graph, naming
// the subscription its block names.
type famClient struct {
	sub string
	g   *famGraph
}

func (famClient) SweepKind() substrate.Sweep { return substrate.SweepResourceGraph }
func (c famClient) Subscription() string     { return c.sub }
func (c famClient) Search(ctx context.Context, sub, estate string) ([]azsweep.Resource, error) {
	return c.g.Search(ctx, sub, estate)
}

// famFamily is the Azure family with every block's client over one graph.
type famFamily struct {
	substrate.Substrate
	g *famGraph
}

func (f famFamily) NewSweeper(val cty.Value, ok bool) (substrate.Sweeper, error) {
	return famClient{sub: substrate.AzureSweepAttrs(val, ok).SubscriptionID, g: f.g}, nil
}

// famListed is a resource as Resource Graph lists it: id lowercased, the
// estate's markers for addr ("" for none).
func famListed(id, armType, addr string) azsweep.Resource {
	tags := map[string]string{markers.TagEstate: famEstate}
	if addr != "" {
		tags[markers.TagAddress] = markers.EscapeAddress(addr)
	}
	sub, _ := discovery.ARMSubscription(id)
	parts := strings.Split(id, "/")
	return azsweep.Resource{ID: strings.ToLower(id), Type: strings.ToLower(armType), Name: parts[len(parts)-1], ResourceGroup: "refaz-rg", SubscriptionID: sub, Tags: tags}
}

func famAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	a, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	return a
}

func famConcrete(t *testing.T, addr, id string) identity.Resolution {
	t.Helper()
	return identity.Resolution{Addr: famAddr(t, addr), Class: identity.ClassConcrete, ImportID: id}
}

var (
	famAzurerm = addrs.NewDefaultProvider("azurerm")
	famAzapi   = addrs.Provider{Hostname: addrs.DefaultProviderRegistryHost, Namespace: "azure", Type: "azapi"}
)

// famProviders is a mock per provider type: subscription_id on the block,
// and the resource schemas the passes' record leg and lister ask for.
func famProviders() map[addrs.Provider]*tofu.MockProvider {
	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"subscription_id": {Type: cty.String, Optional: true},
	}}
	tags := &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
	str := func(req bool) *configschema.Attribute {
		return &configschema.Attribute{Type: cty.String, Required: req, Optional: !req}
	}
	azurermType := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id": {Type: cty.String, Computed: true}, "name": str(true), "resource_group_name": str(true), "location": str(false), "tags": tags,
	}}}
	azapiType := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id": {Type: cty.String, Computed: true}, "type": str(true), "name": str(true), "parent_id": str(true), "tags": tags,
		"body": {Type: cty.DynamicPseudoType, Optional: true},
	}}}
	mk := func(types map[string]providers.Schema) *tofu.MockProvider {
		p := &tofu.MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: block},
			ResourceTypes: types,
		}}
		p.ConfigureProviderCalled = true
		return p
	}
	return map[addrs.Provider]*tofu.MockProvider{
		famAzurerm: mk(map[string]providers.Schema{
			"azurerm_virtual_network":        azurermType,
			"azurerm_public_ip":              azurermType,
			"azurerm_user_assigned_identity": azurermType,
		}),
		famAzapi: mk(map[string]providers.Schema{"azapi_resource": azapiType}),
	}
}

// famRun is what one multi-provider plan's discovery produced.
type famRun struct {
	merged     *discovery.Result
	providerOf map[string]addrs.AbsProviderConfig
	passes     []discovery.Pass
	diags      tfdiags.Diagnostics
	graph      *famGraph
}

// famPlan runs one pass per provider configuration the source declares
// resources under, legs and all, then merges them as liveDiscover does.
func famPlan(t *testing.T, src string, resolutions []identity.Resolution, listed []azsweep.Resource, store staterecord.Store) famRun {
	t.Helper()
	cfg := liveLsLoadConfig(t, src)
	mocks := famProviders()
	factories := plugins.ProviderFactories{}
	for addr, m := range mocks {
		factories[addr] = providers.FactoryFixed(m)
	}
	p := newProjectionProviders(cfg, plugins.NewLibrary(factories, nil))
	graph := &famGraph{bySub: map[string][]azsweep.Resource{}}
	for _, r := range listed {
		k := strings.ToLower(r.SubscriptionID)
		graph.bySub[k] = append(graph.bySub[k], r)
	}
	family := famFamily{Substrate: substrate.Azure, g: graph}

	run := famRun{graph: graph}
	configsRun := liveManagedResourceProviders(cfg)
	if len(configsRun) < 2 {
		t.Fatalf("the configuration has %d provider configurations; these cases are about the multi-provider path", len(configsRun))
	}
	for _, addr := range configsRun {
		legs, sweep, legDiags := p.liveSweepLegs(context.Background(), family, true, addr)
		if legDiags.HasErrors() || sweep {
			t.Fatalf("legs for %s: sweep %v, %s", addr, sweep, legDiags.Err())
		}
		res, diags := discovery.Discover(context.Background(), discovery.Request{
			Estate:        famEstate,
			Config:        cfg,
			Provider:      pairLister{mocks[addr.Provider]},
			Resolutions:   resolutions,
			Sweepers:      legs,
			ScopeProvider: addr,
			VouchProvider: addr,
			HintStore:     store,
			RecordRemoval: store != nil,
		})
		run.diags = run.diags.Append(diags)
		if diags.HasErrors() {
			t.Fatalf("discovery through %s: %s", addr, diags.Err())
		}
		run.passes = append(run.passes, discovery.Pass{Provider: addr, Result: res})
	}
	merged, providerOf, mdiags := discovery.Merge(famEstate, run.passes, false)
	run.merged, run.providerOf = merged, providerOf
	run.diags = run.diags.Append(mdiags)
	return run
}

const famOutOfScopeSummary = "Marked resource outside its address's provider configuration"

func famWith(diags tfdiags.Diagnostics, summary string) []tfdiags.Diagnostic {
	return pairWith(diags, summary)
}

// famReference is the reference estate's shape: azurerm for the network,
// azapi for an identity, both blocks naming one subscription.
func famReference(extra string) string {
	return `
terraform {
  required_providers {
    azapi = {
      source = "azure/azapi"
    }
  }
}
provider "azurerm" {
  subscription_id = "` + famSub + `"
}
provider "azapi" {
  subscription_id = "` + famSub + `"
}
resource "azurerm_virtual_network" "app" {
  name                = "refaz-vnet"
  resource_group_name = "refaz-rg"
}
resource "azapi_resource" "uai" {
  type      = "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31"
  name      = "refaz-uai"
  parent_id = "` + famRG(famSub) + `"
}
` + extra
}

var (
	famVnetID = famID(famSub, "Microsoft.Network/virtualNetworks", "refaz-vnet")
	famUaiID  = famID(famSub, "Microsoft.ManagedIdentity/userAssignedIdentities", "refaz-uai")
)

func famReferenceResolutions(t *testing.T) []identity.Resolution {
	return []identity.Resolution{
		famConcrete(t, "azurerm_virtual_network.app", famVnetID),
		famConcrete(t, "azapi_resource.uai", famUaiID),
	}
}

// TestAzureSweepOfAzurermObjectIsNotOutsideItsConfiguration is the run-four
// failure, reproduced: a marked azurerm object and a marked azapi object in
// the one subscription both provider blocks name. The plan must not refuse
// the azurerm object as unreachable by its own configuration, and both
// declared instances are vouched.
func TestAzureSweepOfAzurermObjectIsNotOutsideItsConfiguration(t *testing.T) {
	listed := []azsweep.Resource{
		famListed(famVnetID, "Microsoft.Network/virtualNetworks", "azurerm_virtual_network.app"),
		famListed(famUaiID, "Microsoft.ManagedIdentity/userAssignedIdentities", "azapi_resource.uai"),
	}
	run := famPlan(t, famReference(""), famReferenceResolutions(t), listed, nil)
	if got := famWith(run.diags, famOutOfScopeSummary); len(got) != 0 {
		t.Errorf("the plan refuses an object its own configuration reaches:\n%s", pairRender(run.diags))
	}
	if run.diags.HasErrors() {
		t.Errorf("errors:\n%s", pairRender(run.diags))
	}
	verified := run.merged.MarkerVerified()
	for _, a := range []string{"azurerm_virtual_network.app", "azapi_resource.uai"} {
		if !verified[a] {
			t.Errorf("%s is not vouched by the sweep; verified %v", a, verified)
		}
	}
}

// TestAzureSweepOfAzapiObjectIsNotOutsideItsConfiguration is the corpus
// estate's half: an azurerm identity beside an azapi role assignment, the
// listed object the azurerm one.
func TestAzureSweepOfAzapiObjectIsNotOutsideItsConfiguration(t *testing.T) {
	uaiID := famID(famSub, "Microsoft.ManagedIdentity/userAssignedIdentities", "corpus-uai")
	src := `
terraform {
  required_providers {
    azapi = {
      source = "azure/azapi"
    }
  }
}
provider "azurerm" {
  subscription_id = "` + famSub + `"
}
provider "azapi" {
  subscription_id = "` + famSub + `"
}
resource "azurerm_user_assigned_identity" "uai" {
  name                = "corpus-uai"
  resource_group_name = "refaz-rg"
}
resource "azapi_resource" "role" {
  type      = "Microsoft.Authorization/roleAssignments@2022-04-01"
  name      = "6faae21a-0cd6-4536-8c23-a278823d12ed"
  parent_id = "` + famRG(famSub) + `"
}
`
	roleID := famRG(famSub) + "/providers/Microsoft.Authorization/roleAssignments/6faae21a-0cd6-4536-8c23-a278823d12ed"
	run := famPlan(t, src, []identity.Resolution{
		famConcrete(t, "azurerm_user_assigned_identity.uai", uaiID),
		famConcrete(t, "azapi_resource.role", roleID),
	}, []azsweep.Resource{famListed(uaiID, "Microsoft.ManagedIdentity/userAssignedIdentities", "azurerm_user_assigned_identity.uai")}, nil)
	if got := famWith(run.diags, famOutOfScopeSummary); len(got) != 0 || run.diags.HasErrors() {
		t.Errorf("the plan refuses:\n%s", pairRender(run.diags))
	}
	if !run.merged.MarkerVerified()["azurerm_user_assigned_identity.uai"] {
		t.Errorf("the identity is not vouched")
	}
}

// famAliases is two azurerm blocks: the default, and alias over the given
// subscription, the network declared under the alias when underAlias and
// a public IP under the other block.
func famAliases(aliasSub string, underAlias bool) string {
	// The other block always declares the public IP, so both
	// configurations have a resource and a pass of their own.
	provider, keep := "", "\n  provider            = azurerm.other"
	if underAlias {
		provider, keep = keep, ""
	}
	return `
provider "azurerm" {
  subscription_id = "` + famSub + `"
}
provider "azurerm" {
  alias           = "other"
  subscription_id = "` + aliasSub + `"
}
resource "azurerm_virtual_network" "app" {` + provider + `
  name                = "refaz-vnet"
  resource_group_name = "refaz-rg"
}
resource "azurerm_public_ip" "keep" {` + keep + `
  name                = "keep"
  resource_group_name = "refaz-rg"
}
`
}

// TestAzureObjectInAnotherAliasSubscriptionStillRefuses is the honest
// refusal, kept: the network is declared under the default azurerm block
// (subscription f001), and its marked object sits in f002, which only
// azurerm.other names. No configuration that declares it reaches it, so the
// plan refuses rather than creating a second object carrying its marker.
func TestAzureObjectInAnotherAliasSubscriptionStillRefuses(t *testing.T) {
	moved := famID(famOther, "Microsoft.Network/virtualNetworks", "refaz-vnet")
	keepID := famID(famOther, "Microsoft.Network/publicIPAddresses", "keep")
	run := famPlan(t, famAliases(famOther, false), []identity.Resolution{
		famConcrete(t, "azurerm_virtual_network.app", famVnetID),
		famConcrete(t, "azurerm_public_ip.keep", keepID),
	}, []azsweep.Resource{famListed(moved, "Microsoft.Network/virtualNetworks", "azurerm_virtual_network.app")}, nil)
	got := famWith(run.diags, famOutOfScopeSummary)
	if len(got) != 1 || got[0].Severity() != tfdiags.Error || !strings.Contains(got[0].Description().Detail, "azurerm_virtual_network.app") {
		t.Errorf("want one %q error for the network, got:\n%s", famOutOfScopeSummary, pairRender(run.diags))
	}
}

// TestAzureAliasInTheSameSubscriptionReachesItsObject: azurerm.other names
// the same subscription as the default block (in other casing), and the
// network is declared under it. Its object is in that subscription, so the
// configuration that declares it reaches it, and nothing refuses.
func TestAzureAliasInTheSameSubscriptionReachesItsObject(t *testing.T) {
	keepID := famID(famSub, "Microsoft.Network/publicIPAddresses", "keep")
	run := famPlan(t, famAliases(strings.ToUpper(famSub), true), []identity.Resolution{
		famConcrete(t, "azurerm_virtual_network.app", famVnetID),
		famConcrete(t, "azurerm_public_ip.keep", keepID),
	}, []azsweep.Resource{
		famListed(famVnetID, "Microsoft.Network/virtualNetworks", "azurerm_virtual_network.app"),
		famListed(keepID, "Microsoft.Network/publicIPAddresses", "azurerm_public_ip.keep"),
	}, nil)
	if got := famWith(run.diags, famOutOfScopeSummary); len(got) != 0 || run.diags.HasErrors() {
		t.Errorf("the plan refuses an object its own configuration reaches:\n%s", pairRender(run.diags))
	}
}
