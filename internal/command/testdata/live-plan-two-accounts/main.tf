terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

# Two provider configurations in one region and two ACCOUNTS, each named by
# its own static key pair the way live/smoke/scenarios/the-boundary-holds-
# across-accounts.sh names them (the emulator reads a 12-digit access key id
# as the account id). The command-level unit-test twin of that scenario's
# provider blocks, for GitHub issue #957: the sweep clients built for each
# configuration must sign as that configuration's principal.
provider "aws" {
  region     = "us-east-1"
  access_key = "111111111111"
  secret_key = "test"
}

provider "aws" {
  alias      = "other_account"
  region     = "us-east-1"
  access_key = "222222222222"
  secret_key = "test"
}

resource "aws_s3_bucket" "home" {
  bucket = "tofu-two-accounts-home"
}

resource "aws_s3_bucket" "other_account" {
  provider = aws.other_account
  bucket   = "tofu-two-accounts-other"
}
