# The same estate as testdata/live-block, with a construct outside the
# stateless subset added on top: lifecycle { ignore_changes = all }, which
# discards the very update that writes the ownership markers.
#
# It is used rather than a second resource type (e.g. random_pet)
# so that this fixture needs no provider beyond the "aws" one the test
# harness already stands in for - the ordinary dependency lock check every
# plan and apply runs ahead of the stateless pipeline would otherwise reject
# an unmocked provider before lint ever ran, which is not the property this
# fixture exists to test. It needs no provider SCHEMA either, which a
# count.index or admission-table refusal would.
#
# It carried a provisioner until choudoufu #364. That was the same kind of
# choice - a construct refused with nothing but "aws" in play - and it
# stopped being a refusal when every live block started implying a local
# record store: the record store is where a provisioner's tainted bit lives,
# and #353 admits provisioners the moment there is one. ignore_changes = all
# is refused for a reason nothing about a record store touches - the markers
# are on the resource, not in any store - which is what this fixture needs.
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

  lifecycle {
    ignore_changes = all
  }
}
