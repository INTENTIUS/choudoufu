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

# Two instances of the same untaggable, server-assigned shape as
# live-plan-stampgaps-unmarked-apply-950's single one, so that GitHub issue
# #1203's target-scope test can keep one of them in the plan graph and drop
# the other. Both are ClassNeedsDiscovery against a schema with no "tags"
# attribute, so the pre-#1203 refusal fired on both regardless of what the
# run was narrowed to.
resource "aws_vpc" "targeted" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_vpc" "excluded" {
  cidr_block = "10.1.0.0/16"
}
