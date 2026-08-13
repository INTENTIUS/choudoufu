# The same estate as testdata/live-block, with a construct outside the
# stateless subset added on top: a provisioner block, which describes an
# effect that a stateless run has no stored record to say already happened.
# A provisioner is used rather than a second resource type (e.g. random_pet)
# so that this fixture needs no provider beyond the "aws" one the test
# harness already stands in for - the ordinary dependency lock check every
# plan and apply runs ahead of the stateless pipeline would otherwise reject
# an unmocked provider before lint ever ran, which is not the property this
# fixture exists to test.
#
# Used to prove that plain "tofu plan" and "tofu apply" reject a
# configuration like this before anything is read from the live system,
# exactly as "tofu live-plan" does.
terraform {
  live {
    estate = "stateless-unit"
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

  provisioner "local-exec" {
    command = "echo hi"
  }
}
