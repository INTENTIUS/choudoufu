// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesApigateway is the apigateway cohort's slice of [admittedTypesV0]:
// the types the apigateway ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesApigateway = map[string]struct{}{
	// ---- Registry-ratified (#40, #44): fourth batch, API Gateway v1 and v2
	// ---- (issue #65). Same tools/row-gen pipeline as the earlier batches,
	// ---- cross-checked against live/import-grammar.json (the pinned
	// ---- v6.58.0 provider docs) and, for several composites, against the
	// ---- provider's Argument Reference and source directly — row-gen's own
	// ---- "needs hand separator" output only says a primaryIdentifier has
	// ---- more than one part, not whether every part is a configuration
	// ---- argument, and several of API Gateway's are not. See
	// ---- internal/live/identity/table.go for the per-type evidence and
	// ---- rejections, and live/e2e/estates/apigateway/README.md for the
	// ---- floci verification (including a provider crash reading
	// ---- aws_api_gateway_api_key and the re-confirmed aws_api_gateway_rest_api
	// ---- availability-waiter gap). 25 ApiGateway and 13 ApiGatewayV2 types
	// ---- were in row-gen's scope; 16 and 5 respectively ratify here.
	// ---- Cohort estate: live/e2e/estates/apigateway.
	"aws_api_gateway_account":                        {},
	"aws_api_gateway_api_key":                        {},
	"aws_api_gateway_base_path_mapping":              {},
	"aws_api_gateway_client_certificate":             {},
	"aws_api_gateway_documentation_version":          {},
	"aws_api_gateway_domain_name":                    {},
	"aws_api_gateway_domain_name_access_association": {},
	"aws_api_gateway_gateway_response":               {},
	"aws_api_gateway_method":                         {},
	"aws_api_gateway_model":                          {},
	"aws_api_gateway_rest_api":                       {},
	"aws_api_gateway_rest_api_policy":                {},
	"aws_api_gateway_stage":                          {},
	"aws_api_gateway_usage_plan":                     {},
	"aws_api_gateway_usage_plan_key":                 {},
	"aws_api_gateway_vpc_link":                       {},
	"aws_apigatewayv2_api":                           {},
	"aws_apigatewayv2_domain_name":                   {},
	"aws_apigatewayv2_routing_rule":                  {},
	"aws_apigatewayv2_stage":                         {},
	"aws_apigatewayv2_vpc_link":                      {},
	// ---- Fold-children (issue #68): declared property-children of an
	// ---- admitted parent, admitted the same way live/mapping.json's other
	// ---- ~170 "fold" rows will be as this path picks them up in future
	// ---- batches. See internal/live/identity/table.go's own "Fold-children
	// ---- (issue #68)" section comment for the per-type evidence and the
	// ---- two sub-shapes (API Gateway's four duplicate an already-admitted
	// ---- parent's own composite identity; the APS three key on a single
	// ---- parent argument, the same named-singleton-child shape
	// ---- aws_s3_bucket_policy and aws_sns_topic_policy already ratify).
	// ---- Cohort estate: live/e2e/estates/apigateway (the API Gateway four)
	// ---- and live/e2e/estates/aps (the APS three plus their two new
	// ---- parents, aws_prometheus_workspace and aws_prometheus_scraper,
	// ---- neither previously admitted).
	"aws_api_gateway_integration":                  {},
	"aws_api_gateway_integration_response":         {},
	"aws_api_gateway_method_response":              {},
	"aws_api_gateway_method_settings":              {},
	"aws_prometheus_workspace":                     {},
	"aws_prometheus_scraper":                       {},
	"aws_prometheus_alert_manager_definition":      {},
	"aws_prometheus_query_logging_configuration":   {},
	"aws_prometheus_scraper_logging_configuration": {},
}

func init() { registerCohortAdmitted(admittedTypesApigateway) }
