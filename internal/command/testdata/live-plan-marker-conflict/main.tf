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

# A resource whose ownership marker names an address it does not have. A
# stamping pass will not rewrite it: that is a rename, and a rename is
# live-mv's job.
resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-unit-data"

  tags = {
    tofu-estate  = "stateless-unit"
    tofu-address = "aws_s3_bucket.old_name"
  }
}
