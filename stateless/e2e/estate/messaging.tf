# Coverage: the account-derived identity path (stateless/SURVEY.md flags F1
# and F2). Both blocks name their resource the way a bucket or a log group
# does, and neither name is what the provider imports by: an SQS queue is
# imported by its URL and an SNS topic by its ARN, and both of those are the
# name wrapped in the account and region of the cloud the run is against.
#
# internal/stateless/identity's CloudContext is what closes that gap. A
# caller that knows the account and region gets a concrete identity built
# from these names; a caller that does not — which is every run in this fork
# today, because identity resolution runs before any provider is launched —
# gets NEEDS_DISCOVERY and finds these two by the marker tags below. Both
# outcomes are correct and the fixture exercises the second one live.

resource "aws_sqs_queue" "jobs" {
  name = "tofu-stateless-e2e-jobs"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_sqs_queue.jobs"
  }
}

resource "aws_sns_topic" "alerts" {
  name = "tofu-stateless-e2e-alerts"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_sns_topic.alerts"
  }
}
