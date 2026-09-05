# Issue #745's estate shape, for the vouch pass.
#
# Two aws provider configurations, one per region, mirroring one
# client-chosen CloudWatch log group name into both. A log group's import
# identity IS its name and DescribeLogGroups is region-scoped, so these are
# two distinct live objects answering to the same import ID - the case where
# an unpartitioned sighting from one region vouches existence for the
# other region's instance.
#
# aws_kms_key.east is declared under the default (east) configuration alone.
# It is the second half of the same issue: the west pass has no instance of
# that type to vouch for, so it has no business paying for a list of it.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

provider "aws" {
  alias  = "west"
  region = "us-west-2"
}

resource "aws_cloudwatch_log_group" "east" {
  name = "/app/logs"
}

resource "aws_cloudwatch_log_group" "west" {
  provider = aws.west
  name     = "/app/logs"
}

resource "aws_kms_key" "east" {
  description = "east only"
}
