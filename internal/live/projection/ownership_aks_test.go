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

// TestAKSRetag_clusterMarkersCopiedIsTheMarkedOverwriteRefusal pins the
// shape AKS actually produces while the cluster lives: this estate's
// tofu-estate with the cluster's tofu-address. It is not declared_untagged
// (the object is marked, by this estate, for another of its instances), so
// before GitHub issue #2135 it was #244's wrong-address Warning, the
// object left out of the prior state and the create proposed, which on ARM
// is a PUT to the existing id. The maintainer's ruling on #2135 widens
// #1120's refusal to it: an Error, no create, the marker found named, and
// the causes and remedies said.
//
// Under every declared_untagged verb too (GitHub issue #2140, finding 3):
// the object carries this estate's marker, so it is in no declared_untagged
// quadrant, and no verb there reaches it or softens the refusal.
func TestAKSRetag_clusterMarkersCopiedIsTheMarkedOverwriteRefusal(t *testing.T) {
	for _, a := range aksTargets {
		for _, verb := range declaredUntaggedVerbs {
			t.Run(a.typ+"/declared_untagged="+verb, func(t *testing.T) {
				own := &Ownership{Estate: aksEstate}
				if verb != "" {
					own.Policy = buildPolicy(t, "", verb)
				}
				clusterMarkersCopiedRefused(t, aksBuilder(t, own), a)
			})
		}
	}
}

// declaredUntaggedVerbs is every declared_untagged verb, with "" for no
// policy block: the loop #2135's marked overwrite refusal must hold under.
var declaredUntaggedVerbs = []string{"", "refuse", "keep", "report", "adopt", "converge"}

// clusterMarkersCopiedRefused is TestAKSRetag_clusterMarkersCopiedIsTheMarkedOverwriteRefusal's
// assertion for one target under one builder.
func clusterMarkersCopiedRefused(t *testing.T, b *builder, a aksTarget) {
	t.Helper()
	if got := b.checkOwnership(a.addr(), a.typ, a.id, armTagsSchema(), aksLive(a, aksCopiedTags(aksEstate)), true, false, false); got != ownershipUnowned {
		t.Fatalf("verdict %d, want ownershipUnowned", got)
	}
	errs := errorDiagsWith(b.diags, SummaryOverwriteMarkedAtKey)
	if len(errs) != 1 {
		t.Fatalf("want exactly one Error %q, got:\n%s", SummaryOverwriteMarkedAtKey, renderDiags(b.diags))
	}
	detail := errs[0].Description().Detail
	for _, want := range []string{
		a.id,
		`tofu-estate="` + aksEstate + `"`,
		`tofu-address="` + aksClusterAddr + `"`,
		"another address than " + a.addr().String(),
		"AKS copies a cluster's tags",
		"Azure Policy assignment with a modify effect",
		`strict { markers "record" { types = ["` + a.typ + `"] } }`,
		"moved block",
		"hashicorp/azurerm reads the id before that PUT",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, detail)
		}
	}
	if hasDiag(b.diags, SummaryWrongAddress, "") {
		t.Errorf("the #244 warning still fires beside the refusal:\n%s", renderDiags(b.diags))
	}
	if strings.Contains(detail, "declared_untagged") {
		t.Errorf("the refusal names a declared_untagged verb for this estate's own marker:\n%s", detail)
	}
	if om, ok := b.omitted[a.addr().String()]; !ok || om.Reason != ReasonUnowned {
		t.Errorf("omission = %+v (present %v), want ReasonUnowned", om, ok)
	}
}

// TestAKSRetag_clusterOfAnotherEstate: a cluster stamped by a different
// estate copies that estate's tofu-estate onto this estate's route table.
// Before #2135 that was #1166's another-estate Warning beside a create; now
// the marked overwrite refusal, under every declared_untagged verb, since
// none of them reaches a marked object (refuse and converge included,
// GitHub issue #2140, finding 3).
func TestAKSRetag_clusterOfAnotherEstate(t *testing.T) {
	a := aksTargets[0]
	for _, verb := range declaredUntaggedVerbs {
		t.Run("declared_untagged="+verb, func(t *testing.T) {
			own := &Ownership{Estate: aksEstate}
			if verb != "" {
				own.Policy = buildPolicy(t, "", verb)
			}
			b := aksBuilder(t, own)
			if got := b.checkOwnership(a.addr(), a.typ, a.id, armTagsSchema(), aksLive(a, aksCopiedTags("platform")), true, false, false); got != ownershipUnowned {
				t.Fatalf("verdict %d, want ownershipUnowned", got)
			}
			errs := errorDiagsWith(b.diags, SummaryOverwriteMarkedAtKey)
			if len(errs) != 1 {
				t.Fatalf("want exactly one Error %q, got:\n%s", SummaryOverwriteMarkedAtKey, renderDiags(b.diags))
			}
			detail := errs[0].Description().Detail
			for _, want := range []string{
				`tofu-estate="platform"`,
				`tofu-address="` + aksClusterAddr + `"`,
				`estate "platform"'s marker`,
				"live-mv -from-estate=platform",
				"AKS copies a cluster's tags",
			} {
				if !strings.Contains(detail, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, detail)
				}
			}
			if verb != "" && verb != "refuse" && !strings.Contains(detail, `policy { declared_untagged = "`+verb+`" } does not reach it`) {
				t.Errorf("the refusal does not say why %q did not apply:\n%s", verb, detail)
			}
			if hasDiag(b.diags, SummaryOutsideEstate, "") {
				t.Errorf("the #1166 warning still fires beside the refusal:\n%s", renderDiags(b.diags))
			}
		})
	}
}

// TestAKSRetag_thisBlocksOwnMarkerIsAdmitted: the one marked object a
// create at the declared id may meet is the one carrying this exact
// block's marker, which is admitted (no create is proposed at all), as is
// this estate's marker with no address yet, which the stamp completes
// (see addressNames).
func TestAKSRetag_thisBlocksOwnMarkerIsAdmitted(t *testing.T) {
	a := aksTargets[0]
	for name, tags := range map[string]map[string]string{
		"own address":    {markers.TagEstate: aksEstate, markers.TagAddress: markers.EscapeAddress(a.addr().String())},
		"no address yet": {markers.TagEstate: aksEstate},
	} {
		t.Run(name, func(t *testing.T) {
			b := aksBuilder(t, &Ownership{Estate: aksEstate})
			if got := b.checkOwnership(a.addr(), a.typ, a.id, armTagsSchema(), aksLive(a, tags), true, false, false); got != ownershipOK || len(b.diags) != 0 {
				t.Errorf("verdict %d diags:\n%s", got, renderDiags(b.diags))
			}
		})
	}
}

// TestAKSRetag_marksOffTheDeclaredIdKeepTheirWarnings: the widening is for
// an object read at the id this block's own configuration computes. One a
// record pointed at, at some other id, is evidence about that id, and a
// create at the declared one would not land on it, so #244's and #1166's
// warnings stand there, as they do for every AWS and Kubernetes object.
func TestAKSRetag_marksOffTheDeclaredIdKeepTheirWarnings(t *testing.T) {
	a := aksTargets[0]
	for name, c := range map[string]struct {
		tags    map[string]string
		summary string
	}{
		"another address": {aksCopiedTags(aksEstate), SummaryWrongAddress},
		"another estate":  {aksCopiedTags("platform"), SummaryOutsideEstate},
	} {
		t.Run(name, func(t *testing.T) {
			b := aksBuilder(t, &Ownership{Estate: aksEstate})
			b.checkOwnershipAt(a.addr(), a.typ, a.id, armTagsSchema(), aksLive(a, c.tags), true, false, false, false)
			if len(errorDiagsWith(b.diags, SummaryOverwriteMarkedAtKey)) != 0 {
				t.Errorf("an object off the declared id raised the declared-id error:\n%s", renderDiags(b.diags))
			}
			if c.summary != "" && !hasDiag(b.diags, c.summary, "") {
				t.Errorf("want the %q warning, got:\n%s", c.summary, renderDiags(b.diags))
			}
		})
	}
}

// TestAKSRetag_recordFirstFallsBack: an estate with a record store reads
// the record before the identity route. The record names the declared id,
// and the object's own tofu-address no longer confirms it in either shape,
// so the record is reported stale and the instance falls back to the
// identity route - which then lands on one of the refusal tests above.
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
// The other half is identity's: since GitHub issue #2135 the record holds
// the identity schema's subscription_id, answered from the provider block
// as the identity route answers it, so the selection is honoured for these
// types (TestSelectedLocatedAKSRetaggedTypes in internal/live/identity) and
// identity routes them as located, which is how this verdict is reached.
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
