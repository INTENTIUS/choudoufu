# Issue #255's fixture, phase 1: both roles declared.
#
# Two resources rather than one, so the removal case in phase 2 has
# something to leave alone. aws_iam_role.keeper stays declared throughout and
# must never appear in a plan; aws_iam_role.demo is the block phase 2
# deletes, and the estate-wide sweep is the only thing that can then find its
# live role.
#
# Type choice: aws_iam_role is taggable, mapped in live/mapping.json (via
# "name" -> AWS::IAM::Role), admitted in identity.DefaultTable, and has an
# unambiguous row in internal/live/discovery/tagging.go's ARN join table
# (iam:role/NAME, no id-shape disambiguation). It is also the type
# live/floci-capabilities.json records as tagging-sweep "implemented" for the
# pinned digest, which is what makes this run meaningful rather than lucky.
#
# The markers are written in the block, the same adoption shape
# live/e2e/dataread-projection's fixture uses: a tag you could have written
# with your own cloud tools.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "tagging-sweep-e2e"
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

locals {
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role" "keeper" {
  name               = "tagging-sweep-e2e-keeper"
  assume_role_policy = local.assume_role_policy

  tags = {
    tofu-estate  = "tagging-sweep-e2e"
    tofu-address = "aws_iam_role.keeper"
  }
}

resource "aws_iam_role" "demo" {
  name               = "tagging-sweep-e2e-demo"
  assume_role_policy = local.assume_role_policy

  tags = {
    tofu-estate  = "tagging-sweep-e2e"
    tofu-address = "aws_iam_role.demo"
  }
}
