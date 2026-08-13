# Supporting, not coverage: aws_iam_role.messaging exists only so
# aws_cloudwatch_metric_stream.app has a role to stream metrics under. It is
# itself client-named-shaped exactly the way live/e2e/estate/'s own
# aws_iam_role.app is, but it is not claimed as a coverage row here — see
# live/e2e/estate/README.md's own "Supporting, not coverage" section for the
# same pattern, and aws_iam_role is already covered there.

resource "aws_iam_role" "messaging" {
  name = "tofu-messaging-cohort-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "streams.metrics.cloudwatch.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_iam_role.messaging"
  }
}
