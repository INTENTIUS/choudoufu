// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/azsweep"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The Azure leg (GitHub issue #1120, unit 11), driven through Discover the
// way the command layer drives it, against a fake Resource Graph client.

const (
	rgSub1   = "11111111-1111-1111-1111-111111111111"
	rgSub2   = "22222222-2222-2222-2222-222222222222"
	rgEstate = "az1120"
)

// fakeGraph is a Resource Graph client answering per subscription, and
// recording which subscriptions it was asked about.
type fakeGraph struct {
	answers map[string][]azsweep.Resource
	errs    map[string]error
	asked   []string
}

func (f *fakeGraph) Search(_ context.Context, subscription, estate string) ([]azsweep.Resource, error) {
	f.asked = append(f.asked, subscription)
	if err := f.errs[subscription]; err != nil {
		return nil, err
	}
	var out []azsweep.Resource
	for _, r := range f.answers[subscription] {
		if v, ok := azsweep.EstateTag(r.Tags); ok && v == estate {
			out = append(out, r)
		}
	}
	return out, nil
}

func rgID(sub, group string) string {
	return "/subscriptions/" + sub + "/resourceGroups/" + group
}

// rgResource is a resource group Resource Graph lists, tagged for address
// with the marker keys spelled as keys says ("" for the stamp's lowercase).
func rgResource(sub, group, address string, spell func(string) string) azsweep.Resource {
	if spell == nil {
		spell = func(k string) string { return k }
	}
	tags := map[string]string{spell(markers.TagEstate): rgEstate}
	if address != "" {
		tags[spell(markers.TagAddress)] = markers.EscapeAddress(address)
	}
	return azsweep.Resource{
		// Resource Graph's casing: the type lowercased, and here the
		// resourcegroups segment too.
		ID:             strings.ToLower(rgID(sub, group)),
		Type:           "microsoft.resources/subscriptions/resourcegroups",
		Name:           group,
		ResourceGroup:  group,
		SubscriptionID: sub,
		Tags:           tags,
	}
}

func azConfig(t *testing.T) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	src := `
provider "azurerm" {
  features {}
}
resource "azurerm_resource_group" "kept" {
  name     = "Kept"
  location = "westeurope"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return loadConfig(t, dir)
}

func azInstance(name string) addrs.AbsResourceInstance {
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "azurerm_resource_group", Name: name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
}

// keptResolution is the declared group, concrete at its ARM id in the
// configuration's spelling.
func keptResolution(sub string) identity.Resolution {
	return identity.Resolution{Addr: azInstance("kept"), Class: identity.ClassConcrete, ImportID: rgID(sub, "Kept")}
}

func discoverAzure(t *testing.T, leg ResourceGraphSweep, resolutions ...identity.Resolution) (*Result, tfdiags.Diagnostics) {
	t.Helper()
	return Discover(context.Background(), Request{
		Estate:      rgEstate,
		Config:      azConfig(t),
		Provider:    newFakeCloud(),
		Resolutions: resolutions,
		Sweepers:    []Sweeper{leg},
	})
}

func TestResourceGraphLegFindsOrphansAndBindsTheRest(t *testing.T) {
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {
		// The declared group, listed in Resource Graph's casing.
		rgResource(rgSub1, "Kept", "azurerm_resource_group.kept", nil),
		// A deleted block's group, marked by the stamp.
		rgResource(rgSub1, "Gone", "azurerm_resource_group.gone", nil),
		// A deleted block's group whose configuration spelled the keys.
		rgResource(rgSub1, "Spelled", "azurerm_resource_group.spelled", func(k string) string {
			return strings.ToUpper(k[:1]) + k[1:]
		}),
	}}}
	leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}

	res, diags := discoverAzure(t, leg, keptResolution(rgSub1))
	assertNoErrors(t, diags)

	removals := removalsByAddr(res)
	for _, want := range []string{"azurerm_resource_group.gone", "azurerm_resource_group.spelled"} {
		o, ok := removals[want]
		if !ok {
			t.Errorf("no removal proposed for %s; orphans: %+v", want, res.Orphans)
			continue
		}
		if _, isARM := ARMSubscription(o.ImportID); !isARM || !o.Swept {
			t.Errorf("%s's removal imports %q (swept %v); want the ARM id, found by the sweep", want, o.ImportID, o.Swept)
		}
	}
	if _, ok := removals["azurerm_resource_group.kept"]; ok || len(removals) != 2 {
		t.Errorf("removals %v; want exactly the two undeclared groups", removals)
	}
	if len(res.Problems) != 0 {
		t.Errorf("problems %+v; the declared group, listed in another case, must bind rather than read as displaced", res.Problems)
	}
	vouched := false
	for _, a := range res.VerifiedDeclared {
		if a.String() == "azurerm_resource_group.kept" {
			vouched = true
		}
	}
	if !vouched {
		t.Errorf("the declared group's sighting did not vouch for it; VerifiedDeclared %v", res.VerifiedDeclared)
	}
	if len(graph.asked) != 1 || graph.asked[0] != rgSub1 {
		t.Errorf("subscriptions asked %v, want [%s]", graph.asked, rgSub1)
	}
}

func TestResourceGraphLegHoldsControllerHeldObjects(t *testing.T) {
	// AKS copies the cluster's tags, the estate's marker among them, onto
	// the node resource group it makes, and sets managedBy on it.
	node := rgResource(rgSub1, "MC_kept_aks_westeurope", "azurerm_resource_group.kept", nil)
	node.ManagedBy = "/subscriptions/" + rgSub1 + "/resourceGroups/Kept/providers/Microsoft.ContainerService/managedClusters/aks"
	// One AKS writes an aks-managed- tag on, at an address nothing declares.
	tagged := rgResource(rgSub1, "Tagged", "azurerm_resource_group.tagged", nil)
	tagged.Tags["aks-managed-cluster-name"] = "aks"

	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {node, tagged}}}
	res, diags := discoverAzure(t, ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}, keptResolution(rgSub1))
	assertNoErrors(t, diags)

	if len(res.Orphans) != 0 || len(res.Problems) != 0 {
		t.Errorf("a held object read as an orphan or a problem: orphans %+v, problems %+v", res.Orphans, res.Problems)
	}
	held := map[string]ControllerHeldResource{}
	for _, h := range res.ControllerHeld {
		held[h.ImportID] = h
	}
	if h, ok := held[node.ID]; !ok || h.Controller != "ARM managedBy" || h.Marked {
		t.Errorf("node resource group held as %+v (found %v); want held by managedBy, unmarked since its address is declared", h, ok)
	}
	if h, ok := held[tagged.ID]; !ok || h.Controller != "AKS" || !h.Marked || h.Addr.String() != "azurerm_resource_group.tagged" {
		t.Errorf("aks-managed group held as %+v (found %v); want held by AKS and marked at its undeclared address", h, ok)
	}
}

// TestResourceGraphLegBindsAStaleMarkerByARMID: an object whose ARM id is a
// declared instance's is that instance's, whatever address its marker
// names, so a block renamed without a moved block proposes no destroy of
// the object the new block declares.
func TestResourceGraphLegBindsAStaleMarkerByARMID(t *testing.T) {
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {
		rgResource(rgSub1, "Kept", "azurerm_resource_group.old_name", nil),
	}}}
	res, diags := discoverAzure(t, ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}, keptResolution(rgSub1))
	assertNoErrors(t, diags)
	if len(res.Removals()) != 0 {
		t.Errorf("removals %v: the declared group's object, under its old marker, would be destroyed", removalsByAddr(res))
	}
}

func TestResourceGraphLegWarnsOfAnUncoveredSubscriptionByName(t *testing.T) {
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{}}
	leg := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}, TakesUnnamed: true}
	elsewhere := identity.Resolution{Addr: azInstance("elsewhere"), Class: identity.ClassConcrete, ImportID: rgID(rgSub2, "Elsewhere")}

	res, diags := discoverAzure(t, leg, keptResolution(rgSub1), elsewhere)
	assertNoErrors(t, diags)
	if !hasDiag(diags, resourceGraphSummaries[azsweep.SummarySubscriptionUncovered], rgSub2) {
		t.Errorf("no warning names the uncovered subscription %s:\n%s", rgSub2, renderDiags(diags))
	}
	gapped := false
	for _, g := range res.SweepGaps {
		if g.Reason == SweepGapListFailed && strings.Contains(g.Detail, rgSub2) {
			gapped = true
		}
	}
	if !gapped {
		t.Errorf("no sweep gap names %s: %v", rgSub2, res.SweepGaps)
	}
	if len(res.SweepCovered) != 0 {
		t.Errorf("SweepCovered %v on an incomplete sweep; an empty removal list would read as complete", res.SweepCovered)
	}

	// The pass that does not take unnamed subscriptions leaves it to the
	// one that does, so it is warned about once per run.
	leg.TakesUnnamed = false
	_, diags = discoverAzure(t, leg, keptResolution(rgSub1), elsewhere)
	if hasDiag(diags, resourceGraphSummaries[azsweep.SummarySubscriptionUncovered], rgSub2) {
		t.Errorf("a pass that does not take unnamed subscriptions warned about %s too", rgSub2)
	}
}

func TestResourceGraphLegRefusesAnUnregisteredProvider(t *testing.T) {
	graph := &fakeGraph{errs: map[string]error{rgSub1: &azsweep.NotRegisteredError{Subscription: rgSub1, Code: "MissingSubscriptionRegistration", Message: "not registered"}}}
	_, diags := discoverAzure(t, ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}, keptResolution(rgSub1))
	if !diags.HasErrors() || !hasDiag(diags, resourceGraphSummaries[azsweep.SummaryResourceGraphNotRegistered], rgSub1) {
		t.Errorf("an unregistered Microsoft.ResourceGraph was not refused by name:\n%s", renderDiags(diags))
	}
}

func TestResourceGraphLegSweepsEachSubscriptionOnce(t *testing.T) {
	leg := ResourceGraphSweep{Subscriptions: []string{rgSub1}, Elsewhere: []string{rgSub2}, TakesUnnamed: true}
	got := leg.SweptSubscriptions([]identity.Resolution{
		{ImportID: rgID(strings.ToUpper(rgSub1), "a")},
		{ImportID: rgID(rgSub2, "b")},
		{ImportID: "/SUBSCRIPTIONS/33333333-3333-3333-3333-333333333333/resourceGroups/c"},
		{ImportID: "arn:aws:s3:::bucket"},
	})
	want := []string{rgSub1, "33333333-3333-3333-3333-333333333333"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("SweptSubscriptions = %v, want %v", got, want)
	}
}

func TestResourceGraphSummariesMatchAzsweep(t *testing.T) {
	for key, value := range resourceGraphSummaries {
		if key != value {
			t.Errorf("resourceGraphSummaries[%q] = %q; the registered words drifted from azsweep's", key, value)
		}
	}
	for _, s := range []string{azsweep.SummaryResourceGraphNotRegistered, azsweep.SummarySubscriptionUnreadable, azsweep.SummaryResourceGraphThrottled, azsweep.SummarySubscriptionUncovered, azsweep.SummarySweepUnavailable} {
		if _, ok := resourceGraphSummaries[s]; !ok {
			t.Errorf("azsweep's summary %q has no entry, so a failure under it would raise a diagnostic with no summary", s)
		}
	}
}

func TestResourceGraphLegServesTheAzureSweep(t *testing.T) {
	if got := (ResourceGraphSweep{}).Leg(); got != substrate.Azure.Sweep() {
		t.Errorf("ResourceGraphSweep serves %q, the Azure family asks for %q", got, substrate.Azure.Sweep())
	}
}
