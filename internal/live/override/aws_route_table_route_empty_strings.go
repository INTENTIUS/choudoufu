// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/providers"
)

// routeTableRouteAttrs are the attributes of an aws_route_table route
// element at provider 6.71.0. Every one is an optional TypeString in an
// SDKv2 set of objects, and the SDK's set writer stores the zero value ""
// for each one a route leaves unset, so state holds "" where configuration
// holds null. The provider's own code cannot tell the two apart: it reads
// each through ResourceData, which yields "" for both, and every expand,
// hash and update helper tests `v != ""`. The resource has no CustomizeDiff
// and reads no raw config.
var routeTableRouteAttrs = []string{
	"cidr_block",
	"destination_prefix_list_id",
	"ipv6_cidr_block",
	"carrier_gateway_id",
	"core_network_arn",
	"egress_only_gateway_id",
	"gateway_id",
	"local_gateway_id",
	"nat_gateway_id",
	"network_interface_id",
	"odb_network_arn",
	"transit_gateway_id",
	"vpc_endpoint_id",
	"vpc_peering_connection_id",
}

func routeTableRouteType() cty.Type {
	attrs := map[string]cty.Type{}
	for _, name := range routeTableRouteAttrs {
		attrs[name] = cty.String
	}
	return cty.Set(cty.Object(attrs))
}

// aws_route_table's inline route set is planned with configuration's nulls
// copied into every element, while state holds "" for them. When anything
// about the resource changes, each configured route then differs from its
// state twin by null against "", and the plan lists every route removed
// and re-added. Reproduced on real AWS with provider 6.71.0 on 2026-11-04
// (#2309, live/override-repro/route-table-17198).
var _ = register(Override{
	Name:             "aws-route-table-route-empty-strings",
	Provider:         awsProvider,
	Type:             "aws_route_table",
	Approach:         RewriteConfig,
	SDK:              SDKv2,
	Upstream:         "hashicorp/terraform-provider-aws#17198",
	Credits:          []Credit{{Login: "rajaie-sg", For: "reported the bug"}},
	StockAlternative: "Declaring routes as separate aws_route resources avoids the churn in stock OpenTofu, at the cost of restructuring the configuration; inline routes are common in modules users do not own.",
	Summary:          "Changing one inline route lists every route removed and re-added, because unset route fields are null in configuration and \"\" in state",
	ProvenOn:         "6.71.0",
	Requires: map[string]cty.Type{
		"route": routeTableRouteType(),
	},
	Plan: routeTableRouteEmptyStrings,
})

// routeTableRouteEmptyStrings is approach A. On an update it rewrites each
// configured route element in Config and ProposedNewState so that a null
// attribute becomes "", for the attributes the prior state shows the
// provider storing as "". An unchanged route is then identical to its state
// element and drops out of the diff; a changed or new route still differs
// in the values the user wrote. It reports a change only when the plan is
// not the prior state, since an unchanged resource plans as no change
// without it.
func routeTableRouteEmptyStrings(ctx context.Context, inner providers.Interface, req providers.PlanResourceChangeRequest) (providers.PlanResourceChangeResponse, bool) {
	if req.PriorState.IsNull() || req.Config.IsNull() || !req.PriorState.IsKnown() {
		return inner.PlanResourceChange(ctx, req), false
	}
	path := cty.GetAttrPath("route")
	prior, err := path.Apply(req.PriorState)
	if err != nil {
		return inner.PlanResourceChange(ctx, req), false
	}
	stored := storedAsEmpty(prior)
	if len(stored) == 0 {
		return inner.PlanResourceChange(ctx, req), false
	}
	cfg, ok1 := emptyNullStrings(req.Config, stored)
	prop, ok2 := emptyNullStrings(req.ProposedNewState, stored)
	if !ok1 && !ok2 {
		return inner.PlanResourceChange(ctx, req), false
	}
	req.Config, req.ProposedNewState = cfg, prop
	resp := inner.PlanResourceChange(ctx, req)
	return resp, !resp.PlannedState.RawEquals(req.PriorState)
}

// storedAsEmpty returns the route attributes every prior element holds as a
// known, non-null string: the ones the provider's own read wrote, which for
// an unset field is "". With no prior elements there is nothing to match and
// it returns nothing.
func storedAsEmpty(prior cty.Value) map[string]bool {
	if prior.IsNull() || prior.IsMarked() || !prior.IsWhollyKnown() || !prior.Type().IsSetType() || prior.LengthInt() == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, name := range routeTableRouteAttrs {
		out[name] = true
	}
	for it := prior.ElementIterator(); it.Next(); {
		_, e := it.Element()
		if e.IsNull() || !e.Type().IsObjectType() {
			return nil
		}
		for name := range out {
			if !e.Type().HasAttribute(name) {
				delete(out, name)
				continue
			}
			if v := e.GetAttr(name); v.IsNull() || v.Type() != cty.String {
				delete(out, name)
			}
		}
	}
	return out
}

// emptyNullStrings returns obj with its route set rebuilt so that, in each
// known element, a null attribute named in stored is "". Unknown elements
// and unknown or non-null attributes are left as they are. It reports
// whether anything changed.
func emptyNullStrings(obj cty.Value, stored map[string]bool) (cty.Value, bool) {
	if obj.IsNull() || !obj.IsKnown() || obj.IsMarked() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute("route") {
		return obj, false
	}
	routes := obj.GetAttr("route")
	if routes.IsNull() || !routes.IsKnown() || routes.IsMarked() || !routes.Type().IsSetType() || routes.LengthInt() == 0 {
		return obj, false
	}
	changed := false
	var elems []cty.Value
	for it := routes.ElementIterator(); it.Next(); {
		_, e := it.Element()
		if e.IsNull() || !e.IsKnown() || e.IsMarked() || !e.Type().IsObjectType() {
			elems = append(elems, e)
			continue
		}
		attrs := e.AsValueMap()
		for name := range stored {
			if v, ok := attrs[name]; ok && v.IsNull() && v.Type() == cty.String {
				attrs[name] = cty.StringVal("")
				changed = true
			}
		}
		elems = append(elems, cty.ObjectVal(attrs))
	}
	if !changed {
		return obj, false
	}
	out := obj.AsValueMap()
	out["route"] = cty.SetVal(elems)
	return cty.ObjectVal(out), true
}
