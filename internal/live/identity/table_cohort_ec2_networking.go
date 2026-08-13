// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableEc2Networking is the ec2-networking cohort's slice of [DefaultTable]:
// the identity rows the ec2-networking ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableEc2Networking = buildTable(
	// ---- Registry-ratified (#40, #44, #65): fifth batch, EC2 networking
	// ---- beyond the core -------------------------------------------------
	//
	// Same pipeline as the earlier batches: every row started as a
	// tools/row-gen proposal from live/registry.json's EC2 section (114
	// types), cross-checked against the AWS provider's documented import
	// behaviour (its Argument Reference, Attribute Reference and Import
	// section, fetched from the provider's own website/docs/r/ source at
	// the pinned v6.58.0 tag) and, for the composites row-gen itself would
	// not paste, against live/import-grammar.json's scraped evidence
	// directly. Scope is issue #65's own wording: VPC endpoints (plus
	// services and connection notifications), the Transit Gateway family,
	// VPN (customer gateways, connections, gateways), Client VPN, IPAM,
	// prefix lists, VPC peering, DHCP options, network ACLs (plus rules),
	// flow logs, and aws_nat_gateway. Cohort estate:
	// live/e2e/estates/ec2-networking.
	//
	// Sixteen of the forty-three ratified rows are corrections or
	// promotions the same shape as the storage and Route53/CloudFront
	// batches' own: row-gen filed them "needs hand separator" or
	// "evidence-only" because the CloudFormation registry's primaryIdentifier
	// evidence does not carry a join character or an argument name, but the
	// provider's own Import section supplies one built entirely from
	// arguments already in configuration (or, for aws_vpc_dhcp_options_association,
	// a *simpler* identity than the registry's composite evidence
	// suggested) — see each TypeIdentity's own comment below.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_ec2_client_vpn_authorization_rule: row-gen proposed
	//     server-assigned via the registry's opaque "Id", but the provider's
	//     own Import section shows the real identity is a composite the
	//     registry evidence never saw — endpoint ID and target network CIDR,
	//     both configuration arguments, comma-separated — and *conditionally*
	//     a third segment (a client-chosen group name) whenever the rule sets
	//     access_group_id instead of authorize_all_groups. Building the
	//     two-segment form always would silently address the wrong rule (or
	//     none) for every group-scoped authorization, and this table's
	//     [Component] vocabulary has no way to write "an extra segment,
	//     conditioned on which of two mutually exclusive arguments is set" —
	//     the same "conditional-literal component this table's vocabulary
	//     does not have" shape the RDS batch's aws_db_proxy_target rejection
	//     already established, here conditioning a whole trailing segment
	//     rather than one literal within it. Untaggable either way (no tags
	//     argument in the provider's schema).
	//   - aws_ec2_client_vpn_network_association: row-gen proposed
	//     server-assigned via the registry's opaque "Id". The provider's
	//     Import section confirms a composite (endpoint ID, association ID,
	//     comma-separated) — but the second half is the association's own
	//     server-minted cvpn-assoc-… id, not the subnet_id or
	//     client_vpn_endpoint_id arguments configuration actually holds, so
	//     nothing reconstructs it. Untaggable (no tags argument).
	//   - aws_ec2_transit_gateway_multicast_domain_association,
	//     aws_ec2_transit_gateway_multicast_group_member and
	//     aws_ec2_transit_gateway_multicast_group_source: row-gen filed all
	//     three "needs hand separator" against the registry's composite
	//     primaryIdentifier. The provider's own docs at the pinned v6.58.0
	//     tag carry no "## Import" section at all for any of the three
	//     (confirmed by fetching each page directly) — not merely silent on
	//     the separator the way row-gen's own rule expects, genuinely
	//     unimportable — and none carries a tags argument, so there is no
	//     marker path either.
	//   - aws_network_acl_association: row-gen proposed server-assigned via
	//     the registry's opaque "AssociationId", confirmed against the
	//     provider's Import section (a bare aclassoc-… id). Untaggable (no
	//     tags argument in the provider's schema — a subnet-to-ACL
	//     association is not a taggable EC2 object), and the import id is a
	//     third, independently server-minted token: neither subnet_id nor
	//     network_acl_id, the association's own two configuration arguments,
	//     appears in it.
	//   - aws_vpc_endpoint_connection_notification: row-gen proposed
	//     server-assigned via the registry's opaque
	//     "VPCEndpointConnectionNotificationId", confirmed against the
	//     provider's Import section (a bare vpce-nfn-… id, unrelated to the
	//     service_id/vpc_endpoint_id arguments). Untaggable (no tags
	//     argument in the provider's schema).
	//   - aws_vpc_endpoint_service_allowed_principal: row-gen found no
	//     mapped CloudFormation type to fold this onto at all
	//     (AWS::EC2::VPCEndpointServicePermissions has no corresponding
	//     Terraform resource in live/mapping.json) and proposed nothing.
	//     Independently checked directly against the provider's docs: no
	//     "## Import" section, and its two arguments
	//     (vpc_endpoint_service_id, principal_arn) are both required with no
	//     tags argument between them — the same unimportable-and-untaggable
	//     shape as the three Transit Gateway multicast types above.
	//   - aws_vpc_ipam_pool_cidr_allocation: row-gen filed this "needs hand
	//     separator" against the registry's composite primaryIdentifier. The
	//     provider's own Import section confirms a real composite (the
	//     allocation's own id, an underscore, the pool id) — but the first
	//     half is IPAM's own server-minted ipam-pool-alloc-… id, not the
	//     ipam_pool_id or cidr arguments configuration holds, so it is not
	//     parent-derived. It is taggable, so the marker path is the only
	//     candidate left, and that needs an enumeration mechanism: this
	//     type's CloudFormation mapping (AWS::EC2::IPAMAllocation) is
	//     `handlers.list: true` but with a required `IpamPoolId` list input
	//     (live/registry.json), which internal/live/registry.Roster's own
	//     `listable` map (roster.go) only ever sets true for zero required
	//     input — the same "list-free" bar every ratified marker row in this
	//     batch clears and this one alone in the IPAM family does not — and
	//     the provider ships it no native list resource either
	//     (live/survey-full.json). No admission path reaches a specific live
	//     allocation.
	//   - aws_vpn_gateway_attachment: row-gen filed this "needs hand
	//     separator" against the registry's composite primaryIdentifier
	//     (AttachmentType, VpcId). The provider's own Import section is
	//     unambiguous and stronger than a mere gap: "You cannot import this
	//     resource." Untaggable either way (vpc_id and vpn_gateway_id are
	//     its only two arguments).
	//   - aws_vpn_gateway_route_propagation and aws_vpn_connection_route:
	//     row-gen proposed the first server-assigned via the registry's
	//     opaque "Id" and filed the second "needs hand separator". Neither
	//     of the provider's own docs pages carries a "## Import" section at
	//     all, and neither type's schema carries a tags argument (each is a
	//     two- or three-argument join resource: vpn_gateway_id/route_table_id
	//     for the first, destination_cidr_block/vpn_connection_id for the
	//     second) — the same unimportable-and-untaggable shape as the
	//     Transit Gateway multicast rejections above.
	//
	// Out of scope, never proposed by row-gen and not named in issue #65's
	// own wording: aws_vpn_concentrator (a distinct, newer EC2 networking
	// feature — VPN Concentrators are not "VPN gateways" or "VPN
	// connections" in the classic sense the issue names) and every EC2
	// sub-service this batch's own scope excludes (Carrier Gateway, Local
	// Gateway/Outposts, Network Insights, Traffic Mirroring, Verified
	// Access, Route Server, VPC Block Public Access, VPC Encryption
	// Control) — none of these were part of "EC2 networking beyond the
	// core" as issue #65 phrased it, and none is this batch's to decide.

	serverAssigned("aws_vpc_endpoint",
		"EC2 assigns the VPC endpoint's own id (vpce-…) at create time; the service_name and vpc_id arguments configure it but do not identify it.",
		"vpce-ID", "id"),
	serverAssigned("aws_vpc_endpoint_service",
		"EC2 assigns the VPC endpoint service's own id (vpce-svc-…) at create time; no argument in configuration reconstructs it.",
		"vpce-svc-ID", "id"),
	// The five VPC endpoint children below carry no registry evidence of
	// their own (row-gen's fold notes point back at aws_vpc_endpoint with no
	// argument names attached) but are real, commonly used Terraform
	// resources with their own documented Import sections, all built
	// entirely from the parent endpoint's id plus, for three of the five,
	// one more configuration argument. aws_vpc_endpoint_policy and
	// aws_vpc_endpoint_private_dns are named singleton children — at most
	// one per endpoint, imported by the endpoint's own id verbatim, the same
	// shape as aws_s3_bucket_policy and aws_cloudfront_monitoring_subscription
	// above — and the other three are parent/child composites joined by a
	// slash.
	TypeIdentity{
		Type:          "aws_vpc_endpoint_policy",
		Components:    []Component{attr("vpc_endpoint_id")},
		ImportSyntax:  "VPCENDPOINTID",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type:          "aws_vpc_endpoint_private_dns",
		Components:    []Component{attr("vpc_endpoint_id")},
		ImportSyntax:  "VPCENDPOINTID",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_vpc_endpoint_route_table_association",
		Components: []Component{
			attr("vpc_endpoint_id"),
			sep("/"),
			attr("route_table_id"),
		},
		ImportSyntax:  "VPCENDPOINTID/ROUTETABLEID",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_vpc_endpoint_subnet_association",
		Components: []Component{
			attr("vpc_endpoint_id"),
			sep("/"),
			attr("subnet_id"),
		},
		ImportSyntax:  "VPCENDPOINTID/SUBNETID",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_vpc_endpoint_security_group_association",
		Components: []Component{
			attr("vpc_endpoint_id"),
			sep("/"),
			attr("security_group_id"),
		},
		ImportSyntax:  "VPCENDPOINTID/SECURITYGROUPID",
		IdentityAttrs: nil,
	},

	// Transit Gateway family. Every attachment type (Connect, peering, VPC)
	// shares the same tgw-attach-… id space as the core plumbing; every
	// table type (policy, route) shares tgw-rtb-…; each is server-assigned
	// and taggable, confirmed against live/survey-full.json.
	serverAssigned("aws_ec2_transit_gateway",
		"EC2 assigns the transit gateway's own id (tgw-…) at create time; the amazon_side_asn argument configures it but does not identify it.",
		"tgw-ID", "id"),
	serverAssigned("aws_ec2_transit_gateway_connect",
		"EC2 assigns the Connect attachment's own id (tgw-attach-…) at create time, the same attachment-id space every other Transit Gateway attachment in this batch shares; the transport_transit_gateway_attachment_id argument names what it rides on, not itself.",
		"tgw-attach-ID", "id"),
	serverAssigned("aws_ec2_transit_gateway_connect_peer",
		"EC2 assigns the Connect peer's own id (tgw-connect-peer-…) at create time; the transit_gateway_attachment_id argument and the peer address arguments configure it but do not identify it.",
		"tgw-connect-peer-ID", "id"),
	serverAssigned("aws_ec2_transit_gateway_metering_policy",
		"EC2 assigns the metering policy's own id (tgw-mp-…) at create time; the transit_gateway_id argument names the parent gateway but not the policy.",
		"tgw-mp-ID", "id", "transit_gateway_metering_policy_id"),
	serverAssigned("aws_ec2_transit_gateway_multicast_domain",
		"EC2 assigns the multicast domain's own id (tgw-mcast-domain-…) at create time; the transit_gateway_id argument names the parent gateway but not the domain.",
		"tgw-mcast-domain-ID", "id"),
	serverAssigned("aws_ec2_transit_gateway_peering_attachment",
		"EC2 assigns the peering attachment's own id (tgw-attach-…) at create time; the peer_transit_gateway_id, peer_region and peer_account_id arguments configure the peering but do not identify the attachment.",
		"tgw-attach-ID", "id"),
	serverAssigned("aws_ec2_transit_gateway_policy_table",
		"EC2 assigns the policy table's own id (tgw-rtb-…) at create time; the transit_gateway_id argument names the parent gateway but not the table.",
		"tgw-rtb-ID", "id"),
	serverAssigned("aws_ec2_transit_gateway_route_table",
		"EC2 assigns the route table's own id (tgw-rtb-…) at create time; the transit_gateway_id argument names the parent gateway but not the table.",
		"tgw-rtb-ID", "id"),
	serverAssigned("aws_ec2_transit_gateway_vpc_attachment",
		"EC2 assigns the VPC attachment's own id (tgw-attach-…) at create time; the transit_gateway_id, vpc_id and subnet_ids arguments configure it but do not identify it.",
		"tgw-attach-ID", "id"),

	// The Transit Gateway sub-resources below are all parent-derived
	// composites row-gen itself filed "needs hand separator" (registry
	// primaryIdentifier with no join character in any schema); each
	// provider Import section supplies the separator directly, confirmed by
	// fetching the page at the pinned v6.58.0 tag. All four route-table
	// composites (association, propagation, and the route itself) join the
	// route table's own id to the attachment's or destination's with an
	// underscore; the policy table association and the metering policy
	// entry are their own two shapes.
	TypeIdentity{
		// terraform import aws_ec2_transit_gateway_metering_policy_entry.example
		// tgw-policy-12345678,100 — both halves are required arguments.
		Type: "aws_ec2_transit_gateway_metering_policy_entry",
		Components: []Component{
			attr("transit_gateway_metering_policy_id"),
			sep(","),
			attr("policy_rule_number"),
		},
		ImportSyntax:  "TRANSITGATEWAYMETERINGPOLICYID,POLICYRULENUMBER",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// terraform import aws_ec2_transit_gateway_policy_table_association.example
		// tgw-rtb-12345678_tgw-attach-87654321 — both halves are required
		// arguments (transit_gateway_policy_table_id, transit_gateway_attachment_id).
		Type: "aws_ec2_transit_gateway_policy_table_association",
		Components: []Component{
			attr("transit_gateway_policy_table_id"),
			sep("_"),
			attr("transit_gateway_attachment_id"),
		},
		ImportSyntax:  "TRANSITGATEWAYPOLICYTABLEID_TRANSITGATEWAYATTACHMENTID",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// terraform import aws_ec2_transit_gateway_route.example
		// tgw-rtb-12345678_0.0.0.0/0 — both halves are required arguments
		// (transit_gateway_route_table_id, destination_cidr_block), the same
		// shape as aws_route above.
		Type: "aws_ec2_transit_gateway_route",
		Components: []Component{
			attr("transit_gateway_route_table_id"),
			sep("_"),
			attr("destination_cidr_block"),
		},
		ImportSyntax:  "TRANSITGATEWAYROUTETABLEID_DESTINATIONCIDRBLOCK",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// terraform import aws_ec2_transit_gateway_route_table_association.example
		// tgw-rtb-12345678_tgw-attach-87654321 — both halves are required
		// arguments.
		Type: "aws_ec2_transit_gateway_route_table_association",
		Components: []Component{
			attr("transit_gateway_route_table_id"),
			sep("_"),
			attr("transit_gateway_attachment_id"),
		},
		ImportSyntax:  "TRANSITGATEWAYROUTETABLEID_TRANSITGATEWAYATTACHMENTID",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// terraform import aws_ec2_transit_gateway_route_table_propagation.example
		// tgw-rtb-12345678_tgw-attach-87654321 — both halves are required
		// arguments, same shape as the association above.
		Type: "aws_ec2_transit_gateway_route_table_propagation",
		Components: []Component{
			attr("transit_gateway_route_table_id"),
			sep("_"),
			attr("transit_gateway_attachment_id"),
		},
		ImportSyntax:  "TRANSITGATEWAYROUTETABLEID_TRANSITGATEWAYATTACHMENTID",
		IdentityAttrs: nil,
	},

	// VPN: customer gateways, connections, gateways (issue #65's own
	// wording).
	serverAssigned("aws_customer_gateway",
		"EC2 assigns the customer gateway's own id (cgw-…) at create time; the ip_address, bgp_asn and type arguments describe the on-prem device but do not identify EC2's own record of it.",
		"cgw-ID", "id"),
	serverAssigned("aws_vpn_connection",
		"EC2 assigns the VPN connection's own id (vpn-…) at create time; the customer_gateway_id argument and the vpn_gateway_id/transit_gateway_id arguments name what it connects but not the connection itself.",
		"vpn-ID", "id"),
	serverAssigned("aws_vpn_gateway",
		"EC2 assigns the VPN gateway's own id (vgw-…) at create time; the amazon_side_asn argument configures it but does not identify it.",
		"vgw-ID", "id"),

	// Client VPN.
	serverAssigned("aws_ec2_client_vpn_endpoint",
		"EC2 assigns the Client VPN endpoint's own id (cvpn-endpoint-…) at create time; the client_cidr_block and authentication_options arguments configure it but do not identify it.",
		"cvpn-endpoint-ID", "id"),
	TypeIdentity{
		// terraform import aws_ec2_client_vpn_route.example
		// cvpn-endpoint-1234567890abcdef,subnet-9876543210fedcba,10.1.0.0/24
		// — always three segments, all required arguments, unlike
		// aws_ec2_client_vpn_authorization_rule's conditional third segment
		// rejected above.
		Type: "aws_ec2_client_vpn_route",
		Components: []Component{
			attr("client_vpn_endpoint_id"),
			sep(","),
			attr("target_vpc_subnet_id"),
			sep(","),
			attr("destination_cidr_block"),
		},
		ImportSyntax:  "CLIENTVPNENDPOINTID,TARGETVPCSUBNETID,DESTINATIONCIDRBLOCK",
		IdentityAttrs: nil,
	},

	// IPAM family.
	serverAssigned("aws_vpc_ipam",
		"EC2 assigns the IPAM's own id (ipam-…) at create time; no argument in configuration reconstructs it.",
		"ipam-ID", "id"),
	serverAssigned("aws_vpc_ipam_pool",
		"EC2 assigns the pool's own id (ipam-pool-…) at create time; the ipam_scope_id, locale and address_family arguments configure it but do not identify it.",
		"ipam-pool-ID", "id"),
	TypeIdentity{
		// row-gen filed this "needs hand separator" against the registry's
		// composite primaryIdentifier (IpamPoolId, IpamPoolCidrId — a
		// server-minted second id row-gen's own evidence could not have
		// resolved). The provider's own Import section gives a different,
		// simpler grammar entirely: "<cidr>_<ipam-pool-id>", both
		// reconstructible from configuration. cidr is Optional (it
		// conflicts with netmask_length, the alternative way to request an
		// allocation size without naming the block), so a pool CIDR
		// configured only with netmask_length correctly fails resolution
		// naming "cidr" rather than guessing — the same "identity argument
		// not set" honesty aws_lb_target_group_attachment's optional port
		// already established above, not the silently-wrong shape
		// aws_ec2_client_vpn_authorization_rule was rejected for.
		Type: "aws_vpc_ipam_pool_cidr",
		Components: []Component{
			attr("cidr"),
			sep("_"),
			attr("ipam_pool_id"),
		},
		ImportSyntax:  "CIDR_IPAMPOOLID",
		IdentityAttrs: nil,
	},
	// aws_vpc_ipam_pool_cidr_allocation is rejected — see the "Rejected"
	// note above.
	serverAssigned("aws_vpc_ipam_resource_discovery",
		"EC2 assigns the resource discovery's own id (ipam-res-disco-…) at create time; no argument in configuration reconstructs it.",
		"ipam-res-disco-ID", "id"),
	serverAssigned("aws_vpc_ipam_resource_discovery_association",
		"EC2 assigns the association's own id (ipam-res-disco-assoc-…) at create time; the ipam_id and ipam_resource_discovery_id arguments name what it associates but not the association itself.",
		"ipam-res-disco-assoc-ID", "id"),
	serverAssigned("aws_vpc_ipam_scope",
		"EC2 assigns the scope's own id (ipam-scope-…) at create time; the ipam_id argument names the parent IPAM but not the scope.",
		"ipam-scope-ID", "id"),

	// Prefix lists.
	serverAssigned("aws_ec2_managed_prefix_list",
		"EC2 assigns the prefix list's own id (pl-…) at create time; the name argument is client-chosen but is not the import identity.",
		"pl-ID", "id"),
	TypeIdentity{
		// row-gen filed this evidence-only (a property-child fold of
		// AWS::EC2::PrefixList with no registry primaryIdentifier of its
		// own). The provider's own Import section gives a complete,
		// documented grammar: prefix_list_id and cidr, both required
		// arguments, comma-separated (terraform import
		// aws_ec2_managed_prefix_list_entry.default
		// pl-0570a1d2d725c16be,10.0.3.0/24). Concrete whenever the parent
		// prefix list above is.
		Type: "aws_ec2_managed_prefix_list_entry",
		Components: []Component{
			attr("prefix_list_id"),
			sep(","),
			attr("cidr"),
		},
		ImportSyntax:  "PREFIXLISTID,CIDR",
		IdentityAttrs: nil,
	},

	// VPC peering.
	serverAssigned("aws_vpc_peering_connection",
		"EC2 assigns the peering connection's own id (pcx-…) at create time; the peer_vpc_id and vpc_id arguments name the two sides but not the connection itself.",
		"pcx-ID", "id"),

	// DHCP options.
	serverAssigned("aws_vpc_dhcp_options",
		"EC2 assigns the DHCP options set's own id (dopt-…) at create time; the domain_name and domain_name_servers arguments configure it but do not identify it.",
		"dopt-ID", "id"),
	TypeIdentity{
		// row-gen filed this "needs hand separator" against the registry's
		// composite primaryIdentifier (DhcpOptionsId, VpcId). The provider's
		// own Import section shows a simpler identity than the registry
		// evidence suggested: "import DHCP associations using the VPC ID
		// associated with the options" — vpc_id alone, because a VPC has at
		// most one DHCP options association at a time (terraform import
		// aws_vpc_dhcp_options_association.imported vpc-0f001273ec18911b1).
		// The same correction shape as aws_backup_framework's in the storage
		// batch: the registry's composite evidence oversold the real
		// grammar.
		Type:          "aws_vpc_dhcp_options_association",
		Components:    []Component{attr("vpc_id")},
		ImportSyntax:  "VPCID",
		IdentityAttrs: nil,
	},

	// Network ACLs, plus rules.
	serverAssigned("aws_network_acl",
		"EC2 assigns the network ACL's own id (acl-…) at create time; the vpc_id argument names the parent VPC but not the ACL.",
		"acl-ID", "id"),
	TypeIdentity{
		// row-gen filed this evidence-only (no registry primaryIdentifier:
		// AWS::EC2::NetworkAclEntry embeds directly in its parent's
		// registry entry). The provider's own Import section gives a
		// complete, documented grammar built from four required arguments,
		// colon-separated (terraform import aws_network_acl_rule.my_rule
		// acl-7aaabd18:100:tcp:false). egress defaults to false in the
		// provider's schema but is not resolved from that default here — a
		// rule that omits it fails resolution naming "egress" rather than
		// guessing, the same honest-optional shape as
		// aws_lb_target_group_attachment's port and aws_vpc_ipam_pool_cidr's
		// cidr above.
		Type: "aws_network_acl_rule",
		Components: []Component{
			attr("network_acl_id"),
			sep(":"),
			attr("rule_number"),
			sep(":"),
			attr("protocol"),
			sep(":"),
			attr("egress"),
		},
		ImportSyntax:  "NETWORKACLID:RULENUMBER:PROTOCOL:EGRESS",
		IdentityAttrs: nil,
	},

	// Flow logs.
	serverAssigned("aws_flow_log",
		"EC2 assigns the flow log's own id (fl-…) at create time; the resource_id and log_destination arguments configure it but do not identify it.",
		"fl-ID", "id"),

	// aws_nat_gateway: this batch's headline type (issue #65's own
	// next-batch suggestion, and the roster's former unadmitted-type
	// example — see live/e2e/limits/unadmitted-type/main.tf and
	// live/LIMITATIONS.md for the swap). live/survey-full.json shows it
	// ships a real identity schema and a native list resource in the pinned
	// provider release, so its identity path is sound independent of any
	// one emulator: the old blocked-emulator note (floci returns an empty
	// NatGatewayAddresses list, so the provider's own subnet_id read fails
	// and every plan proposes replacement) is a floci gap, not an identity
	// gap, the same standing the messaging and RDS batches' own emulator
	// caveats already established — see
	// live/e2e/estates/ec2-networking/README.md.
	serverAssigned("aws_nat_gateway",
		"EC2 assigns the NAT gateway's own id (nat-…) at create time; the subnet_id and allocation_id arguments configure it but do not identify it.",
		"nat-ID", "id"),
	TypeIdentity{
		// row-gen filed this evidence-only (a property-child fold of
		// AWS::EC2::NatGateway with no registry primaryIdentifier of its
		// own). The provider's own Import section gives a complete grammar:
		// nat_gateway_id and allocation_id, both required arguments,
		// comma-separated (terraform import
		// aws_nat_gateway_eip_association.example
		// nat-1234567890abcdef1,eipalloc-1234567890abcdef1) — allocation_id
		// names an aws_eip, already admitted above via list-plus-content
		// match. Concrete whenever the parent NAT gateway above is.
		Type: "aws_nat_gateway_eip_association",
		Components: []Component{
			attr("nat_gateway_id"),
			sep(","),
			attr("allocation_id"),
		},
		ImportSyntax:  "NATGATEWAYID,ALLOCATIONID",
		IdentityAttrs: nil,
	},
)

func init() { registerCohortTable(identityTableEc2Networking) }
