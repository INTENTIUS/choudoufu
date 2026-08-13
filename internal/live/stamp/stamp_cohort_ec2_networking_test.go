// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The ec2-networking cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableEc2Networking = []string{
	// Registry-ratified EC2 networking beyond the core batch (#40, #44,
	// #65): the twenty-six marker-path types, confirmed taggable against
	// live/survey-full.json's signal for each. See
	// live/e2e/estates/ec2-networking/README.md, "Untaggable types", for
	// this batch's sixteen untaggable rows, below.
	"aws_vpc_endpoint",
	"aws_vpc_endpoint_service",
	"aws_ec2_transit_gateway",
	"aws_ec2_transit_gateway_connect",
	"aws_ec2_transit_gateway_connect_peer",
	"aws_ec2_transit_gateway_metering_policy",
	"aws_ec2_transit_gateway_multicast_domain",
	"aws_ec2_transit_gateway_peering_attachment",
	"aws_ec2_transit_gateway_policy_table",
	"aws_ec2_transit_gateway_route_table",
	"aws_ec2_transit_gateway_vpc_attachment",
	"aws_customer_gateway",
	"aws_vpn_connection",
	"aws_vpn_gateway",
	"aws_ec2_client_vpn_endpoint",
	"aws_vpc_ipam",
	"aws_vpc_ipam_pool",
	"aws_vpc_ipam_resource_discovery",
	"aws_vpc_ipam_resource_discovery_association",
	"aws_vpc_ipam_scope",
	"aws_ec2_managed_prefix_list",
	"aws_vpc_peering_connection",
	"aws_vpc_dhcp_options",
	"aws_network_acl",
	"aws_flow_log",
	"aws_nat_gateway",
}

var untaggableEc2Networking = []string{
	// Registry-ratified EC2 networking beyond the core batch (#40, #44,
	// #65): sixteen parent-derived composites, none carrying a tags
	// argument (confirmed against live/survey-full.json for each) —
	// they do not need the marker path at all, since their identity is
	// built straight from configuration. See
	// live/e2e/estates/ec2-networking/README.md, "Untaggable types".
	"aws_ec2_managed_prefix_list_entry",
	"aws_ec2_transit_gateway_metering_policy_entry",
	"aws_ec2_transit_gateway_policy_table_association",
	"aws_ec2_transit_gateway_route",
	"aws_ec2_transit_gateway_route_table_association",
	"aws_ec2_transit_gateway_route_table_propagation",
	"aws_ec2_client_vpn_route",
	"aws_vpc_ipam_pool_cidr",
	"aws_vpc_dhcp_options_association",
	"aws_network_acl_rule",
	"aws_nat_gateway_eip_association",
	"aws_vpc_endpoint_policy",
	"aws_vpc_endpoint_private_dns",
	"aws_vpc_endpoint_route_table_association",
	"aws_vpc_endpoint_subnet_association",
	"aws_vpc_endpoint_security_group_association",
}

func init() {
	registerCohortStamp(taggableEc2Networking, untaggableEc2Networking, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified EC2 networking beyond the core batch (#40, #44,
			// #65). Marker-path types first, confirmed taggable against
			// live/survey-full.json's signal for each; the sixteen
			// parent-derived composites below carry no tags argument at all.
			"aws_vpc_endpoint":                                 taggedSchema("id", "vpc_id", "service_name"),
			"aws_vpc_endpoint_service":                         taggedSchema("id", "service_name"),
			"aws_ec2_transit_gateway":                          taggedSchema("id", "amazon_side_asn"),
			"aws_ec2_transit_gateway_connect":                  taggedSchema("id", "transit_gateway_id", "transport_attachment_id"),
			"aws_ec2_transit_gateway_connect_peer":             taggedSchema("id", "transit_gateway_attachment_id", "peer_address"),
			"aws_ec2_transit_gateway_metering_policy":          taggedSchema("id", "transit_gateway_id"),
			"aws_ec2_transit_gateway_multicast_domain":         taggedSchema("id", "transit_gateway_id"),
			"aws_ec2_transit_gateway_peering_attachment":       taggedSchema("id", "transit_gateway_id", "peer_transit_gateway_id"),
			"aws_ec2_transit_gateway_policy_table":             taggedSchema("id", "transit_gateway_id"),
			"aws_ec2_transit_gateway_route_table":              taggedSchema("id", "transit_gateway_id"),
			"aws_ec2_transit_gateway_vpc_attachment":           taggedSchema("id", "transit_gateway_id", "vpc_id"),
			"aws_customer_gateway":                             taggedSchema("id", "bgp_asn", "ip_address", "type"),
			"aws_vpn_connection":                               taggedSchema("id", "customer_gateway_id", "type"),
			"aws_vpn_gateway":                                  taggedSchema("id", "amazon_side_asn"),
			"aws_ec2_client_vpn_endpoint":                      taggedSchema("id", "server_certificate_arn", "client_cidr_block"),
			"aws_vpc_ipam":                                     taggedSchema("id"),
			"aws_vpc_ipam_pool":                                taggedSchema("id", "ipam_scope_id", "address_family"),
			"aws_vpc_ipam_resource_discovery":                  taggedSchema("id"),
			"aws_vpc_ipam_resource_discovery_association":      taggedSchema("id", "ipam_id", "ipam_resource_discovery_id"),
			"aws_vpc_ipam_scope":                               taggedSchema("id", "ipam_id"),
			"aws_ec2_managed_prefix_list":                      taggedSchema("id", "name", "address_family", "max_entries"),
			"aws_vpc_peering_connection":                       taggedSchema("id", "vpc_id", "peer_vpc_id"),
			"aws_vpc_dhcp_options":                             taggedSchema("id"),
			"aws_network_acl":                                  taggedSchema("id", "vpc_id"),
			"aws_flow_log":                                     taggedSchema("id", "vpc_id", "traffic_type"),
			"aws_nat_gateway":                                  taggedSchema("id", "subnet_id", "allocation_id"),
			"aws_ec2_managed_prefix_list_entry":                untaggedSchema("id", "prefix_list_id", "cidr"),
			"aws_ec2_transit_gateway_metering_policy_entry":    untaggedSchema("id", "transit_gateway_metering_policy_id", "policy_rule_number", "metered_account"),
			"aws_ec2_transit_gateway_policy_table_association": untaggedSchema("id", "transit_gateway_policy_table_id", "transit_gateway_attachment_id"),
			"aws_ec2_transit_gateway_route":                    untaggedSchema("id", "transit_gateway_route_table_id", "destination_cidr_block"),
			"aws_ec2_transit_gateway_route_table_association":  untaggedSchema("id", "transit_gateway_route_table_id", "transit_gateway_attachment_id"),
			"aws_ec2_transit_gateway_route_table_propagation":  untaggedSchema("id", "transit_gateway_route_table_id", "transit_gateway_attachment_id"),
			"aws_ec2_client_vpn_route":                         untaggedSchema("id", "client_vpn_endpoint_id", "target_vpc_subnet_id", "destination_cidr_block"),
			"aws_vpc_ipam_pool_cidr":                           untaggedSchema("id", "ipam_pool_id", "cidr"),
			"aws_vpc_dhcp_options_association":                 untaggedSchema("id", "vpc_id", "dhcp_options_id"),
			"aws_network_acl_rule":                             untaggedSchema("id", "network_acl_id", "rule_number", "protocol", "rule_action"),
			"aws_nat_gateway_eip_association":                  untaggedSchema("id", "nat_gateway_id", "allocation_id"),
			"aws_vpc_endpoint_policy":                          untaggedSchema("id", "vpc_endpoint_id", "policy"),
			"aws_vpc_endpoint_private_dns":                     untaggedSchema("id", "vpc_endpoint_id", "private_dns_enabled"),
			"aws_vpc_endpoint_route_table_association":         untaggedSchema("id", "vpc_endpoint_id", "route_table_id"),
			"aws_vpc_endpoint_subnet_association":              untaggedSchema("id", "vpc_endpoint_id", "subnet_id"),
			"aws_vpc_endpoint_security_group_association":      untaggedSchema("id", "vpc_endpoint_id", "security_group_id"),
		})
	})
}
