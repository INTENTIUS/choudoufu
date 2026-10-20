// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
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

// TestAzureIdentityRoutes (#1120 unit 10): which azurerm and azapi types
// the family answers for, and which it leaves to the identity-schema route
// exactly as before the family existed.
func TestAzureIdentityRoutes(t *testing.T) {
	schema := providers.Schema{Block: tagsBlock(true, false)}
	// identity-schema class, unmapped class, a withheld row, and an azapi
	// type of the wrong shape: the identity-schema route.
	for _, typ := range []string{"azurerm_resource_group", "azurerm_virtual_network", "azurerm_role_assignment", "azurerm_lb_nat_rule", "azurerm_pim_active_role_assignment", "azapi_update_resource", "azurerm_no_such_type"} {
		if _, ok := Azure.SynthesizeIdentity(typ, schema); ok {
			t.Errorf("Azure.SynthesizeIdentity(%s) answered", typ)
		}
		if got, ok := SynthesizeIdentity(typ, schema); !ok || !got.FromIdentitySchema {
			t.Errorf("SynthesizeIdentity(%s) = %+v, %v; want the identity-schema route", typ, got, ok)
		}
	}
	// A type of another family is never claimed, whatever its name says.
	if _, ok := Azure.SynthesizeIdentity("aws_dns_zone", schema); ok {
		t.Error("Azure answered for an AWS type")
	}

	got, ok := Azure.SynthesizeIdentity("azurerm_dns_zone", schema)
	if !ok || !got.ARMResourceID || !got.NonAWSProvider || got.FromIdentitySchema {
		t.Fatalf("azurerm_dns_zone = %+v, %v; want the template route", got, ok)
	}
	var render []string
	for _, c := range got.Components {
		switch {
		case c.Cloud != "":
			render = append(render, "<"+c.Cloud+">")
		case len(c.Attrs) > 0:
			render = append(render, "{"+strings.Join(c.Attrs, "|")+"}")
		default:
			render = append(render, c.Literal)
		}
	}
	if want := "/subscriptions/<subscription-id>/resourceGroups/{resource_group_name}/providers/Microsoft.Network/dnsZones/{name}"; strings.Join(render, "") != want {
		t.Errorf("azurerm_dns_zone components render %q, want %q", strings.Join(render, ""), want)
	}
	if len(got.IdentityAttrs) != 1 || got.IdentityAttrs[0] != "id" {
		t.Errorf("IdentityAttrs = %v, want [id]", got.IdentityAttrs)
	}

	api := azapiBlock()
	got, ok = Azure.SynthesizeIdentity("azapi_resource", providers.Schema{Block: api})
	if !ok || !got.ARMResourceID || len(got.Components) != 3 || !got.Components[1].ARMTypeSegment {
		t.Errorf("azapi_resource = %+v, %v; want parent_id, the type segment, name", got, ok)
	}
}

func azapiBlock() *configschema.Block {
	return &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"type":      {Type: cty.String, Required: true},
		"parent_id": {Type: cty.String, Optional: true, Computed: true},
		"name":      {Type: cty.String, Optional: true, Computed: true},
		"tags":      {Type: cty.Map(cty.String), Optional: true, Computed: true},
		"id":        {Type: cty.String, Computed: true},
	}}
}

// TestAzapiResourceShape: azapi_resource's shape, and the three other
// azapi 2.13.0 types that carry a type and a parent or name but do not own
// the object those name.
func TestAzapiResourceShape(t *testing.T) {
	if !AzapiResourceShape(azapiBlock()) {
		t.Fatal("azapi_resource's shape is not recognised")
	}
	update := azapiBlock()
	delete(update.Attributes, "tags")
	update.Attributes["resource_id"] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	if AzapiResourceShape(update) {
		t.Error("azapi_update_resource's shape (resource_id, no tags) read as azapi_resource")
	}
	dataPlane := azapiBlock()
	delete(dataPlane.Attributes, "tags")
	if AzapiResourceShape(dataPlane) {
		t.Error("azapi_data_plane_resource's shape (no tags) read as azapi_resource")
	}
	if AzapiResourceShape(nil) {
		t.Error("a nil block read as azapi_resource")
	}
}

// The admission figures at the pinned azurerm. TO CHANGE A NUMBER HERE,
// regenerate live/azurerm-ids.json with tools/azurerm-id-gen, re-copy it,
// and say in the commit message which types moved and why.
func TestAzurermAdmissionCounts(t *testing.T) {
	if v, c := AzurermIDSource(); v != "5.10.0" || c != "b24dfdc1dafe6d0721572b0f6debecb7ab5e81c6" {
		t.Errorf("embedded artifact is from azurerm %s (%s)", v, c)
	}
	want := map[string]int{
		"template":        314,
		"parent-id":       337,
		"withheld":        21,
		"identity-schema": 196,
		"unmapped":        238,
	}
	got := AzurermAdmission()
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s: %d, pinned %d", k, got[k], n)
		}
	}
	if len(got) != len(want) {
		t.Errorf("classes %v, pinned %v", got, want)
	}
	total := 0
	for _, n := range got {
		total += n
	}
	if total != 1106 {
		t.Errorf("%d azurerm types, want 1106", total)
	}
	for _, w := range AzurermWithheld() {
		t.Logf("withheld %s: %s", w[0], w[1])
	}
}

// TestEmbeddedAzurermIDsMatchLive is the drift gate on the embedded copy
// (cp live/azurerm-ids.json internal/live/substrate/).
func TestEmbeddedAzurermIDsMatchLive(t *testing.T) {
	want, err := os.ReadFile(filepath.Join(repoRoot(t), "live", "azurerm-ids.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(embeddedAzurermIDs, want) {
		t.Error("internal/live/substrate/azurerm-ids.json differs from live/azurerm-ids.json; re-copy it (the artifact was regenerated without refreshing the embedded copy)")
	}
}

func TestCreateOverwritesAtKey(t *testing.T) {
	if !CreateOverwritesAtKey(markers.SurfaceARMTags) {
		t.Error("an ARM PUT updates the object at its id, so the arm-tags surface overwrites at its key")
	}
	for _, s := range []markers.Surface{markers.SurfaceTags, markers.SurfaceLabels, markers.SurfaceManifest, ""} {
		if CreateOverwritesAtKey(s) {
			t.Errorf("CreateOverwritesAtKey(%q) = true", s)
		}
	}
}
