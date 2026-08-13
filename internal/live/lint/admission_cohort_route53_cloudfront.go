// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesRoute53Cloudfront is the route53-cloudfront cohort's slice of [admittedTypesV0]:
// the types the route53-cloudfront ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesRoute53Cloudfront = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): fourth batch, Route53
	// ---- remainder and CloudFront. Same tools/row-gen pipeline as the
	// ---- earlier batches, cross-checked against the AWS provider's
	// ---- documented import behaviour (its own Argument/Attribute/Import
	// ---- sections, fetched from the pinned v6.58.0 tag) and, where
	// ---- row-gen's registry-only evidence was silent on recoverability,
	// ---- against live/tag-verbs.json (which AWS API each service's
	// ---- generic tagging operation actually covers) and
	// ---- live/survey-full.json's mechanical per-type signals — not
	// ---- accepted on row-gen's classification alone. See
	// ---- internal/live/identity/table.go for the per-type evidence and
	// ---- for the rejected and deferred proposals. Cohort estate:
	// ---- live/e2e/estates/route53-cloudfront.
	"aws_route53_health_check":                             {},
	"aws_route53_hosted_zone_dnssec":                       {},
	"aws_route53_key_signing_key":                          {},
	"aws_route53_zone_association":                         {},
	"aws_route53profiles_association":                      {},
	"aws_route53profiles_profile":                          {},
	"aws_route53recoverycontrolconfig_cluster":             {},
	"aws_route53recoverycontrolconfig_control_panel":       {},
	"aws_route53recoverycontrolconfig_safety_rule":         {},
	"aws_route53_resolver_endpoint":                        {},
	"aws_route53_resolver_firewall_domain_list":            {},
	"aws_route53_resolver_firewall_rule":                   {},
	"aws_route53_resolver_firewall_rule_group":             {},
	"aws_route53_resolver_firewall_rule_group_association": {},
	"aws_route53_resolver_query_log_config":                {},
	"aws_route53_resolver_rule":                            {},
	"aws_route53_resolver_rule_association":                {},
	"aws_cloudfront_anycast_ip_list":                       {},
	"aws_cloudfront_connection_function":                   {},
	"aws_cloudfront_connection_group":                      {},
	"aws_cloudfront_distribution":                          {},
	"aws_cloudfront_distribution_tenant":                   {},
	"aws_cloudfront_function":                              {},
	"aws_cloudfront_key_value_store":                       {},
	"aws_cloudfront_monitoring_subscription":               {},
	"aws_cloudfront_multitenant_distribution":              {},
	"aws_cloudfront_origin_access_control":                 {},
	"aws_cloudfront_realtime_log_config":                   {},
	"aws_cloudfront_trust_store":                           {},
	"aws_cloudfront_vpc_origin":                            {},
}

func init() { registerCohortAdmitted(admittedTypesRoute53Cloudfront) }
