# GitHub issue #1859: a rename whose destination's identity-bearing argument
# reads another estate's recorded output. live-mv resolves identities the way
# a plan does, so terraform_estate_outputs is read before resolution, through
# the record store the live block names.
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }

  live {
    estate = "live-unit"

    record_store "local" {}
  }
}

provider "aws" {
  region = "us-east-1"
}

data "terraform_estate_outputs" "network" {
  estate = "network"
  names  = ["suffix"]
}

resource "aws_s3_bucket" "archive" {
  bucket = "tofu-mv-unit-${data.terraform_estate_outputs.network.values.suffix}"

  tags = {
    tofu-estate  = "live-unit"
    tofu-address = "aws_s3_bucket.archive"
  }
}
