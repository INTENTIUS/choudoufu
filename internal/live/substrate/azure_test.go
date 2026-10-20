// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/azsweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1120: the Azure family's answers and its dispatch.

func TestAzureIsChosenByProvider(t *testing.T) {
	for _, typ := range []string{"azurerm", "azapi"} {
		if s, ok := ForProvider(typ); !ok || s != Azure {
			t.Errorf("ForProvider(%q) = %v, %v; want the Azure family", typ, s, ok)
		}
	}
	block := tagsBlock(true, false)
	for typ, want := range map[string]markers.Surface{
		"azurerm": markers.SurfaceARMTags,
		"azapi":   markers.SurfaceARMTags,
		"aws":     markers.SurfaceTags,
		// A provider no family claims keeps the AWS answer the compatible
		// default always gave it: Azure is not asked for it.
		"datadog": markers.SurfaceTags,
		"":        markers.SurfaceTags,
	} {
		if got, ok := SurfaceOf(typ, block); !ok || got != want {
			t.Errorf("SurfaceOf(%q, tags map) = %q, %v; want %q", typ, got, ok, want)
		}
	}
	if For(markers.SurfaceARMTags) != Azure {
		t.Error("the arm-tags surface is not the Azure family's")
	}
}

func TestAzureFamilyAnswers(t *testing.T) {
	s := markers.SurfaceARMTags
	if Azure.Name() != "azurerm" {
		t.Errorf("Name = %q", Azure.Name())
	}
	if !Azure.CarriesAddress() || !Azure.AddressInMarkers() {
		t.Error("the tofu-address tag is in the marker map")
	}
	if key, noun := Azure.AddressCarrier(s); key != markers.TagAddress || noun != "tag" {
		t.Errorf("AddressCarrier = %q, %q", key, noun)
	}
	if !Azure.MarkerOutlivesObject() || !TombstonesRead("azurerm") || !TombstonesRead("azapi") {
		t.Error("Resource Graph lags deletes, so tombstones are read")
	}
	if Azure.CreateCollidesOnKey(s) {
		t.Error("an ARM PUT is create-or-update, so a create never collides on its key")
	}
	if !Azure.SweepFindsUnadmitted() || Azure.Sweep() != SweepResourceGraph {
		t.Errorf("sweep %q, finds unadmitted %v", Azure.Sweep(), Azure.SweepFindsUnadmitted())
	}
	if w := Azure.Writes(s); w != (Writes{Create: WriteInCreate, Adopt: WriteTagsPlan, PostCreate: WriteNeverNeeded}) {
		t.Errorf("Writes = %+v", w)
	}
	if _, needed := Azure.PostCreateNeeded(s, Created{}, nil); needed || Azure.MarkerWriter() != WriteNeverNeeded {
		t.Error("no post-create write until the generated list lands")
	}
	if got := CarrierPaths(s); len(got) != 1 || !got[0].Equals(cty.GetAttrPath("tags")) {
		t.Errorf("CarrierPaths = %#v", got)
	}
}

// TestAzureMarkersOfFoldsCase: MarkersOf reads the top-level tags with the
// marker keys case-folded, which is where azapi copies a body.tags write
// back to after an apply.
func TestAzureMarkersOfFoldsCase(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{"tags": cty.MapVal(map[string]cty.Value{
		"Tofu-Estate": cty.StringVal("prod"), "TOFU-ADDRESS": cty.StringVal("azapi_resource.vnet"),
	})})
	got, ok := MarkersOf(markers.SurfaceARMTags, obj)
	if !ok || got[markers.TagEstate] != "prod" || got[markers.TagAddress] != "azapi_resource.vnet" {
		t.Errorf("MarkersOf = %v, %v", got, ok)
	}
}

func TestAzureControllerHeld(t *testing.T) {
	if h, ok := ControllerHeld(HoldEvidence{ManagedBy: "/subscriptions/0/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks"}); !ok || !strings.Contains(h.HeldBy, "managedClusters/aks") {
		t.Errorf("managedBy: %+v, %v", h, ok)
	}
	if h, ok := ControllerHeld(HoldEvidence{ARMTags: map[string]string{"AKS-Managed-Cluster-Name": "aks"}}); !ok || h.Controller != "AKS" {
		t.Errorf("AKS tag: %+v, %v", h, ok)
	}
	if _, ok := Azure.ControllerHeld(HoldEvidence{ARMTags: map[string]string{"env": "prod"}, Tags: map[string]string{"aks-managed-cluster-name": "aks"}}); ok {
		t.Error("the Azure family read the AWS family's Tags field")
	}
}

func TestAzureManualMarkFix(t *testing.T) {
	id := "/subscriptions/0/resourceGroups/rg"
	got := Azure.ManualMarkFix(Created{Object: cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal(id)})}, map[string]string{markers.TagEstate: "prod", markers.TagAddress: "azurerm_resource_group.rg"}, nil)
	want := "az tag update --resource-id " + id + " --operation Merge --tags 'tofu-address=azurerm_resource_group.rg' 'tofu-estate=prod'"
	if !strings.Contains(got, want) {
		t.Errorf("ManualMarkFix = %q, want it to contain %q", got, want)
	}
}

// TestAzureLeavesIdentityToTheSchemaRoute: until #1120's identity unit,
// azurerm and azapi types reach the identity-schema route exactly as they
// did before the family existed.
func TestAzureLeavesIdentityToTheSchemaRoute(t *testing.T) {
	schema := providers.Schema{Block: tagsBlock(true, false)}
	for _, typ := range []string{"azurerm_resource_group", "azapi_resource"} {
		if _, ok := Azure.SynthesizeIdentity(typ, schema); ok {
			t.Errorf("Azure.SynthesizeIdentity(%s) answered", typ)
		}
		if got, ok := SynthesizeIdentity(typ, schema); !ok || !got.FromIdentitySchema {
			t.Errorf("SynthesizeIdentity(%s) = %+v, %v; want the identity-schema route", typ, got, ok)
		}
	}
}

// TestAzureNewSweeperBuildsAResourceGraphClient (#1120 unit 11): the
// client is built from the block's own arguments, names the block's
// subscription, and serves the family's sweep; a block it cannot be built
// from is an error and a nil Sweeper, never a client holding nil.
func TestAzureNewSweeperBuildsAResourceGraphClient(t *testing.T) {
	for _, name := range azsweep.EnvNames() {
		t.Setenv(name, "")
	}
	const sub = "11111111-1111-1111-1111-111111111111"
	block := cty.ObjectVal(map[string]cty.Value{
		"features":        cty.EmptyObjectVal,
		"subscription_id": cty.StringVal(sub),
		"tenant_id":       cty.StringVal("22222222-2222-2222-2222-222222222222"),
		"client_id":       cty.StringVal("33333333-3333-3333-3333-333333333333"),
		"client_secret":   cty.StringVal("s3cret"),
		"use_cli":         cty.False,
	})
	sw, err := Azure.NewSweeper(block, true)
	if err != nil {
		t.Fatalf("NewSweeper: %s", err)
	}
	if sw.SweepKind() != Azure.Sweep() {
		t.Errorf("SweepKind = %q, want %q", sw.SweepKind(), Azure.Sweep())
	}
	rg, ok := sw.(ResourceGraphSweeper)
	if !ok || rg.Client == nil || rg.Subscription() != sub {
		t.Fatalf("NewSweeper = %#v; want a ResourceGraphSweeper over a client for %s", sw, sub)
	}
	var _ azsweep.Searcher = rg

	marked := cty.ObjectVal(map[string]cty.Value{"subscription_id": cty.StringVal(sub).Mark(marks.Sensitive)})
	if sw, err := Azure.NewSweeper(marked, true); err == nil || sw != nil {
		t.Errorf("NewSweeper over a sensitive subscription_id = %v, %v; want a nil Sweeper and an error", sw, err)
	}
}

// TestAzureSweepAttrsTellsTheProvidersApart: NewSweeper is handed the
// block's value alone, so which provider it configures is read off its
// shape, and decides which environment variables apply.
func TestAzureSweepAttrsTellsTheProvidersApart(t *testing.T) {
	azapi := cty.ObjectVal(map[string]cty.Value{"endpoint": cty.ListValEmpty(cty.EmptyObject), "default_tags": cty.NullVal(cty.Map(cty.String))})
	azurerm := cty.ObjectVal(map[string]cty.Value{"features": cty.EmptyObjectVal, "metadata_host": cty.NullVal(cty.String)})
	for _, tc := range []struct {
		val  cty.Value
		ok   bool
		want string
	}{
		{azapi, true, azsweep.ProviderAzAPI},
		{azurerm, true, azsweep.ProviderAzureRM},
		{cty.NilVal, false, azsweep.ProviderAzureRM},
	} {
		if got := AzureSweepAttrs(tc.val, tc.ok).Provider; got != tc.want {
			t.Errorf("AzureSweepAttrs(%#v).Provider = %q, want %q", tc.val, got, tc.want)
		}
	}
}
