# Coverage: client-named path (aws_cloudwatch_log_group.app — identity is the
# log group name in config) and conditional idiom
# (aws_cloudwatch_log_group.optional, count = var.enabled ? 1 : 0).

resource "aws_cloudwatch_log_group" "app" {
  name              = "/stateless-e2e/app"
  retention_in_days = 1

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_cloudwatch_log_group.app"
  }
}

resource "aws_cloudwatch_log_group" "optional" {
  count = var.enabled ? 1 : 0

  name              = "/stateless-e2e/optional"
  retention_in_days = 1

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_cloudwatch_log_group.optional:${count.index}"
  }
}
