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

	// Registry-ratified data-plane batch (Kinesis, KinesisFirehose, Glue,
	// Athena; issue #65's recipe). Several of these types share the generic
	// pass's own "name" argument with other resources in this cohort that
	// self-identify by "name" too (aws_athena_data_catalog,
	// aws_athena_workgroup, aws_glue_classifier, aws_glue_crawler,
	// aws_glue_job, aws_glue_trigger, aws_kinesis_stream all have a
	// single-component, self-named identity per internal/live/identity's
	// table) - none of these six is a real parent of any of them, but
	// parentRef's own tiebreaker (see its doc comment) only guards the case
	// where selfType owns argName as its own identity too; every type below
	// has either no identity argument of its own (server-assigned) or an
	// account-derived composite identity (len(Components) != 1, so
	// identityArgName returns ok=false), so parentRef treats any same-named
	// candidate as fair game and picks the lexicographically-first one,
	// aws_athena_data_catalog, for all of them. Every "name" override below
	// exists only to give that argument this type's own placeholder instead
	// of a coincidental, meaningless cross-reference.
	"aws_athena_data_catalog": {
		Reasons: []string{
			`type is a required enum (validate: "expected type to be one of [...]"), and the generic string placeholder is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"GLUE"`))
			body.SetAttributeRaw("parameters", exprTokens(`{}`))
		},
	},
	"aws_glue_catalog_database": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment - so the generic pass's parentRef wiring points it at that unrelated resource instead of giving it its own placeholder`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_cohort_glue_catalog_database"`, g.cohort)))
		},
	},
	"aws_glue_catalog_table": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment; database_name is a plain required string argument the schema does not mark as any other type's identity, so parentRef never connects it to the sibling aws_glue_catalog_database (whose own identity is the account-derived catalog_id:name composite, not a single "database_name"-named argument) and the generic pass gives it an unrelated placeholder instead`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu_%s_cohort_glue_catalog_table"`, g.cohort)))
			if database, ok := g.byType["aws_glue_catalog_database"]; ok {
				body.SetAttributeRaw("database_name", exprTokens(fmt.Sprintf("%s.name", database)))
			}
		},
	},
	"aws_glue_connection": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-glue-connection"`, g.cohort)))
		},
	},
	"aws_glue_crawler": {
		Reasons: []string{
			`schema requires only database_name, name and role; the provider also requires exactly one of catalog_target/delta_target/dynamodb_target/hudi_target/iceberg_target/jdbc_target/mongodb_target/s3_target (validate: "one of ... must be specified"), and s3_target requires a path`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			s3 := body.AppendNewBlock("s3_target", nil)
			s3.Body().SetAttributeRaw("path", exprTokens(fmt.Sprintf(`"s3://tofu-%s-cohort-glue-crawler/"`, g.cohort)))
		},
	},
	"aws_glue_data_catalog_encryption_settings": {
		Reasons: []string{
			`data_catalog_encryption_settings.encryption_at_rest.catalog_encryption_mode is a required enum (validate: "expected catalog_encryption_mode to be one of [...]"), and the generic string placeholder is not a member`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			for _, blk := range body.Blocks() {
				if blk.Type() != "data_catalog_encryption_settings" {
					continue
				}
				for _, inner := range blk.Body().Blocks() {
					if inner.Type() == "encryption_at_rest" {
						inner.Body().SetAttributeRaw("catalog_encryption_mode", exprTokens(`"DISABLED"`))
					}
				}
			}
		},
	},
	"aws_glue_job": {
		Reasons: []string{
			`role_arn does not match isRoleArg's "role"/"*_role_arn" alias (it is exactly "role_arn", one underscore short), so the generic pass leaves it as an unparseable placeholder string (validate: "is an invalid ARN"); command.script_location has no schema default and an empty string is not a valid S3 or local path`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if ref, ok := g.iamRoleRefExpr(); ok {
				body.SetAttributeRaw("role_arn", exprTokens(ref))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "command" {
					blk.Body().SetAttributeRaw("script_location", exprTokens(fmt.Sprintf(
						`"s3://tofu-%s-cohort-glue-job/script.py"`, g.cohort)))
				}
			}
		},
	},
	"aws_glue_ml_transform": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment; role_arn does not match isRoleArg's alias, the same gap as aws_glue_job above; parameters.transform_type is a required enum (validate: "expected transform_type to be one of [...]") the generic placeholder does not satisfy`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-glue-ml-transform"`, g.cohort)))
			if ref, ok := g.iamRoleRefExpr(); ok {
				body.SetAttributeRaw("role_arn", exprTokens(ref))
			}
			for _, blk := range body.Blocks() {
				if blk.Type() == "parameters" {
					blk.Body().SetAttributeRaw("transform_type", exprTokens(`"FIND_MATCHES"`))
				}
			}
			if database, ok := g.byType["aws_glue_catalog_database"]; ok {
				if table, ok := g.byType["aws_glue_catalog_table"]; ok {
					for _, blk := range body.Blocks() {
						if blk.Type() == "input_record_tables" {
							blk.Body().SetAttributeRaw("database_name", exprTokens(fmt.Sprintf("%s.name", database)))
							blk.Body().SetAttributeRaw("table_name", exprTokens(fmt.Sprintf("%s.name", table)))
						}
					}
				}
			}
		},
	},
	"aws_glue_trigger": {
		Reasons: []string{
			`type is a required enum (validate: "expected type to be one of [...]"), and the generic string placeholder is not a member; actions is a required block but its own contents (job_name or crawler_name) are all optional in the schema while the provider requires one of them in practice`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("type", exprTokens(`"ON_DEMAND"`))
			if job, ok := g.byType["aws_glue_job"]; ok {
				for _, blk := range body.Blocks() {
					if blk.Type() == "actions" {
						blk.Body().SetAttributeRaw("job_name", exprTokens(fmt.Sprintf("%s.name", job)))
					}
				}
			}
		},
	},
	"aws_kinesis_firehose_delivery_stream": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment; destination is a required enum naming which optional *_configuration block the provider actually reads (validate: "expected destination to be one of [...]"), and the matching extended_s3_configuration block is itself optional in the schema while the provider requires it in practice once destination = "extended_s3"`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-firehose"`, g.cohort)))
			body.SetAttributeRaw("destination", exprTokens(`"extended_s3"`))
			s3 := body.AppendNewBlock("extended_s3_configuration", nil)
			s3.Body().SetAttributeRaw("bucket_arn", exprTokens(fmt.Sprintf(`"arn:aws:s3:::tofu-%s-cohort-firehose"`, g.cohort)))
			if ref, ok := g.iamRoleRefExpr(); ok {
				s3.Body().SetAttributeRaw("role_arn", exprTokens(ref))
			}
		},
	},
	"aws_kinesis_stream": {
		Reasons: []string{
			`shard_count has no schema default and is left unset by the generic pass, but the provider's own CustomizeDiff defaults stream_mode_details.stream_mode to "PROVISIONED" and then requires shard_count to be at least 1 (found only by running the generic pass's output through terraform apply, not validate - the check is plan-time, not schema-level)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("shard_count", exprTokens(`1`))
		},
	},
	"aws_kinesis_stream_consumer": {
		Reasons: []string{
			`name coincidentally collides with aws_athena_data_catalog's own "name" identity argument in this cohort - see this file's data-plane batch header comment; stream_arn is a required argument the schema alone cannot wire to the sibling aws_kinesis_stream (its identity is "name", not "stream_arn", so parentRef never connects the two)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("name", exprTokens(fmt.Sprintf(`"tofu-%s-cohort-kinesis-stream-consumer"`, g.cohort)))
			if stream, ok := g.byType["aws_kinesis_stream"]; ok {
				body.SetAttributeRaw("stream_arn", exprTokens(fmt.Sprintf("%s.arn", stream)))
			}
		},
	},
}
