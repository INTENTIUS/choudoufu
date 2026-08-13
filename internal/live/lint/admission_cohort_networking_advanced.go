// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesNetworkingAdvanced is the networking-advanced cohort's slice of [admittedTypesV0]:
// the types the networking-advanced ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesNetworkingAdvanced = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): sixth batch, advanced
	// ---- networking (Network Firewall, NetworkManager/Cloud WAN, VPC
	// ---- Lattice, Global Accelerator, Route53 Recovery Readiness). Same
	// ---- tools/row-gen pipeline as the batches above, cross-checked
	// ---- against the AWS provider's documented Argument/Attribute/Import
	// ---- sections and, where the doc text alone left the schema argument
	// ---- names or the exact import-ID mechanics ambiguous, the pinned
	// ---- provider's own resource source (internal/service/... on the
	// ---- hashicorp/terraform-provider-aws repository). VPC Lattice is
	// ---- the notable catch: row-gen's flat serverAssigned() template
	// ---- read the CFN registry's primaryIdentifier field name ("Arn")
	// ---- for eleven of its fourteen types and proposed ARN-based
	// ---- identities for all of them, but the provider's own documented
	// ---- Import sections disagree for nine of the eleven — VPC Lattice
	// ---- imports almost its whole family by the short, provider-minted
	// ---- id (svc-…, sn-…, tg-…, rgw-…, rcfg-…, dv-…, snra-…, rft-…), not
	// ---- the arn attribute the same resources also export. See
	// ---- internal/live/identity/table.go for the per-type evidence, the
	// ---- NetworkManager composite identities resolved by hand past
	// ---- row-gen's own "needs hand separator" refusal, and the deferred
	// ---- App Mesh (deprecated service) and Cloud WAN (not a distinct CFN
	// ---- service; folded into NetworkManager's CoreNetwork family)
	// ---- scope notes. Cohort estate:
	// ---- live/e2e/estates/networking-advanced.
	"aws_networkfirewall_firewall":                              {},
	"aws_networkfirewall_firewall_policy":                       {},
	"aws_networkfirewall_logging_configuration":                 {},
	"aws_networkfirewall_rule_group":                            {},
	"aws_networkfirewall_tls_inspection_configuration":          {},
	"aws_networkfirewall_vpc_endpoint_association":              {},
	"aws_networkmanager_connect_attachment":                     {},
	"aws_networkmanager_connect_peer":                           {},
	"aws_networkmanager_core_network":                           {},
	"aws_networkmanager_customer_gateway_association":           {},
	"aws_networkmanager_device":                                 {},
	"aws_networkmanager_dx_gateway_attachment":                  {},
	"aws_networkmanager_global_network":                         {},
	"aws_networkmanager_link":                                   {},
	"aws_networkmanager_link_association":                       {},
	"aws_networkmanager_prefix_list_association":                {},
	"aws_networkmanager_site":                                   {},
	"aws_networkmanager_site_to_site_vpn_attachment":            {},
	"aws_networkmanager_transit_gateway_peering":                {},
	"aws_networkmanager_transit_gateway_registration":           {},
	"aws_networkmanager_transit_gateway_route_table_attachment": {},
	"aws_networkmanager_vpc_attachment":                         {},
	"aws_globalaccelerator_accelerator":                         {},
	"aws_globalaccelerator_cross_account_attachment":            {},
	"aws_globalaccelerator_endpoint_group":                      {},
	"aws_globalaccelerator_listener":                            {},
	"aws_vpclattice_access_log_subscription":                    {},
	"aws_vpclattice_auth_policy":                                {},
	"aws_vpclattice_domain_verification":                        {},
	"aws_vpclattice_listener":                                   {},
	"aws_vpclattice_listener_rule":                              {},
	"aws_vpclattice_resource_configuration":                     {},
	"aws_vpclattice_resource_gateway":                           {},
	"aws_vpclattice_resource_policy":                            {},
	"aws_vpclattice_service":                                    {},
	"aws_vpclattice_service_network":                            {},
	"aws_vpclattice_service_network_resource_association":       {},
	"aws_vpclattice_service_network_service_association":        {},
	"aws_vpclattice_service_network_vpc_association":            {},
	"aws_vpclattice_target_group":                               {},
	"aws_route53recoveryreadiness_cell":                         {},
	"aws_route53recoveryreadiness_readiness_check":              {},
	"aws_route53recoveryreadiness_recovery_group":               {},
	"aws_route53recoveryreadiness_resource_set":                 {},
}

func init() { registerCohortAdmitted(admittedTypesNetworkingAdvanced) }
