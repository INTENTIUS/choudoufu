# The estate the five Ops drive. Small on purpose: two taggable resources,
# which is enough for every stage of the pipeline to have something real to
# say, and no backend block - choudoufu refuses one on a live root, because
# ownership is the marker tags on the resources themselves rather than an
# entry in a state file.
#
# Nothing here is choudoufu-specific. This is an ordinary AWS root that stock
# OpenTofu runs unchanged; what makes it live is estate.chdf.hcl beside it.

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source = "hashicorp/aws"
      # The provider version choudoufu's own admission evidence was surveyed
      # against (live/survey.json's provider_version). Pin it: the identity a
      # marker carries is derived from the provider's identity schema, so the
      # provider version is part of what a plan means.
      version = "~> 6.59.0"
    }
  }
}

variable "aws_region" {
  description = "Region the estate lives in. The pipelines pass this as AWS_REGION."
  type        = string
  default     = "us-east-1"
}

variable "name_prefix" {
  description = "Prefix for the two resource names, so two copies of this example can coexist in one account."
  type        = string
  default     = "choudoufu-ci-pipelines-example"
}

provider "aws" {
  region = var.aws_region
}

# Taggable through the Resource Groups Tagging API, so its ownership marker is
# listable account-wide: this is the resource `live-ls` finds.
resource "aws_cloudwatch_log_group" "app" {
  name              = "/${var.name_prefix}/app"
  retention_in_days = 14

  tags = {
    Example = "ci-pipelines"
  }
}

# Taggable too, but IAM is one of the services whose tagging call choudoufu
# does not print a paste-ready adopt command for (Route53 and S3 are the
# others). An unmarked IAM role therefore shows up in the adoption ledger as a
# refusal naming both marker values rather than as something `live-adopt`
# claims - which is the honest half of adoption a CI example should carry.
resource "aws_iam_role" "app" {
  name = "${var.name_prefix}-app"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = {
    Example = "ci-pipelines"
  }
}
