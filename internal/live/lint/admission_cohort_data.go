// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesData is the data cohort's slice of [admittedTypesV0]:
// the types the data ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesData = map[string]struct{}{
	// ---- Registry-ratified (#40, #44): fourth batch, data plane (Kinesis,
	// ---- KinesisFirehose, Glue, Athena; issue #65's recipe). Same
	// ---- tools/row-gen pipeline as the earlier batches, cross-checked
	// ---- against the AWS provider's documented import behaviour and, for
	// ---- several rows, against the pinned provider's own wire schema (read
	// ---- with `terraform providers schema -json`) rather than accepted on
	// ---- the registry's word alone — see internal/live/identity/table.go
	// ---- for the per-type evidence and for the row-gen proposals this
	// ---- batch corrected, rejected or left out of scope. Cohort estate:
	// ---- live/e2e/estates/data.
	"aws_kinesis_stream":                        {},
	"aws_kinesis_stream_consumer":               {},
	"aws_kinesis_firehose_delivery_stream":      {},
	"aws_glue_catalog_database":                 {},
	"aws_glue_catalog_table":                    {},
	"aws_glue_registry":                         {},
	"aws_glue_job":                              {},
	"aws_glue_crawler":                          {},
	"aws_glue_connection":                       {},
	"aws_glue_classifier":                       {},
	"aws_glue_data_catalog_encryption_settings": {},
	"aws_glue_trigger":                          {},
	"aws_glue_ml_transform":                     {},
	"aws_athena_workgroup":                      {},
	"aws_athena_data_catalog":                   {},
}

func init() { registerCohortAdmitted(admittedTypesData) }
