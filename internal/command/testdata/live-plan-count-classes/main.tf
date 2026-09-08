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

# Two count blocks, one of each kind the marker spec distinguishes.
#
# aws_eip.pool is a fungible set: nothing in its body says which live
# allocation is which instance, so identity resolution answers
# NEEDS_DISCOVERY and the slot marker is the only record of which member is
# which.
resource "aws_eip" "pool" {
  count = 2

  domain = "vpc"
}

# aws_s3_bucket.shard is not fungible: the configuration names each member,
# lint admits the count.index shape because it can prove the two names
# distinct, and identity resolution answers CONCRETE per instance. There is
# nothing for a slot to decide, and none is written.
resource "aws_s3_bucket" "shard" {
  count = 2

  bucket = "shard-${count.index}"
}
