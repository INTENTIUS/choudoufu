// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The networking-advanced cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableNetworkingAdvanced = []string{
	// Registry-ratified advanced networking batch (#40, #44, issue
	// #65): Network Firewall, NetworkManager, VPC Lattice, Global
	// Accelerator and Route53 Recovery Readiness types with a
	// top-level tags argument in the pinned provider's own wire
	// schema. See live/e2e/estates/networking-advanced/README.md.
	"aws_networkfirewall_firewall",
	"aws_networkfirewall_firewall_policy",
	"aws_networkfirewall_rule_group",
	"aws_networkfirewall_tls_inspection_configuration",
	"aws_networkfirewall_vpc_endpoint_association",
	"aws_networkmanager_connect_attachment",
	"aws_networkmanager_connect_peer",
	"aws_networkmanager_core_network",
	"aws_networkmanager_device",
	"aws_networkmanager_dx_gateway_attachment",
	"aws_networkmanager_global_network",
	"aws_networkmanager_link",
	"aws_networkmanager_site",
	"aws_networkmanager_site_to_site_vpn_attachment",
	"aws_networkmanager_transit_gateway_peering",
	"aws_networkmanager_transit_gateway_route_table_attachment",
	"aws_networkmanager_vpc_attachment",
	"aws_globalaccelerator_accelerator",
	"aws_globalaccelerator_cross_account_attachment",
	"aws_vpclattice_access_log_subscription",
	"aws_vpclattice_domain_verification",
	"aws_vpclattice_listener",
	"aws_vpclattice_listener_rule",
	"aws_vpclattice_resource_configuration",
	"aws_vpclattice_resource_gateway",
	"aws_vpclattice_service",
	"aws_vpclattice_service_network",
	"aws_vpclattice_service_network_resource_association",
	"aws_vpclattice_service_network_service_association",
	"aws_vpclattice_service_network_vpc_association",
	"aws_vpclattice_target_group",
	"aws_route53recoveryreadiness_cell",
	"aws_route53recoveryreadiness_readiness_check",
	"aws_route53recoveryreadiness_recovery_group",
	"aws_route53recoveryreadiness_resource_set",
}

var untaggableNetworkingAdvanced = []string{
	// Registry-ratified advanced networking batch (#40, #44, issue
	// #65): nine types with no tags argument at all in the pinned
	// provider's own wire schema — logging_configuration and the four
	// NetworkManager association/registration types are all
	// parent-derived composites (the same untaggable shape as
	// aws_route above), and the two Global Accelerator and two VPC
	// Lattice rows carry no tags block in the provider's own Argument
	// Reference. See live/e2e/estates/networking-advanced/README.md,
	// "Untaggable types".
	"aws_networkfirewall_logging_configuration",
	"aws_networkmanager_customer_gateway_association",
	"aws_networkmanager_link_association",
	"aws_networkmanager_prefix_list_association",
	"aws_networkmanager_transit_gateway_registration",
	"aws_globalaccelerator_endpoint_group",
	"aws_globalaccelerator_listener",
	"aws_vpclattice_auth_policy",
	"aws_vpclattice_resource_policy",
}

func init() {
	registerCohortStamp(taggableNetworkingAdvanced, untaggableNetworkingAdvanced, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified advanced networking batch (#40, #44, issue
			// #65's ratification campaign). Taggable/untaggable per the real
			// provider's documented Argument Reference for each type: the
			// NetworkManager association/registration quartet and Network
			// Firewall's logging_configuration are parent-derived composites
			// with no tags argument at all, the same untagged shape as
			// aws_route above; Global Accelerator's endpoint_group/listener
			// and VPC Lattice's auth_policy/resource_policy carry no tags
			// block either.
			"aws_networkfirewall_firewall":                              taggedSchema("id", "arn", "name", "firewall_policy_arn", "vpc_id"),
			"aws_networkfirewall_firewall_policy":                       taggedSchema("id", "arn", "name"),
			"aws_networkfirewall_logging_configuration":                 untaggedSchema("id", "firewall_arn"),
			"aws_networkfirewall_rule_group":                            taggedSchema("id", "arn", "name", "capacity", "type"),
			"aws_networkfirewall_tls_inspection_configuration":          taggedSchema("id", "arn", "name"),
			"aws_networkfirewall_vpc_endpoint_association":              taggedSchema("id", "vpc_endpoint_association_arn", "firewall_arn", "vpc_id"),
			"aws_networkmanager_connect_attachment":                     taggedSchema("id", "arn", "core_network_id", "transport_attachment_id"),
			"aws_networkmanager_connect_peer":                           taggedSchema("id", "connect_attachment_id", "peer_address"),
			"aws_networkmanager_core_network":                           taggedSchema("id", "arn", "global_network_id"),
			"aws_networkmanager_customer_gateway_association":           untaggedSchema("global_network_id", "customer_gateway_arn"),
			"aws_networkmanager_device":                                 taggedSchema("id", "arn", "global_network_id"),
			"aws_networkmanager_dx_gateway_attachment":                  taggedSchema("id", "arn", "core_network_id", "direct_connect_gateway_arn"),
			"aws_networkmanager_global_network":                         taggedSchema("id", "arn"),
			"aws_networkmanager_link":                                   taggedSchema("id", "arn", "global_network_id", "site_id"),
			"aws_networkmanager_link_association":                       untaggedSchema("global_network_id", "link_id", "device_id"),
			"aws_networkmanager_prefix_list_association":                untaggedSchema("core_network_id", "prefix_list_arn"),
			"aws_networkmanager_site":                                   taggedSchema("id", "arn", "global_network_id"),
			"aws_networkmanager_site_to_site_vpn_attachment":            taggedSchema("id", "arn", "core_network_id", "vpn_connection_arn"),
			"aws_networkmanager_transit_gateway_peering":                taggedSchema("id", "arn", "core_network_id", "transit_gateway_arn"),
			"aws_networkmanager_transit_gateway_registration":           untaggedSchema("global_network_id", "transit_gateway_arn"),
			"aws_networkmanager_transit_gateway_route_table_attachment": taggedSchema("id", "arn", "peering_id", "transit_gateway_route_table_arn"),
			"aws_networkmanager_vpc_attachment":                         taggedSchema("id", "arn", "core_network_id", "vpc_arn"),
			"aws_globalaccelerator_accelerator":                         taggedSchema("id", "arn", "name"),
			"aws_globalaccelerator_cross_account_attachment":            taggedSchema("id", "arn", "name"),
			"aws_globalaccelerator_endpoint_group":                      untaggedSchema("id", "arn", "listener_arn"),
			"aws_globalaccelerator_listener":                            untaggedSchema("id", "arn", "accelerator_arn", "protocol"),
			"aws_vpclattice_access_log_subscription":                    taggedSchema("id", "arn", "resource_identifier", "destination_arn"),
			"aws_vpclattice_auth_policy":                                untaggedSchema("resource_identifier", "policy"),
			"aws_vpclattice_domain_verification":                        taggedSchema("id", "arn", "domain_name"),
			"aws_vpclattice_listener":                                   taggedSchema("id", "arn", "name", "service_identifier", "protocol"),
			"aws_vpclattice_listener_rule":                              taggedSchema("id", "arn", "name", "listener_identifier", "service_identifier", "priority"),
			"aws_vpclattice_resource_configuration":                     taggedSchema("id", "arn", "name"),
			"aws_vpclattice_resource_gateway":                           taggedSchema("id", "arn", "name", "vpc_id"),
			"aws_vpclattice_resource_policy":                            untaggedSchema("resource_arn", "policy"),
			"aws_vpclattice_service":                                    taggedSchema("id", "arn", "name"),
			"aws_vpclattice_service_network":                            taggedSchema("id", "arn", "name"),
			"aws_vpclattice_service_network_resource_association":       taggedSchema("id", "arn", "resource_configuration_identifier", "service_network_identifier"),
			"aws_vpclattice_service_network_service_association":        taggedSchema("id", "arn", "service_identifier", "service_network_identifier"),
			"aws_vpclattice_service_network_vpc_association":            taggedSchema("id", "arn", "service_network_identifier", "vpc_identifier"),
			"aws_vpclattice_target_group":                               taggedSchema("id", "arn", "name", "type"),
			"aws_route53recoveryreadiness_cell":                         taggedSchema("id", "arn", "cell_name"),
			"aws_route53recoveryreadiness_readiness_check":              taggedSchema("id", "arn", "readiness_check_name"),
			"aws_route53recoveryreadiness_recovery_group":               taggedSchema("id", "arn", "recovery_group_name"),
			"aws_route53recoveryreadiness_resource_set":                 taggedSchema("id", "arn", "resource_set_name", "resource_set_type"),
		})
	})
}
