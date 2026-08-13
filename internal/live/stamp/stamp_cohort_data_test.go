// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The data cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableData = []string{
	// Registry-ratified data-plane batch (#40, #44, issue #65): Kinesis,
	// KinesisFirehose, Glue and Athena types with a top-level tags
	// argument in the pinned provider's own wire schema.
	"aws_kinesis_stream",
	"aws_kinesis_stream_consumer",
	"aws_kinesis_firehose_delivery_stream",
	"aws_glue_catalog_database",
	"aws_glue_registry",
	"aws_glue_job",
	"aws_glue_crawler",
	"aws_glue_connection",
	"aws_glue_trigger",
	"aws_glue_ml_transform",
	"aws_athena_workgroup",
	"aws_athena_data_catalog",
}

var untaggableData = []string{
	// Registry-ratified data-plane batch (#40, #44, issue #65): three
	// types with no top-level tags argument in the pinned provider's own
	// wire schema — aws_glue_catalog_table and aws_glue_classifier
	// mirror aws_cloudwatch_dashboard's shape (a plain client-named
	// identity, just an untaggable one); aws_glue_data_catalog_encryption_settings
	// is a singleton-per-account type, the same shape as the IAM/ECR
	// batch's three ECR registry singletons above.
	"aws_glue_catalog_table",
	"aws_glue_classifier",
	"aws_glue_data_catalog_encryption_settings",
}

func init() {
	registerCohortStamp(taggableData, untaggableData, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified data-plane batch (#40, #44, issue #65):
			// Kinesis, KinesisFirehose, Glue and Athena. Taggable/untaggable per
			// the pinned provider's own wire schema (`terraform providers schema
			// -json` against the real hashicorp/aws 6.58.0 binary) for each
			// type: aws_glue_catalog_table, aws_glue_classifier and
			// aws_glue_data_catalog_encryption_settings carry no tags argument
			// at all.
			"aws_kinesis_stream":                        taggedSchema("id", "arn", "name"),
			"aws_kinesis_stream_consumer":               taggedSchema("id", "arn", "name", "stream_arn"),
			"aws_kinesis_firehose_delivery_stream":      taggedSchema("id", "arn", "name"),
			"aws_glue_catalog_database":                 taggedSchema("id", "arn", "name", "catalog_id"),
			"aws_glue_catalog_table":                    untaggedSchema("id", "arn", "name", "database_name", "catalog_id"),
			"aws_glue_registry":                         taggedSchema("id", "arn", "registry_name"),
			"aws_glue_job":                              taggedSchema("id", "arn", "name", "role_arn"),
			"aws_glue_crawler":                          taggedSchema("id", "arn", "name", "database_name", "role"),
			"aws_glue_connection":                       taggedSchema("id", "arn", "name", "catalog_id"),
			"aws_glue_classifier":                       untaggedSchema("id", "name"),
			"aws_glue_data_catalog_encryption_settings": untaggedSchema("id", "catalog_id"),
			"aws_glue_trigger":                          taggedSchema("id", "arn", "name", "type"),
			"aws_glue_ml_transform":                     taggedSchema("id", "arn", "name", "role_arn"),
			"aws_athena_workgroup":                      taggedSchema("id", "arn", "name"),
			"aws_athena_data_catalog":                   taggedSchema("id", "arn", "name", "type"),
		})
	})
}
