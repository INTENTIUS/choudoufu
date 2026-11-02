// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// Migrating an Azure estate off a stock state file (GitHub issue #1120 unit
// 19, workstream G). The Azure family's Adopt write is tags-only-plan on the
// arm-tags surface (internal/live/substrate's azure Writes): the same
// PlanResourceChange/ApplyResourceChange pair an AWS migration makes, with
// two differences the tests below pin. ARM tag keys are case-insensitive, so
// a marker is read in any spelling and written under the one the object
// already uses; and azapi_resource is one Terraform type for every ARM type,
// so whether an object can carry a marker at all, and whether its tags live
// in body.tags or the top-level argument, is read off the object.
//
// The fake provider is azureCloud: schemas narrowed from azurerm 5.10.0 and
// azapi 2.13.0 (tofu providers schema -json), and a plan that does what
// azapi's ModifyPlan does with tags (tagsWithDefaultTags: the configuration's
// top-level tags, else body.tags).

const (
	azSub        = "00000000-0000-0000-0000-000000000001"
	azRGID       = "/subscriptions/" + azSub + "/resourceGroups/rg-app"
	azVNetID     = azRGID + "/providers/Microsoft.Network/virtualNetworks/vnet-app"
	azSubnetID   = azVNetID + "/subnets/snet-web"
	azVNetType   = "Microsoft.Network/virtualNetworks@2024-05-01"
	azSubnetType = "Microsoft.Network/virtualNetworks/subnets@2024-05-01"
)

var (
	azurermProvider = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")}
	azapiProvider   = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azapi")}
)

func azureSchemas() map[string]providers.Schema {
	str := func(req, opt, comp bool) *configschema.Attribute {
		return &configschema.Attribute{Type: cty.String, Required: req, Optional: opt, Computed: comp}
	}
	ident := func(names ...string) *configschema.Object {
		attrs := map[string]*configschema.Attribute{}
		for _, n := range names {
			attrs[n] = &configschema.Attribute{Type: cty.String, Required: true}
		}
		return &configschema.Object{Nesting: configschema.NestingSingle, Attributes: attrs}
	}
	return map[string]providers.Schema{
		// azurerm_resource_group: taggable, and on the identity-schema
		// route (name, subscription_id).
		"azurerm_resource_group": {
			Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id":         str(false, true, true),
				"name":       str(true, false, false),
				"location":   str(true, false, false),
				"managed_by": str(false, true, false),
				"tags":       {Type: cty.Map(cty.String), Optional: true},
			}},
			IdentitySchema: ident("name", "subscription_id"),
		},
		// azurerm_subnet: no tags argument at all.
		"azurerm_subnet": {
			Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id":                   str(false, true, true),
				"name":                 str(true, false, false),
				"resource_group_name":  str(true, false, false),
				"virtual_network_name": str(true, false, false),
			}},
			IdentitySchema: ident("name", "resource_group_name", "subscription_id", "virtual_network_name"),
		},
		"azapi_resource": {
			Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"id":        str(false, false, true),
				"type":      str(true, false, false),
				"name":      str(false, true, true),
				"parent_id": str(false, true, true),
				"location":  str(false, true, true),
				"body":      {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
				"output":    {Type: cty.DynamicPseudoType, Computed: true},
				"tags":      {Type: cty.Map(cty.String), Optional: true, Computed: true},
			}},
			IdentitySchema: &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{
				"id":   {Type: cty.String, Required: true},
				"type": {Type: cty.String, Optional: true},
			}},
		},
	}
}

// azureCloud is the live side: objects by id, read and written through one
// mock provider that serves both azurerm's and azapi's types.
type azureCloud struct {
	*tofu.MockProvider
	objects map[string]cty.Value
	applied []string
	// planHook, when set, rewrites every planned object, to stand in for a
	// provider that proposes more than the tags.
	planHook func(cty.Value) cty.Value
	// lastConfig is the configuration the last plan was sent.
	lastConfig cty.Value
}

func newAzureCloud() *azureCloud {
	c := &azureCloud{
		MockProvider: &tofu.MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: azureSchemas(),
		}},
		objects: map[string]cty.Value{},
	}
	c.ConfigureProviderCalled = true
	c.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		obj, ok := c.objects[r.PriorState.GetAttr("id").AsString()]
		if !ok {
			// What azapi's Read and azurerm's do with a 404: remove the
			// resource from state, no diagnostic.
			return providers.ReadResourceResponse{NewState: cty.NullVal(r.PriorState.Type())}
		}
		return providers.ReadResourceResponse{NewState: obj}
	}
	c.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		c.lastConfig = r.Config
		planned := r.ProposedNewState
		if r.TypeName == "azapi_resource" {
			planned = azapiPlannedTags(r.Config, planned)
		}
		if c.planHook != nil {
			planned = c.planHook(planned)
		}
		return providers.PlanResourceChangeResponse{PlannedState: planned}
	}
	c.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		id := r.PriorState.GetAttr("id").AsString()
		c.objects[id] = r.PlannedState
		c.applied = append(c.applied, id)
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return c
}

// azapiPlannedTags is azapi 2.13.0's tagsWithDefaultTags for a known body
// (internal/services/azapi_resource.go): the configuration's top-level tags
// when set, else the body's tags when it has them, else what was proposed.
func azapiPlannedTags(config, planned cty.Value) cty.Value {
	if !config.GetAttr("tags").IsNull() {
		return planned
	}
	body := config.GetAttr("body")
	if body.IsNull() || !body.IsKnown() || !body.Type().IsObjectType() || !body.Type().HasAttribute("tags") {
		return planned
	}
	tags := map[string]cty.Value{}
	for it := body.GetAttr("tags").ElementIterator(); it.Next(); {
		k, v := it.Element()
		tags[k.AsString()] = v
	}
	vals := planned.AsValueMap()
	vals["tags"] = cty.MapVal(tags)
	if len(tags) == 0 {
		vals["tags"] = cty.MapValEmpty(cty.String)
	}
	return cty.ObjectVal(vals)
}

func (c *azureCloud) ConfiguredProvider(context.Context, addrs.AbsProviderConfig) (providers.Interface, error) {
	return c, nil
}

func (c *azureCloud) tags(t *testing.T, id string) map[string]string {
	t.Helper()
	if _, ok := markers.ARMTagsOf(c.objects[id]); !ok {
		t.Fatalf("%s has no readable tags", id)
	}
	raw := map[string]string{}
	for it := c.objects[id].GetAttr("tags").ElementIterator(); it.Next(); {
		k, v := it.Element()
		raw[k.AsString()] = v.AsString()
	}
	return raw
}

func strMap(m map[string]string) cty.Value {
	if len(m) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	vals := map[string]cty.Value{}
	for k, v := range m {
		vals[k] = cty.StringVal(v)
	}
	return cty.MapVal(vals)
}

func resourceGroup(tags map[string]string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":         cty.StringVal(azRGID),
		"name":       cty.StringVal("rg-app"),
		"location":   cty.StringVal("westeurope"),
		"managed_by": cty.NullVal(cty.String),
		"tags":       strMap(tags),
	})
}

func subnet() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":                   cty.StringVal(azSubnetID),
		"name":                 cty.StringVal("snet-web"),
		"resource_group_name":  cty.StringVal("rg-app"),
		"virtual_network_name": cty.StringVal("vnet-app"),
	})
}

// azapiObject is an azapi_resource as state records it: tags copied into
// the top-level attribute whether the configuration put them there or in
// body (azapi's Read, lines 1182-1184).
func azapiObject(id, typ, parent, name string, body cty.Value, tags map[string]string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":        cty.StringVal(id),
		"type":      cty.StringVal(typ),
		"name":      cty.StringVal(name),
		"parent_id": cty.StringVal(parent),
		"location":  cty.StringVal("westeurope"),
		"body":      body,
		"output":    cty.EmptyObjectVal,
		"tags":      strMap(tags),
	})
}

func vnetBody(tags map[string]string) cty.Value {
	props := cty.ObjectVal(map[string]cty.Value{
		"addressSpace": cty.ObjectVal(map[string]cty.Value{
			"addressPrefixes": cty.TupleVal([]cty.Value{cty.StringVal("10.0.0.0/16")}),
		}),
	})
	if tags == nil {
		return cty.ObjectVal(map[string]cty.Value{"properties": props})
	}
	tv := map[string]cty.Value{}
	for k, v := range tags {
		tv[k] = cty.StringVal(v)
	}
	return cty.ObjectVal(map[string]cty.Value{"properties": props, "tags": cty.ObjectVal(tv)})
}

// azureState is a stock state file holding objs, each at its address under
// its provider, encoded as stock writes it.
func azureState(t *testing.T, objs map[string]cty.Value) *states.State {
	t.Helper()
	state := states.NewState()
	schemas := azureSchemas()
	for addrStr, obj := range objs {
		addr := mustAddr(t, addrStr)
		typ := addr.Resource.Resource.Type
		raw, err := ctyjson.Marshal(obj, schemas[typ].Block.ImpliedType())
		if err != nil {
			t.Fatalf("encoding %s: %s", addrStr, err)
		}
		prov := azurermProvider
		if strings.HasPrefix(typ, "azapi_") {
			prov = azapiProvider
		}
		state.RootModule().SetResourceInstanceCurrent(addr.Resource,
			&states.ResourceInstanceObjectSrc{AttrsJSON: raw, Status: states.ObjectReady}, prov, addrs.NoKey)
	}
	return state
}

func azureRatify(t *testing.T, cloud *azureCloud, state *states.State, store *projection.RecordStore) *Ratification {
	t.Helper()
	req := Request{Estate: testEstate, State: state, Providers: cloud}
	if store != nil {
		req.RecordStore = store
	}
	rat, diags := Ratify(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("Ratify: %s", diags.Err())
	}
	return rat
}

func approveAll(t *testing.T, rat *Ratification) map[string]StampOutcome {
	t.Helper()
	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve: %s", diags.Err())
	}
	out := map[string]StampOutcome{}
	for _, o := range rep.Outcomes {
		out[o.Addr.String()] = o
	}
	return out
}

func entryFor(rat *Ratification, addr string) (Entry, bool) {
	for _, e := range rat.Entries {
		if e.Addr.String() == addr {
			return e, true
		}
	}
	return Entry{}, false
}

// TestAzurermAdoptWritesLowercaseMarkers: an azurerm resource group in a
// stock state file is VERIFIED, and -approve writes tofu-estate and
// tofu-address in the lowercase spelling the rest of the tree compares
// against, beside the object's own tags, which are left as they were.
func TestAzurermAdoptWritesLowercaseMarkers(t *testing.T) {
	cloud := newAzureCloud()
	cloud.objects[azRGID] = resourceGroup(map[string]string{"env": "prod"})
	state := azureState(t, map[string]cty.Value{"azurerm_resource_group.main": resourceGroup(map[string]string{"env": "prod"})})

	rat := azureRatify(t, cloud, state, nil)
	if e, _ := entryFor(rat, "azurerm_resource_group.main"); e.Status != StatusVerified {
		t.Fatalf("ratified %s (%s), want VERIFIED", e.Status, e.Detail)
	}
	if o := approveAll(t, rat)["azurerm_resource_group.main"]; o.Outcome != OutcomeStamped {
		t.Fatalf("outcome %s (%s), want STAMPED", o.Outcome, o.Detail)
	}
	want := map[string]string{"env": "prod", "tofu-estate": testEstate, "tofu-address": "azurerm_resource_group.main"}
	if got := cloud.tags(t, azRGID); !mapsEqual(got, want) {
		t.Errorf("live tags %v, want %v", got, want)
	}
}

// TestAzurermAdoptRefusesAPlanThatChangesMoreThanTags: the provider's plan
// for the tag write also moves location (a ForceNew argument on a resource
// group), so nothing is applied and the outcome names the attribute.
func TestAzurermAdoptRefusesAPlanThatChangesMoreThanTags(t *testing.T) {
	cloud := newAzureCloud()
	cloud.objects[azRGID] = resourceGroup(nil)
	cloud.planHook = func(v cty.Value) cty.Value {
		vals := v.AsValueMap()
		vals["location"] = cty.StringVal("northeurope")
		return cty.ObjectVal(vals)
	}
	state := azureState(t, map[string]cty.Value{"azurerm_resource_group.main": resourceGroup(nil)})

	o := approveAll(t, azureRatify(t, cloud, state, nil))["azurerm_resource_group.main"]
	if o.Outcome != OutcomeFailed || !strings.Contains(o.Detail, "location") {
		t.Fatalf("outcome %s (%s), want FAILED naming location", o.Outcome, o.Detail)
	}
	if len(cloud.applied) != 0 {
		t.Errorf("applied %v; a plan that changes more than tags must never be applied", cloud.applied)
	}
}

// TestAzurermAdoptKeepsTheObjectsOwnMarkerSpelling: an object already
// tagged Tofu-Estate (an operator's az tag update, an Azure Policy modify
// effect) with this estate is read as marked, and the write adds
// tofu-address without a second, lowercase tofu-estate key: ARM refuses two
// keys that differ only by case. A second run reads both markers back and
// writes nothing.
func TestAzurermAdoptKeepsTheObjectsOwnMarkerSpelling(t *testing.T) {
	cloud := newAzureCloud()
	live := map[string]string{"Tofu-Estate": testEstate, "env": "prod"}
	cloud.objects[azRGID] = resourceGroup(live)
	state := azureState(t, map[string]cty.Value{"azurerm_resource_group.main": resourceGroup(live)})

	if o := approveAll(t, azureRatify(t, cloud, state, nil))["azurerm_resource_group.main"]; o.Outcome != OutcomeStamped {
		t.Fatalf("outcome %s (%s), want STAMPED", o.Outcome, o.Detail)
	}
	got := cloud.tags(t, azRGID)
	want := map[string]string{"Tofu-Estate": testEstate, "env": "prod", "tofu-address": "azurerm_resource_group.main"}
	if !mapsEqual(got, want) {
		t.Fatalf("live tags %v, want %v (one tofu-estate key, under the object's own spelling)", got, want)
	}

	applied := len(cloud.applied)
	state2 := azureState(t, map[string]cty.Value{"azurerm_resource_group.main": cloud.objects[azRGID]})
	if o := approveAll(t, azureRatify(t, cloud, state2, nil))["azurerm_resource_group.main"]; o.Outcome != OutcomeAlreadyStamped {
		t.Errorf("second run: outcome %s (%s), want ALREADY_STAMPED", o.Outcome, o.Detail)
	}
	if len(cloud.applied) != applied {
		t.Errorf("the second run wrote again: %v", cloud.applied)
	}
}

// TestAzurermAdoptRefusesAnotherEstatesMarkerInAnySpelling: TOFU-ESTATE
// naming another estate is another estate's marker, whatever its case.
func TestAzurermAdoptRefusesAnotherEstatesMarkerInAnySpelling(t *testing.T) {
	cloud := newAzureCloud()
	live := map[string]string{"TOFU-ESTATE": "other-team"}
	cloud.objects[azRGID] = resourceGroup(live)
	state := azureState(t, map[string]cty.Value{"azurerm_resource_group.main": resourceGroup(live)})

	o := approveAll(t, azureRatify(t, cloud, state, nil))["azurerm_resource_group.main"]
	if o.Outcome != OutcomeFailed || !strings.Contains(o.Detail, "other-team") {
		t.Fatalf("outcome %s (%s), want FAILED naming the other estate", o.Outcome, o.Detail)
	}
	if len(cloud.applied) != 0 {
		t.Errorf("applied %v onto another estate's object", cloud.applied)
	}
}

// TestAzapiAdoptThroughTopLevelTags: an azapi virtual network whose
// configuration uses the top-level tags argument gets its markers there, and
// its body is not touched.
func TestAzapiAdoptThroughTopLevelTags(t *testing.T) {
	cloud := newAzureCloud()
	obj := azapiObject(azVNetID, azVNetType, azRGID, "vnet-app", vnetBody(nil), map[string]string{"env": "prod"})
	cloud.objects[azVNetID] = obj
	state := azureState(t, map[string]cty.Value{"azapi_resource.vnet": obj})

	rat := azureRatify(t, cloud, state, nil)
	if e, _ := entryFor(rat, "azapi_resource.vnet"); e.Status != StatusVerified {
		t.Fatalf("ratified %s (%s), want VERIFIED", e.Status, e.Detail)
	}
	if o := approveAll(t, rat)["azapi_resource.vnet"]; o.Outcome != OutcomeStamped {
		t.Fatalf("outcome %s (%s), want STAMPED", o.Outcome, o.Detail)
	}
	want := map[string]string{"env": "prod", "tofu-estate": testEstate, "tofu-address": "azapi_resource.vnet"}
	if got := cloud.tags(t, azVNetID); !mapsEqual(got, want) {
		t.Errorf("live tags %v, want %v", got, want)
	}
	if body := cloud.objects[azVNetID].GetAttr("body"); body.Type().HasAttribute("tags") {
		t.Errorf("the body gained a tags key %#v; this configuration keeps its tags at the top level", body)
	}
}

// TestAzapiAdoptThroughBodyTags: an azapi virtual network whose
// configuration keeps its tags in body.tags gets its markers there. azapi
// sends body.tags and ignores the top-level argument whenever the body has
// them, so a marker written only at the top level is planned, applied and
// never stored. The configuration sent leaves the top-level argument null,
// as the operator's did (azapi refuses one that sets both).
func TestAzapiAdoptThroughBodyTags(t *testing.T) {
	cloud := newAzureCloud()
	obj := azapiObject(azVNetID, azVNetType, azRGID, "vnet-app", vnetBody(map[string]string{"env": "prod"}), map[string]string{"env": "prod"})
	cloud.objects[azVNetID] = obj
	state := azureState(t, map[string]cty.Value{"azapi_resource.vnet": obj})

	if o := approveAll(t, azureRatify(t, cloud, state, nil))["azapi_resource.vnet"]; o.Outcome != OutcomeStamped {
		t.Fatalf("outcome %s (%s), want STAMPED", o.Outcome, o.Detail)
	}
	if cfg := cloud.lastConfig.GetAttr("tags"); !cfg.IsNull() {
		t.Errorf("the configuration sent sets top-level tags %#v beside body.tags", cfg)
	}
	bodyTags := map[string]string{}
	for it := cloud.objects[azVNetID].GetAttr("body").GetAttr("tags").ElementIterator(); it.Next(); {
		k, v := it.Element()
		bodyTags[k.AsString()] = v.AsString()
	}
	want := map[string]string{"env": "prod", "tofu-estate": testEstate, "tofu-address": "azapi_resource.vnet"}
	if !mapsEqual(bodyTags, want) {
		t.Errorf("body.tags %v, want %v", bodyTags, want)
	}
	if got := cloud.tags(t, azVNetID); !mapsEqual(got, want) {
		t.Errorf("top-level tags read back %v, want %v", got, want)
	}
}

// TestAzapiBodyAdoptRefusesABodyChangeOutsideTags: the body may move in its
// tags and nowhere else.
func TestAzapiBodyAdoptRefusesABodyChangeOutsideTags(t *testing.T) {
	cloud := newAzureCloud()
	obj := azapiObject(azVNetID, azVNetType, azRGID, "vnet-app", vnetBody(map[string]string{"env": "prod"}), map[string]string{"env": "prod"})
	cloud.objects[azVNetID] = obj
	cloud.planHook = func(v cty.Value) cty.Value {
		vals := v.AsValueMap()
		body := vals["body"].AsValueMap()
		body["properties"] = cty.ObjectVal(map[string]cty.Value{"addressSpace": cty.NullVal(cty.DynamicPseudoType)})
		vals["body"] = cty.ObjectVal(body)
		return cty.ObjectVal(vals)
	}
	state := azureState(t, map[string]cty.Value{"azapi_resource.vnet": obj})

	o := approveAll(t, azureRatify(t, cloud, state, nil))["azapi_resource.vnet"]
	if o.Outcome != OutcomeFailed || !strings.Contains(o.Detail, "body outside its tags") {
		t.Fatalf("outcome %s (%s), want FAILED naming the body", o.Outcome, o.Detail)
	}
	if len(cloud.applied) != 0 {
		t.Errorf("applied %v", cloud.applied)
	}
}

// TestUntaggableAzureTypesTakeTheRecordPath: the AWS precedent is aws_route
// (TestRatify_classifiesEveryStatus's UNTAGGABLE row) with #364 unit A2's
// identity record: an untaggable type is never stamped, and its identity is
// recorded so a later plan can find it and a removal can be proposed from
// it. azurerm_subnet has no tags argument; an azapi subnet has one in its
// schema, but its ARM type takes no tags at its API version, which the
// stamp honours (armTagTarget) and so does this. Both are UNTAGGABLE by
// name, nothing is applied, and both leave an identity record.
func TestUntaggableAzureTypesTakeTheRecordPath(t *testing.T) {
	cloud := newAzureCloud()
	azapiSubnet := azapiObject(azSubnetID, azSubnetType, azVNetID, "snet-web", cty.ObjectVal(map[string]cty.Value{
		"properties": cty.ObjectVal(map[string]cty.Value{"addressPrefix": cty.StringVal("10.0.1.0/24")}),
	}), nil)
	cloud.objects[azSubnetID] = azapiSubnet
	rawStore, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := projection.NewRecordEnvelopeStore(rawStore, projection.RecordKeyPrefix(testEstate))
	state := azureState(t, map[string]cty.Value{
		"azapi_resource.subnet": azapiSubnet,
		"azurerm_subnet.web":    subnet(),
	})

	rat := azureRatify(t, cloud, state, store)
	sub, _ := entryFor(rat, "azapi_resource.subnet")
	if sub.Status != StatusUntaggable || !strings.Contains(sub.Detail, "Microsoft.Network/virtualNetworks/subnets") || !strings.Contains(sub.Detail, "2024-05-01") {
		t.Errorf("azapi_resource.subnet ratified %s (%s), want UNTAGGABLE naming its ARM type and API version", sub.Status, sub.Detail)
	}
	if rm, _ := entryFor(rat, "azurerm_subnet.web"); rm.Status != StatusUntaggable || !strings.Contains(rm.Detail, "azurerm_subnet") {
		t.Errorf("azurerm_subnet.web ratified %s (%s), want UNTAGGABLE naming its type", rm.Status, rm.Detail)
	}

	out := approveAll(t, rat)
	for _, addr := range []string{"azapi_resource.subnet", "azurerm_subnet.web"} {
		if out[addr].Outcome != OutcomeSkipped {
			t.Errorf("%s: outcome %s (%s), want SKIPPED", addr, out[addr].Outcome, out[addr].Detail)
		}
	}
	if len(cloud.applied) != 0 {
		t.Fatalf("applied %v; an untaggable object is never written to", cloud.applied)
	}

	rec, _, _, found, err := store.GetIdentity(context.Background(), mustAddr(t, "azapi_resource.subnet"))
	if err != nil || !found {
		t.Fatalf("no identity record for azapi_resource.subnet (found %v, err %v)", found, err)
	}
	if rec.ImportID != azSubnetID || rec.Components["type"] != azSubnetType || rec.Components["parent_id"] != azVNetID || rec.Components["name"] != "snet-web" {
		t.Errorf("azapi_resource.subnet's record = %+v, want its id with type, parent_id and name beside it", rec)
	}
}

// TestAzapiMissingObjectIsMissing: azapi answers a 404 with an empty state
// and no diagnostic, on Read and on import alike (RemoveResource; see
// internal/live/absent's package comment), and live-import verifies through
// Read. It reads such an object as gone, MISSING, never as an error and
// never as something to stamp.
func TestAzapiMissingObjectIsMissing(t *testing.T) {
	cloud := newAzureCloud()
	obj := azapiObject(azVNetID, azVNetType, azRGID, "vnet-app", vnetBody(nil), nil)
	state := azureState(t, map[string]cty.Value{"azapi_resource.vnet": obj})

	rat := azureRatify(t, cloud, state, nil)
	if e, _ := entryFor(rat, "azapi_resource.vnet"); e.Status != StatusMissing {
		t.Fatalf("ratified %s (%s), want MISSING", e.Status, e.Detail)
	}
	if o := approveAll(t, rat)["azapi_resource.vnet"]; o.Outcome != OutcomeSkipped {
		t.Errorf("outcome %s (%s), want SKIPPED", o.Outcome, o.Detail)
	}
	if len(cloud.applied) != 0 {
		t.Errorf("applied %v to an object that does not exist", cloud.applied)
	}
}

func mapsEqual(a, b map[string]string) bool {
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
