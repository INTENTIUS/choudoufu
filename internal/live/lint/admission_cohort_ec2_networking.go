// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesEc2Networking is the ec2-networking cohort's slice of [admittedTypesV0]:
// the types the ec2-networking ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesEc2Networking = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): fifth batch, EC2 networking
	// ---- beyond the core (VPC endpoints, Transit Gateway, VPN, Client VPN,
	// ---- IPAM, prefix lists, VPC peering, DHCP options, network ACLs, flow
	// ---- logs, NAT gateway; issue #65's own next-batch suggestion). Same
	// ---- tools/row-gen pipeline and verification standard as the batches
	// ---- above, cross-checked against the AWS provider's documented import
	// ---- behaviour (its Argument Reference, Attribute Reference and Import
	// ---- section, fetched from the provider's own website/docs/r/ source at
	// ---- the pinned v6.58.0 tag) and against live/import-grammar.json's
	// ---- scraped evidence directly — see internal/live/identity/table.go
	// ---- for the per-type evidence and for the rows this batch rejected.
	// ---- Cohort estate: live/e2e/estates/ec2-networking.
	//
	// aws_nat_gateway is this batch's headline type: the repo's
	// long-standing canonical unadmitted-type example
	// (live/e2e/limits/unadmitted-type, live/LIMITATIONS.md) swaps to
	// aws_cloudwatch_event_rule in the same change — see that fixture's own
	// comment for why.
	"aws_vpc_endpoint":                                 {},
	"aws_vpc_endpoint_service":                         {},
	"aws_vpc_endpoint_policy":                          {},
	"aws_vpc_endpoint_private_dns":                     {},
	"aws_vpc_endpoint_route_table_association":         {},
	"aws_vpc_endpoint_subnet_association":              {},
	"aws_vpc_endpoint_security_group_association":      {},
	"aws_ec2_transit_gateway":                          {},
	"aws_ec2_transit_gateway_connect":                  {},
	"aws_ec2_transit_gateway_connect_peer":             {},
	"aws_ec2_transit_gateway_metering_policy":          {},
	"aws_ec2_transit_gateway_metering_policy_entry":    {},
	"aws_ec2_transit_gateway_multicast_domain":         {},
	"aws_ec2_transit_gateway_peering_attachment":       {},
	"aws_ec2_transit_gateway_policy_table":             {},
	"aws_ec2_transit_gateway_policy_table_association": {},
	"aws_ec2_transit_gateway_route":                    {},
	"aws_ec2_transit_gateway_route_table":              {},
	"aws_ec2_transit_gateway_route_table_association":  {},
	"aws_ec2_transit_gateway_route_table_propagation":  {},
	"aws_ec2_transit_gateway_vpc_attachment":           {},
	"aws_customer_gateway":                             {},
	"aws_vpn_connection":                               {},
	"aws_vpn_gateway":                                  {},
	"aws_ec2_client_vpn_endpoint":                      {},
	"aws_ec2_client_vpn_route":                         {},
	"aws_vpc_ipam":                                     {},
	"aws_vpc_ipam_pool":                                {},
	"aws_vpc_ipam_pool_cidr":                           {},
	"aws_vpc_ipam_resource_discovery":                  {},
	"aws_vpc_ipam_resource_discovery_association":      {},
	"aws_vpc_ipam_scope":                               {},
	"aws_ec2_managed_prefix_list":                      {},
	"aws_ec2_managed_prefix_list_entry":                {},
	"aws_vpc_peering_connection":                       {},
	"aws_vpc_dhcp_options":                             {},
	"aws_vpc_dhcp_options_association":                 {},
	"aws_network_acl":                                  {},
	"aws_network_acl_rule":                             {},
	"aws_flow_log":                                     {},
	"aws_nat_gateway":                                  {},
	"aws_nat_gateway_eip_association":                  {},
}

func init() { registerCohortAdmitted(admittedTypesEc2Networking) }
