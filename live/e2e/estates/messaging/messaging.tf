# Coverage: the registry-ratified messaging batch (#40's registry-backed
# admission strategy, #44's row-gen tool). Every resource below tagged
# "Coverage" is one of the six types this batch ratified into
# admittedTypesV0 (internal/live/lint/admission.go) and DefaultTable
# (internal/live/identity/table.go) — see table.go's second
# "Registry-ratified" section comment for the per-type evidence, for the
# two row-gen proposals (aws_cloudwatch_alarm_mute_rule,
# aws_cloudwatch_event_rule) this batch rejected, and for the one
# (aws_sns_topic_subscription) it defers despite a clean classification.

# Supporting, not coverage: aws_sns_topic.app exists only so
# aws_sns_topic_policy.app has a topic to attach to. aws_sns_topic is
# already covered by internal/live/lint/admission.go's original
# client-named-with-account-derived section, not by this batch.
resource "aws_sns_topic" "app" {
  name = "tofu-messaging-cohort-topic"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_sns_topic.app"
  }
}

# Coverage: client-named path (aws_cloudwatch_composite_alarm — identity is
# the alarm_name argument, already in config; row-gen proposed this
# correctly the first time, confirmed against the provider's documented
# import command and its Attribute Reference, which states id equals
# alarm_name). alarm_rule references a placeholder alarm name rather than a
# real aws_cloudwatch_metric_alarm — that type is already covered by
# live/e2e/estate/, and composing a real rule expression is outside what
# this cohort tests.
resource "aws_cloudwatch_composite_alarm" "app" {
  alarm_name = "tofu-messaging-cohort-composite"
  alarm_rule = "ALARM(\"tofu-messaging-cohort-placeholder\")"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_cloudwatch_composite_alarm.app"
  }
}

# Coverage: client-named path (aws_cloudwatch_dashboard — identity is the
# dashboard_name argument, already in config; row-gen proposed this
# correctly). Carries no tags argument in the provider — untaggable, the
# same shape as the Lambda batch's aws_lambda_layer_version — see this
# cohort's README, "Untaggable types".
resource "aws_cloudwatch_dashboard" "app" {
  dashboard_name = "tofu-messaging-cohort-dashboard"
  dashboard_body = jsonencode({
    widgets = []
  })
}

# Coverage: client-named path (aws_cloudwatch_metric_stream — identity is
# the name argument, already in config; row-gen proposed this correctly).
# firehose_arn is a literal placeholder rather than a real
# aws_kinesis_firehose_delivery_stream resource, which is outside this
# batch's scope.
resource "aws_cloudwatch_metric_stream" "app" {
  name          = "tofu-messaging-cohort-stream"
  role_arn      = aws_iam_role.messaging.arn
  firehose_arn  = "arn:aws:firehose:us-east-1:000000000000:deliverystream/tofu-messaging-cohort-placeholder"
  output_format = "json"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_cloudwatch_metric_stream.app"
  }
}

# Coverage: parent-derived path (aws_sns_topic_policy — row-gen proposed
# server-assigned via the registry's opaque, undocumented "Id"; rejected as
# a proposal, but the provider's documented import command is the topic's
# own ARN, which this resource's sole reference argument, "arn", already
# carries — the same named-singleton-child shape as
# live/e2e/estate/'s aws_s3_bucket_policy). Carries no tags argument.
resource "aws_sns_topic_policy" "app" {
  arn = aws_sns_topic.app.arn

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AllowPublish"
      Effect    = "Allow"
      Principal = { AWS = "*" }
      Action    = "SNS:Publish"
      Resource  = aws_sns_topic.app.arn
      Condition = {
        StringEquals = { "AWS:SourceOwner" = "000000000000" }
      }
    }]
  })
}

# Coverage: server-assigned/marker path (aws_sqs_queue — row-gen proposed
# server-assigned via the registry's QueueUrl/Arn; right in spirit, but
# this is the account-derived shape aws_sns_topic already has, not a flat
# opaque ID — see table.go. Ratified on paper despite a floci gap: see this
# cohort's README, "Verifying by hand".
resource "aws_sqs_queue" "app" {
  name = "tofu-messaging-cohort-queue"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_sqs_queue.app"
  }
}

# Coverage: parent-derived path (aws_sqs_queue_policy — row-gen proposed
# server-assigned via the registry's opaque, undocumented "Id"; rejected as
# a proposal, but the provider's documented import command is the queue's
# own url, which this resource's sole reference argument, "queue_url",
# already carries — the same named-singleton-child shape as
# aws_sns_topic_policy.app above). Carries no tags argument.
resource "aws_sqs_queue_policy" "app" {
  queue_url = aws_sqs_queue.app.url

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AllowSend"
      Effect    = "Allow"
      Principal = { AWS = "*" }
      Action    = "SQS:SendMessage"
      Resource  = aws_sqs_queue.app.arn
      Condition = {
        ArnEquals = { "aws:SourceArn" = aws_sns_topic.app.arn }
      }
    }]
  })
}

# aws_sns_topic_subscription is deliberately not a coverage row here: it
# classifies cleanly (server-assigned, the topic ARN plus a UUID SNS mints
# only once the subscription confirms) but is deferred rather than
# ratified — see this cohort's README, "Rejected, and deferred", for why.
