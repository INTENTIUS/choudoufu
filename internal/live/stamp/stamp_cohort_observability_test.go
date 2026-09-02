// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The observability cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableObservability = []string{
	// Registry-ratified observability and eventing remainder batch
	// (#40, #44, issue #65). See
	// live/e2e/estates/observability/README.md, "Untaggable types",
	// for this batch's untaggable rows.
	"aws_cloudwatch_alarm_mute_rule",
	"aws_cloudwatch_contributor_insight_rule",
	"aws_cloudwatch_event_bus",
	"aws_cloudwatch_log_anomaly_detector",
	"aws_cloudwatch_log_delivery",
	"aws_cloudwatch_log_delivery_destination",
	"aws_cloudwatch_log_delivery_source",
	"aws_cloudwatch_log_destination",
	"aws_grafana_workspace",
	"aws_rum_app_monitor",
	"aws_sfn_activity",
	"aws_synthetics_canary",
	"aws_synthetics_group",
	"aws_xray_group",
	"aws_xray_sampling_rule",
	// #175 reversal batch, 2026-08-15: taggability per the provider
	// schema survey (live/survey-full.json, v6.59.0 signals.taggable).
	"aws_cloudwatch_event_rule",
}

var untaggableObservability = []string{
	// Registry-ratified observability and eventing remainder batch
	// (#40, #44, issue #65): fourteen types with no top-level tags
	// argument in the pinned provider's own wire schema, confirmed
	// against each type's documented Argument Reference. See
	// live/e2e/estates/observability/README.md, "Untaggable types".
	"aws_cloudwatch_log_account_policy",
	"aws_cloudwatch_log_metric_filter",
	"aws_cloudwatch_log_resource_policy",
	"aws_cloudwatch_log_stream",
	"aws_cloudwatch_log_subscription_filter",
	"aws_cloudwatch_log_transformer",
	"aws_cloudwatch_event_api_destination",
	"aws_cloudwatch_event_archive",
	"aws_cloudwatch_event_connection",
	"aws_cloudwatch_event_endpoint",
	"aws_cloudwatch_event_permission",
	"aws_xray_resource_policy",
	// #175 reversal batch, 2026-08-15: taggability per the provider
	// schema survey (live/survey-full.json, v6.59.0 signals.taggable).
	"aws_cloudwatch_event_target",
}

func init() {
	registerCohortStamp(taggableObservability, untaggableObservability, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified observability and eventing remainder batch
			// (#40, #44, issue #65). Taggable/untaggable per the real
			// provider's documented Argument Reference for each type; the
			// fourteen untaggable rows are exactly this batch's own
			// "Untaggable types" list in
			// live/e2e/estates/observability/README.md.
			"aws_cloudwatch_alarm_mute_rule":          taggedSchema("id", "name", "rule"),
			"aws_cloudwatch_contributor_insight_rule": taggedSchema("id", "rule_name", "rule_definition"),
			"aws_cloudwatch_otel_enrichment":          untaggedSchema("id", "region"),
			"aws_cloudwatch_log_account_policy":       untaggedSchema("id", "policy_name", "policy_type", "policy_document"),
			"aws_cloudwatch_log_anomaly_detector":     taggedSchema("id", "arn", "log_group_arn_list"),
			"aws_cloudwatch_log_delivery":             taggedSchema("id", "delivery_source_name", "delivery_destination_arn"),
			"aws_cloudwatch_log_delivery_destination": taggedSchema("id", "arn", "name"),
			"aws_cloudwatch_log_delivery_source":      taggedSchema("id", "arn", "name", "log_type", "resource_arn"),
			"aws_cloudwatch_log_destination":          taggedSchema("id", "arn", "name", "role_arn", "target_arn"),
			"aws_cloudwatch_log_metric_filter":        untaggedSchema("id", "name", "log_group_name", "pattern"),
			"aws_cloudwatch_log_resource_policy":      untaggedSchema("id", "policy_name", "policy_document"),
			"aws_cloudwatch_log_stream":               untaggedSchema("id", "name", "log_group_name"),
			"aws_cloudwatch_log_subscription_filter":  untaggedSchema("id", "name", "log_group_name", "destination_arn"),
			"aws_cloudwatch_log_transformer":          untaggedSchema("id", "log_group_arn"),
			"aws_cloudwatch_query_definition":         untaggedSchema("id", "name", "query_string"),
			"aws_cloudwatch_event_api_destination":    untaggedSchema("id", "arn", "name", "connection_arn", "http_method"),
			"aws_cloudwatch_event_archive":            untaggedSchema("id", "name", "event_source_arn"),
			"aws_cloudwatch_event_bus":                taggedSchema("id", "arn", "name"),
			"aws_cloudwatch_event_connection":         untaggedSchema("id", "arn", "name", "authorization_type"),
			"aws_cloudwatch_event_endpoint":           untaggedSchema("id", "arn", "name"),
			"aws_cloudwatch_event_permission":         untaggedSchema("id", "statement_id", "principal"),
			"aws_sfn_activity":                        taggedSchema("id", "arn", "name"),
			"aws_xray_group":                          taggedSchema("id", "arn", "group_name", "filter_expression"),
			"aws_xray_resource_policy":                untaggedSchema("id", "policy_name", "policy_document"),
			"aws_xray_sampling_rule":                  taggedSchema("id", "arn", "rule_name", "priority"),
			"aws_grafana_workspace":                   taggedSchema("id", "arn", "account_access_type", "authentication_providers", "permission_type"),
			"aws_rum_app_monitor":                     taggedSchema("id", "name", "domain"),
			"aws_synthetics_canary":                   taggedSchema("id", "arn", "name"),
			"aws_synthetics_group":                    taggedSchema("id", "arn", "name"),
			// #175 reversal batch, 2026-08-15.
			"aws_cloudwatch_event_rule":   taggedSchema("id", "arn", "name", "event_bus_name"),
			"aws_cloudwatch_event_target": untaggedSchema("id", "rule", "target_id", "event_bus_name", "arn"),
		})
	})
}
