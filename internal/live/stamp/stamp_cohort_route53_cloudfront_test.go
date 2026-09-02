// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The route53-cloudfront cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableRoute53Cloudfront = []string{
	// Registry-ratified Route53 remainder and CloudFront batch (#40,
	// #44, #65). See live/e2e/estates/route53-cloudfront/README.md.
	"aws_route53_health_check",
	"aws_route53profiles_association",
	"aws_route53profiles_profile",
	"aws_route53recoverycontrolconfig_cluster",
	"aws_route53recoverycontrolconfig_control_panel",
	"aws_route53recoverycontrolconfig_safety_rule",
	"aws_route53_resolver_endpoint",
	"aws_route53_resolver_firewall_domain_list",
	"aws_route53_resolver_firewall_rule_group",
	"aws_route53_resolver_firewall_rule_group_association",
	"aws_route53_resolver_query_log_config",
	"aws_route53_resolver_rule",
	"aws_cloudfront_anycast_ip_list",
	"aws_cloudfront_connection_function",
	"aws_cloudfront_connection_group",
	"aws_cloudfront_distribution",
	"aws_cloudfront_distribution_tenant",
	"aws_cloudfront_function",
	"aws_cloudfront_key_value_store",
	"aws_cloudfront_multitenant_distribution",
	"aws_cloudfront_trust_store",
	"aws_cloudfront_vpc_origin",
}

var untaggableRoute53Cloudfront = []string{
	// Registry-ratified Route53 remainder and CloudFront batch (#40,
	// #44, #65). aws_cloudfront_origin_access_control is untaggable and
	// inside the curated 68 - the same shape the messaging batch's
	// aws_sns_topic_subscription hit and deferred over, but issue #54
	// landed since then and live/LIMITATIONS.md's untaggable-admitted
	// span now derives from live/survey-full.json across the whole
	// registry-backed roster rather than the curated 68 intersected
	// with the admission table, so it joins this list rather than being
	// deferred. See live/e2e/estates/route53-cloudfront/README.md.
	"aws_route53_hosted_zone_dnssec",
	"aws_route53_key_signing_key",
	"aws_route53_zone_association",
	"aws_route53_resolver_firewall_rule",
	"aws_cloudfront_monitoring_subscription",
	"aws_cloudfront_realtime_log_config",
}

func init() {
	registerCohortStamp(taggableRoute53Cloudfront, untaggableRoute53Cloudfront, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified Route53 remainder and CloudFront batch (#40,
			// #44, #65). Taggable/untaggable per the real provider's documented
			// Argument Reference for each type; the eight untaggable rows are
			// exactly this batch's own "Untaggable types" list in
			// live/e2e/estates/route53-cloudfront/README.md.
			"aws_route53_health_check":                             taggedSchema("id", "type"),
			"aws_route53_hosted_zone_dnssec":                       untaggedSchema("id", "hosted_zone_id", "signing_status"),
			"aws_route53_key_signing_key":                          untaggedSchema("id", "hosted_zone_id", "name", "key_management_service_arn"),
			"aws_route53_zone_association":                         untaggedSchema("id", "zone_id", "vpc_id"),
			"aws_route53profiles_association":                      taggedSchema("id", "arn", "name", "profile_id", "resource_id"),
			"aws_route53profiles_profile":                          taggedSchema("id", "arn", "name"),
			"aws_route53recoverycontrolconfig_cluster":             taggedSchema("id", "arn", "name"),
			"aws_route53recoverycontrolconfig_control_panel":       taggedSchema("id", "arn", "name", "cluster_arn"),
			"aws_route53recoverycontrolconfig_safety_rule":         taggedSchema("id", "arn", "name", "control_panel_arn"),
			"aws_route53_resolver_endpoint":                        taggedSchema("id", "arn", "direction"),
			"aws_route53_resolver_firewall_domain_list":            taggedSchema("id", "arn", "name"),
			"aws_route53_resolver_firewall_rule":                   untaggedSchema("id", "name", "action", "firewall_rule_group_id", "firewall_domain_list_id", "priority"),
			"aws_route53_resolver_firewall_rule_group":             taggedSchema("id", "arn", "name"),
			"aws_route53_resolver_firewall_rule_group_association": taggedSchema("id", "arn", "name", "firewall_rule_group_id", "vpc_id", "priority"),
			"aws_route53_resolver_query_log_config":                taggedSchema("id", "arn", "name", "destination_arn"),
			"aws_route53_resolver_rule":                            taggedSchema("id", "arn", "domain_name", "rule_type"),
			"aws_route53_resolver_rule_association":                untaggedSchema("id", "resolver_rule_id", "vpc_id"),
			"aws_cloudfront_anycast_ip_list":                       taggedSchema("id", "arn", "name", "ip_count"),
			"aws_cloudfront_connection_function":                   taggedSchema("id", "arn", "name", "connection_function_code"),
			"aws_cloudfront_connection_group":                      taggedSchema("id", "arn", "name"),
			"aws_cloudfront_distribution":                          taggedSchema("id", "arn", "enabled"),
			"aws_cloudfront_distribution_tenant":                   taggedSchema("id", "arn", "name", "distribution_id"),
			"aws_cloudfront_function":                              taggedSchema("id", "arn", "name", "runtime", "code"),
			"aws_cloudfront_key_value_store":                       taggedSchema("id", "arn", "name"),
			"aws_cloudfront_monitoring_subscription":               untaggedSchema("id", "distribution_id"),
			"aws_cloudfront_multitenant_distribution":              taggedSchema("id", "arn", "enabled"),
			"aws_cloudfront_origin_access_control":                 untaggedSchema("id", "arn", "name", "origin_access_control_origin_type", "signing_behavior", "signing_protocol"),
			"aws_cloudfront_realtime_log_config":                   untaggedSchema("id", "arn", "name", "sampling_rate"),
			"aws_cloudfront_trust_store":                           taggedSchema("id", "arn", "name"),
			"aws_cloudfront_vpc_origin":                            taggedSchema("id", "arn"),
		})
	})
}
