# GitHub issue #380's live-plan wiring fixture.
#
# aws_vpc.main is selected by strict { markers "record" }, so stamp writes
# no ownership marker into its tags and its identity comes from the
# estate's record store instead (identity.ClassRecordLocated,
# projection/located.go's materializeLocated). The test seeds both that
# record and the live object's existing tofu-address/tofu-estate tags by
# hand, the way a resource looks right after it migrates onto this
# selection: something stamped it before the selection existed, and the
# selection must not plan that marker away just because this configuration
# stopped declaring it.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }

  live {
    estate = "markers-record-unit"

    record_store "local" {
      path = ".tofu-records"
    }

    strict {
      markers "record" {
        addresses = ["aws_vpc.main"]
      }
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
