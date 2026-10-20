// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// live-mv on the arm-tags surface (GitHub issue #1120 unit 19, workstream
// G). A rename rewrites tofu-address and a move between estates rewrites
// tofu-estate too, through the same tags-only plan and apply as on AWS; the
// differences are the ones ARM makes. Tag keys are case-insensitive, so a
// marker the object carries as Tofu-Address is rewritten under that
// spelling, never joined by a second key ARM would refuse. An
// azapi_resource keeps its tags where its configuration put them, so a
// marker in body.tags is rewritten there. And an azapi_resource whose ARM
// type takes no tags carries no marker to rewrite, which is refused by name
// as an AWS type with no tags argument is.
//
// Proving them red: drop the fold in [mover.rewrite]'s setMarkerTag calls
// and TestMove_AzurermRenameKeepsTheMarkerSpelling finds two address keys;
// drop the ARMTagPlacement switch and TestMove_AzapiBodyTagsAreRewrittenInTheBody
// finds body.tags unchanged and TestMove_AzapiUntaggableTypeIsRefusedByName
// a write to a subnet.

const (
	azMvRGID     = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-app"
	azMvVNetID   = azMvRGID + "/providers/Microsoft.Network/virtualNetworks/vnet-app"
	azMvSubnetID = azMvVNetID + "/subnets/snet-web"
)

func azMvSchemas() map[string]providers.Schema {
	str := func(req, opt, comp bool) *configschema.Attribute {
		return &configschema.Attribute{Type: cty.String, Required: req, Optional: opt, Computed: comp}
	}
	return map[string]providers.Schema{
		"azurerm_resource_group": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":       str(false, true, true),
			"name":     str(true, false, false),
			"location": str(true, false, false),
			"tags":     {Type: cty.Map(cty.String), Optional: true},
		}}},
		"azapi_resource": {
			Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id":        str(false, false, true),
				"type":      str(true, false, false),
				"name":      str(false, true, true),
				"parent_id": str(false, true, true),
				"body":      {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
				"tags":      {Type: cty.Map(cty.String), Optional: true, Computed: true},
			}},
			IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
				"id":   {Type: cty.String, Required: true},
				"type": {Type: cty.String, Optional: true},
			}},
		},
	}
}

// azMvCloud is one live Azure object behind a provider that serves both
// azurerm's and azapi's types, counting the calls a move makes. Its plan
// does what azapi's does with tags (tagsWithDefaultTags): the
// configuration's top-level tags when set, else body.tags.
type azMvCloud struct {
	*tofu.MockProvider
	object     cty.Value
	plans      int
	applies    int
	lastConfig cty.Value
}

func newAzMvCloud(object cty.Value) *azMvCloud {
	c := &azMvCloud{MockProvider: &tofu.MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
		Provider:      providers.Schema{Block: &configschema.Block{}},
		ResourceTypes: azMvSchemas(),
	}}, object: object}
	c.ConfigureProviderCalled = true
	c.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{TypeName: r.TypeName, State: c.object}}}
	}
	c.ReadResourceFn = func(providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: c.object}
	}
	c.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		if r.PriorState.IsNull() {
			return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
		}
		c.plans++
		c.lastConfig = r.Config
		planned := r.ProposedNewState
		if r.TypeName == "azapi_resource" && r.Config.GetAttr("tags").IsNull() {
			if body := r.Config.GetAttr("body"); !body.IsNull() && body.Type().IsObjectType() && body.Type().HasAttribute("tags") {
				tags := map[string]cty.Value{}
				for it := body.GetAttr("tags").ElementIterator(); it.Next(); {
					k, v := it.Element()
					tags[k.AsString()] = v
				}
				vals := planned.AsValueMap()
				vals["tags"] = cty.MapVal(tags)
				planned = cty.ObjectVal(vals)
			}
		}
		return providers.PlanResourceChangeResponse{PlannedState: planned}
	}
	c.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		c.applies++
		c.object = r.PlannedState
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return c
}

func (*azMvCloud) ListResourceStream(context.Context, providers.ListResourceRequest, func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	return nil
}

func (c *azMvCloud) tags(t *testing.T) map[string]string {
	t.Helper()
	return ctyStringMap(t, c.object.GetAttr("tags"))
}

func ctyStringMap(t *testing.T, v cty.Value) map[string]string {
	t.Helper()
	out := map[string]string{}
	if v.IsNull() {
		return out
	}
	for it := v.ElementIterator(); it.Next(); {
		k, val := it.Element()
		out[k.AsString()] = val.AsString()
	}
	return out
}

func azMvStrMap(m map[string]string) cty.Value {
	vals := map[string]cty.Value{}
	for k, v := range m {
		vals[k] = cty.StringVal(v)
	}
	if len(vals) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	return cty.MapVal(vals)
}

func azMvResourceGroup(tags map[string]string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal(azMvRGID),
		"name":     cty.StringVal("rg-app"),
		"location": cty.StringVal("westeurope"),
		"tags":     azMvStrMap(tags),
	})
}

func azMvAzapi(id, typ, parent, name string, body cty.Value, tags map[string]string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":        cty.StringVal(id),
		"type":      cty.StringVal(typ),
		"name":      cty.StringVal(name),
		"parent_id": cty.StringVal(parent),
		"body":      body,
		"tags":      azMvStrMap(tags),
	})
}

// azMvConfig declares the destination block.
func azMvConfig(t *testing.T, src string) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
    azapi   = { source = "azure/azapi" }
  }
}
`+src)
	return loadConfigDir(t, dir)
}

func azMvRequest(cloud *azMvCloud, cfg *configs.Config, provider string, old, new addrs.AbsResourceInstance, importID, from, to string) Request {
	return Request{
		Estate:      to,
		FromEstate:  from,
		Old:         old,
		New:         new,
		Config:      cfg,
		Resolutions: []identity.Resolution{{Addr: new, Class: identity.ClassConcrete, ImportID: importID}},
		Providers: projection.SingleProvider(addrs.AbsProviderConfig{
			Module:   addrs.RootModule,
			Provider: addrs.NewProvider(addrs.DefaultProviderRegistryHost, map[string]string{"azurerm": "hashicorp", "azapi": "azure"}[provider], provider),
		}, cloud),
	}
}

const azMvRGBlock = `
resource "azurerm_resource_group" "%s" {
  name     = "rg-app"
  location = "westeurope"
}
`

func rgBlock(name string) string { return strings.Replace(azMvRGBlock, "%s", name, 1) }

// TestMove_AzurermRenameKeepsTheMarkerSpelling: the object carries its
// markers as Tofu-Estate and Tofu-Address. The rename rewrites the address
// under that spelling and adds no lowercase key beside it.
func TestMove_AzurermRenameKeepsTheMarkerSpelling(t *testing.T) {
	cloud := newAzMvCloud(azMvResourceGroup(map[string]string{
		"Tofu-Estate": "app", "Tofu-Address": "azurerm_resource_group.old", "env": "prod",
	}))
	old, renamed := mustAddr(t, "azurerm_resource_group.old"), mustAddr(t, "azurerm_resource_group.main")
	res, diags := Move(t.Context(), azMvRequest(cloud, azMvConfig(t, rgBlock("main")), "azurerm", old, renamed, azMvRGID, "", "app"))
	if diags.HasErrors() {
		t.Fatalf("the rename was refused: %s", diags.Err())
	}
	if res.Surface != markers.SurfaceARMTags || !res.Written || !res.Verified {
		t.Errorf("Surface %q, Written %v, Verified %v; want arm-tags, written and verified", res.Surface, res.Written, res.Verified)
	}
	want := map[string]string{"Tofu-Estate": "app", "Tofu-Address": "azurerm_resource_group.main", "env": "prod"}
	if got := cloud.tags(t); !azMvMapsEqual(got, want) {
		t.Errorf("live tags %v, want %v", got, want)
	}
}

// TestMove_AzurermBetweenEstates: -from-estate moves the resource group
// from estate app to estate data, rewriting tofu-estate and keeping the
// address; a second run reports it already ran and writes nothing.
func TestMove_AzurermBetweenEstates(t *testing.T) {
	addr := mustAddr(t, "azurerm_resource_group.main")
	cloud := newAzMvCloud(azMvResourceGroup(map[string]string{"tofu-estate": "app", "tofu-address": "azurerm_resource_group.main"}))
	res, diags := Move(t.Context(), azMvRequest(cloud, azMvConfig(t, rgBlock("main")), "azurerm", addr, addr, azMvRGID, "app", "data"))
	if diags.HasErrors() {
		t.Fatalf("the move was refused: %s", diags.Err())
	}
	if !res.Written || !res.Verified {
		t.Errorf("Written %v, Verified %v; want both", res.Written, res.Verified)
	}
	want := map[string]string{"tofu-estate": "data", "tofu-address": "azurerm_resource_group.main"}
	if got := cloud.tags(t); !azMvMapsEqual(got, want) {
		t.Errorf("live tags %v, want %v", got, want)
	}

	again := newAzMvCloud(cloud.object)
	_, diags = Move(t.Context(), azMvRequest(again, azMvConfig(t, rgBlock("main")), "azurerm", addr, addr, azMvRGID, "app", "data"))
	if got := RefusalFrom(diags); got != RefusalNewAddressClaimed {
		t.Errorf("second move: refusal %q, want %q: %v", got, RefusalNewAddressClaimed, diags.Err())
	}
	if again.applies != 0 {
		t.Errorf("the second move wrote %d times", again.applies)
	}
}

const azMvVNetBlock = `
resource "azapi_resource" "%s" {
  type      = "Microsoft.Network/virtualNetworks@2024-05-01"
  parent_id = "` + azMvRGID + `"
  name      = "vnet-app"
  body = {
    properties = { addressSpace = { addressPrefixes = ["10.0.0.0/16"] } }
    tags       = { env = "prod" }
  }
}
`

// TestMove_AzapiBodyTagsAreRewrittenInTheBody: an azapi virtual network
// whose configuration keeps its tags in body.tags has its address
// rewritten there, with a null top-level argument in the configuration
// sent, because azapi sends body.tags and ignores the top-level argument
// whenever the body has them.
func TestMove_AzapiBodyTagsAreRewrittenInTheBody(t *testing.T) {
	tags := map[string]string{"env": "prod", "tofu-estate": "app", "tofu-address": "azapi_resource.old"}
	tagObj := map[string]cty.Value{}
	for k, v := range tags {
		tagObj[k] = cty.StringVal(v)
	}
	body := cty.ObjectVal(map[string]cty.Value{
		"properties": cty.ObjectVal(map[string]cty.Value{"addressSpace": cty.ObjectVal(map[string]cty.Value{
			"addressPrefixes": cty.TupleVal([]cty.Value{cty.StringVal("10.0.0.0/16")}),
		})}),
		"tags": cty.ObjectVal(tagObj),
	})
	cloud := newAzMvCloud(azMvAzapi(azMvVNetID, "Microsoft.Network/virtualNetworks@2024-05-01", azMvRGID, "vnet-app", body, tags))
	old, renamed := mustAddr(t, "azapi_resource.old"), mustAddr(t, "azapi_resource.vnet")
	_, diags := Move(t.Context(), azMvRequest(cloud, azMvConfig(t, strings.Replace(azMvVNetBlock, "%s", "vnet", 1)), "azapi", old, renamed, azMvVNetID, "", "app"))
	if diags.HasErrors() {
		t.Fatalf("the rename was refused: %s", diags.Err())
	}
	want := map[string]string{"env": "prod", "tofu-estate": "app", "tofu-address": "azapi_resource.vnet"}
	if got := ctyStringMap(t, cloud.object.GetAttr("body").GetAttr("tags")); !azMvMapsEqual(got, want) {
		t.Errorf("body.tags %v, want %v", got, want)
	}
	if got := cloud.tags(t); !azMvMapsEqual(got, want) {
		t.Errorf("top-level tags read back %v, want %v", got, want)
	}
	if cfg := cloud.lastConfig.GetAttr("tags"); !cfg.IsNull() {
		t.Errorf("the configuration sent sets top-level tags %#v beside body.tags", cfg)
	}
}

// TestMove_AzapiUntaggableTypeIsRefusedByName: a subnet declared through
// azapi carries no marker, its ARM type taking no tags at its API version,
// so there is nothing to rewrite. Refused by name, with the ARM type and
// version, as an AWS type with no tags argument is ("Resource type with no
// tags"), and nothing is planned or written.
func TestMove_AzapiUntaggableTypeIsRefusedByName(t *testing.T) {
	cloud := newAzMvCloud(azMvAzapi(azMvSubnetID, "Microsoft.Network/virtualNetworks/subnets@2024-05-01", azMvVNetID, "snet-web",
		cty.ObjectVal(map[string]cty.Value{"properties": cty.ObjectVal(map[string]cty.Value{"addressPrefix": cty.StringVal("10.0.1.0/24")})}), nil))
	old, renamed := mustAddr(t, "azapi_resource.old"), mustAddr(t, "azapi_resource.web")
	src := `
resource "azapi_resource" "web" {
  type      = "Microsoft.Network/virtualNetworks/subnets@2024-05-01"
  parent_id = "` + azMvVNetID + `"
  name      = "snet-web"
  body      = { properties = { addressPrefix = "10.0.1.0/24" } }
}
`
	_, diags := Move(t.Context(), azMvRequest(cloud, azMvConfig(t, src), "azapi", old, renamed, azMvSubnetID, "", "app"))
	if !diags.HasErrors() {
		t.Fatal("the rename of an untaggable azapi type was not refused")
	}
	msg := diags.Err().Error()
	for _, want := range []string{"Resource type with no tags", "Microsoft.Network/virtualNetworks/subnets", "2024-05-01", "renaming the block is the whole rename"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, msg)
		}
	}
	if cloud.plans != 0 || cloud.applies != 0 {
		t.Errorf("planned %d and applied %d times; want nothing", cloud.plans, cloud.applies)
	}
}

func azMvMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
