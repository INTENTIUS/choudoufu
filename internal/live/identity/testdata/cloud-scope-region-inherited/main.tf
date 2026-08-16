# #217: an explicit `region` argument and an inherited one must produce the
# same scope when they resolve to the SAME effective region, or a real
# collision goes undetected - the regression a commit meant to fix issue
# #200 introduced. "explicit" states region = "eu-west-1" directly on the
# resource body; "inherited" sets no region argument at all and gets the
# identical eu-west-1 from the provider block below. Both name the same log
# group, so checkCollisions must still flag them against each other.
# "elsewhere" inherits the same provider block but names a DIFFERENT log
# group, so it must not collide with either.

provider "aws" {
  region = "eu-west-1"
}

resource "aws_cloudwatch_log_group" "explicit" {
  name   = "/aws/bedrock"
  region = "eu-west-1"
}

resource "aws_cloudwatch_log_group" "inherited" {
  name = "/aws/bedrock"
}

resource "aws_cloudwatch_log_group" "elsewhere" {
  name = "/aws/other"
}
