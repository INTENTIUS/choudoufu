// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesEc2Networking is the ec2-networking cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesEc2Networking = map[string]typeOverride{
	// EC2 networking beyond the core batch (issue #65). Same two failure
	// shapes as every batch above: a Required argument the wire schema
	// types as a plain string or number but the provider validates against
	// a closed enum, a CIDR/IP/ARN shape, or a range - or a nested block
	// left Optional in the schema while the provider requires its contents,
	// or ExactlyOneOf/AtLeastOneOf combinations the wire schema does not
	// express at all.
	"aws_customer_gateway": {
		Reasons: []string{
			`type is Required and the provider validates it against a closed enum (validate: "expected type to be one of [\"ipsec.1\"]"); the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"ipsec.1"`))
		},
	},
	"aws_ec2_client_vpn_endpoint": {
		Reasons: []string{
			`authentication_options is a required block whose "type" argument the provider validates against a closed enum (validate: "expected type to be one of [...]"); the certificate-authentication member also needs root_certificate_chain_arn in practice, which the schema leaves Optional; server_certificate_arn is Required and the provider validates it is a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("server_certificate_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:acm:us-east-1:000000000000:certificate/tofu-%s-cohort-server-cert"`, g.cohort)))
			for _, blk := range body.Blocks() {
				if blk.Type() == "authentication_options" {
					blk.Body().SetAttributeRaw("type", exprTokens(`"certificate-authentication"`))
					blk.Body().SetAttributeRaw("root_certificate_chain_arn", exprTokens(fmt.Sprintf(
						`"arn:aws:acm:us-east-1:000000000000:certificate/tofu-%s-cohort-root-cert"`, g.cohort)))
				}
			}
		},
	},
	"aws_ec2_client_vpn_route": {
		Reasons: []string{
			`destination_cidr_block is Required and the provider validates it is a well-formed CIDR (validate: "is not a valid CIDR block"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("destination_cidr_block", exprTokens(`"10.1.0.0/24"`))
		},
	},
	"aws_ec2_managed_prefix_list_entry": {
		Reasons: []string{
			`cidr is Required and the provider validates it is a well-formed CIDR (validate: "to be a valid CIDR Value"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cidr", exprTokens(`"10.0.3.0/24"`))
		},
	},
	"aws_ec2_transit_gateway_connect_peer": {
		Reasons: []string{
			`peer_address is Required and the provider validates it is a well-formed IP (validate: "expected peer_address to contain a valid IP"); inside_cidr_blocks is Required and the provider validates each element is a well-formed CIDR inside a fixed range, 169.254.0.0/16 for an IPv4 element (validate: "is not a valid CIDR block", then "IPv4 range must be from range 169.254.0.0/16"); the generic pass's single "placeholder" element satisfies neither check`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("peer_address", exprTokens(`"10.0.0.1"`))
			body.SetAttributeRaw("inside_cidr_blocks", exprTokens(`["169.254.6.0/29"]`))
		},
	},
	"aws_ec2_transit_gateway_metering_policy_entry": {
		Reasons: []string{
			`metered_account is Required and the provider validates it against a closed enum (validate: "Invalid String Enum Value", valid values: source-attachment-owner, destination-attachment-owner, transit-gateway-owner); the generic placeholder string is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("metered_account", exprTokens(`"source-attachment-owner"`))
		},
	},
	"aws_flow_log": {
		Reasons: []string{
			`every argument naming what the flow log watches (vpc_id, subnet_id, eni_id, transit_gateway_id, transit_gateway_attachment_id, regional_nat_gateway_id) is Optional in the schema, so the generic pass renders an empty body, but the provider requires exactly one (validate: "Invalid combination of arguments": "one of ... must be specified")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("vpc_id", exprTokens(`"vpc-0123456789abcdef0"`))
		},
	},
	"aws_network_acl_rule": {
		Reasons: []string{
			`cidr_block and ipv6_cidr_block are both Optional in the schema, but the provider requires exactly one (validate: "Invalid combination of arguments": "one of cidr_block,ipv6_cidr_block must be specified"); protocol is Required and the provider validates it against its own protocol-number/name table (validate: "unsupported NACL protocol"); rule_action is Required and validated against a closed enum (validate: "expected rule_action to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cidr_block", exprTokens(`"10.0.0.0/16"`))
			body.SetAttributeRaw("protocol", exprTokens(`"-1"`))
			body.SetAttributeRaw("rule_action", exprTokens(`"allow"`))
		},
	},
	"aws_security_group_rule": {
		Reasons: []string{
			`#175's demand-head batch: aws_security_group_rule, ADMITTED despite provider deprecation under the type-parity ruling. type is Required and the provider validates it against a closed enum (validate: "expected type to be one of [\"egress\" \"ingress\"]"); protocol is Required and the provider validates it against its own protocol-number/name table, same as aws_network_acl_rule's own protocol above; cidr_blocks is a list of strings in the schema, but the provider validates each element is a well-formed CIDR (validate: "is not a valid CIDR block"); the generic pass's placeholder strings satisfy none of the three`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"ingress"`))
			body.SetAttributeRaw("protocol", exprTokens(`"tcp"`))
			body.SetAttributeRaw("cidr_blocks", exprTokens(`["10.0.3.0/24"]`))
		},
	},
	"aws_vpc_dhcp_options": {
		Reasons: []string{
			`domain_name, domain_name_servers, ipv6_address_preferred_lease_time, netbios_name_servers, netbios_node_type and ntp_servers are all Optional in the schema, so the generic pass renders an empty body, but the provider requires at least one (validate: "Missing required argument": "one of ... must be specified")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("domain_name", exprTokens(`"example.com"`))
		},
	},
	"aws_vpc_ipam": {
		Reasons: []string{
			`operating_regions is a required block, but its own region_name argument is validated by the provider as a well-formed AWS region (validate: "doesn't look like AWS Region"), and the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "operating_regions" {
					blk.Body().SetAttributeRaw("region_name", exprTokens(`"us-east-1"`))
				}
			}
		},
	},
	"aws_vpc_ipam_resource_discovery": {
		Reasons: []string{
			`operating_regions is a required block, the same shape as aws_vpc_ipam above; region_name is validated as a well-formed AWS region and the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "operating_regions" {
					blk.Body().SetAttributeRaw("region_name", exprTokens(`"us-east-1"`))
				}
			}
		},
	},
}

func init() { registerCohortOverrides(typeOverridesEc2Networking) }
