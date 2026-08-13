// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesObservability is the observability cohort's slice of [admittedTypesV0]:
// the types the observability ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesObservability = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): fifth batch, observability and
	// ---- eventing remainder (CloudWatch, Logs, EventBridge/Events, Step
	// ---- Functions, X-Ray, Grafana, RUM, Synthetics; issue #65's
	// ---- ratification campaign). Same tools/row-gen pipeline as the earlier
	// ---- batches, cross-checked against live/import-grammar.json (the
	// ---- provider's own documented Import sections, fetched at the pinned
	// ---- v6.59.0 tag) and, for several corrections, against the provider's
	// ---- Argument Reference directly — see internal/live/identity/table.go
	// ---- for the per-type evidence and for the rows this batch rejected or
	// ---- left out of scope. Amazon Managed Prometheus (AWS::APS::*) is
	// ---- deliberately untouched here: it is issue #68's concurrent batch,
	// ---- and admitting any of its nine types from this batch too would be a
	// ---- straight collision on both this table and DefaultTable. Amazon
	// ---- Application Signals has no CloudFormation resource type in
	// ---- live/mapping.json's roster at all, so there is no row-gen evidence
	// ---- for it to ratify or reject. Cohort estate:
	// ---- live/e2e/estates/observability.
	"aws_cloudwatch_alarm_mute_rule":          {},
	"aws_cloudwatch_contributor_insight_rule": {},
	"aws_cloudwatch_otel_enrichment":          {},
	"aws_cloudwatch_log_account_policy":       {},
	"aws_cloudwatch_log_anomaly_detector":     {},
	"aws_cloudwatch_log_delivery":             {},
	"aws_cloudwatch_log_delivery_destination": {},
	"aws_cloudwatch_log_delivery_source":      {},
	"aws_cloudwatch_log_destination":          {},
	"aws_cloudwatch_log_metric_filter":        {},
	"aws_cloudwatch_log_resource_policy":      {},
	"aws_cloudwatch_log_stream":               {},
	"aws_cloudwatch_log_subscription_filter":  {},
	"aws_cloudwatch_log_transformer":          {},
	"aws_cloudwatch_query_definition":         {},
	"aws_cloudwatch_event_api_destination":    {},
	"aws_cloudwatch_event_archive":            {},
	"aws_cloudwatch_event_bus":                {},
	"aws_cloudwatch_event_connection":         {},
	"aws_cloudwatch_event_endpoint":           {},
	"aws_cloudwatch_event_permission":         {},
	"aws_sfn_activity":                        {},
	"aws_xray_group":                          {},
	"aws_xray_resource_policy":                {},
	"aws_xray_sampling_rule":                  {},
	"aws_grafana_workspace":                   {},
	"aws_rum_app_monitor":                     {},
	"aws_synthetics_canary":                   {},
	"aws_synthetics_group":                    {},
}

func init() { registerCohortAdmitted(admittedTypesObservability) }
