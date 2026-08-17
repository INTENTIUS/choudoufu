// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The streaming cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableStreaming = []string{
	// Registry-ratified streaming and app integration batch (#40, #44,
	// issue #65). aws_msk_configuration and aws_appflow_connector_profile
	// are this batch's two untaggable types, below. See
	// live/e2e/estates/streaming/README.md, "Untaggable types".
	"aws_mq_broker",
	"aws_mq_configuration",
	"aws_msk_cluster",
	"aws_msk_serverless_cluster",
	"aws_mskconnect_connector",
	"aws_mskconnect_custom_plugin",
	"aws_mskconnect_worker_configuration",
	"aws_appflow_flow",
	"aws_appsync_graphql_api",
	"aws_pipes_pipe",
	"aws_scheduler_schedule_group",
	// #175 ratification batch (PROPOSE, issue #65), 2026-08-15:
	// taggability per the provider schema survey (live/survey-full.json,
	// v6.59.0 signals.taggable).
	"aws_appsync_channel_namespace",
}

var untaggableStreaming = []string{
	// Registry-ratified streaming and app integration batch (#40, #44,
	// issue #65): two types with no tags argument at all —
	// aws_msk_configuration's Argument Reference names no tags block,
	// and aws_appflow_connector_profile's exports only arn and
	// credentials_arn. See live/e2e/estates/streaming/README.md,
	// "Untaggable types".
	"aws_appflow_connector_profile",
	// #175 ratification batch (PROPOSE, issue #65), 2026-08-15:
	// taggability per the provider schema survey (live/survey-full.json,
	// v6.59.0 signals.taggable).
	"aws_appsync_domain_name",
	"aws_msk_scram_secret_association",
	"aws_msk_topic",
}

func init() {
	registerCohortStamp(taggableStreaming, untaggableStreaming, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified streaming and app integration batch (#40, #44,
			// issue #65). Taggable/untaggable per the real provider's
			// documented Argument Reference for each type: aws_msk_configuration
			// and aws_appflow_connector_profile carry no tags argument at all.
			"aws_mq_broker":                       taggedSchema("id", "arn", "broker_name"),
			"aws_mq_configuration":                taggedSchema("id", "arn", "name"),
			"aws_msk_cluster":                     taggedSchema("id", "arn", "cluster_name"),
			"aws_msk_configuration":               untaggedSchema("arn", "name"),
			"aws_msk_serverless_cluster":          taggedSchema("id", "arn", "cluster_name"),
			"aws_mskconnect_connector":            taggedSchema("id", "arn", "name"),
			"aws_mskconnect_custom_plugin":        taggedSchema("id", "arn", "name"),
			"aws_mskconnect_worker_configuration": taggedSchema("id", "arn", "name"),
			"aws_appflow_connector_profile":       untaggedSchema("arn", "name"),
			"aws_appflow_flow":                    taggedSchema("id", "arn", "name"),
			"aws_appsync_graphql_api":             taggedSchema("id", "arn", "name"),
			"aws_pipes_pipe":                      taggedSchema("id", "arn", "name"),
			"aws_scheduler_schedule_group":        taggedSchema("id", "arn", "name"),
			// #175 ratification batch (PROPOSE, issue #65), 2026-08-15.
			"aws_appsync_channel_namespace":    taggedSchema("id", "arn", "api_id", "name"),
			"aws_appsync_domain_name":          untaggedSchema("id", "domain_name", "appsync_domain_name"),
			"aws_msk_scram_secret_association": untaggedSchema("id", "cluster_arn"),
			"aws_msk_topic":                    untaggedSchema("id", "cluster_arn", "name"),
		})
	})
}
