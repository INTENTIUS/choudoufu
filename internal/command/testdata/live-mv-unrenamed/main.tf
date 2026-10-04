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
  bucket = "tofu-mv-unit-data"

  tags = {
    tofu-estate  = "live-unit"
    tofu-address = "aws_s3_bucket.data"
  }
}
