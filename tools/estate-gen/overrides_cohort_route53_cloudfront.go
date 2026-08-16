// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesRoute53Cloudfront is the route53-cloudfront cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesRoute53Cloudfront = map[string]typeOverride{
	// ---- Registry-ratified (#40, #44, #65): fourth batch, Route53
	// ---- remainder and CloudFront -----------------------------------------
	"aws_cloudfront_distribution": {
		Reasons: []string{
			`default_cache_behavior.viewer_protocol_policy and restrictions.geo_restriction.restriction_type are both required strings the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]"); the generic placeholder string matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				switch blk.Type() {
				case "default_cache_behavior":
					blk.Body().SetAttributeRaw("viewer_protocol_policy", exprTokens(`"allow-all"`))
				case "restrictions":
					for _, inner := range blk.Body().Blocks() {
						if inner.Type() == "geo_restriction" {
							inner.Body().SetAttributeRaw("restriction_type", exprTokens(`"none"`))
						}
					}
				}
			}
		},
	},
	"aws_cloudfront_anycast_ip_list": {
		Reasons: []string{
			`"ip_count" is a required number the schema does not constrain, but the provider validates it against a fixed set (validate: "Attribute ip_count value must be one of: [3 21]"); the generic placeholder 0 matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("ip_count", exprTokens(`3`))
		},
	},
	"aws_cloudfront_connection_function": {
		Reasons: []string{
			`connection_function_config is Optional+Computed in the wire schema, but the provider requires it present in practice (validate: "Block connection_function_config must have a configuration value as the provider has marked it as required"), and its comment/runtime members are both required with no default the generic pass can infer`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			cfg := body.AppendNewBlock("connection_function_config", nil)
			cfg.Body().SetAttributeRaw("comment", exprTokens(fmt.Sprintf(`"tofu %s cohort connection function"`, g.cohort)))
			cfg.Body().SetAttributeRaw("runtime", exprTokens(`"cloudfront-js-2.0"`))
		},
	},
	"aws_cloudfront_origin_access_control": {
		Reasons: []string{
			`origin_access_control_origin_type, signing_behavior and signing_protocol are all required strings the schema does not constrain to an enum, but the provider validates each against a fixed set (validate: "expected ... to be one of [...]"); the generic placeholder string matches none of them`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("origin_access_control_origin_type", exprTokens(`"s3"`))
			body.SetAttributeRaw("signing_behavior", exprTokens(`"always"`))
			body.SetAttributeRaw("signing_protocol", exprTokens(`"sigv4"`))
		},
	},
	"aws_cloudfront_multitenant_distribution": {
		Reasons: []string{
			`viewer_certificate, tenant_config and default_cache_behavior are all Optional+Computed in the wire schema, but the provider requires all three present in practice (validate: "Block ... must have a configuration value as the provider has marked it as required") — a newer plugin-framework resource whose Required bit the generic required-only pass does not see the same way it sees aws_cloudfront_distribution's SDKv2 schema above; default_cache_behavior's own nested allowed_methods block is the same shape one level down (validate: "Block default_cache_behavior[0].allowed_methods must have a configuration value as the provider has marked it as required"), with items and cached_methods both required sets no default the generic pass can infer`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.AppendNewBlock("viewer_certificate", nil)
			body.AppendNewBlock("tenant_config", nil)
			dcb := body.AppendNewBlock("default_cache_behavior", nil)
			dcb.Body().SetAttributeRaw("target_origin_id", exprTokens(`"placeholder"`))
			dcb.Body().SetAttributeRaw("viewer_protocol_policy", exprTokens(`"allow-all"`))
			am := dcb.Body().AppendNewBlock("allowed_methods", nil)
			am.Body().SetAttributeRaw("items", exprTokens(`["GET", "HEAD"]`))
			am.Body().SetAttributeRaw("cached_methods", exprTokens(`["GET", "HEAD"]`))
		},
	},
	"aws_cloudfront_realtime_log_config": {
		Reasons: []string{
			`"sampling_rate" is a required number the schema constrains only to a type, but the provider validates it is in range 1-100 (validate: "expected sampling_rate to be in the range (1 - 100), got 0"); endpoint.stream_type is a required string the provider validates against a fixed one-member set (validate: "expected stream_type to be one of [Kinesis]"); and kinesis_stream_config's role_arn/stream_arn are both required strings the provider validates are well-formed ARNs (validate: "is an invalid ARN") the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("sampling_rate", exprTokens(`50`))
			for _, blk := range body.Blocks() {
				if blk.Type() != "endpoint" {
					continue
				}
				blk.Body().SetAttributeRaw("stream_type", exprTokens(`"Kinesis"`))
				for _, inner := range blk.Body().Blocks() {
					if inner.Type() != "kinesis_stream_config" {
						continue
					}
					inner.Body().SetAttributeRaw("role_arn", exprTokens(fmt.Sprintf(
						`"arn:aws:iam::000000000000:role/tofu-%s-cohort-realtime-log-role"`, g.cohort)))
					inner.Body().SetAttributeRaw("stream_arn", exprTokens(fmt.Sprintf(
						`"arn:aws:kinesis:us-east-1:000000000000:stream/tofu-%s-cohort-realtime-log-stream"`, g.cohort)))
				}
			}
		},
	},
	"aws_cloudfront_trust_store": {
		Reasons: []string{
			`ca_certificates_bundle_source is Optional+Computed in the wire schema, but the provider requires it present in practice (validate: "Block ca_certificates_bundle_source must have a configuration value as the provider has marked it as required"), and its nested ca_certificates_bundle_s3_location block's bucket/key/region are all required with no default the generic pass can infer`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			src := body.AppendNewBlock("ca_certificates_bundle_source", nil)
			loc := src.Body().AppendNewBlock("ca_certificates_bundle_s3_location", nil)
			loc.Body().SetAttributeRaw("bucket", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-trust-store-bundle"`, g.cohort)))
			loc.Body().SetAttributeRaw("key", exprTokens(`"ca-bundle.pem"`))
			loc.Body().SetAttributeRaw("region", exprTokens(`"us-east-1"`))
		},
	},
	"aws_cloudfront_vpc_origin": {
		Reasons: []string{
			`vpc_origin_endpoint_config is Optional+Computed in the wire schema, but the provider requires it present in practice (validate: "Block vpc_origin_endpoint_config must have a configuration value as the provider has marked it as required"), and its arn/http_port/https_port/name/origin_protocol_policy members are all required with no default the generic pass can infer; its own nested origin_ssl_protocols block is the same shape one level down (validate: "Block vpc_origin_endpoint_config[0].origin_ssl_protocols must have a configuration value as the provider has marked it as required"), with items and quantity both required with no default either`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			cfg := body.AppendNewBlock("vpc_origin_endpoint_config", nil)
			cfg.Body().SetAttributeRaw("arn", exprTokens(fmt.Sprintf(
				`"arn:aws:ec2:us-east-1:000000000000:vpc-endpoint-service/vpce-svc-tofu-%s-cohort"`, g.cohort)))
			cfg.Body().SetAttributeRaw("http_port", exprTokens(`80`))
			cfg.Body().SetAttributeRaw("https_port", exprTokens(`443`))
			cfg.Body().SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-vpc-origin"`, g.cohort)))
			cfg.Body().SetAttributeRaw("origin_protocol_policy", exprTokens(`"https-only"`))
			ssl := cfg.Body().AppendNewBlock("origin_ssl_protocols", nil)
			ssl.Body().SetAttributeRaw("items", exprTokens(`["TLSv1.2"]`))
			ssl.Body().SetAttributeRaw("quantity", exprTokens(`1`))
		},
	},
	"aws_route53_resolver_firewall_domain_list": {
		Reasons: []string{
			`"name" cannot be greater than 64 characters (validate: "\"name\" cannot be greater than 64 characters"); the generic tofu-<cohort>-cohort-<type> placeholder exceeds it once the cohort/type-fix rule (issue #136) gives this type its own name instead of borrowing a shorter unrelated sibling's`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-firewall-domains"`, g.cohort)))
		},
	},
	"aws_route53_resolver_firewall_rule_group": {
		Reasons: []string{
			`"name" cannot be greater than 64 characters (validate: "\"name\" cannot be greater than 64 characters"); the generic tofu-<cohort>-cohort-<type> placeholder exceeds it once the cohort/type-fix rule (issue #136) gives this type its own name instead of borrowing a shorter unrelated sibling's`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-firewall-rule-group"`, g.cohort)))
		},
	},
	"aws_route53_resolver_firewall_rule_group_association": {
		Reasons: []string{
			`"name" cannot be greater than 64 characters (validate: "\"name\" cannot be greater than 64 characters"); the generic tofu-<cohort>-cohort-<type> placeholder exceeds it once the cohort/type-fix rule (issue #136) gives this type its own name instead of borrowing a shorter unrelated sibling's`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-firewall-assoc"`, g.cohort)))
		},
	},
	"aws_route53_health_check": {
		Reasons: []string{
			`"type" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [...]"); the generic placeholder string matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"HTTP"`))
		},
	},
	"aws_route53_resolver_endpoint": {
		Reasons: []string{
			`"direction" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected direction to be one of [...]"); and ip_address is a set-nesting block with MinItems 2 - the generic pass emits two blocks, but both carry the identical placeholder subnet_id, and a set collapses identical elements to one, so the provider sees only 1 (validate: "Attribute ip_address requires 2 item minimum, but config has only 1 declared")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("direction", exprTokens(`"INBOUND"`))
			i := 0
			for _, blk := range body.Blocks() {
				if blk.Type() != "ip_address" {
					continue
				}
				i++
				blk.Body().SetAttributeRaw("subnet_id", exprTokens(fmt.Sprintf(`"subnet-tofu%02dcohortplaceholder"`, i)))
			}
		},
	},
	"aws_route53_resolver_rule": {
		Reasons: []string{
			`"rule_type" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected rule_type to be one of [...]"); the generic placeholder string matches neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("rule_type", exprTokens(`"FORWARD"`))
		},
	},
	"aws_route53_key_signing_key": {
		Reasons: []string{
			`"key_management_service_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("key_management_service_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:kms:us-east-1:000000000000:key/tofu-%s-cohort-ksk"`, g.cohort)))
		},
	},
	"aws_route53_resolver_firewall_rule": {
		Reasons: []string{
			`"action" is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected action to be one of [ALLOW BLOCK ALERT]"); the generic placeholder string matches none of them, and only ALLOW/ALERT need no further conditionally-required block_response. firewall_domain_list_id is Optional in the schema (it names the standard-rule shape; dns_threat_protection names the advanced-rule shape instead), but the provider requires exactly one of the two set - not caught by "terraform validate" itself, only surfaced at apply (validate: "one of firewall_domain_list_id or dns_threat_protection must be specified") - and the fixture picks the standard-rule shape, the one internal/live/identity/table.go's own parent-derived aws_route53_resolver_firewall_rule entry composes an identity for.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("action", exprTokens(`"ALLOW"`))
			body.SetAttributeRaw("firewall_domain_list_id", exprTokens(`"placeholder"`))
		},
	},
	"aws_route53_resolver_query_log_config": {
		Reasons: []string{
			`"destination_arn" is a required string the schema does not constrain, but the provider validates it is a well-formed ARN (validate: "is an invalid ARN"); the generic placeholder string is not`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("destination_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:logs:us-east-1:000000000000:log-group:/tofu-%s-cohort-query-logs"`, g.cohort)))
		},
	},
	"aws_route53recoverycontrolconfig_safety_rule": {
		Reasons: []string{
			`asserted_controls and gating_controls are both Optional in the schema, but the provider requires exactly one of them set (validate: "one of asserted_controls,gating_controls must be specified"); a gating rule also needs target_controls, the list of controls it gates, which the schema likewise leaves Optional; rule_config.type is a required string the schema does not constrain to an enum, but the provider validates it against a fixed set (validate: "expected type to be one of [ATLEAST AND OR]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			gating := fmt.Sprintf(`["arn:aws:route53-recovery-control::000000000000:controlpanel/tofu-%s-cohort-panel/routingcontrol/tofu-%s-cohort-gating"]`, g.cohort, g.cohort)
			target := fmt.Sprintf(`["arn:aws:route53-recovery-control::000000000000:controlpanel/tofu-%s-cohort-panel/routingcontrol/tofu-%s-cohort-target"]`, g.cohort, g.cohort)
			body.SetAttributeRaw("gating_controls", exprTokens(gating))
			body.SetAttributeRaw("target_controls", exprTokens(target))
			for _, blk := range body.Blocks() {
				if blk.Type() == "rule_config" {
					blk.Body().SetAttributeRaw("type", exprTokens(`"ATLEAST"`))
				}
			}
		},
	},
}

func init() { registerCohortOverrides(typeOverridesRoute53Cloudfront) }
