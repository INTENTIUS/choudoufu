# Supporting, not coverage: aws_iam_role.lambda exists only so
# aws_lambda_function.app and aws_lambda_capacity_provider.app have a role
# to assume/operate under. It is itself client-named-shaped exactly the way
# live/e2e/estate/'s own aws_iam_role.app is, but it is not claimed as a
# coverage row here — see live/e2e/estate/README.md's own "Supporting, not
# coverage" section for the same pattern, and aws_iam_role is already
# covered there.

resource "aws_iam_role" "lambda" {
  name = "tofu-lambda-cohort-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_iam_role.lambda"
  }
}
