// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/sweepconfig"
	"github.com/intentius/choudoufu/pkg/azsweep"
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
		if v, ok := azsweep.OwnerTag(sweepconfig.Markers, r.Tags); ok && v == estate {
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
	return azConfigWith(t, "")
}

// azConfigWith is azConfig with extra appended to its source.
func azConfigWith(t *testing.T, extra string) *configs.Config {
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
` + extra
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
	return discoverAzureWith(t, azConfig(t), leg, resolutions...)
}

func discoverAzureWith(t *testing.T, cfg *configs.Config, leg ResourceGraphSweep, resolutions ...identity.Resolution) (*Result, tfdiags.Diagnostics) {
	t.Helper()
	return Discover(context.Background(), Request{
		Estate:      rgEstate,
		Config:      cfg,
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

// A managed disk attached to a virtual machine: ARM sets the disk's
// managedBy to the VM's id. An attachment is not a lifecycle owner, so the
// disk is the estate's like any other (#2133 finding 2).
func attachedDisk(name, address string) azsweep.Resource {
	return azsweep.Resource{
		ID:             strings.ToLower(rgID(rgSub1, "Kept")) + "/providers/microsoft.compute/disks/" + name,
		Type:           "microsoft.compute/disks",
		Name:           name,
		ResourceGroup:  "kept",
		SubscriptionID: rgSub1,
		ManagedBy:      rgID(rgSub1, "Kept") + "/providers/Microsoft.Compute/virtualMachines/vm1",
		Tags:           map[string]string{markers.TagEstate: rgEstate, markers.TagAddress: markers.EscapeAddress(address)},
	}
}

func TestResourceGraphLegProposesRemovingAnAttachedDiskWhoseBlockIsGone(t *testing.T) {
	disk := attachedDisk("data", "azurerm_managed_disk.data")
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {disk}}}
	res, diags := discoverAzure(t, ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}, keptResolution(rgSub1))
	assertNoErrors(t, diags)
	if len(res.ControllerHeld) != 0 {
		t.Errorf("the attached disk read as held: %+v", res.ControllerHeld)
	}
	if o, ok := removalsByAddr(res)["azurerm_managed_disk.data"]; !ok || !strings.EqualFold(o.ImportID, disk.ID) {
		t.Errorf("no removal proposed for the attached disk whose block is gone (got %+v, %v); orphans %+v, problems %+v", o, ok, res.Orphans, res.Problems)
	}
}

func TestResourceGraphLegBindsADeclaredAttachedDisk(t *testing.T) {
	disk := attachedDisk("data", "azurerm_managed_disk.data")
	cfg := azConfigWith(t, `
resource "azurerm_managed_disk" "data" {
  name                 = "data"
  resource_group_name  = "Kept"
  location             = "westeurope"
  storage_account_type = "Standard_LRS"
  create_option        = "Empty"
  disk_size_gb         = 1
}
`)
	declared := identity.Resolution{
		Addr:     addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "azurerm_managed_disk", Name: "data"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
		Class:    identity.ClassConcrete,
		ImportID: rgID(rgSub1, "Kept") + "/providers/Microsoft.Compute/disks/data",
	}
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {disk}}}
	res, diags := discoverAzureWith(t, cfg, ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}, keptResolution(rgSub1), declared)
	assertNoErrors(t, diags)
	if len(res.ControllerHeld) != 0 {
		t.Errorf("the declared attached disk read as held: %+v", res.ControllerHeld)
	}
	if len(res.Removals()) != 0 || len(res.Problems) != 0 {
		t.Errorf("the declared attached disk did not bind: removals %v, problems %+v", removalsByAddr(res), res.Problems)
	}
	vouched := false
	for _, a := range res.VerifiedDeclared {
		if a.String() == "azurerm_managed_disk.data" {
			vouched = true
		}
	}
	if !vouched {
		t.Errorf("the declared disk's sighting did not vouch for it; VerifiedDeclared %v", res.VerifiedDeclared)
	}
}

// TestResourceGraphLegNeverHoldsADeclaredObject: a declared instance's own
// ARM id binds before a hold is asked, so a declared group that names a
// lifecycle owner in managedBy (azurerm_resource_group's managed_by
// argument can set it to anything) still binds.
func TestResourceGraphLegNeverHoldsADeclaredObject(t *testing.T) {
	kept := rgResource(rgSub1, "Kept", "azurerm_resource_group.kept", nil)
	kept.ManagedBy = rgID(rgSub1, "Apps") + "/providers/Microsoft.Solutions/applications/app"
	kept.Tags["aks-managed-cluster-name"] = "aks"
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {kept}}}
	res, diags := discoverAzure(t, ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}, keptResolution(rgSub1))
	assertNoErrors(t, diags)
	if len(res.ControllerHeld) != 0 {
		t.Errorf("the declared group read as held: %+v", res.ControllerHeld)
	}
	if len(res.Removals()) != 0 || len(res.Problems) != 0 {
		t.Errorf("removals %v, problems %+v", removalsByAddr(res), res.Problems)
	}
}

func TestResourceGraphLegHoldsAManagedApplicationsGroup(t *testing.T) {
	managed := rgResource(rgSub1, "mrg-app", "azurerm_resource_group.app_managed", nil)
	managed.ManagedBy = rgID(rgSub1, "Apps") + "/providers/Microsoft.Solutions/applications/app"
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {managed}}}
	res, diags := discoverAzure(t, ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}}, keptResolution(rgSub1))
	assertNoErrors(t, diags)
	if len(res.Removals()) != 0 {
		t.Errorf("a managed application's group was proposed for removal: %v", removalsByAddr(res))
	}
	if len(res.ControllerHeld) != 1 || res.ControllerHeld[0].Controller != "ARM managedBy" {
		t.Errorf("held %+v; want the managed resource group held by managedBy", res.ControllerHeld)
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

// TestResourceGraphLegReportsUntaggableTypes is GitHub issue #2119's D3:
// every type the command layer classified as untaggable is a
// TYPE_NOT_TAGGABLE gap, with no per-run warning, and is left out of the
// covered list; a taggable declared type stays covered and is no gap.
func TestResourceGraphLegReportsUntaggableTypes(t *testing.T) {
	graph := &fakeGraph{answers: map[string][]azsweep.Resource{rgSub1: {rgResource(rgSub1, "Kept", "azurerm_resource_group.kept", nil)}}}
	leg := ResourceGraphSweep{
		Coverage:      azsweep.Coverage{rgSub1: graph},
		Subscriptions: []string{rgSub1},
		Untaggable: []UntaggableType{
			{TypeName: "azurerm_subnet", Why: "carries no tags"},
			{TypeName: "azapi_resource (Microsoft.Network/virtualNetworks/subnets@2024-05-01)", Why: "takes no tags at that API version"},
		},
	}
	// A declared subnet, so the covered list has it to leave out.
	subnet := identity.Resolution{
		Addr:     addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "azurerm_subnet", Name: "a"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
		Class:    identity.ClassConcrete,
		ImportID: rgID(rgSub1, "Kept") + "/providers/Microsoft.Network/virtualNetworks/vnet/subnets/a",
	}
	res, diags := discoverAzure(t, leg, keptResolution(rgSub1), subnet)
	assertNoErrors(t, diags)
	for _, d := range diags {
		if d.Description().Summary == SummaryIncompleteSweep {
			t.Errorf("an untaggable type raised a per-run warning: %s", d.Description().Detail)
		}
	}

	gaps := map[string]SweepGap{}
	for _, g := range res.SweepGaps {
		gaps[g.TypeName] = g
	}
	for _, u := range leg.Untaggable {
		g, ok := gaps[u.TypeName]
		if !ok || g.Reason != SweepGapNotTaggable {
			t.Errorf("%s: gap %+v; want %s", u.TypeName, g, SweepGapNotTaggable)
			continue
		}
		if !strings.HasPrefix(g.Detail, u.TypeName+" "+u.Why+",") || !strings.Contains(g.Detail, "live/RECOVERY.md") {
			t.Errorf("%s: detail %q; want it to name the type, why, and live/RECOVERY.md", u.TypeName, g.Detail)
		}
	}
	if g, ok := gaps["azurerm_resource_group"]; ok {
		t.Errorf("a taggable type is a gap: %+v", g)
	}
	covered := map[string]bool{}
	for _, c := range res.SweepCovered {
		covered[c] = true
	}
	if !covered["azurerm_resource_group"] {
		t.Errorf("SweepCovered %v; want the taggable declared type", res.SweepCovered)
	}
	for _, u := range leg.Untaggable {
		if covered[u.TypeName] {
			t.Errorf("SweepCovered names %s, which the sweep can never find", u.TypeName)
		}
	}
}

// TestResourceGraphSharedSubscriptionFailureReportedByItsOwner is GitHub
// issue #1120's family split: azurerm and azapi name one subscription, so
// both passes query it, and a failed query there is reported once, by the
// pass that owns the subscription. The other pass still counts the sweep
// incomplete and claims no covered type.
func TestResourceGraphSharedSubscriptionFailureReportedByItsOwner(t *testing.T) {
	azurerm := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}
	azapi := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.Provider{Hostname: addrs.DefaultProviderRegistryHost, Namespace: "azure", Type: "azapi"}}
	family := []FamilyConfig{{Provider: azapi, Subscription: rgSub1}, {Provider: azurerm, Subscription: rgSub1}}
	graph := &fakeGraph{errs: map[string]error{rgSub1: errors.New("boom")}}
	owner := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Subscriptions: []string{rgSub1}, TakesUnnamed: true, Self: azapi, Family: family}
	sharer := ResourceGraphSweep{Coverage: azsweep.Coverage{rgSub1: graph}, Elsewhere: []string{rgSub1}, Self: azurerm, Family: family}

	_, ownerDiags := discoverAzure(t, owner, keptResolution(rgSub1))
	res, sharerDiags := discoverAzure(t, sharer, keptResolution(rgSub1))
	if len(ownerDiags) == 0 {
		t.Errorf("the owning pass reported nothing for the failed subscription")
	}
	for _, d := range sharerDiags {
		t.Errorf("the sharing pass reported %q: %s", d.Description().Summary, d.Description().Detail)
	}
	if len(res.SweepCovered) != 0 {
		t.Errorf("the sharing pass claims %v covered after a failed query", res.SweepCovered)
	}
	if got := strings.Join(graph.asked, ","); got != rgSub1+","+rgSub1 {
		t.Errorf("asked %q; both passes query the subscription their blocks name", got)
	}
}
