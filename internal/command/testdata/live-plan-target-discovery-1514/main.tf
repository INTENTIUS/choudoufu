# GitHub issue #1514's shape: the block a -target run is about, beside a
# block the run excludes whose identity is server-assigned, so it needs
# marker discovery, and whose type the test's fake cloud cannot list.
#
# -target=aws_s3_bucket.data drops the certificate from the plan graph:
# the bucket reads nothing from it. Untargeted, the certificate's discovery
# need is the run's own and the unlistable type refuses it; targeted, it is
# a block the run excludes and must not refuse the run.
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

resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
  tags = {
    Name = "example"
  }
}
