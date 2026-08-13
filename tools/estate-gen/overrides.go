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

	"aws_db_instance": {
		Reasons: []string{
			`schema requires only identifier and instance_class; the provider's create-time logic also requires allocated_storage, engine, username and one of password/password_wo/manage_master_user_password (validate does not catch any of these - they are enforced only once Create actually runs, confirmed by hand against floci during this batch's verification), and instance_class needs a real instance type, not an arbitrary string`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("instance_class", exprTokens(`"db.t3.micro"`))
			body.SetAttributeRaw("allocated_storage", exprTokens(`10`))
			body.SetAttributeRaw("engine", exprTokens(`"mysql"`))
			body.SetAttributeRaw("username", exprTokens(`"admin"`))
			body.SetAttributeRaw("password", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-pw"`, g.cohort)))
			body.SetAttributeRaw("skip_final_snapshot", exprTokens(`true`))
		},
	},
	"aws_rds_cluster_instance": {
		Reasons: []string{
			`schema requires identifier, cluster_identifier, engine and instance_class; the provider validates engine against the same fixed enum as aws_rds_cluster (validate: "expected engine to be one of [aurora-mysql aurora-postgresql mysql postgres]"), and the documented example sets it from the parent cluster's own engine argument rather than an independent literal - instance_class also needs a real instance type, not an arbitrary string`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			engineExpr := `"aurora-mysql"`
			if parent, ok := g.byType["aws_rds_cluster"]; ok {
				engineExpr = parent.String() + ".engine"
			}
			body.SetAttributeRaw("engine", exprTokens(engineExpr))
			body.SetAttributeRaw("instance_class", exprTokens(`"db.r4.large"`))
		},
	},

	// RDS batch (issue #65's ratification campaign). None of these six
	// types' Required arguments alone satisfy the provider's plan-time
	// validation, and none of the *_role_arn/*_arn arguments below trigger
	// planCohort's isRoleArg alias (which matches only "role" and the
	// "_role_arn" suffix, not the bare "role_arn" every RDS association type
	// happens to use) or a parentRef match (the associations' own argument
	// names - db_instance_identifier, db_cluster_identifier, db_proxy_name -
	// never exactly match another admitted type's identity-table argument
	// name in every case below), so every ARN and every intra-cohort parent
	// link here is a hand override rather than something the generic pass
	// or planCohort's curated aliases already reach.
	"aws_db_event_subscription": {
		Reasons: []string{
			`schema requires only name and sns_topic; the provider validates sns_topic is a well-formed ARN (validate: "is an invalid ARN"), and no aws_sns_topic is part of this cohort to reference`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("sns_topic", exprTokens(fmt.Sprintf(
				`"arn:aws:sns:us-east-1:000000000000:tofu-%s-cohort-events"`, g.cohort)))
		},
	},
	"aws_db_instance_role_association": {
		Reasons: []string{
			`schema requires db_instance_identifier, feature_name and role_arn; the provider validates role_arn is a well-formed ARN (validate: "is an invalid ARN"), and db_instance_identifier is a bare string the generic pass has no parentRef alias for (its own name differs from aws_db_instance's "identifier" identity argument), so it defaults to a placeholder that names no real instance in this cohort`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if parent, ok := g.byType["aws_db_instance"]; ok {
				body.SetAttributeRaw("db_instance_identifier", exprTokens(parent.String()+".identifier"))
			}
			body.SetAttributeRaw("role_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-role"`, g.cohort)))
		},
	},
	"aws_db_proxy": {
		Reasons: []string{
			`schema requires name, engine_family, role_arn and vpc_subnet_ids; the provider validates engine_family is one of MYSQL/POSTGRESQL/SQLSERVER (validate: "expected engine_family to be one of ...") and role_arn is a well-formed ARN (validate: "is an invalid ARN")`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine_family", exprTokens(`"MYSQL"`))
			body.SetAttributeRaw("role_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-role"`, g.cohort)))
		},
	},
	"aws_db_proxy_default_target_group": {
		Reasons: []string{
			`schema requires only db_proxy_name; the generic pass's parentRef alias does not fire because this type's own identity argument (db_proxy_name, per internal/live/identity/table.go) has the same name as its own Required argument, so it fills its own identity placeholder instead of referencing the sibling aws_db_proxy this batch's cohort also admits - db_proxy_name is set to aws_db_proxy.app's own "name" argument by hand instead`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if parent, ok := g.byType["aws_db_proxy"]; ok {
				body.SetAttributeRaw("db_proxy_name", exprTokens(parent.String()+".name"))
			}
		},
	},
	"aws_rds_cluster": {
		Reasons: []string{
			`schema requires cluster_identifier and engine; the provider validates engine against a fixed enum (validate: "expected engine to be one of [aurora-mysql aurora-postgresql mysql postgres]"; a second, independent validator on the same attribute also rejects it: "invalid value for engine (must begin with custom-)"); skip_final_snapshot also defaults to false, and the provider refuses a destroy without it or a final_snapshot_identifier (found only by exercising a destroy against floci during this batch's verification, not by validate)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine", exprTokens(`"aurora-mysql"`))
			body.SetAttributeRaw("skip_final_snapshot", exprTokens(`true`))
		},
	},
	"aws_rds_cluster_role_association": {
		Reasons: []string{
			`schema requires db_cluster_identifier, feature_name and role_arn; the provider validates role_arn is a well-formed ARN (validate: "is an invalid ARN"), and db_cluster_identifier is a bare string the generic pass has no parentRef alias for (its own name differs from aws_rds_cluster's "cluster_identifier" identity argument), so it defaults to a placeholder that names no real cluster in this cohort`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if parent, ok := g.byType["aws_rds_cluster"]; ok {
				body.SetAttributeRaw("db_cluster_identifier", exprTokens(parent.String()+".cluster_identifier"))
			}
			body.SetAttributeRaw("role_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:iam::000000000000:role/tofu-%s-cohort-role"`, g.cohort)))
		},
	},
	"aws_rds_custom_db_engine_version": {
		Reasons: []string{
			`schema requires engine, database_installation_files_s3_bucket_name, database_installation_files_s3_prefix and engine_version; the provider validates engine must begin with "custom-" (validate: "invalid value for engine (must begin with custom-)"), the same shape as aws_rds_cluster's engine but a disjoint enum`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine", exprTokens(`"custom-oracle-ee-cdb"`))
		},
	},
	"aws_rds_shard_group": {
		Reasons: []string{
			`schema requires db_cluster_identifier, db_shard_group_identifier and max_acu; db_cluster_identifier is a bare string the generic pass has no parentRef alias for (its own name differs from aws_rds_cluster's "cluster_identifier" identity argument), so it defaults to a placeholder that names no real cluster in this cohort - no provider-side validation catches the mismatch, but the fix keeps this cohort's shard group pointed at the real cluster it admits alongside it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if parent, ok := g.byType["aws_rds_cluster"]; ok {
				body.SetAttributeRaw("db_cluster_identifier", exprTokens(parent.String()+".cluster_identifier"))
			}
		},
	},
	"aws_rds_integration": {
		Reasons: []string{
			`schema requires integration_name, source_arn and target_arn; the provider validates both are well-formed ARNs (validate: "Invalid ARN Value") - source_arn references the cohort's own aws_rds_cluster, target_arn names a Redshift Serverless namespace no type in this cohort covers`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			sourceExpr := fmt.Sprintf(`"arn:aws:rds:us-east-1:000000000000:cluster:tofu-%s-cohort-cluster"`, g.cohort)
			if parent, ok := g.byType["aws_rds_cluster"]; ok {
				sourceExpr = parent.String() + ".arn"
			}
			body.SetAttributeRaw("source_arn", exprTokens(sourceExpr))
			body.SetAttributeRaw("target_arn", exprTokens(fmt.Sprintf(
				`"arn:aws:redshift-serverless:us-east-1:000000000000:namespace/tofu-%s-cohort-namespace"`, g.cohort)))
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
	"aws_dynamodb_resource_policy": {
		Reasons: []string{
			`schema requires "resource_arn" as a plain string, but the provider validates it is a well-formed ARN (validate: "The provided value cannot be parsed as an ARN"); the generic identity-argument placeholder ("tofu-<cohort>-cohort-...") is not one. schema also requires "policy" as a plain string, but the provider validates it is well-formed JSON (validate: "is not valid JSON string format"); the generic string placeholder is not JSON`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			tableARN := fmt.Sprintf(`"arn:aws:dynamodb:us-east-1:000000000000:table/tofu-%s-cohort-app"`, g.cohort)
			body.SetAttributeRaw("resource_arn", exprTokens(tableARN))
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
      Action    = "dynamodb:DescribeTable"
      Resource  = %s
    }]
  })`, tableARN)))
		},
	},
	"aws_elasticache_cluster": {
		Reasons: []string{
			`schema requires only cluster_id; the provider also requires exactly one of engine/replication_group_id (validate: "one of engine,replication_group_id must be specified") and, once engine is chosen, node_type, num_cache_nodes and parameter_group_name become required in practice too (validate: "Missing required argument"). engine is set to "memcached" rather than the more familiar "redis": AWS's CreateCacheCluster API (which this resource calls directly, unlike aws_elasticache_replication_group) only accepts the redis/valkey engines when joining an existing replication group, confirmed by floci's own emulation of that same real-AWS rule (apply: "InvalidParameterValue: Engine must be 'memcached'. For Redis/Valkey use CreateReplicationGroup.") — not a floci gap, the standalone-cluster shape genuinely requires memcached. cluster_id is also length-limited to 50 characters (validate: "expected length ... to be in the range (1 - 50)"), and this cohort's own name ("dynamodb-elasticache") makes the generic tofu-<cohort>-cohort-<type> placeholder 54 characters - shortened here to a value that still names the cohort and the type`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("cluster_id", exprTokens(`"tofu-ddb-ec-cluster"`))
			body.SetAttributeRaw("engine", exprTokens(`"memcached"`))
			body.SetAttributeRaw("node_type", exprTokens(`"cache.t3.micro"`))
			body.SetAttributeRaw("num_cache_nodes", exprTokens(`1`))
			body.SetAttributeRaw("parameter_group_name", exprTokens(`"default.memcached1.6"`))
		},
	},
	"aws_elasticache_replication_group": {
		Reasons: []string{
			`schema requires only replication_group_id; the provider also requires node_type in practice once no global_replication_group_id is set (validate: "\"node_type\" is required unless \"global_replication_group_id\" is set"), and engine defaults to redis but is set explicitly here for clarity. replication_group_id is also length-limited to 40 characters (validate: "expected length ... to be in the range (1 - 40)"), and this cohort's own name ("dynamodb-elasticache") makes the generic tofu-<cohort>-cohort-<type> placeholder 58 characters - shortened here to a value that still names the cohort and the type`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("replication_group_id", exprTokens(`"tofu-ddb-ec-replgrp"`))
			body.SetAttributeRaw("engine", exprTokens(`"redis"`))
			body.SetAttributeRaw("node_type", exprTokens(`"cache.t3.micro"`))
		},
	},
	"aws_elasticache_user": {
		Reasons: []string{
			`engine is a required argument the schema types as an unconstrained string, but the provider validates it against a closed enum (validate: "expected engine to be one of [\"redis\" \"valkey\"]"); the generic placeholder string is neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine", exprTokens(`"redis"`))
		},
	},
	"aws_elasticache_user_group": {
		Reasons: []string{
			`engine is a required argument the schema types as an unconstrained string, but the provider validates it against a closed enum (validate: "expected engine to be one of [\"redis\" \"valkey\"]"); the generic placeholder string is neither`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("engine", exprTokens(`"redis"`))
		},
	},
}
