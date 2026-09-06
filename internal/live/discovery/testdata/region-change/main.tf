# Issue #906's estate shape: a resource block that has been repointed from
# one aliased provider configuration to another.
#
# aws_vpc.west was declared under aws.west and applied there, so a live VPC
# in us-west-2 carries tofu-address=aws_vpc.west. The block now names the
# default (east) configuration and nothing else about it changed - the state
# this fixture is frozen in.
#
# aws_cloudwatch_log_group.west is what keeps aws.west a configured provider
# configuration, so the west region is still swept. Without it this would be
# the other case entirely, the one the-boundary-holds-across-regions.sh's
# step 3 pins: a region no declaration points at, which drops out of the
# sweep with its last block.
#
# aws_kms_key.east is declared under the default configuration and its live
# object is where that configuration looks. It is the control: an address
# whose own pass sights its object must never be read as stranded.

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

resource "aws_vpc" "west" {
  cidr_block = "10.71.0.0/16"
}

resource "aws_cloudwatch_log_group" "west" {
  provider = aws.west
  name     = "/app/logs"
}

resource "aws_kms_key" "east" {
  description = "east only"
}
