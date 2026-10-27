# GitHub issue #2296, the "local" case. TestOfflineStampHonorsADeclaredStore
# runs this directory and its two siblings with one schema, under which
# aws_vpc has nowhere to carry a marker and its identity is server-assigned,
# so it is found only by a marker or by the record the apply writes.
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }

  live {
    estate = "stamp-declared-store-2296"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

resource "aws_vpc" "unmarkable" {
  cidr_block = "10.0.0.0/16"
}
