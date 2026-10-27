// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/pkg/azsweep"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// AKS and the existing resources a cluster is given. Microsoft's "Use Azure
// tags in AKS" (https://learn.microsoft.com/en-us/azure/aks/use-tags): the
// cluster's tags are assigned to its route table, public IP, virtual network
// and the rest, and "If you're using existing resources when you create a
// new cluster, such as an IP address or route table, the az aks create
// command overwrites the set of tags." The cluster's tags are the ones this
// estate stamped on it, so an existing route table the estate also declares
// comes back from Resource Graph carrying the cluster's tofu-estate and
// tofu-address, not its own.

const (
	aksClusterAddr = "azurerm_kubernetes_cluster.aks"
	aksRouteAddr   = "azurerm_route_table.rt"
)

func aksClusterID() string {
	return rgID(rgSub1, "Kept") + "/providers/Microsoft.ContainerService/managedClusters/aks"
}

func aksRouteTableID() string {
	return rgID(rgSub1, "Kept") + "/providers/Microsoft.Network/routeTables/rt"
}

// aksTagged is a resource Resource Graph lists carrying the cluster's tags:
// the estate marker and the cluster's address, plus a tag of the operator's.
func aksTagged(id, typ, name string) azsweep.Resource {
	return azsweep.Resource{
		ID:             strings.ToLower(id),
		Type:           typ,
		Name:           name,
		ResourceGroup:  "kept",
		SubscriptionID: rgSub1,
		Tags: map[string]string{
			markers.TagEstate:  rgEstate,
			markers.TagAddress: markers.EscapeAddress(aksClusterAddr),
			"dept":             "IT",
		},
	}
}

func aksResolution(typ, name, id string) identity.Resolution {
	return identity.Resolution{
		Addr:     addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typ, Name: name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
		Class:    identity.ClassConcrete,
		ImportID: id,
	}
}

// TestResourceGraphLegAKSCopiedClusterMarker pins what the sweep does with
// a declared route table that AKS retagged with the cluster's markers. It
// is not proposed for removal and not held (AKS sets no managedBy on a
// resource it was given).
//
// It carries the cluster's address at an ARM id that is not the one the
// cluster's address resolves to, which [configIdentityMismatch] compares
// for two ARM ids (#2136), so it does not vouch for the cluster (only the
// cluster's own sighting does), and it never vouches for the route table's
// own instance. The projection therefore reads it at the route table's id
// and refuses it by name (#2135, ownership_aks_test.go in
// internal/live/projection), and that refusal is the one diagnostic for
// this object: the displaced marker the comparison would also file is left
// out ([taggedCandidate.refusedAt]), since it said the object was "not
// read" while the route table's own read was refusing it.
// internal/command's TestAKSCopiedMarkerIsOneRefusalNoDisplacedWarning runs
// the pair through both layers.
func TestResourceGraphLegAKSCopiedClusterMarker(t *testing.T) {
	cfg := azConfigWith(t, `
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
`)
	cluster := aksTagged(aksClusterID(), "microsoft.containerservice/managedclusters", "aks")
	route := aksTagged(aksRouteTableID(), "microsoft.network/routetables", "rt")
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {cluster, route}}}
	leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}

	res, diags := discoverAzureWith(t, cfg, leg,
		keptResolution(rgSub1),
		aksResolution("azurerm_kubernetes_cluster", "aks", aksClusterID()),
		aksResolution("azurerm_route_table", "rt", aksRouteTableID()),
	)
	assertNoErrors(t, diags)
	if len(res.Orphans) != 0 || len(res.ControllerHeld) != 0 {
		t.Errorf("orphans %+v, held %+v; want none", res.Orphans, res.ControllerHeld)
	}
	if len(res.Problems) != 0 {
		t.Errorf("problems %+v; want none: the route table's own read refuses it, and a displaced marker beside that refusal is a second diagnostic for one object", res.Problems)
	}
	vouched := map[string]int{}
	for _, a := range res.VerifiedDeclared {
		vouched[a.String()]++
	}
	if vouched[aksRouteAddr] != 0 {
		t.Errorf("the route table was vouched for by a sighting carrying the cluster's marker: %v", res.VerifiedDeclared)
	}
	if vouched[aksClusterAddr] != 1 {
		t.Errorf("VerifiedDeclared %v; want the cluster vouched by its own sighting only", res.VerifiedDeclared)
	}
}

// TestResourceGraphLegDisplacedNamesTheObjectsOwnType is GitHub issue
// #2138's finding on the leg above: a route table the configuration does
// not declare, carrying the cluster's copied markers, is a displaced
// marker (the cluster's address resolves to another ARM id). The leg files
// the object under the type its marker names, and the warning used to say
// "A live azurerm_kubernetes_cluster with identity .../routeTables/rt". It
// names the object by its own type now, and the address by the address's.
func TestResourceGraphLegDisplacedNamesTheObjectsOwnType(t *testing.T) {
	cfg := azConfigWith(t, `
resource "azurerm_kubernetes_cluster" "aks" {
  name                = "aks"
  resource_group_name = "Kept"
  location            = "westeurope"
  dns_prefix          = "aks"
}
`)
	cluster := aksTagged(aksClusterID(), "microsoft.containerservice/managedclusters", "aks")
	route := aksTagged(aksRouteTableID(), "microsoft.network/routetables", "rt")
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {cluster, route}}}
	leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}

	res, diags := discoverAzureWith(t, cfg, leg,
		keptResolution(rgSub1),
		aksResolution("azurerm_kubernetes_cluster", "aks", aksClusterID()),
	)
	assertNoErrors(t, diags)
	if len(res.Problems) != 1 || res.Problems[0].Kind != ProblemDisplacedMarker {
		t.Fatalf("problems %+v; want one displaced marker for the route table", res.Problems)
	}
	p := res.Problems[0]
	if want := "A live azurerm_route_table (Microsoft.Network/routeTables) with identity"; !strings.HasPrefix(p.Detail, want) {
		t.Errorf("the warning does not name the object by its own type (%q): %s", want, p.Detail)
	}
	if strings.Contains(p.Detail, "A live azurerm_kubernetes_cluster") {
		t.Errorf("the warning calls the route table a cluster: %s", p.Detail)
	}
	if want := `the address "azurerm_kubernetes_cluster.aks"`; !strings.Contains(p.Detail, want) {
		t.Errorf("the warning does not name the marked address (%q): %s", want, p.Detail)
	}
	if p.TypeName != "azurerm_kubernetes_cluster" {
		t.Errorf("the problem is filed under %q, want the marked address's type", p.TypeName)
	}
}

// TestAzureObjectType: one azurerm type for the ARM type names it; several,
// or none, leave the ARM type alone.
func TestAzureObjectType(t *testing.T) {
	for in, want := range map[string]string{
		"microsoft.network/routetables":      "azurerm_route_table (Microsoft.Network/routeTables)",
		"microsoft.compute/virtualmachines":  "Microsoft.Compute/virtualMachines",
		"microsoft.example/notanazurermtype": "microsoft.example/notanazurermtype",
		"":                                   "",
	} {
		if got := azureObjectType(in); got != want {
			t.Errorf("azureObjectType(%q) = %q, want %q", in, got, want)
		}
	}
}
