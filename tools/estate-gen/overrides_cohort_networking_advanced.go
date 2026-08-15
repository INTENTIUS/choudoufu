// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesNetworkingAdvanced is the networking-advanced cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesNetworkingAdvanced = map[string]typeOverride{
	// ---- networking-advanced cohort (issue #65's sixth ratification batch) ----
	//
	// This cohort's own name ("networking-advanced") is long enough that
	// several VPC Lattice types' 40-character name ceiling (validate:
	// "expected length of name to be in the range (3 - 40)") blows past it
	// on the generic pass's own tofu-COHORT-cohort-SUFFIX naming
	// convention; every VPC Lattice named type below gets a short, literal
	// override name instead. The rest of this section is almost entirely
	// server-assigned types (NetworkManager, VPC Lattice, Network Firewall
	// and Global Accelerator all mint their own identities - see
	// internal/live/identity/table_cohort_networking_advanced.go's own
	// comment for this batch), so identityArgName never wires their
	// cross-references automatically; every g.byType lookup below is the
	// same cross-resource-reference pattern aws_eip_association and
	// aws_network_interface_attachment use in
	// overrides_cohort_ec2_core.go. Arguments that name a real resource
	// type outside this batch's own scope (EC2 VPCs, subnets, transit
	// gateways, VPN connections, customer gateways, DX gateways, prefix
	// lists - explicitly not this agent's to ratify or add coverage for)
	// get literal, plausibly-shaped placeholder ARNs/IDs instead of a
	// cross-resource reference, the same choice aws_volume_attachment's
	// own comment in that file makes for aws_ebs_volume.
	"aws_networkfirewall_firewall": {
		Reasons: []string{
			`the schema marks transit_gateway_id and vpc_id both Optional, but the provider requires exactly one (validate: "Invalid combination of arguments" x2); firewall_policy_arn is Required but a generic-string placeholder is not a valid ARN (validate: "is an invalid ARN") - overridden to this cohort's own aws_networkfirewall_firewall_policy.app.arn. subnet_mapping carries no schema-level minimum, but a VPC-scoped firewall needs at least one subnet in practice, so one is added for behavioral realism even though validate does not require it.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if policy, ok := g.byType["aws_networkfirewall_firewall_policy"]; ok {
				body.SetAttributeRaw("firewall_policy_arn", exprTokens(fmt.Sprintf("%s.arn", policy)))
			} else {
				body.SetAttributeRaw("firewall_policy_arn", exprTokens(`"arn:aws:network-firewall:us-east-1:123456789012:firewall-policy/tofu-networking-advanced-cohort"`))
			}
			body.SetAttributeRaw("vpc_id", exprTokens(`"vpc-0123456789abcdef0"`))
			sm := body.AppendNewBlock("subnet_mapping", nil)
			sm.Body().SetAttributeRaw("subnet_id", exprTokens(`"subnet-0123456789abcdef0"`))
		},
	},
	"aws_networkfirewall_firewall_policy": {
		Reasons: []string{
			`stateless_default_actions and stateless_fragment_default_actions are both Required sets of strings the schema does not constrain (no closed-enum validate error - Network Firewall validates these only once Create actually runs), but the generic pass's own "placeholder" element is not a real action name; overridden to the documented built-in aws:pass for behavioral realism during a floci apply, not to silence validate`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "firewall_policy" {
					blk.Body().SetAttributeRaw("stateless_default_actions", exprTokens(`["aws:pass"]`))
					blk.Body().SetAttributeRaw("stateless_fragment_default_actions", exprTokens(`["aws:pass"]`))
				}
			}
		},
	},
	"aws_networkfirewall_logging_configuration": {
		Reasons: []string{
			`firewall_arn is Required; the generic pass's own cross-reference guess (this type shares no identity-table shape with any sibling, so parentRef never fires) left a literal, non-ARN string in place (validate: "is an invalid ARN") - overridden to this cohort's own aws_networkfirewall_firewall.app.arn. log_destination_config's log_destination_type and log_type are both Required strings validated against closed enums (validate: "expected ... to be one of [...]" x2); log_destination is a Required map the generic pass leaves empty, but CloudWatchLogs destinations need a logGroup key in practice`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if firewall, ok := g.byType["aws_networkfirewall_firewall"]; ok {
				body.SetAttributeRaw("firewall_arn", exprTokens(fmt.Sprintf("%s.arn", firewall)))
			} else {
				body.SetAttributeRaw("firewall_arn", exprTokens(`"arn:aws:network-firewall:us-east-1:123456789012:firewall/tofu-networking-advanced-cohort"`))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() != "logging_configuration" {
					continue
				}
				for _, ldc := range blk.Body().Blocks() {
					if ldc.Type() != "log_destination_config" {
						continue
					}
					ldc.Body().SetAttributeRaw("log_destination_type", exprTokens(`"CloudWatchLogs"`))
					ldc.Body().SetAttributeRaw("log_type", exprTokens(`"FLOW"`))
					ldc.Body().SetAttributeRaw("log_destination", exprTokens(fmt.Sprintf(
						`{ logGroup = "tofu-%s-cohort-firewall-logs" }`, g.cohort)))
				}
			}
		},
	},
	"aws_networkfirewall_rule_group": {
		Reasons: []string{
			`type is Required and validated against a closed enum (validate: "expected type to be one of [\"STATELESS\" \"STATEFUL\" \"STATEFUL_DOMAIN\"]"); rules and rule_group are both Optional in the schema (either supplies the rule content), but a rule group with neither has nothing for the STATELESS type it declares, so a minimal valid stateless rules_source is added for behavioral realism, not because validate requires it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"STATELESS"`))
			body.SetAttributeRaw("capacity", exprTokens(`100`))
			rg := body.AppendNewBlock("rule_group", nil)
			rs := rg.Body().AppendNewBlock("rules_source", nil)
			sr := rs.Body().AppendNewBlock("stateless_rules_and_custom_actions", nil)
			sRule := sr.Body().AppendNewBlock("stateless_rule", nil)
			sRule.Body().SetAttributeRaw("priority", exprTokens(`1`))
			rd := sRule.Body().AppendNewBlock("rule_definition", nil)
			rd.Body().SetAttributeRaw("actions", exprTokens(`["aws:pass"]`))
			ma := rd.Body().AppendNewBlock("match_attributes", nil)
			src := ma.Body().AppendNewBlock("source", nil)
			src.Body().SetAttributeRaw("address_definition", exprTokens(`"10.0.0.0/8"`))
			dst := ma.Body().AppendNewBlock("destination", nil)
			dst.Body().SetAttributeRaw("address_definition", exprTokens(`"10.0.0.0/8"`))
		},
	},
	"aws_networkfirewall_tls_inspection_configuration": {
		Reasons: []string{
			`tls_inspection_configuration is a Required block the generic pass emits empty, but the provider requires at least one of server_certificate_configuration[0].certificate_authority_arn/server_certificate once it exists (validate: "At least one of these attributes must be configured" and "Block ... must have a configuration value"); server_certificate_configuration's own scope block is likewise required once that block exists (validate: "Block ... scope must have a configuration value"), and scope's protocols is Required`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			tic := body.AppendNewBlock("tls_inspection_configuration", nil)
			scc := tic.Body().AppendNewBlock("server_certificate_configuration", nil)
			scc.Body().SetAttributeRaw("certificate_authority_arn", exprTokens(`"arn:aws:acm-pca:us-east-1:123456789012:certificate-authority/12345678-1234-1234-1234-123456789012"`))
			scope := scc.Body().AppendNewBlock("scope", nil)
			scope.Body().SetAttributeRaw("protocols", exprTokens(`[6]`))
			src := scope.Body().AppendNewBlock("source", nil)
			src.Body().SetAttributeRaw("address_definition", exprTokens(`"10.0.0.0/8"`))
			dst := scope.Body().AppendNewBlock("destination", nil)
			dst.Body().SetAttributeRaw("address_definition", exprTokens(`"10.0.0.0/8"`))
		},
	},
	"aws_networkfirewall_vpc_endpoint_association": {
		Reasons: []string{
			`firewall_arn is Required; the generic pass's own cross-resource guess pointed it at this cohort's aws_networkfirewall_logging_configuration.app.firewall_arn (that type happens to share an argument name, but is not this type's real parent) - overridden to this cohort's own aws_networkfirewall_firewall.app.arn instead. vpc_id is Required with no ARN validation, but is nonetheless a real EC2 VPC id this batch's scope does not add coverage for, so it stays a literal placeholder (the same choice aws_volume_attachment's own comment above makes for aws_ebs_volume). subnet_mapping carries no schema-level minimum, but the provider requires at least one once the block exists in practice (validate: "Block subnet_mapping must have a configuration value")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if firewall, ok := g.byType["aws_networkfirewall_firewall"]; ok {
				body.SetAttributeRaw("firewall_arn", exprTokens(fmt.Sprintf("%s.arn", firewall)))
			} else {
				body.SetAttributeRaw("firewall_arn", exprTokens(`"arn:aws:network-firewall:us-east-1:123456789012:firewall/tofu-networking-advanced-cohort"`))
			}
			body.SetAttributeRaw("vpc_id", exprTokens(`"vpc-0123456789abcdef0"`))
			sm := body.AppendNewBlock("subnet_mapping", nil)
			sm.Body().SetAttributeRaw("subnet_id", exprTokens(`"subnet-0123456789abcdef0"`))
		},
	},
	"aws_networkmanager_connect_attachment": {
		Reasons: []string{
			`core_network_id is Required and the provider validates its shape (validate: "Must start with core-network and then have 8 to 17 characters") - overridden to this cohort's own aws_networkmanager_core_network.app.id; transport_attachment_id is Required and shape-validated the same way (validate: "Must start with attachment- and then have 8 to 17 characters") - overridden to this cohort's own aws_networkmanager_vpc_attachment.app.id, itself a real "attachment-..." id, making this Connect attachment's transport a VPC attachment already in this cohort; options is an Optional (max_items 1) block the generic pass still emits empty, given a protocol for behavioral realism`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if cn, ok := g.byType["aws_networkmanager_core_network"]; ok {
				body.SetAttributeRaw("core_network_id", exprTokens(fmt.Sprintf("%s.id", cn)))
			} else {
				body.SetAttributeRaw("core_network_id", exprTokens(`"core-network-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("edge_location", exprTokens(`"us-east-1"`))
			if vpcAttach, ok := g.byType["aws_networkmanager_vpc_attachment"]; ok {
				body.SetAttributeRaw("transport_attachment_id", exprTokens(fmt.Sprintf("%s.id", vpcAttach)))
			} else {
				body.SetAttributeRaw("transport_attachment_id", exprTokens(`"attachment-0123456789abcdef0"`))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "options" {
					blk.Body().SetAttributeRaw("protocol", exprTokens(`"GRE"`))
				}
			}
		},
	},
	"aws_networkmanager_connect_peer": {
		Reasons: []string{
			`connect_attachment_id is Required and shape-validated (validate: "Must start with attachment and then have 8 to 17 characters") - overridden to this cohort's own aws_networkmanager_connect_attachment.app.id; peer_address is Required with no schema-level format constraint, given a real IP literal for behavioral realism`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if connectAttach, ok := g.byType["aws_networkmanager_connect_attachment"]; ok {
				body.SetAttributeRaw("connect_attachment_id", exprTokens(fmt.Sprintf("%s.id", connectAttach)))
			} else {
				body.SetAttributeRaw("connect_attachment_id", exprTokens(`"attachment-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("peer_address", exprTokens(`"10.0.0.1"`))
		},
	},
	"aws_networkmanager_customer_gateway_association": {
		Reasons: []string{
			`global_network_id and device_id are both Required but generic-string placeholders, not references - overridden to this cohort's own aws_networkmanager_global_network.app.id and aws_networkmanager_device.app.id. customer_gateway_arn is Required but a generic-string placeholder is not a valid ARN (validate: "is an invalid ARN"); the real target is an EC2 customer gateway, outside this batch's own scope (aws_customer_gateway is explicitly not this agent's to touch), so it stays a literal placeholder ARN`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if gn, ok := g.byType["aws_networkmanager_global_network"]; ok {
				body.SetAttributeRaw("global_network_id", exprTokens(fmt.Sprintf("%s.id", gn)))
			} else {
				body.SetAttributeRaw("global_network_id", exprTokens(`"global-network-0123456789abcdef0"`))
			}
			if device, ok := g.byType["aws_networkmanager_device"]; ok {
				body.SetAttributeRaw("device_id", exprTokens(fmt.Sprintf("%s.id", device)))
			} else {
				body.SetAttributeRaw("device_id", exprTokens(`"device-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("customer_gateway_arn", exprTokens(`"arn:aws:ec2:us-east-1:123456789012:customer-gateway/cgw-0123456789abcdef0"`))
		},
	},
	"aws_networkmanager_dx_gateway_attachment": {
		Reasons: []string{
			`core_network_id is Required but a generic-string placeholder, not a reference - overridden to this cohort's own aws_networkmanager_core_network.app.id. direct_connect_gateway_arn is Required and the provider validates it is a well-formed ARN (validate: "The provided value cannot be parsed as an ARN"); the real target is an EC2 Direct Connect gateway, outside this batch's own scope, so it stays a literal placeholder ARN`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if cn, ok := g.byType["aws_networkmanager_core_network"]; ok {
				body.SetAttributeRaw("core_network_id", exprTokens(fmt.Sprintf("%s.id", cn)))
			} else {
				body.SetAttributeRaw("core_network_id", exprTokens(`"core-network-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("direct_connect_gateway_arn", exprTokens(`"arn:aws:directconnect::123456789012:dx-gateway/11223344-1122-1122-1122-112233445566"`))
		},
	},
	"aws_networkmanager_link": {
		Reasons: []string{
			`global_network_id and site_id are both Required but generic-string placeholders, not references - overridden to this cohort's own aws_networkmanager_global_network.app.id and aws_networkmanager_site.app.id. bandwidth is a Required (min_items 1) block the generic pass emits empty; both its attributes are schema-Optional, but a link with neither speed set is not a realistic fixture, so both are given values`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if gn, ok := g.byType["aws_networkmanager_global_network"]; ok {
				body.SetAttributeRaw("global_network_id", exprTokens(fmt.Sprintf("%s.id", gn)))
			} else {
				body.SetAttributeRaw("global_network_id", exprTokens(`"global-network-0123456789abcdef0"`))
			}
			if site, ok := g.byType["aws_networkmanager_site"]; ok {
				body.SetAttributeRaw("site_id", exprTokens(fmt.Sprintf("%s.id", site)))
			} else {
				body.SetAttributeRaw("site_id", exprTokens(`"site-0123456789abcdef0"`))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "bandwidth" {
					blk.Body().SetAttributeRaw("download_speed", exprTokens(`100`))
					blk.Body().SetAttributeRaw("upload_speed", exprTokens(`100`))
				}
			}
		},
	},
	"aws_networkmanager_prefix_list_association": {
		Reasons: []string{
			`core_network_id is Required but a generic-string placeholder, not a reference - overridden to this cohort's own aws_networkmanager_core_network.app.id. prefix_list_arn is Required and the provider validates it is a well-formed ARN (validate: "The provided value cannot be parsed as an ARN"); the real target is an EC2 managed prefix list, outside this batch's own scope, so it stays a literal placeholder ARN`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if cn, ok := g.byType["aws_networkmanager_core_network"]; ok {
				body.SetAttributeRaw("core_network_id", exprTokens(fmt.Sprintf("%s.id", cn)))
			} else {
				body.SetAttributeRaw("core_network_id", exprTokens(`"core-network-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("prefix_list_arn", exprTokens(`"arn:aws:ec2:us-east-1:123456789012:prefix-list/pl-0123456789abcdef0"`))
		},
	},
	"aws_networkmanager_site_to_site_vpn_attachment": {
		Reasons: []string{
			`core_network_id is Required but a generic-string placeholder, not a reference - overridden to this cohort's own aws_networkmanager_core_network.app.id. vpn_connection_arn is Required and the provider validates its shape (validate: "Must be valid VPN ARN"); the real target is an EC2 VPN connection, outside this batch's own scope, so it stays a literal placeholder ARN`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if cn, ok := g.byType["aws_networkmanager_core_network"]; ok {
				body.SetAttributeRaw("core_network_id", exprTokens(fmt.Sprintf("%s.id", cn)))
			} else {
				body.SetAttributeRaw("core_network_id", exprTokens(`"core-network-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("vpn_connection_arn", exprTokens(`"arn:aws:ec2:us-east-1:123456789012:vpn-connection/vpn-0123456789abcdef0"`))
		},
	},
	"aws_networkmanager_transit_gateway_peering": {
		Reasons: []string{
			`core_network_id is Required but a generic-string placeholder, not a reference - overridden to this cohort's own aws_networkmanager_core_network.app.id. transit_gateway_arn is Required and the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the real target is an EC2 transit gateway, outside this batch's own scope, so it stays a literal placeholder ARN`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if cn, ok := g.byType["aws_networkmanager_core_network"]; ok {
				body.SetAttributeRaw("core_network_id", exprTokens(fmt.Sprintf("%s.id", cn)))
			} else {
				body.SetAttributeRaw("core_network_id", exprTokens(`"core-network-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("transit_gateway_arn", exprTokens(`"arn:aws:ec2:us-east-1:123456789012:transit-gateway/tgw-0123456789abcdef0"`))
		},
	},
	"aws_networkmanager_transit_gateway_registration": {
		Reasons: []string{
			`global_network_id is Required but a generic-string placeholder, not a reference - overridden to this cohort's own aws_networkmanager_global_network.app.id. transit_gateway_arn is Required and the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the real target is an EC2 transit gateway, outside this batch's own scope, so it stays a literal placeholder ARN, the same one aws_networkmanager_transit_gateway_peering above uses`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if gn, ok := g.byType["aws_networkmanager_global_network"]; ok {
				body.SetAttributeRaw("global_network_id", exprTokens(fmt.Sprintf("%s.id", gn)))
			} else {
				body.SetAttributeRaw("global_network_id", exprTokens(`"global-network-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("transit_gateway_arn", exprTokens(`"arn:aws:ec2:us-east-1:123456789012:transit-gateway/tgw-0123456789abcdef0"`))
		},
	},
	"aws_networkmanager_transit_gateway_route_table_attachment": {
		Reasons: []string{
			`peering_id is Required but a generic-string placeholder, not a reference - overridden to this cohort's own aws_networkmanager_transit_gateway_peering.app.id. transit_gateway_route_table_arn is Required and the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the real target is an EC2 transit gateway route table, outside this batch's own scope, so it stays a literal placeholder ARN`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if peering, ok := g.byType["aws_networkmanager_transit_gateway_peering"]; ok {
				body.SetAttributeRaw("peering_id", exprTokens(fmt.Sprintf("%s.id", peering)))
			} else {
				body.SetAttributeRaw("peering_id", exprTokens(`"peering-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("transit_gateway_route_table_arn", exprTokens(`"arn:aws:ec2:us-east-1:123456789012:transit-gateway-route-table/tgw-rtb-0123456789abcdef0"`))
		},
	},
	"aws_networkmanager_vpc_attachment": {
		Reasons: []string{
			`core_network_id is Required but a generic-string placeholder, not a reference - overridden to this cohort's own aws_networkmanager_core_network.app.id. subnet_arns and vpc_arn are both Required and the provider validates each is a well-formed ARN (validate: "is an invalid ARN" x2); the real targets are EC2 subnets and a VPC, outside this batch's own scope, so both stay literal placeholder ARNs`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if cn, ok := g.byType["aws_networkmanager_core_network"]; ok {
				body.SetAttributeRaw("core_network_id", exprTokens(fmt.Sprintf("%s.id", cn)))
			} else {
				body.SetAttributeRaw("core_network_id", exprTokens(`"core-network-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("subnet_arns", exprTokens(`["arn:aws:ec2:us-east-1:123456789012:subnet/subnet-0123456789abcdef0"]`))
			body.SetAttributeRaw("vpc_arn", exprTokens(`"arn:aws:ec2:us-east-1:123456789012:vpc/vpc-0123456789abcdef0"`))
		},
	},
	"aws_route53recoveryreadiness_resource_set": {
		Reasons: []string{
			`resource_set_type is Required with no closed-enum validation (it names an arbitrary supported AWS resource type string), given a real one for behavioral realism; resources is a Required (min_items 1) block the generic pass emits empty - given a resource_arn for behavioral realism, matching the resource_set_type given`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("resource_set_type", exprTokens(`"AWS::EC2::Subnet"`))
			res := body.AppendNewBlock("resources", nil)
			res.Body().SetAttributeRaw("resource_arn", exprTokens(`"arn:aws:ec2:us-east-1:123456789012:subnet/subnet-0123456789abcdef0"`))
		},
	},
	"aws_vpclattice_access_log_subscription": {
		Reasons: []string{
			`resource_identifier is Required; the generic pass's own cross-resource guess (parentRef matched this argument's name against aws_vpclattice_auth_policy's own identity argument, which happens to share the name "resource_identifier" but is not this type's real target) left this pointed at a sibling resource's own opaque placeholder string - overridden to this cohort's own aws_vpclattice_service_network.app.arn, an actual VPC Lattice resource a log subscription can target. destination_arn is Required with no ARN-format validate error surfaced yet, given a real S3 ARN shape for behavioral realism`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if sn, ok := g.byType["aws_vpclattice_service_network"]; ok {
				body.SetAttributeRaw("resource_identifier", exprTokens(fmt.Sprintf("%s.arn", sn)))
			} else {
				body.SetAttributeRaw("resource_identifier", exprTokens(`"arn:aws:vpc-lattice:us-east-1:123456789012:servicenetwork/sn-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("destination_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:s3:::tofu-%s-cohort-firewall-logs"`, g.cohort)))
		},
	},
	"aws_vpclattice_auth_policy": {
		Reasons: []string{
			`resource_identifier is Required and validate wants an ARN shape; a literal placeholder ARN rather than a reference to this cohort's own aws_vpclattice_service_network, because resource_identifier is this type's identity argument and the sibling's only statically-resolvable identity attribute is its opaque id, not its arn - identity resolution refuses the .arn read ("Not an identity attribute"), and the id value is not the ARN validate wants. No reference satisfies both checks. policy is Required and the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("resource_identifier", exprTokens(`"arn:aws:vpc-lattice:us-east-1:123456789012:servicenetwork/sn-0123456789abcdef0"`))
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "vpc-lattice-svcs:Invoke"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_vpclattice_listener": {
		Reasons: []string{
			`name is Required and the provider validates its length (3-40 characters); this cohort's own generic tofu-COHORT-cohort-SUFFIX naming convention exceeds it, so a short literal name is given instead. protocol is Required and validated against a closed enum (validate: "expected protocol to be one of [\"HTTP\" \"HTTPS\" \"TLS_PASSTHROUGH\"]"). service_arn and service_identifier are both Optional in the schema, but the provider requires exactly one (validate: "Missing required argument" x2) - service_identifier is set to this cohort's own aws_vpclattice_service.app.id. default_action is a Required (min_items 1) block the generic pass emits empty; given a fixed_response for behavioral realism`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-na-listener"`))
			body.SetAttributeRaw("protocol", exprTokens(`"HTTPS"`))
			if svc, ok := g.byType["aws_vpclattice_service"]; ok {
				body.SetAttributeRaw("service_identifier", exprTokens(fmt.Sprintf("%s.id", svc)))
			} else {
				body.SetAttributeRaw("service_identifier", exprTokens(`"svc-0123456789abcdef0"`))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "default_action" {
					fr := blk.Body().AppendNewBlock("fixed_response", nil)
					fr.Body().SetAttributeRaw("status_code", exprTokens(`404`))
				}
			}
		},
	},
	"aws_vpclattice_listener_rule": {
		Reasons: []string{
			`name is Required and length-validated the same way aws_vpclattice_listener's is above, given a short literal name for the same reason. listener_identifier and service_identifier are both Required but generic-string placeholders, not references - overridden to this cohort's own aws_vpclattice_listener.app.id and aws_vpclattice_service.app.id. priority is Required and the provider validates its range (validate: "expected priority to be in the range (1 - 100), got 0"). action is a Required (min_items 1) block the generic pass emits empty; given a fixed_response for behavioral realism, the same shape as the listener's own default_action. match.http_match is a Required (min_items 1) block whose own contents (header_matches, method, path_match) are all Optional, but the provider requires at least one (validate: "Missing required argument"); given a path_match`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-na-listener-rule"`))
			body.SetAttributeRaw("priority", exprTokens(`10`))
			if listener, ok := g.byType["aws_vpclattice_listener"]; ok {
				body.SetAttributeRaw("listener_identifier", exprTokens(fmt.Sprintf("%s.id", listener)))
			} else {
				body.SetAttributeRaw("listener_identifier", exprTokens(`"listener-0123456789abcdef0"`))
			}
			if svc, ok := g.byType["aws_vpclattice_service"]; ok {
				body.SetAttributeRaw("service_identifier", exprTokens(fmt.Sprintf("%s.id", svc)))
			} else {
				body.SetAttributeRaw("service_identifier", exprTokens(`"svc-0123456789abcdef0"`))
			}
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "action":
					fr := blk.Body().AppendNewBlock("fixed_response", nil)
					fr.Body().SetAttributeRaw("status_code", exprTokens(`404`))
				case "match":
					for _, hm := range blk.Body().Blocks() {
						if hm.Type() != "http_match" {
							continue
						}
						pm := hm.Body().AppendNewBlock("path_match", nil)
						m := pm.Body().AppendNewBlock("match", nil)
						m.Body().SetAttributeRaw("exact", exprTokens(`"/"`))
					}
				}
			}
		},
	},
	"aws_vpclattice_resource_configuration": {
		Reasons: []string{
			`name is Required and length-validated the same way aws_vpclattice_listener's is above, given a short literal name for the same reason. resource_gateway_identifier and resource_configuration_group_id are both Optional, but the provider requires exactly one (validate: "No attribute specified when one ... is required") - resource_gateway_identifier is set to this cohort's own aws_vpclattice_resource_gateway.app.id. protocol, resource_configuration_definition[0].arn_resource and resource_configuration_group_id are all Optional, but the provider requires at least one (validate: "At least one attribute out of [...] must be specified") - protocol is set`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-na-resource-config"`))
			body.SetAttributeRaw("protocol", exprTokens(`"TCP"`))
			if rgw, ok := g.byType["aws_vpclattice_resource_gateway"]; ok {
				body.SetAttributeRaw("resource_gateway_identifier", exprTokens(fmt.Sprintf("%s.id", rgw)))
			} else {
				body.SetAttributeRaw("resource_gateway_identifier", exprTokens(`"rgw-0123456789abcdef0"`))
			}
		},
	},
	"aws_vpclattice_resource_gateway": {
		Reasons: []string{
			`name is Required and length-validated the same way aws_vpclattice_listener's is above, given a short literal name for the same reason. subnet_ids and vpc_id are both Required with no ARN-format validate error surfaced, but the real targets are an EC2 VPC and its subnets, outside this batch's own scope, so both stay literal placeholder ids`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-na-resource-gateway"`))
			body.SetAttributeRaw("subnet_ids", exprTokens(`["subnet-0123456789abcdef0"]`))
			body.SetAttributeRaw("vpc_id", exprTokens(`"vpc-0123456789abcdef0"`))
		},
	},
	"aws_vpclattice_resource_policy": {
		Reasons: []string{
			`resource_arn is Required and validate refuses a non-ARN string ("is an invalid ARN"); a literal placeholder ARN rather than a reference to this cohort's own aws_vpclattice_service_network, because resource_arn is this type's identity argument and the sibling's only statically-resolvable identity attribute is its opaque id, not its arn - identity resolution refuses the .arn read ("Not an identity attribute"), and the id value is not an ARN. No reference satisfies both checks. policy is Required and the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("resource_arn", exprTokens(`"arn:aws:vpc-lattice:us-east-1:123456789012:servicenetwork/sn-0123456789abcdef0"`))
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "vpc-lattice-svcs:Invoke"
      Resource  = "*"
    }]
  })`))
		},
	},
	"aws_vpclattice_service": {
		Reasons: []string{
			`name is Required and length-validated the same way aws_vpclattice_listener's is above, given a short literal name for the same reason`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-na-service"`))
		},
	},
	"aws_vpclattice_service_network": {
		Reasons: []string{
			`name is Required and length-validated the same way aws_vpclattice_listener's is above, given a short literal name for the same reason`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-na-service-network"`))
		},
	},
	"aws_vpclattice_service_network_resource_association": {
		Reasons: []string{
			`resource_configuration_identifier and service_network_identifier are both Required but generic-string placeholders, not references - overridden to this cohort's own aws_vpclattice_resource_configuration.app.id and aws_vpclattice_service_network.app.id`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if rc, ok := g.byType["aws_vpclattice_resource_configuration"]; ok {
				body.SetAttributeRaw("resource_configuration_identifier", exprTokens(fmt.Sprintf("%s.id", rc)))
			} else {
				body.SetAttributeRaw("resource_configuration_identifier", exprTokens(`"rcfg-0123456789abcdef0"`))
			}
			if sn, ok := g.byType["aws_vpclattice_service_network"]; ok {
				body.SetAttributeRaw("service_network_identifier", exprTokens(fmt.Sprintf("%s.id", sn)))
			} else {
				body.SetAttributeRaw("service_network_identifier", exprTokens(`"sn-0123456789abcdef0"`))
			}
		},
	},
	"aws_vpclattice_service_network_service_association": {
		Reasons: []string{
			`service_identifier and service_network_identifier are both Required but generic-string placeholders, not references - overridden to this cohort's own aws_vpclattice_service.app.id and aws_vpclattice_service_network.app.id`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if svc, ok := g.byType["aws_vpclattice_service"]; ok {
				body.SetAttributeRaw("service_identifier", exprTokens(fmt.Sprintf("%s.id", svc)))
			} else {
				body.SetAttributeRaw("service_identifier", exprTokens(`"svc-0123456789abcdef0"`))
			}
			if sn, ok := g.byType["aws_vpclattice_service_network"]; ok {
				body.SetAttributeRaw("service_network_identifier", exprTokens(fmt.Sprintf("%s.id", sn)))
			} else {
				body.SetAttributeRaw("service_network_identifier", exprTokens(`"sn-0123456789abcdef0"`))
			}
		},
	},
	"aws_vpclattice_service_network_vpc_association": {
		Reasons: []string{
			`service_network_identifier is Required but a generic-string placeholder, not a reference - overridden to this cohort's own aws_vpclattice_service_network.app.id. vpc_identifier is Required with no ARN-format validate error surfaced, but the real target is an EC2 VPC, outside this batch's own scope, so it stays a literal placeholder id`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if sn, ok := g.byType["aws_vpclattice_service_network"]; ok {
				body.SetAttributeRaw("service_network_identifier", exprTokens(fmt.Sprintf("%s.id", sn)))
			} else {
				body.SetAttributeRaw("service_network_identifier", exprTokens(`"sn-0123456789abcdef0"`))
			}
			body.SetAttributeRaw("vpc_identifier", exprTokens(`"vpc-0123456789abcdef0"`))
		},
	},
	"aws_vpclattice_target_group": {
		Reasons: []string{
			`name is Required and length-validated the same way aws_vpclattice_listener's is above, given a short literal name for the same reason. type is Required and validated against a closed enum (validate: "expected type to be one of [\"IP\" \"LAMBDA\" \"INSTANCE\" \"ALB\"]"); an IP-type target group also needs its config block's vpc_identifier in practice (not caught by validate, enforced at Create), so config is given even though the schema marks it Optional`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(`"tofu-na-target-group"`))
			body.SetAttributeRaw("type", exprTokens(`"IP"`))
			cfg := body.AppendNewBlock("config", nil)
			cfg.Body().SetAttributeRaw("vpc_identifier", exprTokens(`"vpc-0123456789abcdef0"`))
			cfg.Body().SetAttributeRaw("port", exprTokens(`80`))
			cfg.Body().SetAttributeRaw("protocol", exprTokens(`"HTTP"`))
		},
	},
	"aws_vpclattice_domain_verification": {
		Reasons: []string{
			`domain_name is Required with no length/format validate error surfaced against this cohort's own generic naming convention, given a real-looking domain name for behavioral realism`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("domain_name", exprTokens(fmt.Sprintf(
				`"tofu-%s-cohort.example.com"`, g.cohort)))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesNetworkingAdvanced) }
