# Identical to the live-plan fixture except that nothing in this file is
# choudoufu-specific: the live configuration lives in estate.chdf.hcl beside
# it, which is the sidecar form issue #72 adds.
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

resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-unit-data"
}
