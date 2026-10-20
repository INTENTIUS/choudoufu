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
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// AKS rewrites the tags of existing resources a cluster is given. From
// Microsoft's "Use Azure tags in AKS"
// (https://learn.microsoft.com/en-us/azure/aks/use-tags): the cluster's tags
// are assigned to its route table, public IP, virtual network and the rest,
// and "If you're using existing resources when you create a new cluster,
// such as an IP address or route table, the az aks create command
// overwrites the set of tags. If you delete the cluster later, any tags set
// by the cluster are removed."
//
// So an estate that declares the route table or public IP it hands its
// cluster sees one of two live shapes on the next plan, neither written by
// choudoufu:
//
//   - the cluster's tags, which this estate stamped, so the object carries
//     this estate's tofu-estate and the CLUSTER's tofu-address;
//   - no marker at all, once the cluster is deleted (AKS removes what it
//     set), or when the cluster is not this estate's.
//
// These tests pin what the projection's ownership check does with each, on
// the binding path an azurerm type with a resource identity schema takes:
// the object is read at the ARM id the configuration computes, so
// atDeclaredKey holds.

const (
	aksEstate      = "az-aks"
	aksClusterAddr = "azurerm_kubernetes_cluster.aks"
	aksRouteID     = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.Network/routeTables/rt"
	aksPublicIPID  = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/pip"
)

func aksBuilder(t *testing.T, own *Ownership) *builder {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
  }
}
resource "azurerm_route_table" "rt" {
  name                = "rt"
  resource_group_name = "rg"
  location            = "westeurope"
}
resource "azurerm_public_ip" "pip" {
  name                = "pip"
  resource_group_name = "rg"
  location            = "westeurope"
  allocation_method   = "Static"
}
resource "azurerm_kubernetes_cluster" "aks" {
  name                = "aks"
  resource_group_name = "rg"
  location            = "westeurope"
  dns_prefix          = "aks"
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	b := k8sOwnershipBuilder(own)
	b.cfg = loadConfig(t, dir)
	return b
}

// aksTarget is one existing resource AKS retags.
type aksTarget struct {
	typ, name, id string
}

func (a aksTarget) addr() addrs.AbsResourceInstance {
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: a.typ, Name: a.name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
}

var aksTargets = []aksTarget{
	{"azurerm_route_table", "rt", aksRouteID},
	{"azurerm_public_ip", "pip", aksPublicIPID},
}

// aksLive is the object read back at a.id carrying tags.
func aksLive(a aksTarget, tags map[string]string) cty.Value {
	obj := armObject(tags)
	return cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal(a.id),
		"name": cty.StringVal(a.name),
		"tags": obj.GetAttr("tags"),
	})
}

// aksCopiedTags is the set AKS writes from the cluster: the operator's tag
// and the markers this estate stamped on the cluster.
func aksCopiedTags(estate string) map[string]string {
	return map[string]string{
		"dept":             "IT",
		markers.TagEstate:  estate,
		markers.TagAddress: markers.EscapeAddress(aksClusterAddr),
	}
}

// TestAKSRetag_markersGoneIsTheOverwriteRefusal: with no marker left on the
// object (the cluster deleted, or a cluster outside this estate whose tags
// carry none), the declared route table or public IP reads as
// declared_untagged at its declared id, and the ARM create-or-update
// refusal (#1120) stops the plan by name with an Error. No create is
// proposed, so no duplicate and no PUT over the object.
func TestAKSRetag_markersGoneIsTheOverwriteRefusal(t *testing.T) {
	for _, a := range aksTargets {
		for name, tags := range map[string]map[string]string{
			"no tags":           nil,
			"only the operator": {"dept": "IT"},
		} {
			t.Run(a.typ+"/"+name, func(t *testing.T) {
				b := aksBuilder(t, &Ownership{Estate: aksEstate})
				if got := b.checkOwnership(a.addr(), a.typ, a.id, armTagsSchema(), aksLive(a, tags), true, false, false); got != ownershipUnowned {
					t.Fatalf("verdict %d, want ownershipUnowned", got)
				}
				errs := errorDiagsWith(b.diags, SummaryOverwriteAtKey)
				if len(errs) != 1 {
					t.Fatalf("want exactly one Error %q, got:\n%s", SummaryOverwriteAtKey, renderDiags(b.diags))
				}
				if d := errs[0].Description().Detail; !strings.Contains(d, a.id) || !strings.Contains(d, "hashicorp/azurerm reads the id before that PUT") {
					t.Errorf("the refusal does not name the id and azurerm's pre-read:\n%s", d)
				}
				if om, ok := b.omitted[a.addr().String()]; !ok || om.Reason != ReasonUnowned {
					t.Errorf("omission = %+v (present %v), want ReasonUnowned", om, ok)
				}
			})
		}
	}
}

// TestAKSRetag_clusterMarkersCopiedIsOnlyTheWrongAddressWarning pins the
// shape AKS actually produces while the cluster lives: this estate's
// tofu-estate with the cluster's tofu-address. That is not declared_untagged
// (the object is marked, by this estate, for another of its instances), so
// the #1120 overwrite refusal does not reach it. It is #244's wrong-address
// refusal, a Warning: the object is left out of the prior state and the
// plan proposes the create, which on ARM is a PUT to the existing id. The
// only thing between that plan and the PUT is azurerm's own pre-read
// (tf.ImportAsExistsError), which the provider's features block can turn
// off. This test records today's behaviour; it is the gap the AKS issue
// names.
func TestAKSRetag_clusterMarkersCopiedIsOnlyTheWrongAddressWarning(t *testing.T) {
	for _, a := range aksTargets {
		t.Run(a.typ, func(t *testing.T) {
			b := aksBuilder(t, &Ownership{Estate: aksEstate})
			if got := b.checkOwnership(a.addr(), a.typ, a.id, armTagsSchema(), aksLive(a, aksCopiedTags(aksEstate)), true, false, false); got != ownershipUnowned {
				t.Fatalf("verdict %d, want ownershipUnowned", got)
			}
			if b.diags.HasErrors() {
				t.Fatalf("an Error was raised; this test pins the warning-only behaviour:\n%s", renderDiags(b.diags))
			}
			if !hasDiag(b.diags, SummaryWrongAddress, aksClusterAddr) {
				t.Errorf("want the %q warning naming %s, got:\n%s", SummaryWrongAddress, aksClusterAddr, renderDiags(b.diags))
			}
			if hasDiag(b.diags, SummaryOverwriteAtKey, "") {
				t.Errorf("the overwrite refusal fired:\n%s", renderDiags(b.diags))
			}
			if om, ok := b.omitted[a.addr().String()]; !ok || om.Reason != ReasonUnowned {
				t.Errorf("omission = %+v (present %v), want ReasonUnowned, which proposes the create", om, ok)
			}
		})
	}
}

// TestAKSRetag_clusterOfAnotherEstate: a cluster stamped by a different
// estate copies that estate's tofu-estate onto this estate's route table.
// #1166's another-estate refusal, also a Warning, also outside the overwrite
// refusal (pinned for dns zones by TestARMOverwrite_whatTheRuleDoesNotReach).
func TestAKSRetag_clusterOfAnotherEstate(t *testing.T) {
	a := aksTargets[0]
	b := aksBuilder(t, &Ownership{Estate: aksEstate})
	if got := b.checkOwnership(a.addr(), a.typ, a.id, armTagsSchema(), aksLive(a, aksCopiedTags("platform")), true, false, false); got != ownershipUnowned {
		t.Fatalf("verdict %d, want ownershipUnowned", got)
	}
	if b.diags.HasErrors() || !hasDiag(b.diags, SummaryOutsideEstate, `tofu-estate="platform"`) {
		t.Errorf("want only the another-estate warning, got:\n%s", renderDiags(b.diags))
	}
}

// TestAKSRetag_recordFirstFallsBack: an estate with a record store reads
// the record before the identity route. The record names the declared id,
// and the object's own tofu-address no longer confirms it in either shape,
// so the record is reported stale and the instance falls back to the
// identity route - which then lands on one of the two tests above.
func TestAKSRetag_recordFirstFallsBack(t *testing.T) {
	a := aksTargets[0]
	for name, tags := range map[string]map[string]string{
		"markers gone":           {"dept": "IT"},
		"cluster markers copied": aksCopiedTags(aksEstate),
	} {
		t.Run(name, func(t *testing.T) {
			b := aksBuilder(t, &Ownership{Estate: aksEstate})
			if got := b.checkOwnershipAt(a.addr(), a.typ, a.id, armTagsSchema(), aksLive(a, tags), true, false, true, true); got != ownershipStale {
				t.Fatalf("verdict %d, want ownershipStale", got)
			}
			if !hasDiag(b.diags, SummaryStaleRecord, a.addr().String()) || b.diags.HasErrors() {
				t.Errorf("want only the stale-record warning, got:\n%s", renderDiags(b.diags))
			}
		})
	}
}

// TestAKSRetag_markersRecordSelectionAdmits is the projection's half of
// the strict { markers "record" { types = [...] } } option: a located
// instance - the record the estate's own apply wrote is the ownership proof
// - has its live tags left unread, so neither AKS shape refuses it, and
// AdjustIgnoreChanges keeps the live marker keys out of the diff
// (nodestamp_ignorechanges.go), so the cluster's copied markers would not
// be planned away.
//
// The other half is not there today. At azurerm 5.10.0 the selection is
// refused for these types before it reaches this branch
// (TestSelectedLocatedAKSRetaggedTypes in internal/live/identity: the
// identity schema's subscription_id is not a resource argument), so
// identity never routes them as located and this verdict is not reached.
// Pinned so that whoever makes the selection reachable finds the ownership
// half already does the right thing.
func TestAKSRetag_markersRecordSelectionAdmits(t *testing.T) {
	for _, a := range aksTargets {
		for name, tags := range map[string]map[string]string{
			"markers gone":           {"dept": "IT"},
			"cluster markers copied": aksCopiedTags(aksEstate),
		} {
			t.Run(a.typ+"/"+name, func(t *testing.T) {
				b := aksBuilder(t, &Ownership{Estate: aksEstate})
				if got := b.checkOwnership(a.addr(), a.typ, a.id, armTagsSchema(), aksLive(a, tags), true, true, false); got != ownershipOK {
					t.Fatalf("verdict %d, want ownershipOK", got)
				}
				if len(b.diags) != 0 {
					t.Errorf("diagnostics:\n%s", renderDiags(b.diags))
				}
			})
		}
	}
}

// TestAKSRetag_markerNotStoredCannotSeeIt pins why GitHub issue #1192's
// read-back ("Ownership marker was not stored") does not report the AKS
// overwrite. The hook judges the object the provider returned from THIS
// resource's own write. The route table's create returns its markers
// intact; AKS rewrites them later, inside the cluster's create, which is a
// different resource's write whose returned object is the cluster. Nothing
// in the apply reads the route table again.
//
// The hook does cover the arm-tags surface: a write whose returned tags lack
// the marker (an Azure Policy modify effect on the way in, say) is reported
// for azurerm as for AWS and Kubernetes.
func TestAKSRetag_markerNotStoredCannotSeeIt(t *testing.T) {
	n := &NodeResolver{Estate: aksEstate}
	a := aksTargets[0]
	sent := map[string]string{"dept": "IT", markers.TagEstate: aksEstate, markers.TagAddress: a.addr().String()}

	t.Run("the route table's own create is silent", func(t *testing.T) {
		v := aksLive(a, sent)
		if diags := n.VerifyAppliedMarkers(context.Background(), a.addr(), plans.Create, v, v, armTagsSchema()); len(diags) != 0 {
			t.Fatalf("diagnostics: %s", diags.Err())
		}
	})

	t.Run("a strip on the way in is reported on arm-tags", func(t *testing.T) {
		diags := n.VerifyAppliedMarkers(context.Background(), a.addr(), plans.Create,
			aksLive(a, sent), aksLive(a, map[string]string{"dept": "IT"}), armTagsSchema())
		if len(diags) != 1 || diags[0].Description().Summary != SummaryMarkerNotStored || diags[0].Severity() != tfdiags.Warning {
			t.Fatalf("want one %q warning, got: %v", SummaryMarkerNotStored, diags.Err())
		}
	})
}
