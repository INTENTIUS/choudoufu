# #217's own safety direction, one level past the asymmetric spelling case:
# "known" states region = "us-east-1" directly. "unknown" states no region
# at all, and neither does the provider block below, so this run cannot
# determine unknown's effective region by ANY means - not a value that
# happens to differ from "us-east-1", genuinely not established. The safe
# direction is to collide rather than silently distinguish: an unknown
# region must never be treated as evidence that a resource lives somewhere
# else.

provider "aws" {}

resource "aws_cloudwatch_log_group" "known" {
  name   = "/aws/bedrock"
  region = "us-east-1"
}

resource "aws_cloudwatch_log_group" "unknown" {
  name = "/aws/bedrock"
}
