// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The lambda cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableLambda = []string{
	// Registry-ratified Lambda batch (#40, #44).
	"aws_lambda_capacity_provider",
	"aws_lambda_code_signing_config",
	"aws_lambda_event_source_mapping",
	"aws_lambda_function",
}

var untaggableLambda = []string{
	// Registry-ratified Lambda batch (#40, #44): the batch's one
	// untaggable type. See live/e2e/estates/lambda/README.md,
	// "Untaggable types".
	"aws_lambda_layer_version",
	// #175 reversal batch, 2026-08-15: taggability per the provider
	// schema survey (live/survey-full.json, v6.59.0 signals.taggable).
	"aws_lambda_permission",
}

func init() {
	registerCohortStamp(taggableLambda, untaggableLambda, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified Lambda batch (#40, #44). Taggable per the real
			// provider's documented Argument Reference; aws_lambda_layer_version
			// is the batch's one untaggable type — its Argument Reference names
			// no tags block at all.
			"aws_lambda_capacity_provider":    taggedSchema("id", "arn", "name"),
			"aws_lambda_code_signing_config":  taggedSchema("id", "arn", "config_id"),
			"aws_lambda_event_source_mapping": taggedSchema("id", "uuid", "arn", "function_arn"),
			"aws_lambda_function":             taggedSchema("id", "arn", "function_name"),
			"aws_lambda_layer_version":        untaggedSchema("id", "arn", "layer_arn", "layer_name", "version"),
			// #175 reversal batch, 2026-08-15.
			"aws_lambda_permission": untaggedSchema("id", "function_name", "statement_id"),
		})
	})
}
