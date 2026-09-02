# testdata/named, plus the one thing that makes an OLD tofu-address marker
# legitimate: a moved block saying the live resource that used to be called
# aws_cloudwatch_log_group.legacy_app is the one this configuration now calls
# aws_cloudwatch_log_group.app. The marker rewrite that finishes the move is
# the ordinary tags diff the provider plans, so the ownership check has to
# admit the object while it still carries the old address - see
# internal/live/moved's package doc, and GitHub issue #198.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

resource "aws_cloudwatch_log_group" "app" {
  name = "/ours/logs"
}

moved {
  from = aws_cloudwatch_log_group.legacy_app
  to   = aws_cloudwatch_log_group.app
}
