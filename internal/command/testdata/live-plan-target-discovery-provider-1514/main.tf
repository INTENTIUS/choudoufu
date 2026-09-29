# GitHub issue #1514's provider half: the excluded certificate sits under
# a provider configuration whose region reads a managed attribute, so the
# discovery pre-pass cannot configure that provider. Untargeted, the
# certificate's identity needs it, and the run is refused as before.
# Under -target=aws_s3_bucket.data the certificate is excluded, so no
# instance this run acts on needs aws.late, and its pass is downgraded to a
# warning the way a sweep-only provider always was.
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
  alias  = "late"
  region = aws_s3_bucket.region_source.arn
}

resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-unit-data"
}

resource "aws_s3_bucket" "region_source" {
  bucket = "tofu-stateless-unit-region-source"
}

resource "aws_acm_certificate" "cert" {
  provider          = aws.late
  domain_name       = "example.com"
  validation_method = "DNS"
  tags = {
    Name = "example"
  }
}
