# Coverage: the account-derived identity path (stateless/SURVEY.md flag F2).
# The block names its topic the way a bucket or a log group does, and that
# name is not what the provider imports by: an SNS topic is imported by its
# ARN, which is the name wrapped in the account and region of the cloud the
# run is against.
#
# internal/stateless/identity's CloudContext is what closes that gap. A
# caller that knows the account and region gets a concrete identity built
# from this name; a caller that does not — which is every run in this fork
# today, because identity resolution runs before any provider is launched —
# gets NEEDS_DISCOVERY and finds the topic by the marker tags below. Both
# outcomes are correct and the fixture exercises the second one live.
#
# An SQS queue belongs here too and is not. Its identity is the same shape
# and the components express it exactly, but floci reports a queue's URL as
# its own endpoint (http://localhost:4566/ACCOUNT/NAME) rather than the
# amazonaws.com form, and that is the one string the AWS provider's importer
# refuses — so the marker path, which is the path a run without a
# CloudContext takes, cannot complete against the emulator. Flag F1,
# blocked-emulator, choudoufu#26.

resource "aws_sns_topic" "alerts" {
  name = "tofu-stateless-e2e-alerts"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_sns_topic.alerts"
  }
}
