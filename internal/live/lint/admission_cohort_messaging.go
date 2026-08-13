// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesMessaging is the messaging cohort's slice of [admittedTypesV0]:
// the types the messaging ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesMessaging = map[string]struct{}{
	// ---- Registry-ratified (#40, #44): second batch, messaging (SQS, SNS
	// ---- beyond the already-admitted aws_sns_topic, CloudWatch, and
	// ---- EventBridge/Events). Same tools/row-gen pipeline as the Lambda
	// ---- batch above (9 row-gen proposals in scope; 2 rejected and 1
	// ---- deferred on independent verification — see
	// ---- internal/live/identity/table.go for the per-type evidence and
	// ---- live/e2e/estates/messaging/README.md for why
	// ---- aws_sns_topic_subscription is deferred rather than landing here
	// ---- despite classifying cleanly). Cohort estate:
	// ---- live/e2e/estates/messaging.
	"aws_cloudwatch_composite_alarm": {},
	"aws_cloudwatch_dashboard":       {},
	"aws_cloudwatch_metric_stream":   {},
	"aws_sns_topic_policy":           {},
	// aws_sqs_queue ratifies on paper: its identity is the same
	// account-derived shape as aws_sns_topic above, so the "aws_sqs_queue
	// is the same shape and is not here" sentence a few lines up is now
	// stale prose, not current fact — it is kept out no longer. What kept
	// it out was never the identity, only a floci gap (choudoufu#26: floci
	// reports a queue's URL as its own endpoint, and the AWS provider's
	// importer parses only the amazonaws.com form). See
	// live/e2e/estates/messaging/README.md for the emulator caveat.
	"aws_sqs_queue":        {},
	"aws_sqs_queue_policy": {},
}

func init() { registerCohortAdmitted(admittedTypesMessaging) }
