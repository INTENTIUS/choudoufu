# Issue #255's fixture, phase 2: aws_iam_role.demo's block is gone.
#
# Identical to declared/main.tf apart from the deleted block. Nothing in this
# configuration mentions the demo role, and there is no state file, so the
# only way a run can propose destroying it is the estate-wide sweep - which
# is the branch this fixture exists to exercise.

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
