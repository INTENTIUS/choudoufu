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

resource "aws_s3_bucket" "archive" {
  bucket = "tofu-mv-unit-data"

  tags = {
    tofu-estate  = "stateless-unit"
    tofu-address = "aws_s3_bucket.archive"
  }
}

resource "aws_security_group" "renamed" {
  name = "mv-unit"

  tags = {
    tofu-estate  = "stateless-unit"
    tofu-address = "aws_security_group.renamed"
  }
}

# Server-assigned identity that the mock provider cannot list, which is what
# makes a resource unfindable by marker.
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate  = "stateless-unit"
    tofu-address = "aws_vpc.main"
  }
}
