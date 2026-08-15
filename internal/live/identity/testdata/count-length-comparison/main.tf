# count = length(<resource>) > 0 ? 1 : 0: length(<resource>) feeds a
# comparison inside a ternary's condition rather than sitting in one of its
# branches. bod_docker_iam.tf in cyhy-amis uses exactly this to create an
# IAM role policy only when at least one Lambda function exists.
resource "aws_lambda_function" "fns" {
  for_each = toset(["a", "b"])

  function_name = each.key
  role          = "arn:aws:iam::000000000000:role/lambda"
  handler       = "index.handler"
  runtime       = "nodejs18.x"
  filename      = "lambda.zip"
}

resource "aws_cloudwatch_log_group" "policy_gate" {
  count = length(aws_lambda_function.fns) > 0 ? 1 : 0

  name = "/estate/log"
}
