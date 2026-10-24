// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"go/ast"
	"strings"
)

// Composite and association ids (GitHub issue #2204, the maintainer's ruling
// of 2026-10-23 on #2201). An azurerm association resource's id is not an
// ARM id of its own: commonids.NewCompositeResourceID(first, second).ID()
// joins its two linked objects' ids with "|" (go-azure-helpers
// commonids/composite_resource_id.go: fmt.Sprintf("%s|%s", ...)), and the
// resource does d.SetId(id.ID()). The subnet association types are the same
// idea with one part: they SetId the subnet's own id.
//
// A type is derived only when everything below is checked, type by type,
// against azurerm's source at the pin, and stays refused (class unmapped,
// with the reason) the moment any check fails:
//
//   - the importer's ParseCompositeResourceID call names the two id types
//     the rule expects, in order (the importer is the provider's statement of
//     which id it accepts, so a rule written for a different pair is caught);
//   - each part's id type states its own segments, and the rule accounts for
//     every one of them: a static segment is a fixed literal, a value
//     segment is read from exactly the argument the rule names or is the
//     provider's subscription, and an argument holding a whole id stands for
//     the leading run of segments the rule says, which has to be the id of
//     the azurerm type the rule names where there is one;
//   - every argument is one the type's schema lets a configuration set;
//   - every whole-id argument carries a validator that parses that very id
//     type (ValidateFunc), so the value is an id of the part's shape;
//   - the resource's file builds the id with a composite id, which is what
//     Create hands to SetId.
//
// The rules themselves are read from Create in each resource's file (the
// line numbers are azurerm 5.10.0's); the checks above are what keep them
// honest if the pin moves.

// partElem is one piece of a composite part, in id order. Static segments of
// the part's id type are consumed implicitly, as fixed text.
type partElem struct {
	// Whole: Arg holds an ARM id standing for the leading N segments still
	// to be consumed (N 0 means every remaining segment). Type, where set,
	// is the azurerm type whose own id template is exactly those segments.
	Whole string
	N     int
	// Validator is the text the argument's ValidateFunc has to contain.
	Validator string
	// Seg is the parser's name for the value segment this element supplies,
	// from Arg or, with Sub, from the provider's subscription.
	Seg string
	Arg string
	Sub bool
}

type partRule struct {
	// IDType is the part's id type, as azurerm's code names it.
	IDType string
	Elems  []partElem
}

type compositeRule struct {
	Parts []partRule
	// Create is where azurerm builds the id, for the reader.
	Create string
}

func sub(seg string) partElem      { return partElem{Seg: seg, Sub: true} }
func val(seg, arg string) partElem { return partElem{Seg: seg, Arg: arg} }
func whole(typ, arg, validator string, n int) partElem {
	return partElem{Whole: typ, Arg: arg, Validator: validator, N: n}
}

// nicIPConfig is commonids.NetworkInterfaceIPConfigurationId built the way
// the network interface association resources build it: the interface's id
// from network_interface_id, then the configuration name.
var nicIPConfig = partRule{IDType: "NetworkInterfaceIPConfigurationId", Elems: []partElem{
	whole("azurerm_network_interface", "network_interface_id", "NetworkInterfaceID", 8),
	val("ipConfigurationName", "ip_configuration_name"),
}}

var compositeRules = map[string]compositeRule{
	// network_interface_application_gateway_backend_address_pool_association_resource.go
	// lines 114-120, 153: ipConfigId from the interface id and
	// ip_configuration_name, the pool's id parsed with parse.ApplicationGatewayBackendAddressPoolID.
	"azurerm_network_interface_application_gateway_backend_address_pool_association": {
		Create: "network/network_interface_application_gateway_backend_address_pool_association_resource.go:114-153",
		Parts: []partRule{nicIPConfig, {IDType: "ApplicationGatewayBackendAddressPoolId", Elems: []partElem{
			whole("", "backend_address_pool_id", "BackendAddressPoolID", 0),
		}}},
	},
	// ..._application_security_group_association_resource.go lines 70-126.
	"azurerm_network_interface_application_security_group_association": {
		Create: "network/network_interface_application_security_group_association_resource.go:70-126",
		Parts: []partRule{
			{IDType: "NetworkInterfaceId", Elems: []partElem{whole("azurerm_network_interface", "network_interface_id", "NetworkInterfaceID", 0)}},
			{IDType: "ApplicationSecurityGroupId", Elems: []partElem{whole("azurerm_application_security_group", "application_security_group_id", "ApplicationSecurityGroupID", 0)}},
		},
	},
	// ..._backend_address_pool_association_resource.go lines 71-148.
	"azurerm_network_interface_backend_address_pool_association": {
		Create: "network/network_interface_backend_address_pool_association_resource.go:71-148",
		Parts: []partRule{nicIPConfig, {IDType: "BackendAddressPoolId", Elems: []partElem{
			whole("azurerm_lb_backend_address_pool", "backend_address_pool_id", "BackendAddressPoolID", 0),
		}}},
	},
	// ..._nat_rule_association_resource.go lines 72-152.
	"azurerm_network_interface_nat_rule_association": {
		Create: "network/network_interface_nat_rule_association_resource.go:72-152",
		Parts: []partRule{nicIPConfig, {IDType: "InboundNatRuleId", Elems: []partElem{
			whole("azurerm_lb_nat_rule", "nat_rule_id", "InboundNatRuleID", 0),
		}}},
	},
	// ..._security_group_association_resource.go lines 64-111.
	"azurerm_network_interface_security_group_association": {
		Create: "network/network_interface_security_group_association_resource.go:64-111",
		Parts: []partRule{
			{IDType: "NetworkInterfaceId", Elems: []partElem{whole("azurerm_network_interface", "network_interface_id", "NetworkInterfaceID", 0)}},
			{IDType: "NetworkSecurityGroupId", Elems: []partElem{whole("azurerm_network_security_group", "network_security_group_id", "NetworkSecurityGroupID", 0)}},
		},
	},
	// nat_gateway_public_ip_association_resource.go lines 65-141.
	"azurerm_nat_gateway_public_ip_association": {
		Create: "network/nat_gateway_public_ip_association_resource.go:65-141",
		Parts: []partRule{
			{IDType: "NatGatewayId", Elems: []partElem{whole("azurerm_nat_gateway", "nat_gateway_id", "NatGatewayID", 0)}},
			{IDType: "PublicIPAddressId", Elems: []partElem{whole("azurerm_public_ip", "public_ip_address_id", "PublicIPAddressID", 0)}},
		},
	},
	// nat_gateway_public_ip_prefix_association_resource.go lines 65-140.
	"azurerm_nat_gateway_public_ip_prefix_association": {
		Create: "network/nat_gateway_public_ip_prefix_association_resource.go:65-140",
		Parts: []partRule{
			{IDType: "NatGatewayId", Elems: []partElem{whole("azurerm_nat_gateway", "nat_gateway_id", "NatGatewayID", 0)}},
			{IDType: "PublicIPPrefixId", Elems: []partElem{whole("azurerm_public_ip_prefix", "public_ip_prefix_id", "PublicIPPrefixID", 0)}},
		},
	},
	// web/app_service_certificate_binding_resource.go lines 92-152.
	"azurerm_app_service_certificate_binding": {
		Create: "web/app_service_certificate_binding_resource.go:92-152",
		Parts: []partRule{
			{IDType: "HostNameBindingId", Elems: []partElem{whole("", "hostname_binding_id", "HostNameBindingID", 0)}},
			{IDType: "CertificateId", Elems: []partElem{whole("", "certificate_id", "CertificateID", 0)}},
		},
	},
	// automation/automation_job_schedule_resource.go lines 109-175: the
	// schedule's and the runbook's ids, built from the client's subscription
	// and the four arguments below, joined as tfID.
	"azurerm_automation_job_schedule": {
		Create: "automation/automation_job_schedule_resource.go:109-175",
		Parts: []partRule{
			{IDType: "ScheduleId", Elems: []partElem{sub("subscriptionId"), val("resourceGroupName", "resource_group_name"), val("automationAccountName", "automation_account_name"), val("scheduleName", "schedule_name")}},
			{IDType: "RunbookId", Elems: []partElem{sub("subscriptionId"), val("resourceGroupName", "resource_group_name"), val("automationAccountName", "automation_account_name"), val("runbookName", "runbook_name")}},
		},
	},
	// communication/communication_service_email_domain_association_resource.go
	// lines 74-152.
	"azurerm_communication_service_email_domain_association": {
		Create: "communication/communication_service_email_domain_association_resource.go:74-152",
		Parts: []partRule{
			{IDType: "CommunicationServiceId", Elems: []partElem{whole("azurerm_communication_service", "communication_service_id", "CommunicationServiceID", 0)}},
			{IDType: "DomainId", Elems: []partElem{whole("", "email_service_domain_id", "DomainID", 0)}},
		},
	},
	// postgres/postgresql_flexible_server_virtual_endpoint_resource.go lines
	// 117-151: one endpoint id on each server, both named by `name`.
	"azurerm_postgresql_flexible_server_virtual_endpoint": {
		Create: "postgres/postgresql_flexible_server_virtual_endpoint_resource.go:117-151",
		Parts: []partRule{
			{IDType: "VirtualEndpointId", Elems: []partElem{whole("azurerm_postgresql_flexible_server", "source_server_id", "FlexibleServerID", 8), val("virtualEndpointName", "name")}},
			{IDType: "VirtualEndpointId", Elems: []partElem{whole("azurerm_postgresql_flexible_server", "replica_server_id", "FlexibleServerID", 8), val("virtualEndpointName", "name")}},
		},
	},
	// The subnet associations: d.SetId(subnetId.ID()), one part (lines
	// 65-111, 66-152, 66-152).
	"azurerm_subnet_nat_gateway_association": {
		Create: "network/subnet_nat_gateway_association_resource.go:65-111",
		Parts:  []partRule{{IDType: "SubnetId", Elems: []partElem{whole("azurerm_subnet", "subnet_id", "SubnetID", 0)}}},
	},
	"azurerm_subnet_network_security_group_association": {
		Create: "network/subnet_network_security_group_association_resource.go:66-152",
		Parts:  []partRule{{IDType: "SubnetId", Elems: []partElem{whole("azurerm_subnet", "subnet_id", "SubnetID", 0)}}},
	},
	"azurerm_subnet_route_table_association": {
		Create: "network/subnet_route_table_association_resource.go:66-152",
		Parts:  []partRule{{IDType: "SubnetId", Elems: []partElem{whole("azurerm_subnet", "subnet_id", "SubnetID", 0)}}},
	},
}

// compositePart is one recorded part of a composite id.
type compositePart struct {
	IDType   string `json:"id_type"`
	Package  string `json:"package"`
	Template string `json:"template"`
	// Arguments are the configuration arguments the part is built from,
	// with the validator each whole-id one declares.
	Arguments []string          `json:"arguments"`
	Validator map[string]string `json:"validators,omitempty"`
}

// compositeInfo is the entry's record of a composite derivation. Join is
// "" for a single-part type (the subnet associations), "|" otherwise.
type compositeInfo struct {
	Join   string          `json:"join,omitempty"`
	Create string          `json:"create"`
	Parts  []compositePart `json:"parts"`
}

// compositeCalls returns the id types a registration's importer (or
// IDValidationFunc) hands to commonids.ParseCompositeResourceID, in order.
func (l *loader) compositeCalls(r registration) []typeKey {
	var out []typeKey
	seen := map[*ast.FuncDecl]bool{}
	var walk func(p *pkg, f *ast.File, n ast.Node, depth int)
	walk = func(p *pkg, f *ast.File, n ast.Node, depth int) {
		if n == nil || p == nil || len(out) > 0 {
			return
		}
		ast.Inspect(n, func(n ast.Node) bool {
			if len(out) > 0 {
				return false
			}
			switch x := n.(type) {
			case *ast.CallExpr:
				if se, ok := x.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == "ParseCompositeResourceID" && len(x.Args) == 3 {
					for _, a := range x.Args[1:] {
						u, ok := a.(*ast.UnaryExpr)
						if !ok {
							return true
						}
						cl, ok := u.X.(*ast.CompositeLit)
						if !ok {
							return true
						}
						tp, name := l.typeRef(p, f, cl.Type)
						if tp == nil {
							return true
						}
						out = append(out, typeKey{tp, name})
					}
					if len(out) != 2 {
						out = nil
					}
					return false
				}
				if fp, ff, fd := l.funcRef(p, f, x.Fun); fd != nil && !seen[fd] && depth < 3 && l.followable(fp.path) {
					seen[fd] = true
					walk(fp, ff, fd.Body, depth+1)
				}
			case *ast.Ident:
				if fd := p.funcs[x.Name]; fd != nil && !seen[fd] && depth < 3 {
					seen[fd] = true
					walk(p, p.fileOf[fd], fd.Body, depth+1)
				}
			}
			return true
		})
	}
	walk(r.Pkg, r.File, r.Expr, 0)
	return out
}

// argValidators is each argument's ValidateFunc as exprText renders it, the
// first declaration of a key in the file winning, as [argFormsIn] reads it.
func argValidators(f *ast.File) map[string]string {
	out := map[string]string{}
	if f == nil {
		return out
	}
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := stringLit(kv.Key)
		if !ok {
			return true
		}
		if _, done := out[key]; done {
			return true
		}
		if cl, ok := kv.Value.(*ast.CompositeLit); ok {
			for _, e := range cl.Elts {
				inner, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if id, ok := inner.Key.(*ast.Ident); ok && (id.Name == "ValidateFunc" || id.Name == "ValidateDiagFunc") {
					out[key] = exprText(inner.Value)
					break
				}
			}
		}
		return true
	})
	return out
}

// fileBuildsCompositeID reports whether the resource's file builds a
// composite id (commonids.NewCompositeResourceID, or the literal), which is
// what its Create hands to SetId, or for the single-part types a SetId of a
// parsed id's ID().
func fileBuildsID(f *ast.File, single bool) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if !single && x.Sel.Name == "NewCompositeResourceID" {
				found = true
			}
		case *ast.CompositeLit:
			if !single {
				if ix, ok := x.Type.(*ast.IndexListExpr); ok {
					if se, ok := ix.X.(*ast.SelectorExpr); ok && se.Sel.Name == "CompositeResourceID" {
						found = true
					}
				}
			}
		case *ast.CallExpr:
			if se, ok := x.Fun.(*ast.SelectorExpr); ok && single && se.Sel.Name == "SetId" && len(x.Args) == 1 {
				if c, ok := x.Args[0].(*ast.CallExpr); ok {
					if s2, ok := c.Fun.(*ast.SelectorExpr); ok && s2.Sel.Name == "ID" {
						found = true
					}
				}
			}
		}
		return true
	})
	return found
}

// classifyComposite derives (or refuses, with the reason) a type that has a
// compositeRule. ok false means the type has no rule.
func (l *loader) classifyComposite(name string, ts typeSchema, r registration, src *idSource, parentTypes func([]segment) []string) (e entry, ok bool) {
	rule, has := compositeRules[name]
	if !has {
		return entry{}, false
	}
	e = entry{Type: name, Via: r.Via, Source: src, IdentitySchema: ts.Identity}
	refuse := func(why string) (entry, bool) {
		e.Class, e.Reason = classUnmapped, "composite id not derived: "+why
		e.Components, e.Composite = nil, nil
		return e, true
	}
	if ts.Identity != nil {
		return refuse("the type serves a resource identity schema, which the composite derivation does not replace")
	}
	// The id types the importer parses.
	var typs []typeKey
	if len(rule.Parts) == 1 {
		if src == nil || src.TypeName != rule.Parts[0].IDType {
			return refuse(fmt.Sprintf("its importer parses %v, not %s", srcName(src), rule.Parts[0].IDType))
		}
		p := l.load(src.Package)
		if p == nil {
			return refuse("the id type's package is not in the checkout")
		}
		typs = []typeKey{{p, src.TypeName}}
	} else {
		typs = l.compositeCalls(r)
		if len(typs) != len(rule.Parts) {
			return refuse("its importer does not parse a composite id of two ids")
		}
		for i, k := range typs {
			if k.name != rule.Parts[i].IDType {
				return refuse(fmt.Sprintf("its importer's part %d is %s, the rule was written for %s", i+1, k.name, rule.Parts[i].IDType))
			}
		}
	}
	if !fileBuildsID(r.File, len(rule.Parts) == 1) {
		return refuse("its file never builds the id from a composite id (Create's SetId)")
	}
	validators := argValidators(r.File)
	info := &compositeInfo{Create: rule.Create}
	if len(rule.Parts) > 1 {
		info.Join = "|"
	}
	var comps []component
	var tmpls []string
	for i, pr := range rule.Parts {
		k := typs[i]
		psrc, problem := l.segmentsOf(k.p, k.name)
		if problem != "" {
			return refuse(fmt.Sprintf("part %s: %s", pr.IDType, problem))
		}
		pcomps, problem := alignPart(psrc.Segments, pr.Elems, ts, validators, parentTypes)
		if problem != "" {
			return refuse(fmt.Sprintf("part %s: %s", pr.IDType, problem))
		}
		if i > 0 {
			comps = append(comps, component{Literal: "|"})
		}
		for _, c := range pcomps {
			if c.Literal != "" && len(c.Attrs) == 0 && c.Cloud == "" {
				comps = appendLiteral(comps, c.Literal)
				continue
			}
			comps = append(comps, c)
		}
		part := compositePart{IDType: k.name, Package: k.p.path, Template: template(psrc.Segments), Validator: map[string]string{}}
		for _, el := range pr.Elems {
			if el.Arg != "" {
				part.Arguments = append(part.Arguments, el.Arg)
			}
			if el.Whole != "" || el.Validator != "" {
				part.Validator[el.Arg] = validators[el.Arg]
			}
		}
		info.Parts = append(info.Parts, part)
		tmpls = append(tmpls, part.Template)
	}
	e.Class = classComposite
	e.Components = comps
	e.Composite = info
	e.Template = strings.Join(tmpls, "|")
	return e, true
}

func srcName(s *idSource) string {
	if s == nil {
		return "no id type"
	}
	return s.TypeName
}

// alignPart accounts for every segment of one part's id type.
func alignPart(segs []segment, elems []partElem, ts typeSchema, validators map[string]string, parentTypes func([]segment) []string) ([]component, string) {
	var out []component
	lit := ""
	flush := func() {
		if lit != "" {
			out = appendLiteral(out, lit)
			lit = ""
		}
	}
	i := 0
	skipStatics := func() {
		for i < len(segs) && (segs[i].Kind == segStatic || segs[i].Kind == segProvider) {
			lit += "/" + segs[i].Value
			i++
		}
	}
	for _, el := range elems {
		if el.Arg != "" && !ts.Attrs[el.Arg].configurable() {
			return nil, fmt.Sprintf("argument %s is not one the schema lets a configuration set", el.Arg)
		}
		if el.Whole != "" || el.Validator != "" {
			if el.Whole == "" && el.Validator == "" {
				continue
			}
			if v := validators[el.Arg]; !strings.Contains(v, el.Validator) {
				return nil, fmt.Sprintf("argument %s is validated by %q, not by a %s validator", el.Arg, v, el.Validator)
			}
			n := el.N
			if n == 0 {
				n = len(segs) - i
			}
			if n <= 0 || i+n > len(segs) {
				return nil, fmt.Sprintf("%s covers %d segments of an id of %d", el.Arg, n, len(segs)-i)
			}
			if i != 0 {
				return nil, fmt.Sprintf("%s holds a whole id after the part has begun", el.Arg)
			}
			if el.Whole != "" && parentTypes != nil {
				found := false
				for _, t := range parentTypes(segs[:i+n]) {
					if t == el.Whole {
						found = true
					}
				}
				if !found {
					return nil, fmt.Sprintf("the first %d segments are not the id of %s", n, el.Whole)
				}
			}
			flush()
			out = append(out, component{Attrs: []string{el.Arg}, ARMID: true, Segment: segs[i+n-1].Name, Rule: ruleComposite})
			i += n
			continue
		}
		skipStatics()
		if i >= len(segs) {
			return nil, fmt.Sprintf("no segment left for %s", el.Seg)
		}
		s := segs[i]
		if s.Name != el.Seg {
			return nil, fmt.Sprintf("segment %q is where the rule expects %q", s.Name, el.Seg)
		}
		lit += "/"
		flush()
		switch {
		case el.Sub:
			if s.Kind != segSubscription {
				return nil, fmt.Sprintf("segment %s is not a subscription segment", s.Name)
			}
			out = append(out, component{Cloud: cloudSubscription, Segment: s.Name, Rule: ruleSubscription})
		default:
			if s.Kind != segResourceGroup && s.Kind != segUser {
				return nil, fmt.Sprintf("segment %s is a %s segment, not one a name argument supplies", s.Name, s.Kind)
			}
			out = append(out, component{Attrs: []string{el.Arg}, Segment: s.Name, Rule: ruleComposite})
		}
		i++
	}
	skipStatics()
	flush()
	if i != len(segs) {
		return nil, fmt.Sprintf("segment %q is supplied by nothing", segs[i].Name)
	}
	return out, ""
}
