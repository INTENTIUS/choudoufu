# A configuration outside the stateless subset: aws_s3_bucket.data is a
# plausible rename target, and random_pet.name is a logical resource that
# has no existence outside the record stateless mode removes. Used to prove
# that "choudoufu live-mv" refuses a configuration like this before starting a
# provider or reading the live system, exactly as "choudoufu live-plan" does.
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
  bucket = "tofu-mv-unit-data"
}

resource "random_pet" "name" {
  length = 2
}
