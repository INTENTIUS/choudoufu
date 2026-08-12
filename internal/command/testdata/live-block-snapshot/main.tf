# The same estate as testdata/live-block, with snapshot_path set so a
# plain apply exercises the P4.2 observational snapshot end to end.
terraform {
  live {
    estate        = "stateless-unit"
    snapshot_path = "snapshot/estate.json"
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
