# Issue #745's estate shape: two aws provider configurations, one per region,
# mirroring one client-chosen CloudWatch log group name into both of them.
#
# A log group's import identity IS its name, and DescribeLogGroups is
# region-scoped, so these are two distinct live objects that answer to the
# same import ID. That is what makes the cache-vouch sightings' partition
# load-bearing: without one, the west region's listing of "/app/logs" is
# indistinguishable from the east region's, and can vouch existence for an
# east instance whose object was deleted out of band.

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
