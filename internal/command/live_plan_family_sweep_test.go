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
	"github.com/intentius/choudoufu/pkg/azsweep"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/pkg/staterecord"
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
	// What liveDiscover does first: one memo for the run's passes.
	p.beginResourceGraphSweep()
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
	// Both passes query the subscription their blocks name, and the
	// plan sends Resource Graph one query for it.
	if got := run.graph.asked[famSub]; got != 1 {
		t.Errorf("subscription %s queried %d times in one plan, want once", famSub, got)
	}
	// Each object filed by the pass of its own provider, and no other.
	for _, p := range run.passes {
		for _, s := range p.Result.DeclaredSightings {
			if !s.InScope {
				t.Errorf("pass %s sighted %s outside its scope", p.Provider, s.Addr)
			}
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

// famRemovals is every resolution the merged plan carries for addr that a
// removal put there: an undeclared one (its block gone), or a count
// instance past the block's count, which keeps its block.
func famRemovals(run famRun, addr string) []identity.Resolution {
	var out []identity.Resolution
	for _, r := range run.merged.Resolutions {
		if r.Addr.String() == addr {
			out = append(out, r)
		}
	}
	return out
}

// famAssertOneRemoval holds addr to exactly one proposed destroy, filed by
// one pass only, that pass's provider being provider, and, for an
// undeclared address, read through a configuration of it.
func famAssertOneRemoval(t *testing.T, run famRun, addr string, provider addrs.Provider) {
	t.Helper()
	got := famRemovals(run, addr)
	if len(got) != 1 {
		t.Errorf("%d resolutions of %s in the merged plan, want its one removal:\n%s", len(got), addr, run.merged)
		return
	}
	if got[0].Undeclared {
		if p := run.providerOf[addr]; p.Provider != provider {
			t.Errorf("the removal of %s is read through %s, want a %s configuration", addr, p, provider)
		}
	}
	var filers []string
	for _, p := range run.passes {
		for _, o := range p.Result.Orphans {
			if o.Addr.String() != addr {
				continue
			}
			filers = append(filers, p.Provider.String())
			if p.Provider.Provider != provider || !o.Removal {
				t.Errorf("pass %s filed %s (removal %v, withheld %q)", p.Provider, addr, o.Removal, o.Withheld)
			}
		}
	}
	if len(filers) != 1 {
		t.Errorf("%s filed as an orphan by %d passes %v, want one", addr, len(filers), filers)
	}
}

func famAssertNoProblems(t *testing.T, run famRun, kinds ...discovery.ProblemKind) {
	t.Helper()
	for _, k := range kinds {
		for _, p := range run.merged.ProblemsOfKind(k) {
			t.Errorf("%s: %s", k, p.Detail)
		}
	}
	if run.diags.HasErrors() {
		t.Errorf("errors:\n%s", pairRender(run.diags))
	}
}

// TestAzureDay2RemoveProposesTheSweptDestroyUnderAzurerm is the reference
// estate's day2_remove with the sweep live: the taggable public IP's block
// removed, so only the sweep finds it (the record leg leaves a taggable
// type to the sweep). The destroy is proposed once, read through the
// azurerm configuration, with no duplicate from the azapi pass and no
// RECORDED_PROVIDER_ABSENT over the record the estate still holds for it.
func TestAzureDay2RemoveProposesTheSweptDestroyUnderAzurerm(t *testing.T) {
	const pip = "azurerm_public_ip.pip"
	pipID := famID(famSub, "Microsoft.Network/publicIPAddresses", "refaz-pip")
	local, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	azurermCfg := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: famAzurerm}
	store := projection.NewRecordEnvelopeStore(local, projection.RecordKeyPrefix(famEstate))
	if _, err := projection.SeedLocatedForInstance(t.Context(), store, famAddr(t, pip), azurermCfg, projection.LocatedRecord{ImportID: pipID}); err != nil {
		t.Fatal(err)
	}

	run := famPlan(t, famReference(""), famReferenceResolutions(t), []azsweep.Resource{
		famListed(famVnetID, "Microsoft.Network/virtualNetworks", "azurerm_virtual_network.app"),
		famListed(famUaiID, "Microsoft.ManagedIdentity/userAssignedIdentities", "azapi_resource.uai"),
		famListed(pipID, "Microsoft.Network/publicIPAddresses", pip),
	}, local)
	famAssertNoProblems(t, run, discovery.ProblemRecordedProviderAbsent, discovery.ProblemCollision, discovery.ProblemOutOfScopeMarker, discovery.ProblemRemovedTypeUnserved)
	famAssertOneRemoval(t, run, pip, famAzurerm)
	if got := run.providerOf[pip]; got.String() != azurermCfg.String() {
		t.Errorf("the removal is read through %s, want %s", got, azurermCfg)
	}
}

// TestAzureDay2CountScalesDownEachProvidersShards is day2_count with the
// sweep live: an azurerm identity block and an azapi block, each scaled
// from two instances to one. Each surplus instance is removed once,
// through its own provider's configuration.
func TestAzureDay2CountScalesDownEachProvidersShards(t *testing.T) {
	shardID := func(i string) string {
		return famID(famSub, "Microsoft.ManagedIdentity/userAssignedIdentities", "shard-"+i)
	}
	ashardID := func(i string) string {
		return famID(famSub, "Microsoft.ManagedIdentity/userAssignedIdentities", "ashard-"+i)
	}
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
resource "azurerm_user_assigned_identity" "shard" {
  count               = 1
  name                = "shard-${count.index}"
  resource_group_name = "refaz-rg"
}
resource "azapi_resource" "ashard" {
  count     = 1
  type      = "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31"
  name      = "ashard-${count.index}"
  parent_id = "` + famRG(famSub) + `"
}
`
	const uai = "Microsoft.ManagedIdentity/userAssignedIdentities"
	run := famPlan(t, src, []identity.Resolution{
		famConcrete(t, "azurerm_user_assigned_identity.shard[0]", shardID("0")),
		famConcrete(t, "azapi_resource.ashard[0]", ashardID("0")),
	}, []azsweep.Resource{
		famListed(shardID("0"), uai, "azurerm_user_assigned_identity.shard[0]"),
		famListed(shardID("1"), uai, "azurerm_user_assigned_identity.shard[1]"),
		famListed(ashardID("0"), uai, "azapi_resource.ashard[0]"),
		famListed(ashardID("1"), uai, "azapi_resource.ashard[1]"),
	}, nil)
	famAssertNoProblems(t, run, discovery.ProblemCollision, discovery.ProblemOutOfScopeMarker)
	famAssertOneRemoval(t, run, "azurerm_user_assigned_identity.shard[1]", famAzurerm)
	famAssertOneRemoval(t, run, "azapi_resource.ashard[1]", famAzapi)
	for _, kept := range []string{"azurerm_user_assigned_identity.shard[0]", "azapi_resource.ashard[0]"} {
		for _, p := range run.passes {
			for _, o := range p.Result.Orphans {
				if o.Addr.String() == kept {
					t.Errorf("pass %s filed the kept %s as an orphan", p.Provider, kept)
				}
			}
		}
		if !run.merged.MarkerVerified()[kept] {
			t.Errorf("the kept %s is not vouched", kept)
		}
	}
}

// TestAzureDisplacedMarkerIsReportedOnce: the network's name changed and the
// old object still stands carrying its marker. Both blocks name the
// subscription, so both passes query it; the old object is reported
// displaced once, by the azurerm pass, and not refused as out of scope.
func TestAzureDisplacedMarkerIsReportedOnce(t *testing.T) {
	oldID := famID(famSub, "Microsoft.Network/virtualNetworks", "refaz-vnet-old")
	run := famPlan(t, famReference(""), famReferenceResolutions(t), []azsweep.Resource{
		famListed(oldID, "Microsoft.Network/virtualNetworks", "azurerm_virtual_network.app"),
		famListed(famUaiID, "Microsoft.ManagedIdentity/userAssignedIdentities", "azapi_resource.uai"),
	}, nil)
	if got := famWith(run.diags, famOutOfScopeSummary); len(got) != 0 {
		t.Errorf("refused as out of scope:\n%s", pairRender(run.diags))
	}
	got := run.merged.ProblemsOfKind(discovery.ProblemDisplacedMarker)
	if len(got) != 1 || got[0].Marker != "azurerm_virtual_network.app" {
		t.Errorf("want one displaced marker for the network, got %d:\n%s", len(got), pairRender(run.diags))
	}
	if run.merged.MarkerVerified()["azurerm_virtual_network.app"] {
		t.Errorf("the displaced object vouches for the network")
	}
}

// TestAzureObjectDeclaredThroughBothProvidersIsFiledOnce: one virtual
// network declared through azurerm and azapi at one ARM id, the identity
// layer's duplicate-identity refusal (TestAzureDuplicateAcrossProvidersAndCase).
// Discovery adds nothing on top: the object is filed by one pass, the one
// its marker's address is declared under, with no orphan and no
// out-of-scope refusal.
func TestAzureObjectDeclaredThroughBothProvidersIsFiledOnce(t *testing.T) {
	src := famReference(`
resource "azapi_resource" "vnet" {
  type      = "Microsoft.Network/virtualNetworks@2024-05-01"
  name      = "refaz-vnet"
  parent_id = "` + famRG(famSub) + `"
}
`)
	resolutions := append(famReferenceResolutions(t), famConcrete(t, "azapi_resource.vnet", strings.ToUpper(famVnetID)))
	run := famPlan(t, src, resolutions, []azsweep.Resource{
		famListed(famVnetID, "Microsoft.Network/virtualNetworks", "azurerm_virtual_network.app"),
	}, nil)
	famAssertNoProblems(t, run, discovery.ProblemOutOfScopeMarker, discovery.ProblemCollision, discovery.ProblemDisplacedMarker)
	var sightings int
	for _, p := range run.passes {
		for _, s := range p.Result.DeclaredSightings {
			sightings++
			if p.Provider.Provider != famAzurerm || !s.InScope {
				t.Errorf("pass %s sighted %s (in scope %v)", p.Provider, s.Addr, s.InScope)
			}
		}
		if len(p.Result.Orphans) != 0 {
			t.Errorf("pass %s filed orphans %+v", p.Provider, p.Result.Orphans)
		}
	}
	if sightings != 1 {
		t.Errorf("the object was sighted %d times across the passes, want once", sightings)
	}
}
