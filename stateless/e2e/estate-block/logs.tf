# Coverage: client-named path (aws_cloudwatch_log_group — identity is the
# log group name in config). No conditional-idiom instance here (the main
# estate's aws_cloudwatch_log_group.optional already covers that row).

resource "aws_cloudwatch_log_group" "app" {
  name              = "/stateless-e2e-block/app"
  retention_in_days = 1
}
