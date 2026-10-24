// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/armtypes"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1120: the node stamp's arm for the ARM tags surface.

// armFixture is a configuration declaring one azurerm and one azapi
// resource, so the resolver resolves each block's provider type and asks
// the Azure family, the way a real plan does.
func armFixture(t *testing.T) *NodeResolver {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
resource "azurerm_resource_group" "rg" {
  name     = "rg"
  location = "westeurope"
}
resource "azapi_resource" "vnet" {
  type      = "Microsoft.Network/virtualNetworks@2023-09-01"
  name      = "vnet"
  parent_id = "/subscriptions/0/resourceGroups/rg"
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	return &NodeResolver{Estate: "prod", Config: loadConfig(t, dir)}
}

func armAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	return addr
}

func azurermSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name":     {Type: cty.String, Required: true},
		"location": {Type: cty.String, Required: true},
		"tags":     {Type: cty.Map(cty.String), Optional: true},
	}}}
}

func azapiSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"type":      {Type: cty.String, Required: true},
		"name":      {Type: cty.String, Optional: true, Computed: true},
		"parent_id": {Type: cty.String, Optional: true, Computed: true},
		"body":      {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
		"tags":      {Type: cty.Map(cty.String), Optional: true, Computed: true},
	}}}
}

func azurermConfig(tags cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"name":     cty.StringVal("rg"),
		"location": cty.StringVal("westeurope"),
		"tags":     tags,
	})
}

func azapiConfig(typ string, body, tags cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"type":      cty.StringVal(typ),
		"name":      cty.StringVal("vnet"),
		"parent_id": cty.StringVal("/subscriptions/0/resourceGroups/rg"),
		"body":      body,
		"tags":      tags,
	})
}

func stringMap(t *testing.T, v cty.Value) map[string]string {
	t.Helper()
	if v.IsNull() {
		return nil
	}
	out := map[string]string{}
	for k, e := range v.AsValueMap() {
		out[k] = e.AsString()
	}
	return out
}

func hasSummary(diags tfdiags.Diagnostics, summary string) bool {
	for _, d := range diags {
		if d.Description().Summary == summary {
			return true
		}
	}
	return false
}

func TestARMStampWritesLowercaseMarkers(t *testing.T) {
	n := armFixture(t)
	got, diags := n.AdjustConfigValue(context.Background(), armAddr(t, "azurerm_resource_group.rg"),
		azurermConfig(cty.MapVal(map[string]cty.Value{"env": cty.StringVal("prod")})), azurermSchema())
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	want := map[string]string{"env": "prod", markers.TagEstate: "prod", markers.TagAddress: "azurerm_resource_group.rg"}
	if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

// TestARMStampMatchesTheAWSStampForAzurerm pins #1120's promise to existing
// azurerm estates: before the Azure family existed, azurerm reached the AWS
// tags answer through substrate.SurfaceOf's compatible default (a resolver
// with no configuration still does), and for any configuration whose own
// tags carry no marker key in another case the two stamps are identical.
func TestARMStampMatchesTheAWSStampForAzurerm(t *testing.T) {
	addr := armAddr(t, "azurerm_resource_group.rg")
	for name, tags := range map[string]cty.Value{
		"no tags":       cty.NullVal(cty.Map(cty.String)),
		"own tags":      cty.MapVal(map[string]cty.Value{"env": cty.StringVal("prod"), "team": cty.StringVal("a")}),
		"own estate":    cty.MapVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("prod")}),
		"other estate":  cty.MapVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("staging")}),
		"other address": cty.MapVal(map[string]cty.Value{markers.TagAddress: cty.StringVal("azurerm_resource_group.old")}),
	} {
		t.Run(name, func(t *testing.T) {
			asAzure, azDiags := armFixture(t).AdjustConfigValue(context.Background(), addr, azurermConfig(tags), azurermSchema())
			asBefore, awsDiags := (&NodeResolver{Estate: "prod"}).AdjustConfigValue(context.Background(), addr, azurermConfig(tags), azurermSchema())
			if azDiags.HasErrors() != awsDiags.HasErrors() {
				t.Fatalf("Azure stamp errors %v, the stamp azurerm had before #1120 errors %v", azDiags.Err(), awsDiags.Err())
			}
			if !asAzure.RawEquals(asBefore) {
				t.Errorf("Azure stamp %#v, the stamp azurerm had before #1120 %#v", asAzure, asBefore)
			}
		})
	}
}

// TestARMStampKeepsTheConfigurationsSpelling: ARM tag keys are
// case-insensitive and ARM refuses two keys that differ only by case, so a
// configuration's own Tofu-Estate naming this estate is kept as spelled and
// no lowercase tofu-estate is added beside it.
func TestARMStampKeepsTheConfigurationsSpelling(t *testing.T) {
	n := armFixture(t)
	got, diags := n.AdjustConfigValue(context.Background(), armAddr(t, "azurerm_resource_group.rg"),
		azurermConfig(cty.MapVal(map[string]cty.Value{"Tofu-Estate": cty.StringVal("prod")})), azurermSchema())
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	want := map[string]string{"Tofu-Estate": "prod", markers.TagAddress: "azurerm_resource_group.rg"}
	if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
		t.Errorf("tags = %v, want exactly one estate key, under the configuration's spelling: %v", tags, want)
	}
}

func TestARMStampRefusesAnotherEstateInAnySpelling(t *testing.T) {
	n := armFixture(t)
	_, diags := n.AdjustConfigValue(context.Background(), armAddr(t, "azurerm_resource_group.rg"),
		azurermConfig(cty.MapVal(map[string]cty.Value{"TOFU-ESTATE": cty.StringVal("staging")})), azurermSchema())
	if !diags.HasErrors() || !hasSummary(diags, SummaryMarkerConflict) {
		t.Fatalf("a TOFU-ESTATE naming another estate: diags %v, want %q", diags.Err(), SummaryMarkerConflict)
	}
	if msg := diags.Err().Error(); !strings.Contains(msg, "TOFU-ESTATE") {
		t.Errorf("the conflict does not name the configuration's own key: %s", msg)
	}
}

func TestARMStampAzapiTopLevelTags(t *testing.T) {
	requireTaggable(t, "Microsoft.Network/virtualNetworks", "2023-09-01", true)
	n := armFixture(t)
	body := cty.ObjectVal(map[string]cty.Value{"location": cty.StringVal("westeurope")})
	got, diags := n.AdjustConfigValue(context.Background(), armAddr(t, "azapi_resource.vnet"),
		azapiConfig("Microsoft.Network/virtualNetworks@2023-09-01", body, cty.NullVal(cty.Map(cty.String))), azapiSchema())
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	want := map[string]string{markers.TagEstate: "prod", markers.TagAddress: "azapi_resource.vnet"}
	if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
	if !got.GetAttr("body").RawEquals(body) {
		t.Errorf("body changed: %#v", got.GetAttr("body"))
	}
}

// TestARMStampAzapiBodyTags: azapi refuses tags set both at top level and in
// body, and sends body's when both exist, so a configuration that keeps its
// tags in body gets the marker there and nowhere else.
func TestARMStampAzapiBodyTags(t *testing.T) {
	requireTaggable(t, "Microsoft.Network/virtualNetworks", "2023-09-01", true)
	n := armFixture(t)
	body := cty.ObjectVal(map[string]cty.Value{
		"location": cty.StringVal("westeurope"),
		"tags":     cty.ObjectVal(map[string]cty.Value{"env": cty.StringVal("prod")}),
	})
	got, diags := n.AdjustConfigValue(context.Background(), armAddr(t, "azapi_resource.vnet"),
		azapiConfig("Microsoft.Network/virtualNetworks@2023-09-01", body, cty.NullVal(cty.Map(cty.String))), azapiSchema())
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if !got.GetAttr("tags").IsNull() {
		t.Errorf("top-level tags = %#v, want null: azapi refuses both", got.GetAttr("tags"))
	}
	gotBody := got.GetAttr("body")
	if gotBody.GetAttr("location").AsString() != "westeurope" {
		t.Errorf("body lost its location: %#v", gotBody)
	}
	want := map[string]string{"env": "prod", markers.TagEstate: "prod", markers.TagAddress: "azapi_resource.vnet"}
	if tags := stringMap(t, gotBody.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
		t.Errorf("body.tags = %v, want %v", tags, want)
	}
}

// TestARMStampLeavesAnUntaggableAzapiTypeAlone: the taggability table is
// read at the block's own API version, and a type that takes no tags, or a
// version azapi does not know, is never stamped.
func TestARMStampLeavesAnUntaggableAzapiTypeAlone(t *testing.T) {
	requireTaggable(t, "Microsoft.Network/virtualNetworks/subnets", "2023-09-01", false)
	if _, known := armtypes.Taggable("Microsoft.Network/virtualNetworks", "1999-01-01"); known {
		t.Fatal("the fixture's unknown API version is known to the table")
	}
	for name, typ := range map[string]string{
		"untaggable type":     "Microsoft.Network/virtualNetworks/subnets@2023-09-01",
		"unknown API version": "Microsoft.Network/virtualNetworks@1999-01-01",
		"no API version":      "Microsoft.Network/virtualNetworks",
	} {
		t.Run(name, func(t *testing.T) {
			config := azapiConfig(typ, cty.ObjectVal(map[string]cty.Value{"properties": cty.EmptyObjectVal}), cty.NullVal(cty.Map(cty.String)))
			got, diags := armFixture(t).AdjustConfigValue(context.Background(), armAddr(t, "azapi_resource.vnet"), config, azapiSchema())
			if diags.HasErrors() {
				t.Fatal(diags.Err())
			}
			if !got.RawEquals(config) {
				t.Errorf("config changed: %#v", got)
			}
			if !hasSummary(diags, SummaryARMTypeNotStamped) {
				t.Errorf("no %q warning: %v", SummaryARMTypeNotStamped, diags)
			}
		})
	}
}

func TestARMStampRefusesAnUnknownAzapiBody(t *testing.T) {
	requireTaggable(t, "Microsoft.Network/virtualNetworks", "2023-09-01", true)
	config := azapiConfig("Microsoft.Network/virtualNetworks@2023-09-01", cty.DynamicVal, cty.NullVal(cty.Map(cty.String)))
	got, diags := armFixture(t).AdjustConfigValue(context.Background(), armAddr(t, "azapi_resource.vnet"), config, azapiSchema())
	if !diags.HasErrors() || !hasSummary(diags, SummaryARMTagsBodyUnknown) {
		t.Fatalf("diags %v, want the %q refusal", diags.Err(), SummaryARMTagsBodyUnknown)
	}
	if !got.RawEquals(config) {
		t.Errorf("config changed: %#v", got)
	}
}

// TestARMStampUnknownBodyWithTopLevelTags: an unknown body does not stop the
// stamp when the configuration sets the top-level tags argument to a known
// value, because azapi then sends that argument or refuses the
// configuration itself (GitHub issue #2142's Azure audit; azapi 2.13.0
// tagsWithDefaultTags, expandBody and validateDuplicatedDefinitions). The
// body is left unknown and untouched. A top-level tags value that is itself
// unknown could still be null at apply, so it keeps the refusal.
func TestARMStampUnknownBodyWithTopLevelTags(t *testing.T) {
	requireTaggable(t, "Microsoft.Network/virtualNetworks", "2023-09-01", true)
	own := cty.MapVal(map[string]cty.Value{"env": cty.StringVal("prod")})
	config := azapiConfig("Microsoft.Network/virtualNetworks@2023-09-01", cty.DynamicVal, own)
	got, diags := armFixture(t).AdjustConfigValue(context.Background(), armAddr(t, "azapi_resource.vnet"), config, azapiSchema())
	if diags.HasErrors() {
		t.Fatalf("refused: %v", diags.Err())
	}
	want := map[string]string{"env": "prod", markers.TagEstate: "prod", markers.TagAddress: "azapi_resource.vnet"}
	if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
	if got.GetAttr("body").IsKnown() {
		t.Errorf("body became known: %#v", got.GetAttr("body"))
	}

	unknownTags := azapiConfig("Microsoft.Network/virtualNetworks@2023-09-01", cty.DynamicVal, cty.UnknownVal(cty.Map(cty.String)))
	if _, diags := armFixture(t).AdjustConfigValue(context.Background(), armAddr(t, "azapi_resource.vnet"), unknownTags, azapiSchema()); !hasSummary(diags, SummaryARMTagsBodyUnknown) {
		t.Errorf("unknown body and unknown tags: diags %v, want the %q refusal", diags.Err(), SummaryARMTagsBodyUnknown)
	}
}

// TestARMVerifyReadsTheTopLevelTagsAfterABodyWrite: azapi copies the
// response body's tags into the top-level attribute (Read and ImportState,
// internal/services/azapi_resource.go lines 1182-1184 and 1419-1421 at
// v2.13.0), so a body.tags write is verified there, case-insensitively.
func TestARMVerifyReadsTheTopLevelTagsAfterABodyWrite(t *testing.T) {
	n := armFixture(t)
	addr := armAddr(t, "azapi_resource.vnet")
	planned := azapiConfig("Microsoft.Network/virtualNetworks@2023-09-01", cty.EmptyObjectVal, cty.MapVal(map[string]cty.Value{
		markers.TagEstate: cty.StringVal("prod"), markers.TagAddress: cty.StringVal("azapi_resource.vnet"),
	}))
	applied := azapiConfig("Microsoft.Network/virtualNetworks@2023-09-01", cty.EmptyObjectVal, cty.MapVal(map[string]cty.Value{
		"Tofu-Estate": cty.StringVal("prod"), "TOFU-ADDRESS": cty.StringVal("azapi_resource.vnet"),
	}))
	if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Create, planned, applied, azapiSchema()); len(diags) != 0 {
		t.Errorf("markers stored under another case read as lost: %v", diags.Err())
	}
	lost := azapiConfig("Microsoft.Network/virtualNetworks@2023-09-01", cty.EmptyObjectVal, cty.MapVal(map[string]cty.Value{
		"env": cty.StringVal("prod"),
	}))
	if diags := n.VerifyAppliedMarkers(context.Background(), addr, plans.Create, planned, lost, azapiSchema()); !hasSummary(diags, SummaryMarkerNotStored) {
		t.Errorf("a stored object with no marker was not reported: %v", diags)
	}
}

func requireTaggable(t *testing.T, armType, apiVersion string, want bool) {
	t.Helper()
	got, known := armtypes.Taggable(armType, apiVersion)
	if !known || got != want {
		t.Fatalf("armtypes.Taggable(%s, %s) = %v, %v; the fixture needs %v, true", armType, apiVersion, got, known, want)
	}
}

// TestARMStampConfigSetTagsSettleTheMarker: where the configuration itself
// sets known, non-null top-level tags on an azapi resource, the marker goes
// there, for a body marked sensitive as a whole and for a type azapi's
// embedded data does not know (GitHub issue #2207, ruled 2026-10-23 on
// #2201). Without config-set tags both stay warnings with nothing written,
// and a type known to take no tags is never stamped.
func TestARMStampConfigSetTagsSettleTheMarker(t *testing.T) {
	requireTaggable(t, "Microsoft.Network/virtualNetworks", "2023-09-01", true)
	requireTaggable(t, "Microsoft.Network/virtualNetworks/subnets", "2023-09-01", false)
	const known = "Microsoft.Network/virtualNetworks@2023-09-01"
	const unknownType = "Microsoft.Network/virtualNetworks@1999-01-01"
	if _, k := armtypes.Taggable("Microsoft.Network/virtualNetworks", "1999-01-01"); k {
		t.Fatal("the fixture's unknown API version is known to the table")
	}
	own := cty.MapVal(map[string]cty.Value{"env": cty.StringVal("prod")})
	none := cty.NullVal(cty.Map(cty.String))
	marked := cty.ObjectVal(map[string]cty.Value{"location": cty.StringVal("westeurope")}).Mark("sensitive")
	plain := cty.ObjectVal(map[string]cty.Value{"location": cty.StringVal("westeurope")})
	want := map[string]string{"env": "prod", markers.TagEstate: "prod", markers.TagAddress: "azapi_resource.vnet"}

	for name, tc := range map[string]struct {
		config      cty.Value
		wantStamped bool
		warn        string
	}{
		"marked body, config tags":      {azapiConfig(known, marked, own), true, ""},
		"marked body, no tags":          {azapiConfig(known, marked, none), false, SummaryARMBodyNotMergeable},
		"unknown taggability, tags set": {azapiConfig(unknownType, plain, own), true, ""},
		"unknown taggability, no tags":  {azapiConfig(unknownType, plain, none), false, SummaryARMTypeNotStamped},
		"unknown taggability, unknown tags": {azapiConfig(unknownType, plain, cty.UnknownVal(cty.Map(cty.String))),
			false, SummaryARMTypeNotStamped},
		"untaggable type, tags set": {azapiConfig("Microsoft.Network/virtualNetworks/subnets@2023-09-01", plain, own),
			false, SummaryARMTypeNotStamped},
	} {
		t.Run(name, func(t *testing.T) {
			got, diags := armFixture(t).AdjustConfigValue(context.Background(), armAddr(t, "azapi_resource.vnet"), tc.config, azapiSchema())
			if diags.HasErrors() {
				t.Fatal(diags.Err())
			}
			if !tc.wantStamped {
				if !got.RawEquals(tc.config) {
					t.Errorf("config changed: %#v", got)
				}
				if !hasSummary(diags, tc.warn) {
					t.Errorf("no %q warning: %v", tc.warn, diags)
				}
				return
			}
			if hasSummary(diags, SummaryARMBodyNotMergeable) || hasSummary(diags, SummaryARMTypeNotStamped) {
				t.Errorf("unexpected warning: %v", diags)
			}
			if tags := stringMap(t, got.GetAttr("tags")); !reflect.DeepEqual(tags, want) {
				t.Errorf("tags = %v, want %v", tags, want)
			}
			if !got.GetAttr("body").RawEquals(tc.config.GetAttr("body")) {
				t.Errorf("body changed: %#v", got.GetAttr("body"))
			}
		})
	}
}

// TestARMTagPlacementReadObjectIgnoresTopLevelTagsForTheNewRules: the two
// config-set-tags rules are the configuration's, so adopt and mv, which
// read an object whose top-level tags azapi filled from the body, still
// decline a marked body and a type this build does not know.
func TestARMTagPlacementReadObjectIgnoresTopLevelTagsForTheNewRules(t *testing.T) {
	requireTaggable(t, "Microsoft.Network/virtualNetworks", "2023-09-01", true)
	own := cty.MapVal(map[string]cty.Value{"env": cty.StringVal("prod")})
	plain := cty.ObjectVal(map[string]cty.Value{"location": cty.StringVal("westeurope")})
	addr := armAddr(t, "azapi_resource.vnet")
	for name, obj := range map[string]cty.Value{
		"marked body":      azapiConfig("Microsoft.Network/virtualNetworks@2023-09-01", plain.Mark("sensitive"), own),
		"unknown taggable": azapiConfig("Microsoft.Network/virtualNetworks@1999-01-01", plain, own),
	} {
		if _, ok, why := ARMTagPlacement(addr, azapiSchema().Block, obj); ok || why == "" {
			t.Errorf("%s: placed=%v why=%q, want declined with a reason", name, ok, why)
		}
	}
}
