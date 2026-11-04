// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

func init() {
	proofs["aws-route-table-route-empty-strings"] = proof{check: checkRouteTableEmptyStrings}
}

// routeTableFake plans aws_route_table the way the SDKv2 provider does at
// 6.71.0. The SDK reads a null and a "" route field alike, so when the
// proposed routes and tags match prior with null read as "", it plans the
// prior state. Otherwise it plans the proposed value, and the SDK's
// null-normalisation carries configuration's nulls into every route
// element of the planned set: the #17198 plan.
type routeTableFake struct {
	providers.Interface
	typeName string
}

func (f *routeTableFake) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	return providers.GetProviderSchemaResponse{ResourceTypes: map[string]providers.Schema{
		f.typeName: {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":     {Type: cty.String, Computed: true},
			"vpc_id": {Type: cty.String, Required: true},
			"tags":   {Type: cty.Map(cty.String), Optional: true},
			"route":  {Type: routeTableRouteType(), Optional: true, Computed: true},
		}}},
	}}
}

func (f *routeTableFake) PlanResourceChange(_ context.Context, req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
	prior := req.PriorState.AsValueMap()
	planned := req.ProposedNewState.AsValueMap()
	planned["id"] = prior["id"]
	same := sdkRoutes(prior["route"]).RawEquals(sdkRoutes(planned["route"])) &&
		prior["tags"].RawEquals(planned["tags"]) && prior["vpc_id"].RawEquals(planned["vpc_id"])
	if same {
		return providers.PlanResourceChangeResponse{PlannedState: req.PriorState, LegacyTypeSystem: true}
	}
	return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(planned), LegacyTypeSystem: true}
}

// sdkRoutes is a route set as the SDK's ResourceData reads it: null fields
// as "".
func sdkRoutes(v cty.Value) cty.Value {
	all := map[string]bool{}
	for _, name := range routeTableRouteAttrs {
		all[name] = true
	}
	out, _ := emptyNullStrings(cty.ObjectVal(map[string]cty.Value{"route": v}), all)
	return out.GetAttr("route")
}

// rtRoute is one route element. stored writes "" for every unset field, as
// state does; otherwise unset fields are null, as configuration has them.
func rtRoute(stored bool, set map[string]string) cty.Value {
	attrs := map[string]cty.Value{}
	for _, name := range routeTableRouteAttrs {
		switch v, ok := set[name]; {
		case ok:
			attrs[name] = cty.StringVal(v)
		case stored:
			attrs[name] = cty.StringVal("")
		default:
			attrs[name] = cty.NullVal(cty.String)
		}
	}
	return cty.ObjectVal(attrs)
}

func rtObject(id cty.Value, routes ...cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":     id,
		"vpc_id": cty.StringVal("vpc-1"),
		"tags":   cty.NullVal(cty.Map(cty.String)),
		"route":  cty.SetVal(routes),
	})
}

var (
	rtDefault = map[string]string{"cidr_block": "0.0.0.0/0", "gateway_id": "igw-1"}
	rtPeerV1  = map[string]string{"cidr_block": "10.234.0.0/24", "vpc_peering_connection_id": "pcx-1"}
	rtPeerV2  = map[string]string{"cidr_block": "10.234.1.0/24", "vpc_peering_connection_id": "pcx-1"}
	rtNAT     = map[string]string{"cidr_block": "10.235.0.0/24", "nat_gateway_id": "nat-1"}
)

// rtPrior is the state live/override-repro/route-table-17198's v1 leaves.
func rtPrior() cty.Value {
	return rtObject(cty.StringVal("rtb-1"), rtRoute(true, rtDefault), rtRoute(true, rtPeerV1))
}

func rtConfig(routes ...map[string]string) cty.Value {
	var elems []cty.Value
	for _, r := range routes {
		elems = append(elems, rtRoute(false, r))
	}
	return rtObject(cty.NullVal(cty.String), elems...)
}

func rtPlan(t *testing.T, o Override, config cty.Value, on bool) providers.PlanResourceChangeResponse {
	t.Helper()
	fake := &routeTableFake{typeName: o.Type}
	var p providers.Interface = fake
	if on {
		p = Wrap(fake, map[string][]Override{o.Type: {o}})
	}
	prop := config.AsValueMap()
	prop["id"] = rtPrior().GetAttr("id")
	return p.PlanResourceChange(context.Background(), providers.PlanResourceChangeRequest{
		TypeName:         o.Type,
		PriorState:       rtPrior(),
		ProposedNewState: cty.ObjectVal(prop),
		Config:           config,
	})
}

// rtDiff is the route elements a plan removes from prior and adds.
func rtDiff(prior, planned cty.Value) (removed, added []cty.Value) {
	in := func(set, e cty.Value) bool { return set.HasElement(e).True() }
	p, n := prior.GetAttr("route"), planned.GetAttr("route")
	for it := p.ElementIterator(); it.Next(); {
		if _, e := it.Element(); !in(n, e) {
			removed = append(removed, e)
		}
	}
	for it := n.ElementIterator(); it.Next(); {
		if _, e := it.Element(); !in(p, e) {
			added = append(added, e)
		}
	}
	return removed, added
}

// checkRouteTableEmptyStrings is red-then-green on the repro: v2 changes one
// route's CIDR. Off, the plan removes and re-adds both routes; on, it
// replaces only the changed one. The unchanged v1 configuration plans no
// change and names no override.
func checkRouteTableEmptyStrings(t *testing.T, o Override) {
	v2 := rtConfig(rtDefault, rtPeerV2)

	off := rtPlan(t, o, v2, false)
	if removed, added := rtDiff(rtPrior(), off.PlannedState); len(removed) != 2 || len(added) != 2 {
		t.Fatalf("without the override the fake does not plan #17198's every-route diff (removed %d, added %d), so this proof cannot fail", len(removed), len(added))
	}

	on := rtPlan(t, o, v2, true)
	if on.Diagnostics.HasErrors() {
		t.Fatalf("errors with the override: %s", on.Diagnostics.Err())
	}
	removed, added := rtDiff(rtPrior(), on.PlannedState)
	if len(removed) != 1 || !removed[0].RawEquals(rtRoute(true, rtPeerV1)) {
		t.Errorf("with the override the plan removes %#v, want only the 10.234.0.0/24 route", removed)
	}
	if len(added) != 1 || !added[0].RawEquals(rtRoute(true, rtPeerV2)) {
		t.Errorf("with the override the plan adds %#v, want only the 10.234.1.0/24 route", added)
	}
	if !hasWarning(on.Diagnostics, "Provider override applied", o.Name) {
		t.Errorf("the plan does not name the override that changed it: %#v", on.Diagnostics)
	}

	same := rtPlan(t, o, rtConfig(rtDefault, rtPeerV1), true)
	if !same.PlannedState.RawEquals(rtPrior()) {
		t.Errorf("the unchanged configuration plans a change:\n got %#v\nwant %#v", same.PlannedState, rtPrior())
	}
	if len(same.Diagnostics) != 0 {
		t.Errorf("diagnostics on an unchanged plan: %#v", same.Diagnostics)
	}
}

// TestRouteTableRealChangeStillPlans checks the override does not hide a
// real change: a new route is still added, and a route whose target changes
// is still replaced in the set, with the other routes left alone.
func TestRouteTableRealChangeStillPlans(t *testing.T) {
	o, _ := Lookup("aws-route-table-route-empty-strings")

	resp := rtPlan(t, o, rtConfig(rtDefault, rtPeerV1, rtNAT), true)
	removed, added := rtDiff(rtPrior(), resp.PlannedState)
	if len(removed) != 0 || len(added) != 1 || !added[0].RawEquals(rtRoute(true, rtNAT)) {
		t.Errorf("a new route: removed %#v, added %#v; want only the NAT route added", removed, added)
	}

	retarget := map[string]string{"cidr_block": "0.0.0.0/0", "nat_gateway_id": "nat-1"}
	resp = rtPlan(t, o, rtConfig(retarget, rtPeerV1), true)
	removed, added = rtDiff(rtPrior(), resp.PlannedState)
	if len(removed) != 1 || !removed[0].RawEquals(rtRoute(true, rtDefault)) ||
		len(added) != 1 || !added[0].RawEquals(rtRoute(true, retarget)) {
		t.Errorf("a retargeted route: removed %#v, added %#v; want only the default route swapped", removed, added)
	}
}
