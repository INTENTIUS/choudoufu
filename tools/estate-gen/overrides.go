// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverride is the residual, hand-written surface issue #56 asks this
// generator to keep "visible and rare": a provider-side requirement that
// never reaches configschema.Attribute.Required at all, because the AWS
// provider enforces it through plan-time validation (ExactlyOneOf,
// RequiredWith, a ValidateFunc that checks a string's shape) rather than
// through the wire schema fillBlock reads. Nothing in
// GetProviderSchemaResponse names these constraints - they were found only
// by running the generic pass's output through `terraform validate` and
// reading what it refused - so every entry below cites the validate error
// it exists to fix, not a schema field.
type typeOverride struct {
	// Reasons is recorded verbatim in the generated README's provenance
	// table ("overrides: ...") for this type.
	Reasons []string

	// NeedsIAMRole tells planCohort this type needs the shared
	// aws_iam_role support resource even though isRoleArg does not fire
	// on any of its schema-Required argument names (aws_lambda_function's
	// own "role" argument already triggers it on its own; this exists for
	// a type whose role dependency Apply adds by hand, inside an optional
	// block the generic required-only pass never visits).
	NeedsIAMRole bool

	// Apply runs after the generic required-only pass (and after
	// iamRoleRefExpr and identityArgName have already filled in every
	// argument the schema or the identity table account for): it adds or
	// replaces whatever the provider needs in practice that the schema
	// alone does not say. SetAttributeRaw on an argument the generic pass
	// already set replaces its value in place, at the position the
	// generic pass gave it; every other call appends at the end of the
	// block, which is why README provenance calls these out by name
	// rather than leaving them to blend in.
	Apply func(g *generator, body *hclwrite.Body, addr resourceAddr)
}

// typeOverrides is keyed by provider-local type name. Empty for every type
// the generic pass alone renders validate-clean; the two cohorts this
// generator has actually been run against (lambda, s3) are exactly what
// populated this table - see the estate-gen package doc comment and the
// issue #56 final report for the empirical trail (each entry's Reasons
// string is the `terraform validate` error the override exists to silence).
var typeOverrides = map[string]typeOverride{
	"aws_iam_role": {
		Reasons: []string{
			`schema requires "assume_role_policy" as a plain string, but the provider validates it is well-formed JSON (validate: "\"assume_role_policy\" contains an invalid JSON"); the generic string placeholder is not JSON`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("assume_role_policy", exprTokens(fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "%s.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })`, g.cohort)))
		},
	},
	"aws_lambda_code_signing_config": {
		Reasons: []string{
			`allowed_publishers.signing_profile_version_arns is a required set of strings, but the provider validates each element is a well-formed ARN (validate: "is an invalid ARN"); the generic placeholder string is not one`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "allowed_publishers" {
					blk.Body().SetAttributeRaw("signing_profile_version_arns", exprTokens(fmt.Sprintf(
						`["arn:aws:signer:us-east-1:000000000000:/signing-profiles/tofu_%s_cohort/1a2b3c4d5e"]`, g.cohort)))
				}
			}
		},
	},
	"aws_lambda_function": {
		Reasons: []string{
			`schema requires only function_name and role; the provider also requires exactly one of filename/image_uri/s3_bucket (validate: "one of ... must be specified"), and image_uri requires package_type = "Image"`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("package_type", exprTokens(`"Image"`))
			body.SetAttributeRaw("image_uri", exprTokens(fmt.Sprintf(
				`"000000000000.dkr.ecr.us-east-1.amazonaws.com/tofu-%s-cohort-app:latest"`, g.cohort)))
		},
	},
	"aws_lambda_event_source_mapping": {
		Reasons: []string{
			`schema requires only function_name; the provider also requires exactly one of event_source_arn/self_managed_event_source (validate: "one of ... must be specified"), and event_source_arn requires starting_position`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("event_source_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:dynamodb:us-east-1:000000000000:table/tofu-%s-cohort-events/stream/2026-01-01T00:00:00.000"`, g.cohort)))
			body.SetAttributeRaw("starting_position", exprTokens(`"LATEST"`))
		},
	},
	"aws_lambda_layer_version": {
		Reasons: []string{
			`schema requires only layer_name; the provider also requires one of filename/s3_bucket (validate: "one of ... must be specified"), and s3_bucket requires s3_key; compatible_runtimes is optional in the schema but left empty renders a layer no function could ever reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("s3_bucket", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-artifacts"`, g.cohort)))
			body.SetAttributeRaw("s3_key", exprTokens(`"layers/app.zip"`))
			body.SetAttributeRaw("compatible_runtimes", exprTokens(`["python3.13"]`))
		},
	},
	"aws_lambda_capacity_provider": {
		Reasons: []string{
			`schema requires only name; vpc_config and permissions_config are both optional blocks, but the provider requires both in practice (validate: "Missing required argument") - permissions_config needs the shared aws_iam_role's ARN, which is why this type also sets NeedsIAMRole`,
		},
		NeedsIAMRole: true,
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			vpc := body.AppendNewBlock("vpc_config", nil)
			vpc.Body().SetAttributeRaw("subnet_ids", exprTokens(`["subnet-0123456789abcdef0"]`))
			vpc.Body().SetAttributeRaw("security_group_ids", exprTokens(`["sg-0123456789abcdef0"]`))

			perm := body.AppendNewBlock("permissions_config", nil)
			ref, ok := g.iamRoleRefExpr()
			if !ok {
				ref = `"placeholder"`
			}
			perm.Body().SetAttributeRaw("capacity_provider_operator_role_arn", exprTokens(ref))
		},
	},
	"aws_s3_bucket_policy": {
		Reasons: []string{
			`schema requires "policy" as a plain string, but the provider validates it is well-formed JSON (validate: "\"policy\" contains an invalid JSON"); the generic string placeholder is not JSON`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			// The bucket's own "arn" attribute, not the identity attrName
			// parentRef returns (which names the bucket-argument value, the
			// bucket's name - the policy's Resource element needs the ARN).
			// resourceExpr is the Resource element's HCL source: an
			// interpolation of the sibling bucket's arn attribute when this
			// run is also rendering aws_s3_bucket, or a literal placeholder
			// ARN otherwise (a cohort that admits aws_s3_bucket_policy
			// without aws_s3_bucket, which none of this generator's own
			// cohorts do).
			resourceExpr := `"arn:aws:s3:::tofu-` + g.cohort + `-cohort-bucket/*"`
			if parent, _, ok := g.parentRef(addr.Type, "bucket"); ok {
				resourceExpr = fmt.Sprintf(`"${%s.arn}/*"`, parent)
			}
			body.SetAttributeRaw("policy", exprTokens(fmt.Sprintf(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = "*"
      Action    = "s3:GetObject"
      Resource  = %s
    }]
  })`, resourceExpr)))
		},
	},
	"aws_s3_bucket_versioning": {
		Reasons: []string{
			`versioning_configuration is a required block, but its "status" argument has no default the generic pass can infer - set to the provider's documented enum member "Enabled"`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "versioning_configuration" {
					blk.Body().SetAttributeRaw("status", exprTokens(`"Enabled"`))
				}
			}
		},
	},
	"aws_s3_bucket_server_side_encryption_configuration": {
		Reasons: []string{
			`rule is a required block, but its nested apply_server_side_encryption_by_default block is itself optional in the schema while the provider requires it in practice (validate: "Missing required argument") along with its required sse_algorithm enum member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() == "rule" {
					def := blk.Body().AppendNewBlock("apply_server_side_encryption_by_default", nil)
					def.Body().SetAttributeRaw("sse_algorithm", exprTokens(`"AES256"`))
				}
			}
		},
	},
	"aws_s3_bucket_lifecycle_configuration": {
		Reasons: []string{
			`rule is optional in the schema (MinItems 0) but the provider requires at least one (validate: "Missing required argument"), and each rule requires an id and a status enum member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			rule := body.AppendNewBlock("rule", nil)
			rule.Body().SetAttributeRaw("id", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-expire"`, g.cohort)))
			rule.Body().SetAttributeRaw("status", exprTokens(`"Enabled"`))
		},
	},

	// ---- API Gateway v1/v2 batch (issue #65) -------------------------------
	//
	// Several types below also correct a generic-pass mis-wiring, not a
	// validate failure: parentRef (gen.go) only ever proposes a sibling
	// whose own identity is a single self-named argument (identityArgName),
	// which by construction excludes every server-assigned type in this
	// batch (aws_api_gateway_rest_api, aws_api_gateway_usage_plan,
	// aws_api_gateway_api_key, aws_apigatewayv2_api) - their identity is not
	// one configuration argument to name a placeholder for.
	// aws_api_gateway_rest_api_policy's own identity happens to be the
	// single argument "rest_api_id" (the same argument name every REST API
	// child needs), so it is the only candidate parentRef finds for that
	// argument, and every REST API child below silently wired to it instead
	// of to the real REST API - terraform validate does not catch this
	// (both are syntactically valid string attributes), so each is
	// corrected here to the resource that is actually the coverage row's
	// live parent. aws_apigatewayv2_routing_rule has the same shape for a
	// different reason: both API Gateway generations' domain name types
	// self-identify by the same argument name ("domain_name"), and
	// parentRef's alphabetic tiebreak has no way to tell v1 and v2 apart.
	"aws_api_gateway_base_path_mapping": {
		Reasons: []string{
			`api_id has no identity-table candidate to auto-wire from (aws_api_gateway_rest_api is server-assigned, so parentRef never proposes it); wired to the REST API this cohort renders`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if restAPI, ok := g.byType["aws_api_gateway_rest_api"]; ok {
				body.SetAttributeRaw("api_id", exprTokens(fmt.Sprintf("%s.id", restAPI)))
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
	"aws_api_gateway_usage_plan_key": {
		Reasons: []string{
			`usage_plan_id and key_id have no identity-table candidates because aws_api_gateway_usage_plan and aws_api_gateway_api_key are both server-assigned (parentRef never proposes a server-assigned sibling, the same gap as the REST API children above); wired to the two sibling coverage rows this cohort renders, and key_type set to its one documented value`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if plan, ok := g.byType["aws_api_gateway_usage_plan"]; ok {
				body.SetAttributeRaw("usage_plan_id", exprTokens(fmt.Sprintf("%s.id", plan)))
			}
			if key, ok := g.byType["aws_api_gateway_api_key"]; ok {
				body.SetAttributeRaw("key_id", exprTokens(fmt.Sprintf("%s.id", key)))
			}
			body.SetAttributeRaw("key_type", exprTokens(`"API_KEY"`))
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
	"aws_apigatewayv2_stage": {
		Reasons: []string{
			`api_id has no identity-table candidate to auto-wire from (aws_apigatewayv2_api is server-assigned, the same gap as the REST API children above); wired to the v2 API this cohort renders`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if v2api, ok := g.byType["aws_apigatewayv2_api"]; ok {
				body.SetAttributeRaw("api_id", exprTokens(fmt.Sprintf("%s.id", v2api)))
			}
		},
	},
}
