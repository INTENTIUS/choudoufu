# The same estate as testdata/live-plan, with the live block that
# makes plain "tofu plan" and "tofu apply" run the stateless pipeline. The
# two fixtures are otherwise identical on purpose: the parity test plans both
# and compares.
terraform {
  live {
    estate = "stateless-unit"
  }

  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

# Client-named identity: the bucket name is in the configuration, so the
# projection can read this one back with no memory at all.
resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-unit-data"
}

# Server-assigned identity: nothing in configuration names it, so it is found
# by its ownership marker.
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
