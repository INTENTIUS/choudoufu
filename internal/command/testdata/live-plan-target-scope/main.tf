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

# The resource this run is about. Client-named, so its identity comes
# straight out of the configuration.
resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-unit-data"
}

# GitHub issue #352's shape: a resource whose identity this fork cannot
# resolve, sitting in the same configuration and scoped out with -target.
# aws_iam_group's name carries no auto-generated-name promise in the
# provider's Argument Reference, so omitting it refuses rather than
# deferring to discovery - see the live-plan-no-identity fixture, which is
# this block on its own.
#
# Nothing here references it and nothing it references, so the plan graph
# drops it entirely under -target=aws_s3_bucket.data.
resource "aws_iam_group" "orphaned" {
  tags = {
    tofu-estate = "unit"
  }
}
