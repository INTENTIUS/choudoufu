// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

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
	if sw, err := Azure.NewSweeper(cty.NilVal, false); sw != nil || err != nil {
		t.Errorf("NewSweeper = %v, %v; the Resource Graph client is a later unit", sw, err)
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
