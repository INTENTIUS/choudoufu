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

resource "aws_s3_bucket" "east" {
  bucket = "lint-undeclared-alias-east"
}

# aws.nope is declared nowhere; stock OpenTofu's graph refuses this and live
# mode used to configure the provider from the environment instead (#123).
resource "aws_s3_bucket" "stray" {
  provider = aws.nope
  bucket   = "lint-undeclared-alias-stray"
}
