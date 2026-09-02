# Six client-named, independently readable instances of one type: the
# smallest configuration in which the read pass's concrete phase has enough
# instances for GitHub issue #585's concurrency to be observable at all, and
# for the ORDER of what it produces to be distinguishable from the order the
# cloud answered in.
#
# Six separate blocks rather than one for_each block on purpose. The address
# order the concrete phase runs in is [orderWork]'s sort over
# addrs.AbsResourceInstance.String(), and six single-digit block names make
# that order the same as the fixture's own numbering, so a test can name the
# instance it means without a key-to-index table.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

resource "aws_cloudwatch_log_group" "g0" {
  name = "read-parallel-0"
}

resource "aws_cloudwatch_log_group" "g1" {
  name = "read-parallel-1"
}

resource "aws_cloudwatch_log_group" "g2" {
  name = "read-parallel-2"
}

resource "aws_cloudwatch_log_group" "g3" {
  name = "read-parallel-3"
}

resource "aws_cloudwatch_log_group" "g4" {
  name = "read-parallel-4"
}

resource "aws_cloudwatch_log_group" "g5" {
  name = "read-parallel-5"
}
