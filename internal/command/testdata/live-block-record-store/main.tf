# The same estate as testdata/live-block, with a record_store block. The
# store is issue #109's hint carrier: a plain apply persists guided
# discovery's hint (the estate's type roster plus a timestamp) into it, and
# its presence is what turns guided discovery on by default for the next
# plan or apply.
terraform {
  live {
    estate = "stateless-unit"

    record_store "local" {}
  }

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

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
