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
  bucket = "tofu-live-unit-data"
}

# Outside the live-mode subset: a logical resource exists only inside the
# record that live mode removes.
resource "random_pet" "name" {
  length = 2
}
