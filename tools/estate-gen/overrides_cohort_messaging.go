// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "github.com/hashicorp/hcl/v2/hclwrite"

// typeOverridesMessaging is the messaging cohort's slice of [typeOverrides].
// Registered by init below; see contributing/LIVE-TABLES.md.
var typeOverridesMessaging = map[string]typeOverride{
	"aws_cloudwatch_dashboard": {
		Reasons: []string{
			`schema requires "dashboard_body" as a plain string, but the provider validates it is well-formed JSON (validate: "contains an invalid JSON"); the hand-maintained cohort carried an empty widgets document and the fold keeps it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("dashboard_body", exprTokens(`jsonencode({
    widgets = []
  })`))
		},
	},
	"aws_cloudwatch_composite_alarm": {
		Reasons: []string{
			`alarm_rule passes validate as any string but must parse as a rule expression at apply; the hand-maintained cohort referenced a placeholder alarm name inside a well-formed ALARM(...) expression rather than a real aws_cloudwatch_metric_alarm (already covered by live/e2e/estate/), and the fold keeps that choice`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			body.SetAttributeRaw("alarm_rule", exprTokens(`"ALARM(\"tofu-messaging-cohort-placeholder\")"`))
		},
	},
	"aws_sns_topic_policy": {
		Reasons: []string{
			`arn is validated as a well-formed ARN and policy as well-formed JSON; the hand-maintained cohort referenced the sibling aws_sns_topic's own arn for both (the named-singleton-child shape of aws_s3_bucket_policy) and the fold keeps it`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			topic, ok := g.byType["aws_sns_topic"]
			if !ok {
				return
			}
			ref := topic.Type + "." + topic.Label
			body.SetAttributeRaw("arn", exprTokens(ref+".arn"))
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AllowPublish"
      Effect    = "Allow"
      Principal = { AWS = "*" }
      Action    = "SNS:Publish"
      Resource  = `+ref+`.arn
    }]
  })`))
		},
	},
	"aws_sqs_queue_policy": {
		Reasons: []string{
			`policy is validated as well-formed JSON; the hand-maintained cohort granted SQS:SendMessage on the sibling queue, conditioned on the sibling topic's arn, and the fold keeps it (queue_url wires itself: it is the type's identity argument and aws_sqs_queue is in the cohort)`,
		},
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			queue, qok := g.byType["aws_sqs_queue"]
			topic, tok := g.byType["aws_sns_topic"]
			if !qok || !tok {
				return
			}
			q := queue.Type + "." + queue.Label
			tp := topic.Type + "." + topic.Label
			body.SetAttributeRaw("policy", exprTokens(`jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AllowSend"
      Effect    = "Allow"
      Principal = { AWS = "*" }
      Action    = "SQS:SendMessage"
      Resource  = `+q+`.arn
      Condition = {
        ArnEquals = { "aws:SourceArn" = `+tp+`.arn }
      }
    }]
  })`))
		},
	},
	"aws_cloudwatch_metric_stream": {
		Reasons: []string{
			`firehose_arn is validated as a well-formed ARN and output_format against a fixed enum (validate: "is an invalid ARN", "expected output_format to be one of"); the hand-maintained cohort used a literal well-formed firehose ARN (a real aws_kinesis_firehose_delivery_stream is outside this batch's scope) and "json". "role_arn" must name a real IAM role the stream writes under, and the curated role alias does not fire on it (isRoleArg matches "role" and "*_role_arn", and the bare "role_arn" is neither); the shared supporting aws_iam_role is generated (NeedsIAMRole) and referenced. Folds the hand-written iam.tf block #108 criterion 4 found; the role's assume policy trusts streams.metrics.cloudwatch.amazonaws.com via supportingRolePrincipals, not the cohort-name rule`,
		},
		NeedsIAMRole: true,
		Apply: func(g *generator, body *hclwrite.Body, addr resourceAddr) {
			if ref, ok := g.iamRoleRefExpr(); ok {
				body.SetAttributeRaw("role_arn", exprTokens(ref))
			}
			body.SetAttributeRaw("firehose_arn", exprTokens(`"arn:aws:firehose:us-east-1:000000000000:deliverystream/tofu-messaging-cohort-firehose"`))
			body.SetAttributeRaw("output_format", exprTokens(`"json"`))
		},
	},
}

func init() { registerCohortOverrides(typeOverridesMessaging) }
