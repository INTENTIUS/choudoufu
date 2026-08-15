// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// typeOverridesData is the data cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesData = map[string]typeOverride{
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
	//
	// (This is the "data-plane batch header comment" the Reasons below cite;
	// a merge dropped it and #127's census restored it.)
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

func init() { registerCohortOverrides(typeOverridesData) }
