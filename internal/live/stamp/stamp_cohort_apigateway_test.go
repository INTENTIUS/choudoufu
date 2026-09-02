// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The apigateway cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableApigateway = []string{
	// Registry-ratified API Gateway v1/v2 batch (#40, #44, issue #65).
	"aws_api_gateway_api_key",
	"aws_api_gateway_client_certificate",
	"aws_api_gateway_domain_name",
	"aws_api_gateway_domain_name_access_association",
	"aws_api_gateway_rest_api",
	"aws_api_gateway_stage",
	"aws_api_gateway_usage_plan",
	"aws_api_gateway_vpc_link",
	"aws_apigatewayv2_api",
	"aws_apigatewayv2_domain_name",
	"aws_apigatewayv2_stage",
	"aws_apigatewayv2_vpc_link",
	// Fold-child batch (issue #68): the two new APS parents admitted
	// solely so the three APS fold-children below have something to key
	// on. See live/e2e/estates/aps/README.md.
	"aws_prometheus_workspace",
	"aws_prometheus_scraper",
}

var untaggableApigateway = []string{
	// Registry-ratified API Gateway v1/v2 batch (#40, #44, issue #65):
	// nine types with no tags argument at all, confirmed against the
	// provider's documented Argument Reference for each. See
	// live/e2e/estates/apigateway/README.md, "Untaggable types".
	"aws_api_gateway_base_path_mapping",
	"aws_api_gateway_documentation_version",
	"aws_api_gateway_gateway_response",
	"aws_api_gateway_method",
	"aws_api_gateway_model",
	"aws_api_gateway_rest_api_policy",
	"aws_api_gateway_usage_plan_key",
	// Fold-child batch (issue #68): all seven carry no tags argument,
	// confirmed against each type's own Argument Reference. See
	// live/e2e/estates/apigateway/README.md and
	// live/e2e/estates/aps/README.md, both "Untaggable types".
	"aws_api_gateway_integration",
	"aws_api_gateway_integration_response",
	"aws_api_gateway_method_response",
	"aws_api_gateway_method_settings",
	"aws_prometheus_alert_manager_definition",
	"aws_prometheus_query_logging_configuration",
	"aws_prometheus_scraper_logging_configuration",
}

func init() {
	registerCohortStamp(taggableApigateway, untaggableApigateway, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified API Gateway v1/v2 batch (#40, #44, issue #65).
			// Taggable/untaggable per the real provider's documented Argument
			// Reference for each type: aws_api_gateway_account,
			// _base_path_mapping, _documentation_version, _gateway_response,
			// _method, _model, _rest_api_policy, _usage_plan_key and
			// aws_apigatewayv2_routing_rule carry no tags argument at all.
			"aws_api_gateway_account":                        untaggedSchema("id"),
			"aws_api_gateway_api_key":                        taggedSchema("id", "name", "value"),
			"aws_api_gateway_base_path_mapping":              untaggedSchema("id", "api_id", "domain_name", "base_path"),
			"aws_api_gateway_client_certificate":             taggedSchema("id", "description"),
			"aws_api_gateway_documentation_version":          untaggedSchema("id", "rest_api_id", "version"),
			"aws_api_gateway_domain_name":                    taggedSchema("id", "domain_name"),
			"aws_api_gateway_domain_name_access_association": taggedSchema("id", "arn", "domain_name_arn"),
			"aws_api_gateway_gateway_response":               untaggedSchema("id", "rest_api_id", "response_type"),
			// Fold-child batch (issue #68): all four carry no tags argument,
			// confirmed against each type's own Argument Reference.
			"aws_api_gateway_integration":          untaggedSchema("rest_api_id", "resource_id", "http_method", "type"),
			"aws_api_gateway_integration_response": untaggedSchema("rest_api_id", "resource_id", "http_method", "status_code"),
			"aws_api_gateway_method":               untaggedSchema("id", "rest_api_id", "resource_id", "http_method"),
			"aws_api_gateway_method_response":      untaggedSchema("rest_api_id", "resource_id", "http_method", "status_code"),
			"aws_api_gateway_method_settings":      untaggedSchema("rest_api_id", "stage_name", "method_path"),
			"aws_api_gateway_model":                untaggedSchema("id", "rest_api_id", "name"),
			"aws_api_gateway_rest_api":             taggedSchema("id", "arn", "name"),
			"aws_api_gateway_rest_api_policy":      untaggedSchema("id", "rest_api_id", "policy"),
			"aws_api_gateway_stage":                taggedSchema("id", "arn", "rest_api_id", "stage_name", "deployment_id"),
			"aws_api_gateway_usage_plan":           taggedSchema("id", "name"),
			"aws_api_gateway_usage_plan_key":       untaggedSchema("id", "usage_plan_id", "key_id", "key_type"),
			"aws_api_gateway_vpc_link":             taggedSchema("id", "name", "target_arns"),
			"aws_apigatewayv2_api":                 taggedSchema("id", "arn", "name", "protocol_type"),
			"aws_apigatewayv2_domain_name":         taggedSchema("id", "domain_name"),
			"aws_apigatewayv2_routing_rule":        untaggedSchema("id", "domain_name", "action", "condition"),
			"aws_apigatewayv2_stage":               taggedSchema("id", "arn", "api_id", "name"),
			"aws_apigatewayv2_vpc_link":            taggedSchema("id", "name", "security_group_ids", "subnet_ids"),
			// Fold-child batch (issue #68): the two new APS parents are
			// taggable (ordinary marker path); the three fold-children keyed on
			// them carry no tags argument at all, confirmed against each
			// type's own Argument Reference.
			"aws_prometheus_workspace":                     taggedSchema("id", "arn"),
			"aws_prometheus_scraper":                       taggedSchema("id", "arn", "scrape_configuration"),
			"aws_prometheus_alert_manager_definition":      untaggedSchema("workspace_id", "definition"),
			"aws_prometheus_query_logging_configuration":   untaggedSchema("workspace_id"),
			"aws_prometheus_scraper_logging_configuration": untaggedSchema("scraper_id"),
		})
	})
}
