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
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// One object, one diagnostic (follow-up to GitHub issues #2135 and #2136).
// Discovery files a displaced marker for an object carrying a declared
// address's marker at another id; the projection refuses an Azure object at
// a declared id carrying a marker that is not that block's. AKS's copy of a
// cluster's markers onto a declared route table is both at once, and the
// displaced warning there said the object was "not read" while the route
// table's own read was refusing it. These run discovery and the projection
// as a plan does, discovery's verified set handed to the projection through
// [liveOwnershipWith], and read both layers' diagnostics together.

const (
	pairEstate = "az-pair"
	pairSub    = "00000000-0000-0000-0000-000000000001"
)

func pairID(provider, name string) string {
	return "/subscriptions/" + pairSub + "/resourceGroups/Kept/providers/" + provider + "/" + name
}

// pairGraph is a Resource Graph that answers one subscription with a fixed
// listing.
type pairGraph struct{ resources []azsweep.Resource }

func (g pairGraph) Search(_ context.Context, sub, _ string) ([]azsweep.Resource, error) {
	if !strings.EqualFold(sub, pairSub) {
		return nil, nil
	}
	return g.resources, nil
}

// pairARM is the smallest azurerm the projection can read through: objects
// keyed by ARM id, compared case-insensitively.
type pairARM struct {
	mu      sync.Mutex
	objects map[string]cty.Value
}

func (a *pairARM) get(id string) (cty.Value, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, v := range a.objects {
		if strings.EqualFold(k, id) {
			return v, true
		}
	}
	return cty.NilVal, false
}

func (a *pairARM) provider() (addrs.AbsProviderConfig, *tofu.MockProvider) {
	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":                  {Type: cty.String, Computed: true},
		"name":                {Type: cty.String, Required: true},
		"resource_group_name": {Type: cty.String, Required: true},
		"location":            {Type: cty.String, Required: true},
		"dns_prefix":          {Type: cty.String, Optional: true},
		"tags":                {Type: cty.Map(cty.String), Optional: true},
	}}}
	p := &tofu.MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
		Provider: providers.Schema{Block: &configschema.Block{}},
		ResourceTypes: map[string]providers.Schema{
			"azurerm_route_table":        schema,
			"azurerm_kubernetes_cluster": schema,
		},
	}}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		obj, ok := a.get(r.Target.ID)
		if !ok {
			return providers.ImportResourceStateResponse{}
		}
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{TypeName: r.TypeName, State: obj}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		obj, ok := a.get(r.PriorState.GetAttr("id").AsString())
		if !ok {
			return providers.ReadResourceResponse{NewState: cty.NullVal(r.PriorState.Type())}
		}
		return providers.ReadResourceResponse{NewState: obj}
	}
	return addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}, p
}

// pairLister lists nothing: every instance here is concrete, so discovery's
// own scan has nothing to look for and the Resource Graph leg is the only
// listing.
type pairLister struct{ *tofu.MockProvider }

func (pairLister) ListResourceStream(context.Context, providers.ListResourceRequest, func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	return nil
}

func pairObject(id, name string, tags map[string]string) cty.Value {
	vals := map[string]cty.Value{}
	for k, v := range tags {
		vals[k] = cty.StringVal(v)
	}
	tagVal := cty.NullVal(cty.Map(cty.String))
	if len(vals) > 0 {
		tagVal = cty.MapVal(vals)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"id":                  cty.StringVal(id),
		"name":                cty.StringVal(name),
		"resource_group_name": cty.StringVal("Kept"),
		"location":            cty.StringVal("westeurope"),
		"dns_prefix":          cty.NullVal(cty.String),
		"tags":                tagVal,
	})
}

func pairMarkers(addr string) map[string]string {
	return map[string]string{markers.TagEstate: pairEstate, markers.TagAddress: markers.EscapeAddress(addr)}
}

func pairListed(id, typ, name string, tags map[string]string) azsweep.Resource {
	return azsweep.Resource{ID: strings.ToLower(id), Type: typ, Name: name, ResourceGroup: "kept", SubscriptionID: pairSub, Tags: tags}
}

func pairResolution(typ, name, id string) identity.Resolution {
	return identity.Resolution{
		Addr:     addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typ, Name: name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
		Class:    identity.ClassConcrete,
		ImportID: id,
	}
}

// pairPlan runs discovery over listed, then the projection over objects with
// discovery's verified set, and returns both layers' diagnostics.
func pairPlan(t *testing.T, src string, resolutions []identity.Resolution, listed []azsweep.Resource, objects map[string]cty.Value) tfdiags.Diagnostics {
	t.Helper()
	return pairPlanWith(t, src, resolutions, listed, objects, nil)
}

// pairPlanWith is [pairPlan] with a hook over the projection's options,
// for the -refresh=false case.
func pairPlanWith(t *testing.T, src string, resolutions []identity.Resolution, listed []azsweep.Resource, objects map[string]cty.Value, opts func(*projection.Options, addrs.AbsProviderConfig)) tfdiags.Diagnostics {
	t.Helper()
	cfg := liveLsLoadConfig(t, src)
	arm := &pairARM{objects: objects}
	provAddr, p := arm.provider()
	leg := discovery.ResourceGraphSweep{Coverage: azsweep.Coverage{pairSub: pairGraph{resources: listed}}, Subscriptions: []string{pairSub}}
	disco, diags := discovery.Discover(context.Background(), discovery.Request{
		Estate:      pairEstate,
		Config:      cfg,
		Provider:    pairLister{p},
		Resolutions: resolutions,
		Sweepers:    []discovery.Sweeper{leg},
	})
	if diags.HasErrors() {
		t.Fatalf("discovery: %s", diags.Err())
	}
	o := projection.Options{Ownership: liveOwnershipWith(pairEstate, disco, nil, nil)}
	if opts != nil {
		opts(&o, provAddr)
	}
	_, projDiags := projection.BuildWith(context.Background(), cfg, resolutions, projection.SingleProvider(provAddr, p), o)
	return diags.Append(projDiags)
}

func pairWith(diags tfdiags.Diagnostics, summary string) []tfdiags.Diagnostic {
	var out []tfdiags.Diagnostic
	for _, d := range diags {
		if d.Description().Summary == summary {
			out = append(out, d)
		}
	}
	return out
}

func pairRender(diags tfdiags.Diagnostics) string {
	var b strings.Builder
	for _, d := range diags {
		b.WriteString(d.Description().Summary + ": " + d.Description().Detail + "\n")
	}
	return b.String()
}

const pairDisplacedSummary = "Live resource displaced from the address it is marked for"

// TestAKSCopiedMarkerIsOneRefusalNoDisplacedWarning: the cluster and the
// route table it was given, both declared; the route table carries the
// cluster's markers, which AKS copied. The plan stops with exactly the
// #2135 refusal for the route table and no displaced warning about the
// same object.
func TestAKSCopiedMarkerIsOneRefusalNoDisplacedWarning(t *testing.T) {
	const route = "azurerm_route_table.rt"
	routeID := pairID("Microsoft.Network/routeTables", "rt")
	diags := aksCopiedPlan(t, nil)

	refusals := pairWith(diags, projection.SummaryOverwriteMarkedAtKey)
	if len(refusals) != 1 || refusals[0].Severity() != tfdiags.Error || !strings.Contains(refusals[0].Description().Detail, routeID) || !strings.Contains(refusals[0].Description().Detail, route) {
		t.Errorf("want exactly one %q Error for %s at %s, got:\n%s", projection.SummaryOverwriteMarkedAtKey, route, routeID, pairRender(diags))
	}
	if ds := pairWith(diags, pairDisplacedSummary); len(ds) != 0 {
		t.Errorf("a displaced warning beside the refusal for the same object:\n%s", pairRender(diags))
	}
}

// TestAKSCopiedMarkerUnderRefreshFalseIsTheDisplacedWarning is GitHub
// issue #2140, finding 4: the same estate planned with -refresh=false,
// the route table served from the state cache on its record envelope's
// word (listed live at its id, recorded as this estate's). That read
// inspects no tags, so nothing refuses the route table, and the displaced
// warning discovery left waiting on it is the object's one diagnostic.
// Before the fix discovery dropped the warning on the premise the refusal
// was certain, and the plan said nothing about the object at all.
func TestAKSCopiedMarkerUnderRefreshFalseIsTheDisplacedWarning(t *testing.T) {
	const cluster, route = "azurerm_kubernetes_cluster.aks", "azurerm_route_table.rt"
	routeID := pairID("Microsoft.Network/routeTables", "rt")
	served := false
	diags := aksCopiedPlan(t, func(o *projection.Options, provAddr addrs.AbsProviderConfig) {
		served = true
		routeAddr, d := addrs.ParseAbsResourceInstanceStr(route)
		if d.HasErrors() {
			t.Fatal(d.Err())
		}
		local, err := staterecord.NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		store := projection.NewRecordEnvelopeStore(local, projection.RecordKeyPrefix(pairEstate))
		if _, err := projection.SeedLocatedForInstance(t.Context(), store, routeAddr, provAddr, projection.LocatedRecord{ImportID: routeID}); err != nil {
			t.Fatal(err)
		}
		cache := states.NewState()
		src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: pairObject(routeID, "rt", pairMarkers(cluster))}).Encode(pairObject(routeID, "rt", nil).Type(), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		cache.RootModule().SetResourceInstanceCurrent(routeAddr.Resource, src, provAddr, addrs.NoKey)
		o.StateCache = cache
		o.CacheServesReads = true
		o.EnvelopeVouchServes = true
		o.RecordStore = store
		o.CacheVouchSightings = o.CacheVouchSightings.Add(provAddr, "azurerm_route_table", routeID)
	})
	if !served {
		t.Fatal("the options hook did not run")
	}

	if rs := pairWith(diags, projection.SummaryOverwriteMarkedAtKey); len(rs) != 0 {
		t.Fatalf("the route table was refused, so this is not the cache-served case:\n%s", pairRender(diags))
	}
	ds := pairWith(diags, pairDisplacedSummary)
	if len(ds) != 1 || !strings.Contains(ds[0].Description().Detail, cluster) {
		t.Errorf("want exactly one displaced warning about %s's marker at %s, got:\n%s", cluster, routeID, pairRender(diags))
	}
}

// aksCopiedPlan is the cluster and the route table it was given, both
// declared, the route table carrying the cluster's markers, which AKS
// copied.
func aksCopiedPlan(t *testing.T, opts func(*projection.Options, addrs.AbsProviderConfig)) tfdiags.Diagnostics {
	t.Helper()
	const cluster = "azurerm_kubernetes_cluster.aks"
	clusterID := pairID("Microsoft.ContainerService/managedClusters", "aks")
	routeID := pairID("Microsoft.Network/routeTables", "rt")
	return pairPlanWith(t, `
provider "azurerm" {
  features {}
}
resource "azurerm_kubernetes_cluster" "aks" {
  name                = "aks"
  resource_group_name = "Kept"
  location            = "westeurope"
  dns_prefix          = "aks"
}
resource "azurerm_route_table" "rt" {
  name                = "rt"
  resource_group_name = "Kept"
  location            = "westeurope"
}
`,
		[]identity.Resolution{
			pairResolution("azurerm_kubernetes_cluster", "aks", clusterID),
			pairResolution("azurerm_route_table", "rt", routeID),
		},
		[]azsweep.Resource{
			pairListed(clusterID, "microsoft.containerservice/managedclusters", "aks", pairMarkers(cluster)),
			pairListed(routeID, "microsoft.network/routetables", "rt", pairMarkers(cluster)),
		},
		map[string]cty.Value{
			clusterID: pairObject(clusterID, "aks", pairMarkers(cluster)),
			routeID:   pairObject(routeID, "rt", pairMarkers(cluster)),
		}, opts)
}

// TestPlainAzureRenameKeepsTheDisplacedWarning is the bound: the route
// table renamed rt-old -> rt-new in configuration, rt-old still carrying
// its marker and nothing at rt-new. No declared instance reads rt-old, so
// nothing refuses it and the displaced warning is the one diagnostic, in
// its "not read" wording, which is true of it.
func TestPlainAzureRenameKeepsTheDisplacedWarning(t *testing.T) {
	const route = "azurerm_route_table.rt"
	oldID := pairID("Microsoft.Network/routeTables", "rt-old")
	newID := pairID("Microsoft.Network/routeTables", "rt-new")
	diags := pairPlan(t, `
provider "azurerm" {
  features {}
}
resource "azurerm_route_table" "rt" {
  name                = "rt-new"
  resource_group_name = "Kept"
  location            = "westeurope"
}
`,
		[]identity.Resolution{pairResolution("azurerm_route_table", "rt", newID)},
		[]azsweep.Resource{pairListed(oldID, "microsoft.network/routetables", "rt-old", pairMarkers(route))},
		map[string]cty.Value{oldID: pairObject(oldID, "rt-old", pairMarkers(route))})

	ds := pairWith(diags, pairDisplacedSummary)
	if len(ds) != 1 || !strings.Contains(ds[0].Description().Detail, "it is bound to no address, it is not read") {
		t.Errorf("want one displaced warning in its not-read wording, got:\n%s", pairRender(diags))
	}
	if rs := pairWith(diags, projection.SummaryOverwriteMarkedAtKey); len(rs) != 0 {
		t.Errorf("a refusal for an object no declared instance reads:\n%s", pairRender(diags))
	}
}
