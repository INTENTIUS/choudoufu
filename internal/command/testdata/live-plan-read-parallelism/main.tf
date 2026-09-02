terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

# Four provider configurations, one per resource below, and that is the whole
# reason this fixture exists rather than four resources under one provider.
#
# tofu.MockProvider holds its own mutex across every callback
# (internal/tofu/provider_mock.go, ImportResourceState's p.Lock()), and
# newLivePlanCommand hands out one instance per provider CONFIGURATION. So four
# resources under one provider block have their reads serialised by the test
# double no matter how wide the read pass runs, and a probe measuring how many
# overlap sees exactly one at every setting - which is why GitHub issue #612
# concluded nothing observable differs between parallelism settings against a
# mock cloud. Four configurations are four instances and four mutexes, so the
# read pass's own bound is the only thing left that can hold these calls apart.
#
# The regions differ only because the mock insists a provider block name one it
# was told to accept (statelessTestCloud.allowedRegions). Read-back is
# deliberately not region-partitioned, so all four objects are readable through
# any of them.
provider "aws" {
  region = "us-east-1"
}

provider "aws" {
  alias  = "b"
  region = "us-east-2"
}

provider "aws" {
  alias  = "c"
  region = "us-west-1"
}

provider "aws" {
  alias  = "d"
  region = "us-west-2"
}

# Client-named on purpose: the identity is in the configuration, so every one
# of these lands in the projection's CONCRETE phase - the only phase that
# prefetches - with no marker discovery needed to get it there.
#
# Named a, b, c, d so that the addresses and the bucket names sort the same
# way, which is what lets a test assert the sequential pass reads them in loop
# order rather than only one at a time.
resource "aws_s3_bucket" "a" {
  bucket = "tofu-stateless-read-a"
}

resource "aws_s3_bucket" "b" {
  provider = aws.b
  bucket   = "tofu-stateless-read-b"
}

resource "aws_s3_bucket" "c" {
  provider = aws.c
  bucket   = "tofu-stateless-read-c"
}

resource "aws_s3_bucket" "d" {
  provider = aws.d
  bucket   = "tofu-stateless-read-d"
}
