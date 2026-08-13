// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableNetworkingAdvanced is the networking-advanced cohort's slice of [DefaultTable]:
// the identity rows the networking-advanced ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableNetworkingAdvanced = buildTable(
	// ---- Registry-ratified (#40, #44, #65): sixth batch, advanced
	// ---- networking (issue #65) --------------------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented Argument Reference, Attribute Reference
	// and Import section (fetched from the provider's own website/docs/r/
	// source, and from internal/service/... source where the doc text alone
	// left an argument name or the exact import-ID mechanics ambiguous, both
	// off the pinned v6.59.0 tag). Scope: Network Firewall, NetworkManager
	// (Cloud WAN is not a distinct CFN service — its constructs are
	// NetworkManager's own CoreNetwork family, already covered here), VPC
	// Lattice, Global Accelerator, and Route53 Recovery Readiness. App Mesh
	// is a deprecated service, skipped entirely per this batch's own recipe.
	// Cohort estate: live/e2e/estates/networking-advanced.
	//
	// VPC Lattice is this batch's major catch: row-gen's flat
	// serverAssigned() template read the CFN registry's primaryIdentifier
	// field name ("Arn") for eleven of its fourteen types and proposed an
	// ARN-based identity for each — but the provider's own documented Import
	// sections disagree for nine of them. VPC Lattice imports almost its
	// whole family by the short, provider-minted id (svc-…, sn-…, tg-…,
	// rgw-…, rcfg-…, dv-…, snra-…, rft-…) that the SDK resources' own
	// d.SetId() calls set, not the arn attribute the same resources also
	// export from a separate d.Set(names.AttrARN, …) call. Two more
	// (listener, listener_rule) are genuinely server-assigned but via a
	// composite of server-normalized ids (SERVICE-ID/LISTENER-ID[/RULE-ID]),
	// confirmed against internal/service/vpclattice/listener.go's own
	// listenerCreateResourceID; neither the service_identifier argument
	// (which accepts either an ARN or an id) nor any other configuration
	// value reconstructs the normalized form, so no IdentityAttrs are
	// claimed for either. NetworkManager's four association/registration
	// types (customer_gateway_association, link_association,
	// prefix_list_association, transit_gateway_registration) were row-gen
	// "needs hand separator" refusals — composite registry
	// primaryIdentifiers with no separator in any schema row-gen reads —
	// resolved here from the provider's own documented Import sections and
	// internal/service/networkmanager/*.go's own *CreateResourceID
	// functions, the same resolution issue #65 anticipated ("largely
	// resolvable from live/import-grammar.json's separators"). NetworkManager's
	// device, site and link were the same refusal, resolved the other way:
	// the provider imports each by its own arn attribute
	// (@Testing(importStateIdAttribute="arn") in the provider source), not by
	// the registry's GlobalNetworkId+DeviceId composite at all. Route53
	// Recovery Readiness's four types were row-gen evidence-only (its
	// snake_cased argument guesses unconfirmed by any evidence row-gen
	// reads) — confirmed correct here against both the provider's documented
	// Import sections and internal/service/route53recoveryreadiness/*.go's
	// own d.Get calls, so all four are promoted to ratified rows.
	//
	// Deferred, out of this batch's named scope: NetworkManager's
	// core_network_policy_attachment (row-gen: "(property-child of
	// AWS::NetworkManager::CoreNetwork) [evidence-only]" — a parent-derived
	// fold row-gen does not propose and this batch does not construct by
	// hand) and the CFN-unmodeled
	// aws_networkmanager_attachment_routing_policy_label residue row (already
	// terminal "reasoned-none" in live/mapping.json's sweep overlay — CFN
	// models RoutingPolicyLabel as a property on several NetworkManager
	// attachment types, the TF resource attaches generically across all of
	// them, and nothing in this batch changes that).

	// NetworkFirewall: row-gen's server-assigned "ARN" proposals check out
	// unchanged for firewall, firewall_policy and rule_group — the
	// provider's documented Import sections all use the type's own arn
	// verbatim, and each Attribute Reference states plainly that id and arn
	// are the same ARN. tls_inspection_configuration and
	// vpc_endpoint_association are newer plugin-framework resources; the
	// former's Identity Schema requires arn and its Import section matches,
	// but the pinned provider does not document a separate id equal to it,
	// so only arn is claimed. vpc_endpoint_association's own identity
	// attribute is not a bare "arn" but the type-specific
	// vpc_endpoint_association_arn the schema actually exports — row-gen's
	// generic "VPCENDPOINTASSOCIATIONARN" name happened to match the real
	// argument this time. logging_configuration is the client-named
	// exception row-gen already had right: its sole argument, firewall_arn,
	// is exactly what the provider's Import section documents.
	serverAssigned("aws_networkfirewall_firewall",
		"NetworkFirewall assigns the firewall's ARN at create time; no argument reconstructs it.",
		"ARN", "arn", "id"),
	serverAssigned("aws_networkfirewall_firewall_policy",
		"NetworkFirewall assigns the firewall policy's ARN at create time; no argument reconstructs it.",
		"ARN", "arn", "id"),
	TypeIdentity{
		Type:          "aws_networkfirewall_logging_configuration",
		Components:    []Component{attr("firewall_arn")},
		ImportSyntax:  "FIREWALL_ARN",
		IdentityAttrs: []string{"firewall_arn"},
	},
	serverAssigned("aws_networkfirewall_rule_group",
		"NetworkFirewall assigns the rule group's ARN at create time; no argument reconstructs it.",
		"ARN", "arn", "id"),
	serverAssigned("aws_networkfirewall_tls_inspection_configuration",
		"NetworkFirewall assigns the TLS inspection configuration's ARN at create time; no argument reconstructs it.",
		"ARN", "arn"),
	serverAssigned("aws_networkfirewall_vpc_endpoint_association",
		"NetworkFirewall assigns the VPC endpoint association's ARN at create time; no argument reconstructs it.",
		"VPCENDPOINTASSOCIATIONARN", "vpc_endpoint_association_arn"),

	// NetworkManager's association/registration quartet: row-gen filed all
	// four "needs hand separator" (composite registry primaryIdentifier, no
	// separator in any schema it reads). Each provider resource's own
	// *CreateResourceID function (internal/service/networkmanager/*.go)
	// settles both the separator and the argument order, and every argument
	// named is a real, required schema field confirmed against the same
	// source: a comma joins global_network_id and customer_gateway_arn for
	// the customer gateway association, global_network_id, link_id and
	// device_id for the link association, global_network_id and
	// transit_gateway_arn for the transit gateway registration, and
	// core_network_id and prefix_list_arn for the prefix list association
	// (this last one also carries a Terraform-native
	// @IdentityAttribute("core_network_id")/@IdentityAttribute("prefix_list_arn")
	// pair in the provider source, the strongest possible confirmation).
	// None of the four exports a single attribute equal to its own
	// comma-joined id — same standard of care as aws_lb_target_group_attachment
	// above: hand out nothing rather than something that happens to look
	// right.
	TypeIdentity{
		Type: "aws_networkmanager_customer_gateway_association",
		Components: []Component{
			attr("global_network_id"),
			sep(","),
			attr("customer_gateway_arn"),
		},
		ImportSyntax:  "GLOBALNETWORKID,CUSTOMERGATEWAYARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_networkmanager_link_association",
		Components: []Component{
			attr("global_network_id"),
			sep(","),
			attr("link_id"),
			sep(","),
			attr("device_id"),
		},
		ImportSyntax:  "GLOBALNETWORKID,LINKID,DEVICEID",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_networkmanager_prefix_list_association",
		Components: []Component{
			attr("core_network_id"),
			sep(","),
			attr("prefix_list_arn"),
		},
		ImportSyntax:  "CORENETWORKID,PREFIXLISTARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_networkmanager_transit_gateway_registration",
		Components: []Component{
			attr("global_network_id"),
			sep(","),
			attr("transit_gateway_arn"),
		},
		ImportSyntax:  "GLOBALNETWORKID,TRANSITGATEWAYARN",
		IdentityAttrs: nil,
	},

	// NetworkManager's device, site and link: also row-gen "needs hand
	// separator" refusals over the registry's GlobalNetworkId+DeviceId (etc.)
	// composite primaryIdentifier — but the provider does not import these
	// by that composite at all. Each resource's own StateContext importer
	// (internal/service/networkmanager/{device,site,link}.go) parses the
	// type's arn attribute, and each is decorated
	// @Testing(importStateIdAttribute="arn") in the provider source: the real
	// identity is the single arn value, embedding the global network id and
	// the server-minted device/site/link id as ARN path segments no
	// configuration argument reconstructs (global_network_id is a
	// create-time argument, but the device/site/link id half is never
	// client-supplied).
	serverAssigned("aws_networkmanager_device",
		"NetworkManager assigns the device's ARN at create time; the global_network_id argument names its parent but does not, alone, reconstruct the ARN.",
		"ARN", "arn"),
	serverAssigned("aws_networkmanager_site",
		"NetworkManager assigns the site's ARN at create time; the global_network_id argument names its parent but does not, alone, reconstruct the ARN.",
		"ARN", "arn"),
	serverAssigned("aws_networkmanager_link",
		"NetworkManager assigns the link's ARN at create time; the global_network_id argument names its parent but does not, alone, reconstruct the ARN.",
		"ARN", "arn"),

	// NetworkManager's remaining nine row-gen server-assigned proposals check
	// out unchanged: each provider Import section documents the type's own
	// short, provider-minted id (attachment-…, connect-peer-…, peering-…,
	// core-network-… or global-network-…) verbatim, matching row-gen's
	// registry-derived primaryIdentifier field name exactly (these nine
	// registry primaryIdentifiers were never "Arn" in the first place, unlike
	// VPC Lattice's, so the naive template's field-name guess and the
	// provider's real import syntax happened to agree). Each type also
	// exports a longer …Arn attribute (CoreNetworkArn, ResourceArn, and
	// similar) that is a different string from the id and is not claimed
	// here.
	serverAssigned("aws_networkmanager_connect_attachment",
		"NetworkManager assigns the attachment ID at create time; no argument reconstructs it.",
		"ATTACHMENTID", "id"),
	serverAssigned("aws_networkmanager_connect_peer",
		"NetworkManager assigns the connect peer ID at create time; no argument reconstructs it.",
		"CONNECTPEERID", "id"),
	serverAssigned("aws_networkmanager_core_network",
		"NetworkManager assigns the core network ID at create time; the global_network_id argument names its parent but does not identify the core network itself.",
		"CORENETWORKID", "id"),
	serverAssigned("aws_networkmanager_dx_gateway_attachment",
		"NetworkManager assigns the attachment ID at create time; no argument reconstructs it.",
		"ATTACHMENTID", "id"),
	serverAssigned("aws_networkmanager_global_network",
		"NetworkManager assigns the global network ID at create time; it has no client-named argument at all.",
		"ID", "id"),
	serverAssigned("aws_networkmanager_site_to_site_vpn_attachment",
		"NetworkManager assigns the attachment ID at create time; no argument reconstructs it.",
		"ATTACHMENTID", "id"),
	serverAssigned("aws_networkmanager_transit_gateway_peering",
		"NetworkManager assigns the peering ID at create time; no argument reconstructs it.",
		"PEERINGID", "id"),
	serverAssigned("aws_networkmanager_transit_gateway_route_table_attachment",
		"NetworkManager assigns the attachment ID at create time; no argument reconstructs it.",
		"ATTACHMENTID", "id"),
	serverAssigned("aws_networkmanager_vpc_attachment",
		"NetworkManager assigns the attachment ID at create time; the vpc_arn argument names the attached VPC but does not identify the attachment itself.",
		"ATTACHMENTID", "id"),

	// Global Accelerator: all four row-gen server-assigned proposals check
	// out unchanged — every type's provider Identity Schema requires arn,
	// every documented Import section uses it, and every type's own d.SetId
	// (or, for the newer plugin-framework cross_account_attachment,
	// data.ID = data.AttachmentARN) sets id equal to that same arn.
	serverAssigned("aws_globalaccelerator_accelerator",
		"Global Accelerator assigns the accelerator's ARN at create time; no argument reconstructs it.",
		"ARN", "arn", "id"),
	serverAssigned("aws_globalaccelerator_cross_account_attachment",
		"Global Accelerator assigns the cross-account attachment's ARN at create time; the name argument does not identify it.",
		"ARN", "arn", "id"),
	serverAssigned("aws_globalaccelerator_endpoint_group",
		"Global Accelerator assigns the endpoint group's ARN at create time; no argument reconstructs it.",
		"ARN", "arn", "id"),
	serverAssigned("aws_globalaccelerator_listener",
		"Global Accelerator assigns the listener's ARN at create time; no argument reconstructs it.",
		"ARN", "arn", "id"),

	// VPC Lattice, the corrected majority: row-gen proposed server-assigned
	// via the registry's opaque "Arn" for each of these — right that no
	// argument reconstructs the identity, wrong about which exported
	// attribute it is. Every provider Import section below documents the
	// type's own short, provider-minted id (confirmed against each
	// resource's d.SetId(...Id) or, for the newer plugin-framework
	// resources, their tfsdk:"id" field under framework.WithImportByID /
	// WithImportByIdentity) — not the arn attribute the same resources also
	// export from a separate d.Set(names.AttrARN, …) call.
	serverAssigned("aws_vpclattice_access_log_subscription",
		"VpcLattice assigns the access log subscription's ID at create time; no argument reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_vpclattice_domain_verification",
		"VpcLattice assigns the domain verification's ID at create time; the domain_name argument names the target but does not identify this resource.",
		"ID", "id"),
	serverAssigned("aws_vpclattice_resource_configuration",
		"VpcLattice assigns the resource configuration's ID at create time; no argument reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_vpclattice_resource_gateway",
		"VpcLattice assigns the resource gateway's ID at create time; no argument reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_vpclattice_service",
		"VpcLattice assigns the service's ID at create time; the name argument names it but does not identify it — a deleted-and-recreated service of the same name has a different ID.",
		"ID", "id"),
	serverAssigned("aws_vpclattice_service_network",
		"VpcLattice assigns the service network's ID at create time; the name argument names it but does not identify it.",
		"ID", "id"),
	serverAssigned("aws_vpclattice_service_network_resource_association",
		"VpcLattice assigns the association's ID at create time; no argument reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_vpclattice_service_network_service_association",
		"VpcLattice assigns the association's ID at create time; no argument reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_vpclattice_service_network_vpc_association",
		"VpcLattice assigns the association's ID at create time; no argument reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_vpclattice_target_group",
		"VpcLattice assigns the target group's ID at create time; the name argument names it but does not identify it.",
		"ID", "id"),

	// aws_vpclattice_listener and aws_vpclattice_listener_rule: also
	// server-assigned, but via a composite the provider's own
	// *CreateResourceID functions (internal/service/vpclattice/{listener,
	// listener_rule}.go) build from server-normalized ids, not from
	// configuration verbatim — a listener's id is SERVICE-ID/LISTENER-ID and
	// a rule's is SERVICE-ID/LISTENER-ID/RULE-ID, joined with "/". The
	// service_identifier (and listener_identifier) arguments accept either
	// an ARN or an id, but the identity string always embeds the
	// server-normalized id form (output.ServiceId, not whatever the argument
	// held), so no configuration argument reconstructs it and no
	// IdentityAttrs are claimed.
	serverAssigned("aws_vpclattice_listener",
		"VpcLattice assigns the listener's own ID at create time and builds its identity as SERVICE-ID/LISTENER-ID from the server-normalized service ID, not from the service_identifier argument as typed.",
		"SERVICEID/LISTENERID"),
	serverAssigned("aws_vpclattice_listener_rule",
		"VpcLattice assigns the rule's own ID at create time and builds its identity as SERVICE-ID/LISTENER-ID/RULE-ID from the server-normalized service and listener IDs, not from the service_identifier/listener_identifier arguments as typed.",
		"SERVICEID/LISTENERID/RULEID"),

	// aws_vpclattice_resource_policy: row-gen proposed this correctly the
	// first time — client-named via live/import-grammar.json's scraped
	// argument, confirmed against the provider's own source
	// (internal/service/vpclattice/resource_policy.go: d.SetId(resourceARN)
	// where resourceARN := d.Get("resource_arn")) and its documented Import
	// section.
	TypeIdentity{
		Type:          "aws_vpclattice_resource_policy",
		Components:    []Component{attr("resource_arn")},
		ImportSyntax:  "RESOURCE_ARN",
		IdentityAttrs: []string{"resource_arn"},
	},

	// aws_vpclattice_auth_policy: row-gen marked this evidence-only (its
	// resource_identifier argument name was a GUESSED snake-case of the CFN
	// property, backed by neither a provider identity schema nor
	// live/import-grammar.json). Confirmed correct against the provider's
	// own source (internal/service/vpclattice/auth_policy.go:
	// d.SetId(resourceID) where resourceID := d.Get("resource_identifier"))
	// and its documented Import section, and promoted to a ratified row.
	TypeIdentity{
		Type:          "aws_vpclattice_auth_policy",
		Components:    []Component{attr("resource_identifier")},
		ImportSyntax:  "RESOURCE_IDENTIFIER",
		IdentityAttrs: []string{"resource_identifier"},
	},

	// Route53 Recovery Readiness: row-gen marked all four types
	// evidence-only (each argument name — cell_name, readiness_check_name,
	// recovery_group_name, resource_set_name — was a GUESSED snake-case of
	// the CFN property, backed by neither a provider identity schema, the
	// carve seed, nor a live/import-grammar.json separator row, because
	// these types' import syntax is undecorated plain text rather than a
	// composed-of-arguments grammar row row-gen's scraper structures).
	// Confirmed correct against the provider's own source
	// (internal/service/route53recoveryreadiness/*.go: each resource's
	// d.SetId sets the id to exactly the argument row-gen guessed) and each
	// type's documented Import section, and promoted to ratified rows.
	TypeIdentity{
		Type:          "aws_route53recoveryreadiness_cell",
		Components:    []Component{attr("cell_name")},
		ImportSyntax:  "CELL_NAME",
		IdentityAttrs: []string{"cell_name"},
	},
	TypeIdentity{
		Type:          "aws_route53recoveryreadiness_readiness_check",
		Components:    []Component{attr("readiness_check_name")},
		ImportSyntax:  "READINESS_CHECK_NAME",
		IdentityAttrs: []string{"readiness_check_name"},
	},
	TypeIdentity{
		Type:          "aws_route53recoveryreadiness_recovery_group",
		Components:    []Component{attr("recovery_group_name")},
		ImportSyntax:  "RECOVERY_GROUP_NAME",
		IdentityAttrs: []string{"recovery_group_name"},
	},
	TypeIdentity{
		Type:          "aws_route53recoveryreadiness_resource_set",
		Components:    []Component{attr("resource_set_name")},
		ImportSyntax:  "RESOURCE_SET_NAME",
		IdentityAttrs: []string{"resource_set_name"},
	},
)

func init() { registerCohortTable(identityTableNetworkingAdvanced) }
