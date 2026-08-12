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

# The bucket name is the identity, and this one has none: the identity
# resolver cannot name the live object, which is fatal rather than a
# create.
resource "aws_s3_bucket" "data" {
  tags = {
    tofu-estate = "unit"
  }
}
