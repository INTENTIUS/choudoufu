# GitHub issue #1859: live-import over a configuration with a live block and
# a terraform_estate_outputs block. The migration's identity data-read phase
# reads that block through the record store (GitHub issue #1575), so the
# command must open its estate-outputs reader from the store it opens.
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

resource "aws_s3_bucket" "data" {
  bucket = "tofu-import-unit-data"
}
