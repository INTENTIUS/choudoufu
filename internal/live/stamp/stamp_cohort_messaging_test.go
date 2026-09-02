// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The messaging cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableMessaging = []string{
	// Registry-ratified messaging batch (#40, #44).
	"aws_cloudwatch_composite_alarm",
	"aws_cloudwatch_metric_stream",
	"aws_sqs_queue",
}

var untaggableMessaging = []string{
	// Registry-ratified messaging batch (#40, #44). See
	// live/e2e/estates/messaging/README.md, "Untaggable types", for why
	// aws_sns_topic_subscription — untaggable and inside the curated 68
	// — is still not admitted (issue #65 re-examined and confirmed the
	// deferral, for a mechanism reason rather than the doc gate #54
	// already closed).
	"aws_cloudwatch_dashboard",
	"aws_sns_topic_policy",
	"aws_sqs_queue_policy",
}

func init() {
	registerCohortStamp(taggableMessaging, untaggableMessaging, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified messaging batch (#40, #44). Taggable/untaggable
			// per the real provider's documented Argument Reference for each
			// type: aws_cloudwatch_dashboard, aws_sns_topic_policy and
			// aws_sqs_queue_policy carry no tags argument at all.
			"aws_cloudwatch_composite_alarm": taggedSchema("id", "arn", "alarm_name", "alarm_rule"),
			"aws_cloudwatch_dashboard":       untaggedSchema("dashboard_arn", "dashboard_name", "dashboard_body"),
			"aws_cloudwatch_metric_stream":   taggedSchema("id", "arn", "name"),
			"aws_sns_topic_policy":           untaggedSchema("id", "arn", "policy"),
			"aws_sqs_queue":                  taggedSchema("id", "arn", "url", "name"),
			"aws_sqs_queue_policy":           untaggedSchema("id", "queue_url", "policy"),
		})
	})
}
