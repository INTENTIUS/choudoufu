// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesApigateway is the apigateway cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesApigateway = map[string]typeOverride{
	"aws_api_gateway_base_path_mapping": {
		Reasons: []string{
			`domain_name is not a parentRef candidate: it is one of two components of this type's own composite identity (domain_name + base_path, internal/live/identity/table.go), and "domain_name" is one of the argument names more than one admitted type self-identifies by - both API Gateway generations self-identify their own domain name types by it (issue #231) - so parentRef correctly refuses to guess which one this v1 type means, rather than making the same alphabetic-tiebreak mistake aws_apigatewayv2_routing_rule's own override below documents. This type is unambiguously v1 by its own name, unlike routing_rule; wired to the v1 aws_api_gateway_domain_name this cohort renders instead of the generic placeholder the refusal leaves behind, which names no domain aws_api_gateway_base_path_mapping's real API call would find. stage_name is Optional in the wire schema, so the required-only pass never visits it; the seed-derived example reference that used to supply it is skipped once a type carries any override entry (this file's own header comment on seedFromExample's suppression rule), so this override sets it by hand too, matching what the documented example wired it to.`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if domain, ok := g.byType["aws_api_gateway_domain_name"]; ok {
				body.SetAttributeRaw("domain_name", exprTokens(fmt.Sprintf("%s.domain_name", domain)))
			}
			if stage, ok := g.byType["aws_api_gateway_stage"]; ok {
				body.SetAttributeRaw("stage_name", exprTokens(fmt.Sprintf("%s.stage_name", stage)))
			}
		},
	},
	"aws_api_gateway_documentation_version": {
		Reasons: []string{
			`rest_api_id was mis-wired to aws_api_gateway_rest_api_policy.app (parentRef's only candidate whose identity self-names "rest_api_id" - the real parent, aws_api_gateway_rest_api, is server-assigned and never a parentRef candidate); corrected to the REST API this cohort renders`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
			}
			body.SetAttributeRaw("version", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-docs-v1"`, g.cohort)))
		},
	},
	"aws_api_gateway_gateway_response": {
		Reasons: []string{
			`rest_api_id mis-wired the same way as aws_api_gateway_documentation_version above; response_type is a fixed enum the provider validates server-side (terraform validate does not catch "placeholder", but it is not one of the documented values), set to the provider docs' own example value`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
			}
			body.SetAttributeRaw("response_type", exprTokens(`"UNAUTHORIZED"`))
		},
	},
	"aws_api_gateway_method": {
		Reasons: []string{
			`rest_api_id mis-wired the same way as aws_api_gateway_documentation_version above; resource_id has no identity-table candidate at all because aws_api_gateway_resource is not admitted this batch (rejected), and that type cannot be added as supporting infrastructure either - every fixture resource, coverage or supporting, has to be an admitted type (TestAdmissionTableCoversEstate, TestTableCoversFixtureTypes) - so this method attaches to the REST API's own root_resource_id instead of a child resource, which needs no unadmitted type at all`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
				body.SetAttributeRaw("resource_id", exprTokens(fmt.Sprintf("%s.root_resource_id", restAPI)))
			}
			body.SetAttributeRaw("http_method", exprTokens(`"GET"`))
			body.SetAttributeRaw("authorization", exprTokens(`"NONE"`))
		},
	},
	// The three fold-children below (issue #68) all key on the same
	// (rest_api_id, resource_id, http_method) triple aws_api_gateway_method
	// above already does, since each duplicates the method's own composite
	// identity verbatim (internal/live/identity/table.go's "Fold-children"
	// section comment) - parentRef mis-wires rest_api_id the same way as
	// aws_api_gateway_documentation_version above (its only same-named
	// candidate is aws_api_gateway_rest_api_policy.app, whose own identity
	// happens to self-name "rest_api_id" too), and has no candidate at all
	// for resource_id, http_method or status_code, the same gap
	// aws_api_gateway_method's own override closes.
	"aws_api_gateway_integration": {
		Reasons: []string{
			`rest_api_id/resource_id/http_method mis-wired or left as the generic placeholder the same way aws_api_gateway_method's were, for the same reason - corrected to the same REST API root resource and GET method aws_api_gateway_method.app already targets, so this integration is the method's own; type is a fixed enum (validate: "expected type to be one of [...]"), set to MOCK, the shape floci's PutIntegration/GetIntegration round-trip cleanly (verified by hand)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
				body.SetAttributeRaw("resource_id", exprTokens(fmt.Sprintf("%s.root_resource_id", restAPI)))
			}
			body.SetAttributeRaw("http_method", exprTokens(`"GET"`))
			body.SetAttributeRaw("type", exprTokens(`"MOCK"`))
		},
	},
	"aws_api_gateway_integration_response": {
		Reasons: []string{
			`rest_api_id/resource_id/http_method mis-wired or left as the generic placeholder the same way aws_api_gateway_integration's were above, and for the same reason - status_code is left schema-Required-but-unvalidated by terraform validate, but the provider expects a real HTTP status string, set to the aws_api_gateway_method_response.app row below's own value so the two agree`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
				body.SetAttributeRaw("resource_id", exprTokens(fmt.Sprintf("%s.root_resource_id", restAPI)))
			}
			body.SetAttributeRaw("http_method", exprTokens(`"GET"`))
			body.SetAttributeRaw("status_code", exprTokens(`"200"`))
		},
	},
	"aws_api_gateway_method_response": {
		Reasons: []string{
			`rest_api_id/resource_id/http_method/status_code, the same corrections as aws_api_gateway_integration_response above and for the same reason`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
				body.SetAttributeRaw("resource_id", exprTokens(fmt.Sprintf("%s.root_resource_id", restAPI)))
			}
			body.SetAttributeRaw("http_method", exprTokens(`"GET"`))
			body.SetAttributeRaw("status_code", exprTokens(`"200"`))
		},
	},
	"aws_api_gateway_method_settings": {
		Reasons: []string{
			`rest_api_id mis-wired the same way as aws_api_gateway_documentation_version above; stage_name has no identity-table candidate to auto-wire from (aws_api_gateway_stage's own identity is the two-component rest_api_id/stage_name pair, not a single self-named argument, so identityArgName never fires on it, the same gap aws_api_gateway_method_settings's own fold parent has), wired to the stage this cohort renders instead of the generic placeholder; method_path left as the generic placeholder is not a real method path, set to the */* wildcard the provider docs use for "every method of every resource in the stage"`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
			}
			if stage, ok := g.byType["aws_api_gateway_stage"]; ok {
				body.SetAttributeRaw("stage_name", exprTokens(fmt.Sprintf("%s.stage_name", stage)))
			}
			body.SetAttributeRaw("method_path", exprTokens(`"*/*"`))
		},
	},
	"aws_api_gateway_model": {
		Reasons: []string{
			`rest_api_id mis-wired the same way as aws_api_gateway_documentation_version above; content_type and schema left as the generic placeholder would not be valid JSON, set to a minimal real value`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
			}
			body.SetAttributeRaw("content_type", exprTokens(`"application/json"`))
			body.SetAttributeRaw("schema", exprTokens(`jsonencode({})`))
		},
	},
	"aws_api_gateway_stage": {
		Reasons: []string{
			`rest_api_id mis-wired the same way as aws_api_gateway_documentation_version above; deployment_id has no identity-table candidate because aws_api_gateway_deployment is not admitted this batch (rejected), so it is left as the generic placeholder string - a stage is its own coverage row and the deployment it names existing is not this type's identity concern`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("rest_api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
			}
		},
	},
	"aws_api_gateway_rest_api_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON"), the same shape as aws_s3_bucket_policy above`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			resourceExpr := fmt.Sprintf(`"arn:aws:execute-api:us-east-1:000000000000:tofu-%s-cohort-placeholder/*"`, g.cohort)
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				resourceExpr = fmt.Sprintf(`"${%s.execution_arn}/*"`, restAPI)
			}
			body.SetAttributeRaw("policy", exprTokens(fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "execute-api:Invoke"
      Resource  = %s
    }]
  })`, resourceExpr)))
		},
	},
	"aws_api_gateway_domain_name_access_association": {
		Reasons: []string{
			`access_association_source_type is a fixed enum (validate: "Invalid String Enum Value", valid values: VPCE); domain_name_arn is validated as a well-formed ARN (validate: "Invalid ARN Value") - both need real-shaped values, not the generic placeholder`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("access_association_source_type", exprTokens(`"VPCE"`))
			body.SetAttributeRaw("access_association_source", exprTokens(`"vpce-0123456789abcdef0"`))
			body.SetAttributeRaw("domain_name_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:apigateway:us-east-1::/domainnames/tofu-%s-cohort-api-gateway-domain-name"`, g.cohort)))
		},
	},
	"aws_apigatewayv2_api": {
		Reasons: []string{
			`protocol_type is a fixed enum (validate: "expected protocol_type to be one of [WEBSOCKET HTTP]"), not the generic placeholder`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("protocol_type", exprTokens(`"HTTP"`))
		},
	},
	"aws_apigatewayv2_domain_name": {
		Reasons: []string{
			`domain_name_configuration's three required arguments are each validated: certificate_arn as a well-formed ARN (validate: "invalid ARN: arn: invalid prefix"), endpoint_type and security_policy as fixed enums (validate: "expected ... to be one of [...]")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "domain_name_configuration" {
					blk.Body().SetAttributeRaw("certificate_arn", exprTokens(fmt.Sprintf(
						`"arn:aws:acm:us-east-1:000000000000:certificate/tofu-%s-cohort-placeholder"`, g.cohort)))
					blk.Body().SetAttributeRaw("endpoint_type", exprTokens(`"REGIONAL"`))
					blk.Body().SetAttributeRaw("security_policy", exprTokens(`"TLS_1_2"`))
				}
			}
		},
	},
	// The three APS fold-children below (issue #68) all need their parent
	// reference wired by hand, the same reason aws_api_gateway_base_path_mapping
	// above does: aws_prometheus_workspace and aws_prometheus_scraper are
	// both server-assigned, so identityArgName never fires on them and
	// parentRef has no candidate to propose - even though each child's own
	// identity happens to self-name the same argument
	// (workspace_id/scraper_id) its parent's id lives under, valueExpr's
	// own-identity tier (3) fires first and fills in a placeholder name
	// instead of a reference, the same shape as aws_s3_bucket_policy or
	// aws_sns_topic_policy would hit had their own parents been
	// server-assigned instead of client-named/account-derived.
	"aws_prometheus_alert_manager_definition": {
		Reasons: []string{
			`workspace_id left as a generic placeholder name instead of a reference (aws_prometheus_workspace is server-assigned, so parentRef never proposes it); wired to the workspace this cohort renders. definition is a required string the provider expects as YAML Alertmanager configuration; the generic placeholder is not valid YAML`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if ws, ok := g.byType["aws_prometheus_workspace"]; ok {
				body.SetAttributeRaw("workspace_id", exprTokens(fmt.Sprintf("%s.id", ws)))
			}
			body.SetAttributeRaw("definition", exprTokens(`<<-EOT
    route:
      receiver: default
    receivers:
      - name: default
  EOT
  `))
		},
	},
	"aws_prometheus_query_logging_configuration": {
		Reasons: []string{
			`workspace_id left as a generic placeholder name instead of a reference, the same reason and correction as aws_prometheus_alert_manager_definition above. destination is a required block the schema marks optional-in-shape but the provider requires present, and its own nested filters block is required in turn (validate: "Block destination[0].filters must have a configuration value")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if ws, ok := g.byType["aws_prometheus_workspace"]; ok {
				body.SetAttributeRaw("workspace_id", exprTokens(fmt.Sprintf("%s.id", ws)))
			}
			dest := body.AppendNewBlock("destination", nil)
			cwl := dest.Body().AppendNewBlock("cloudwatch_logs", nil)
			cwl.Body().SetAttributeRaw("log_group_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:logs:us-east-1:000000000000:log-group:/aws/prometheus/tofu-%s-cohort:*"`, g.cohort)))
			filters := dest.Body().AppendNewBlock("filters", nil)
			filters.Body().SetAttributeRaw("qsp_threshold", exprTokens(`0`))
		},
	},
	"aws_prometheus_scraper_logging_configuration": {
		Reasons: []string{
			`scraper_id left as a generic placeholder name instead of a reference (aws_prometheus_scraper is server-assigned, so parentRef never proposes it); wired to the scraper this cohort renders. logging_destination is a required block the schema marks optional-in-shape but the provider requires present`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if sc, ok := g.byType["aws_prometheus_scraper"]; ok {
				body.SetAttributeRaw("scraper_id", exprTokens(fmt.Sprintf("%s.id", sc)))
			}
			dest := body.AppendNewBlock("logging_destination", nil)
			cwl := dest.Body().AppendNewBlock("cloudwatch_logs", nil)
			cwl.Body().SetAttributeRaw("log_group_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:logs:us-east-1:000000000000:log-group:/aws/prometheus/scraper/tofu-%s-cohort:*"`, g.cohort)))
		},
	},
	"aws_prometheus_scraper": {
		Reasons: []string{
			`schema requires only scrape_configuration; the provider also requires the source and destination blocks (validate: "Missing required argument"), each with their own nested required arguments (an EKS-shaped source, an AMP workspace ARN destination) the generic pass has no schema signal for`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("scrape_configuration", exprTokens(`<<-EOT
    global:
      scrape_interval: 30s
    scrape_configs:
      - job_name: placeholder
  EOT
  `))
			src := body.AppendNewBlock("source", nil)
			eks := src.Body().AppendNewBlock("eks", nil)
			eks.Body().SetAttributeRaw("cluster_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:eks:us-east-1:000000000000:cluster/tofu-%s-cohort"`, g.cohort)))
			eks.Body().SetAttributeRaw("subnet_ids", exprTokens(`["subnet-0123456789abcdef0"]`))

			dest := body.AppendNewBlock("destination", nil)
			amp := dest.Body().AppendNewBlock("amp", nil)
			if ws, ok := g.byType["aws_prometheus_workspace"]; ok {
				amp.Body().SetAttributeRaw("workspace_arn", exprTokens(fmt.Sprintf("%s.arn", ws)))
			}
		},
	},
	"aws_apigatewayv2_routing_rule": {
		Reasons: []string{
			`domain_name was mis-wired to aws_api_gateway_domain_name.app (the v1 type) - both v1 and v2 domain name types self-identify by the same argument name, and parentRef's alphabetic tiebreak prefers "aws_api_gateway_domain_name" over "aws_apigatewayv2_domain_name" with no way to tell they are different API generations; corrected to the v2 domain name this type actually needs. action and condition are both required blocks the schema marks optional-in-shape but the provider requires present (validate: "Block action/condition must have a configuration value"), and priority must be 1-1000000 (validate: "must be between 1 and 1000000, got: 0")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if domain, ok := g.byType["aws_apigatewayv2_domain_name"]; ok {
				body.SetAttributeRaw("domain_name", exprTokens(fmt.Sprintf("%s.domain_name", domain)))
			}
			body.SetAttributeRaw("priority", exprTokens(`1`))

			action := body.AppendNewBlock("action", nil)
			invoke := action.Body().AppendNewBlock("invoke_api", nil)
			if v2api, ok := g.byType["aws_apigatewayv2_api"]; ok {
				invoke.Body().SetAttributeRaw("api_id", exprTokens(fmt.Sprintf("%s.id", v2api)))
			}
			if v2stage, ok := g.byType["aws_apigatewayv2_stage"]; ok {
				invoke.Body().SetAttributeRaw("stage", exprTokens(fmt.Sprintf("%s.name", v2stage)))
			}

			condition := body.AppendNewBlock("condition", nil)
			mbp := condition.Body().AppendNewBlock("match_base_paths", nil)
			mbp.Body().SetAttributeRaw("any_of", exprTokens(`["/"]`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesApigateway) }
