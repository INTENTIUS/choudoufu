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

# Client-named identity: the bucket name is in the configuration, so the
# projection can read this one back with no memory at all.
resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-unit-data"
}

# Server-assigned identity: nothing in configuration names it, so it waits
# for marker discovery and shows up in the omissions section.
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
