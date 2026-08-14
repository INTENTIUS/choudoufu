terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

# aws.nope is declared by no provider block. Stock OpenTofu refuses this in
# the graph ("Provider configuration not present"); live mode used to
# configure the provider from the environment alone, silently, mid-discovery
# (GitHub issue #123).
resource "aws_s3_bucket" "stray" {
  provider = aws.nope
  bucket   = "limits-undeclared-provider-alias"
}
