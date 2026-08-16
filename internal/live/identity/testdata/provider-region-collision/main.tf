# The other direction of issue #250's region wiring: a region reaching the
# identity must not turn a collision into silence.
#
# "explicit" states region = "eu-west-1"; "inherited" states no region at
# all and gets the identical eu-west-1 from the provider block. Both name
# the queue "jobs" under the same provider configuration, so both render
# https://sqs.eu-west-1.amazonaws.com/ACCOUNT/jobs and checkCollisions must
# still flag them - the same property testdata/cloud-scope-region-inherited
# pins for a type whose identity does NOT embed a region.
#
# "elsewhere" is the same name again in a different region, which is a
# different queue and must not be flagged against either.

provider "aws" {
  region = "eu-west-1"
}

resource "aws_sqs_queue" "explicit" {
  region = "eu-west-1"
  name   = "jobs"
}

resource "aws_sqs_queue" "inherited" {
  name = "jobs"
}

resource "aws_sqs_queue" "elsewhere" {
  region = "eu-west-2"
  name   = "jobs"
}
